// services/tenancy/metering.go

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/evemeta-tony/aether-edit/services/contracts"
)

// ftStreamName is the JetStream stream carrying the frozen v1
// subjects. FT-2 creates it; creation here is idempotent and never
// modifies an existing stream.
const ftStreamName = "AETHER_FT"

// meteringConsumerName is the durable JetStream consumer identity, so
// restarts resume where the service left off.
const meteringConsumerName = "tenancy-metering"

// rollupDelta translates one metering event into rollup increments.
// Unknown kinds return an error (the consumer logs and terms them);
// negative byte or encode-second values are rejected as malformed.
func rollupDelta(ev contracts.MeteringEvent) (UsageRollup, error) {
	if ev.EventID == "" || ev.WorkspaceID == "" {
		return UsageRollup{}, fmt.Errorf("metering event missing eventId or workspaceId")
	}
	if ev.At.IsZero() {
		return UsageRollup{}, fmt.Errorf("metering event %s missing at", ev.EventID)
	}
	if ev.Bytes != nil && *ev.Bytes < 0 {
		return UsageRollup{}, fmt.Errorf("metering event %s: negative bytes", ev.EventID)
	}
	if ev.EncodeSeconds != nil && *ev.EncodeSeconds < 0 {
		return UsageRollup{}, fmt.Errorf("metering event %s: negative encodeSeconds", ev.EventID)
	}
	d := UsageRollup{WorkspaceID: ev.WorkspaceID, Month: monthOf(ev.At)}
	switch ev.Kind {
	case contracts.MeteringUploadSessionCreated:
		d.UploadSessions = 1
	case contracts.MeteringUploadCompleted:
		d.UploadsCompleted = 1
		if ev.Bytes != nil {
			d.BytesUploaded = *ev.Bytes
		}
	case contracts.MeteringJobQueued:
		d.JobsQueued = 1
	case contracts.MeteringJobStarted:
		d.JobsStarted = 1
	case contracts.MeteringJobCompleted:
		d.JobsCompleted = 1
		if ev.EncodeSeconds != nil {
			d.EncodeSeconds = *ev.EncodeSeconds
		}
	case contracts.MeteringJobFailed:
		d.JobsFailed = 1
		if ev.EncodeSeconds != nil {
			d.EncodeSeconds = *ev.EncodeSeconds
		}
	default:
		return UsageRollup{}, fmt.Errorf("metering event %s: unknown kind %q", ev.EventID, ev.Kind)
	}
	return d, nil
}

// consumeMeteringPayload parses and applies one raw metering message.
// It returns (applied, terminal error). Idempotent: replays of an
// already-recorded eventId apply nothing and succeed.
func consumeMeteringPayload(ctx context.Context, store Store, data []byte) (bool, error) {
	var ev contracts.MeteringEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return false, fmt.Errorf("metering payload: %w", err)
	}
	delta, err := rollupDelta(ev)
	if err != nil {
		return false, err
	}
	applied, err := store.ApplyMetering(ctx, ev, delta.Month, delta)
	if err != nil {
		return false, fmt.Errorf("metering apply: %w", err)
	}
	return applied, nil
}

// MeteringConsumer subscribes to aether.ft.metering.v1 and builds the
// per-workspace usage rollups.
type MeteringConsumer struct {
	nc    *nats.Conn
	store Store
	log   *slog.Logger
	cc    jetstream.ConsumeContext
}

// NewMeteringConsumer connects to NATS, ensures the stream exists (only
// creating it when absent), and starts a durable consumer on the
// metering subject.
func NewMeteringConsumer(ctx context.Context, natsURL string, store Store, log *slog.Logger) (*MeteringConsumer, error) {
	nc, err := nats.Connect(natsURL,
		nats.Name("aether-tenancy"),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("jetstream: %w", err)
	}
	stream, err := js.Stream(ctx, ftStreamName)
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		stream, err = js.CreateStream(ctx, jetstream.StreamConfig{
			Name:     ftStreamName,
			Subjects: []string{contracts.SubjectUploadLanded, contracts.SubjectMetering},
			Storage:  jetstream.FileStorage,
		})
	}
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("ensure stream %s: %w", ftStreamName, err)
	}
	cons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       meteringConsumerName,
		FilterSubject: contracts.SubjectMetering,
		AckPolicy:     jetstream.AckExplicitPolicy,
		MaxDeliver:    10,
	})
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("consumer %s: %w", meteringConsumerName, err)
	}
	mc := &MeteringConsumer{nc: nc, store: store, log: log}
	cc, err := cons.Consume(mc.handle)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("consume: %w", err)
	}
	mc.cc = cc
	log.Info("metering consumer running", "stream", ftStreamName, "consumer", meteringConsumerName,
		"subject", contracts.SubjectMetering)
	return mc, nil
}

