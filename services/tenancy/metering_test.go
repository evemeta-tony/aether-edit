// services/tenancy/metering_test.go

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/evemeta-tony/aether-edit/services/contracts"
)

// replayFixtures feeds every recorded metering event (frozen contract
// 2 shapes) through the consumer's payload path.
func replayFixtures(t *testing.T, store Store) int {
	t.Helper()
	f, err := os.Open("testdata/metering_events.jsonl")
	if err != nil {
		t.Fatalf("open fixtures: %v", err)
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		applied, err := consumeMeteringPayload(context.Background(), store, line)
		if err != nil {
			t.Fatalf("fixture line %d: %v", n+1, err)
		}
		if !applied {
			t.Fatalf("fixture line %d reported duplicate on first replay", n+1)
		}
		n++
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan fixtures: %v", err)
	}
	return n
}

// TestRollupFromFixtures replays the recorded stream and checks every
// rollup figure, month bucketing, and cross-workspace isolation.
func TestRollupFromFixtures(t *testing.T) {
	store := newMemStore()
	n := replayFixtures(t, store)
	if n != 12 {
		t.Fatalf("replayed %d fixtures, want 12", n)
	}

	aug, err := store.GetRollup(context.Background(), "ws-fixture", "2026-08")
	if err != nil {
		t.Fatalf("august rollup: %v", err)
	}
	if aug.BytesUploaded != 320000 {
		t.Fatalf("bytesUploaded %d, want 320000", aug.BytesUploaded)
	}
	if aug.EncodeSeconds != 1800.5+120.25 {
		t.Fatalf("encodeSeconds %v, want 1920.75", aug.EncodeSeconds)
	}
	if aug.UploadSessions != 2 || aug.UploadsCompleted != 2 {
		t.Fatalf("uploads %d/%d, want 2/2", aug.UploadSessions, aug.UploadsCompleted)
	}
	if aug.JobsQueued != 2 || aug.JobsStarted != 2 || aug.JobsCompleted != 1 || aug.JobsFailed != 1 {
		t.Fatalf("jobs %d/%d/%d/%d, want 2/2/1/1", aug.JobsQueued, aug.JobsStarted, aug.JobsCompleted, aug.JobsFailed)
	}

	// The July event landed in its own month bucket.
	jul, err := store.GetRollup(context.Background(), "ws-fixture", "2026-07")
	if err != nil {
		t.Fatalf("july rollup: %v", err)
	}
	if jul.EncodeSeconds != 900 || jul.JobsCompleted != 1 {
		t.Fatalf("july %+v", jul)
	}

	// The other workspace is isolated.
	other, err := store.GetRollup(context.Background(), "ws-other", "2026-08")
	if err != nil {
		t.Fatalf("other rollup: %v", err)
	}
	if other.BytesUploaded != 50000 {
		t.Fatalf("other bytes %d, want 50000", other.BytesUploaded)
	}

	// Storage sums across months per workspace.
	sum, err := store.SumStorageBytes(context.Background(), "ws-fixture")
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	if sum != 320000 {
		t.Fatalf("storage sum %d, want 320000", sum)
	}
}