// handle processes one delivery. Malformed payloads are terminated
// (they will never parse on redelivery); store failures are NAKed for
// redelivery.
func (m *MeteringConsumer) handle(msg jetstream.Msg) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	applied, err := consumeMeteringPayload(ctx, m.store, msg.Data())
	if err != nil {
		// Distinguish malformed (terminal) from transient store
		// failure: json and rollupDelta errors are terminal, a
		// redelivery can never fix them.
		if isTerminalMeteringErr(msg.Data()) {
			m.log.Error("metering event terminated", "err", err)
			_ = msg.Term()
			return
		}
		m.log.Warn("metering apply failed; redelivering", "err", err)
		_ = msg.Nak()
		return
	}
	if !applied {
		m.log.Info("metering event replayed; already recorded")
	}
	_ = msg.Ack()
}

// Close stops consumption and drains the connection.
func (m *MeteringConsumer) Close() {
	if m.cc != nil {
		m.cc.Stop()
	}
	if m.nc != nil {
		_ = m.nc.Drain()
	}
}

// isTerminalMeteringErr reports whether the payload itself can never
// succeed (bad JSON or a shape that fails rollupDelta), as opposed to
// a transient store failure.
func isTerminalMeteringErr(data []byte) bool {
	var ev contracts.MeteringEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return true
	}
	_, err := rollupDelta(ev)
	return err != nil
}

// ---- usage endpoint (UserMenu plan/usage meter) ----

// usageResponse feeds the UserMenu plan row and quota meter: plan
// tier, encode seconds used against the monthly limit, storage used
// against the cap, and job counts for the current UTC month.
type usageResponse struct {
	WorkspaceID       string  `json:"workspaceId"`
	PlanTier          string  `json:"planTier"`
	Month             string  `json:"month"`
	EncodeSecondsUsed float64 `json:"encodeSecondsUsed"`
	EncodeHoursUsed   float64 `json:"encodeHoursUsed"`
	EncodeHoursLimit  float64 `json:"encodeHoursLimit"`
	StorageBytesUsed  int64   `json:"storageBytesUsed"`
	StorageBytesLimit int64   `json:"storageBytesLimit"`
	UploadSessions    int64   `json:"uploadSessions"`
	UploadsCompleted  int64   `json:"uploadsCompleted"`
	JobsQueued        int64   `json:"jobsQueued"`
	JobsStarted       int64   `json:"jobsStarted"`
	JobsCompleted     int64   `json:"jobsCompleted"`
	JobsFailed        int64   `json:"jobsFailed"`
}

// handleUsage returns the current month's rollup for the caller's
// workspace. API key identities are workspace-scoped and may read it;
// user identities must be members.
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireIdentity(w, r)
	if !ok {
		return
	}
	if !isAPIKeyIdentity(id) {
		if !s.requireRole(w, r, id.WorkspaceID, id.UserID, RoleMember) {
			return
		}
	}
	ws, err := s.store.GetWorkspace(r.Context(), id.WorkspaceID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "workspace_not_found", "token workspace no longer exists")
			return
		}
		s.internalError(w, r, "usage workspace", err)
		return
	}
	tier, tierOK := s.tiers.Lookup(ws.PlanTier)
	month := monthOf(s.now().UTC())
	rollup, err := s.store.GetRollup(r.Context(), id.WorkspaceID, month)
	if err != nil && !errors.Is(err, ErrNotFound) {
		s.internalError(w, r, "usage rollup", err)
		return
	}
	storage, err := s.store.SumStorageBytes(r.Context(), id.WorkspaceID)
	if err != nil {
		s.internalError(w, r, "usage storage", err)
		return
	}
	resp := usageResponse{
		WorkspaceID:       id.WorkspaceID,
		PlanTier:          ws.PlanTier,
		Month:             month,
		EncodeSecondsUsed: rollup.EncodeSeconds,
		EncodeHoursUsed:   rollup.EncodeSeconds / 3600,
		StorageBytesUsed:  storage,
		UploadSessions:    rollup.UploadSessions,
		UploadsCompleted:  rollup.UploadsCompleted,
		JobsQueued:        rollup.JobsQueued,
		JobsStarted:       rollup.JobsStarted,
		JobsCompleted:     rollup.JobsCompleted,
		JobsFailed:        rollup.JobsFailed,
	}
	if tierOK {
		resp.EncodeHoursLimit = tier.EncodeHoursPerMonth
		resp.StorageBytesLimit = tier.StorageBytes
	}
	writeJSON(w, http.StatusOK, resp)
}

func isAPIKeyIdentity(id Identity) bool {
	return len(id.UserID) > len(apiKeyUserPrefix) && id.UserID[:len(apiKeyUserPrefix)] == apiKeyUserPrefix
}