// TestRollupIdempotency replays the same stream twice: totals must not
// double (JetStream redelivery safety).
func TestRollupIdempotency(t *testing.T) {
	store := newMemStore()
	replayFixtures(t, store)

	f, err := os.Open("testdata/metering_events.jsonl")
	if err != nil {
		t.Fatalf("open fixtures: %v", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		applied, err := consumeMeteringPayload(context.Background(), store, sc.Bytes())
		if err != nil {
			t.Fatalf("second replay: %v", err)
		}
		if applied {
			t.Fatal("duplicate eventId applied on second replay")
		}
	}
	aug, err := store.GetRollup(context.Background(), "ws-fixture", "2026-08")
	if err != nil {
		t.Fatalf("rollup: %v", err)
	}
	if aug.BytesUploaded != 320000 {
		t.Fatalf("bytesUploaded doubled: %d", aug.BytesUploaded)
	}
}

// TestRollupDeltaValidation rejects malformed and unknown events.
func TestRollupDeltaValidation(t *testing.T) {
	now := time.Now()
	neg := int64(-1)
	cases := []contracts.MeteringEvent{
		{},
		{EventID: "e", WorkspaceID: "w"},
		{EventID: "e", WorkspaceID: "w", Kind: "mystery_kind", At: now},
		{EventID: "e", WorkspaceID: "w", Kind: contracts.MeteringUploadCompleted, Bytes: &neg, At: now},
	}
	for i, ev := range cases {
		if _, err := rollupDelta(ev); err == nil {
			t.Fatalf("case %d: malformed event accepted", i)
		}
	}
	if _, err := consumeMeteringPayload(context.Background(), newMemStore(), []byte("{not json")); err == nil {
		t.Fatal("bad json accepted")
	}
	if !isTerminalMeteringErr([]byte("{not json")) {
		t.Fatal("bad json not terminal")
	}
	if isTerminalMeteringErr([]byte(`{"eventId":"e","workspaceId":"w","kind":"upload_completed","bytes":5,"at":"2026-08-01T00:00:00Z"}`)) {
		t.Fatal("well-formed event marked terminal")
	}
}

// TestUsageEndpoint checks the UserMenu meter feed over a replayed
// stream: used values from rollups, limits from the plan tier.
func TestUsageEndpoint(t *testing.T) {
	env := newTestEnv(t)
	sess := env.login("usage-sub", "usage@example.com", "Usage")
	wsID := sess.ActiveWorkspaceID

	events := []contracts.MeteringEvent{
		mkEvent("u1", wsID, contracts.MeteringUploadCompleted, i64(1000), nil, "", time.Now().UTC()),
		mkEvent("u2", wsID, contracts.MeteringJobQueued, nil, nil, "j1", time.Now().UTC()),
		mkEvent("u3", wsID, contracts.MeteringJobCompleted, nil, f64(3600), "j1", time.Now().UTC()),
	}
	for _, ev := range events {
		delta, err := rollupDelta(ev)
		if err != nil {
			t.Fatalf("delta: %v", err)
		}
		if _, err := env.store.ApplyMetering(context.Background(), ev, delta.Month, delta); err != nil {
			t.Fatalf("apply: %v", err)
		}
	}

	resp := env.do(http.MethodGet, "/v1/usage", sess.AccessToken, nil)
	wantStatus(t, resp, http.StatusOK)
	var u usageResponse
	decodeBody(t, resp, &u)
	if u.PlanTier != "demo" {
		t.Fatalf("planTier %q", u.PlanTier)
	}
	if u.EncodeSecondsUsed != 3600 || u.EncodeHoursUsed != 1 {
		t.Fatalf("encode used %v s / %v h", u.EncodeSecondsUsed, u.EncodeHoursUsed)
	}
	if u.EncodeHoursLimit != 2 {
		t.Fatalf("encode limit %v, want 2", u.EncodeHoursLimit)
	}
	if u.StorageBytesUsed != 1000 || u.StorageBytesLimit != 1<<20 {
		t.Fatalf("storage %d/%d", u.StorageBytesUsed, u.StorageBytesLimit)
	}
	if u.JobsQueued != 1 || u.JobsCompleted != 1 {
		t.Fatalf("jobs %d/%d", u.JobsQueued, u.JobsCompleted)
	}
}

// TestProducerConformance certifies the consumer against the actual
// producer encoding: FT-2 and FT-3 publish by json.Marshal of the
// shared contracts.MeteringEvent struct (see services/upload/server.go
// publishJSON on the FT-2 branch), so marshaling that struct here IS
// the producer's byte encoding. Every frozen kind constant round-trips
// through the consumer, and the recorded fixtures decode strictly into
// the contract struct with no unknown or missing-name fields.
func TestProducerConformance(t *testing.T) {
	store := newMemStore()
	kinds := []contracts.MeteringKind{
		contracts.MeteringUploadSessionCreated,
		contracts.MeteringUploadCompleted,
		contracts.MeteringJobQueued,
		contracts.MeteringJobStarted,
		contracts.MeteringJobCompleted,
		contracts.MeteringJobFailed,
	}
	for i, kind := range kinds {
		ev := contracts.MeteringEvent{
			EventID:     "conf-" + string(kind),
			WorkspaceID: "ws-conf",
			UserID:      "u",
			Kind:        kind,
			At:          time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC),
		}
		switch kind {
		case contracts.MeteringUploadSessionCreated, contracts.MeteringUploadCompleted:
			ev.Bytes = i64(1000)
		case contracts.MeteringJobCompleted, contracts.MeteringJobFailed:
			ev.EncodeSeconds = f64(30)
			ev.JobID = "j"
		}
		payload, err := json.Marshal(ev) // exactly what FT-2/FT-3 publish
		if err != nil {
			t.Fatalf("kind %d marshal: %v", i, err)
		}
		applied, err := consumeMeteringPayload(context.Background(), store, payload)
		if err != nil || !applied {
			t.Fatalf("kind %q: applied=%v err=%v", kind, applied, err)
		}
	}
	r, err := store.GetRollup(context.Background(), "ws-conf", "2026-08")
	if err != nil {
		t.Fatalf("rollup: %v", err)
	}
	if r.UploadSessions != 1 || r.UploadsCompleted != 1 || r.JobsQueued != 1 ||
		r.JobsStarted != 1 || r.JobsCompleted != 1 || r.JobsFailed != 1 ||
		r.BytesUploaded != 1000 || r.EncodeSeconds != 60 {
		t.Fatalf("conformance rollup %+v", r)
	}

	// Fixtures carry only contract field names: strict decode with
	// unknown fields disallowed must accept every line.
	f, err := os.Open("testdata/metering_events.jsonl")
	if err != nil {
		t.Fatalf("open fixtures: %v", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		if len(sc.Bytes()) == 0 {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(sc.Bytes()))
		dec.DisallowUnknownFields()
		var ev contracts.MeteringEvent
		if err := dec.Decode(&ev); err != nil {
			t.Fatalf("fixture line %d carries non-contract fields: %v", line, err)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
}

func mkEvent(id, ws string, kind contracts.MeteringKind, bytes *int64, encodeSeconds *float64, jobID string, at time.Time) contracts.MeteringEvent {
	return contracts.MeteringEvent{
		EventID: id, WorkspaceID: ws, UserID: "u", Kind: kind,
		Bytes: bytes, EncodeSeconds: encodeSeconds, JobID: jobID, At: at,
	}
}

func i64(v int64) *int64     { return &v }
func f64(v float64) *float64 { return &v }
