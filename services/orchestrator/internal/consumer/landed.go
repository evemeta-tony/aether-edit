// services/orchestrator/internal/consumer/landed.go
//
// JetStream consumer for the landed-object event (frozen contract 1,
// subject aether.ft.upload.landed.v1). On receipt the service downloads the
// source object from the OVH S3 store to a LOCAL scratch file, probes it with
// ffprobe, and persists the media info with the source, so a later job create
// does not need a synchronous probe. When auto-create is enabled (DEFECT 3),
// it then auto-creates a queued transcode job for the landed source using the
// workspace default preset and emits the job_queued metering event. Malformed
// events are terminated (never redelivered); transient failures are NAKed with
// delay.
package consumer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/engine"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/events"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/jobs"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/natsio"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/store"
)

// SourceStore persists probed sources, resolves the workspace default preset,
// and creates jobs (the auto-create path).
type SourceStore interface {
	UpsertSource(ctx context.Context, s store.Source) error
	DefaultPreset(ctx context.Context, workspaceID string) (jobs.Preset, error)
	CreateJob(ctx context.Context, j jobs.Job) (jobs.Job, error)
}

// ObjectStore is the S3 object storage surface the consumer needs: check the
// source exists and download it to a local scratch file for probing.
type ObjectStore interface {
	Exists(ctx context.Context, key string) (bool, error)
	Download(ctx context.Context, key, dstPath string) error
}

// Meter emits contract 2 metering events (job_queued for auto-created jobs).
type Meter interface {
	Emit(ctx context.Context, workspaceID, userID string, kind events.MeteringKind, jobID string, bytes *int64, encodeSeconds *float64) error
}

// ProgressSink publishes job state transitions for FT-4.
type ProgressSink interface {
	Publish(ev events.JobProgress) error
}

// Runner is the scheduler surface the consumer needs: wake it after an
// auto-created job is enqueued so start latency does not wait on the poll tick.
type Runner interface {
	Wake()
}

// Config tunes the consumer.
type Config struct {
	// ScratchDir is the LOCAL temp directory where source objects are
	// downloaded from S3 for probing. It is not the object store.
	ScratchDir string
	// AutoCreateJobs enables auto-creating a transcode job on landing using
	// the workspace default preset (DEFECT 3). Default of the caller.
	AutoCreateJobs bool
}

// Landed consumes landed-object events, probes sources, and (optionally)
// auto-creates jobs.
type Landed struct {
	cfg      Config
	sources  SourceStore
	objects  ObjectStore
	eng      engine.TranscodeEngine
	meter    Meter
	progress ProgressSink
	runner   Runner
	log      *slog.Logger
}

// New builds the consumer worker.
func New(cfg Config, sources SourceStore, objects ObjectStore, eng engine.TranscodeEngine, meter Meter, progress ProgressSink, runner Runner, log *slog.Logger) *Landed {
	if log == nil {
		log = slog.Default()
	}
	return &Landed{
		cfg:      cfg,
		sources:  sources,
		objects:  objects,
		eng:      eng,
		meter:    meter,
		progress: progress,
		runner:   runner,
		log:      log,
	}
}

// Start binds a durable consumer on the stream carrying the landed subject
// and begins consuming. The returned stop function drains the consumer.
func (l *Landed) Start(ctx context.Context, conn *natsio.Conn, streamName string) (func(), error) {
	cons, err := conn.JS.CreateOrUpdateConsumer(ctx, streamName, jetstream.ConsumerConfig{
		Durable:       "orchestrator-landed",
		FilterSubject: events.SubjectUploadLanded,
		AckPolicy:     jetstream.AckExplicitPolicy,
		MaxDeliver:    10,
		AckWait:       2 * time.Minute,
	})
	if err != nil {
		return nil, fmt.Errorf("create landed consumer: %w", err)
	}
	cc, err := cons.Consume(func(msg jetstream.Msg) {
		l.handle(ctx, msg)
	})
	if err != nil {
		return nil, fmt.Errorf("consume landed: %w", err)
	}
	return cc.Stop, nil
}

// handle processes one landed-object message.
func (l *Landed) handle(ctx context.Context, msg jetstream.Msg) {
	ev, err := events.ParseUploadLanded(msg.Data())
	if err != nil {
		// Contract violation: reject permanently, never coerce (S1).
		l.log.Error("invalid landed event, terminating", "err", err)
		_ = msg.Term()
		return
	}
	ok, err := l.objects.Exists(ctx, ev.ObjectKey)
	if err != nil {
		l.log.Warn("object store check failed, will retry", "key", ev.ObjectKey, "err", err)
		_ = msg.NakWithDelay(10 * time.Second)
		return
	}
	if !ok {
		// The object may still be settling on shared storage; retry.
		l.log.Warn("landed object not yet visible, will retry", "key", ev.ObjectKey)
		_ = msg.NakWithDelay(10 * time.Second)
		return
	}

	// Download the source to a local scratch file for probing; ffprobe reads
	// a local file, not an S3 URL, and only the derived source key is ever
	// fetched (S4).
	scratchDir, err := os.MkdirTemp(l.cfg.ScratchDir, "probe-")
	if err != nil {
		l.log.Warn("scratch dir failed, will retry", "key", ev.ObjectKey, "err", err)
		_ = msg.NakWithDelay(10 * time.Second)
		return
	}
	defer os.RemoveAll(scratchDir)
	localPath := filepath.Join(scratchDir, "source")
	if err := l.objects.Download(ctx, ev.ObjectKey, localPath); err != nil {
		l.log.Warn("download source failed, will retry", "key", ev.ObjectKey, "err", err)
		_ = msg.NakWithDelay(10 * time.Second)
		return
	}

	probeCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	mi, err := l.eng.Probe(probeCtx, localPath)
	if err != nil {
		// A structurally unreadable asset will not become readable on
		// redelivery, but transient conditions might; distinguish by stage.
		var engErr *engine.Error
		if errors.As(err, &engErr) && engErr.Stage == engine.StageAsset {
			l.log.Error("landed object failed probe, terminating", "key", ev.ObjectKey, "err", err)
			_ = msg.Term()
			return
		}
		l.log.Warn("probe failed, will retry", "key", ev.ObjectKey, "err", err)
		_ = msg.NakWithDelay(30 * time.Second)
		return
	}
	err = l.sources.UpsertSource(ctx, store.Source{
		ObjectKey:   ev.ObjectKey,
		WorkspaceID: ev.WorkspaceID,
		SHA256:      ev.SHA256,
		SizeBytes:   ev.SizeBytes,
		Mime:        ev.Mime,
		MediaInfo:   mi,
	})
	if err != nil {
		l.log.Warn("persist source failed, will retry", "key", ev.ObjectKey, "err", err)
		_ = msg.NakWithDelay(10 * time.Second)
		return
	}
	l.log.Info("source probed", "key", ev.ObjectKey, "durationSeconds", mi.DurationSeconds)

	if l.cfg.AutoCreateJobs {
		if !l.autoCreateJob(ctx, ev, mi) {
			// Transient failure creating the job: retry the whole message.
			// UpsertSource is idempotent, so re-probing on redelivery is safe.
			_ = msg.NakWithDelay(10 * time.Second)
			return
		}
	}
	_ = msg.Ack()
}

// autoCreateJob creates a queued transcode job for the landed source using
// the workspace default preset and emits the job_queued metering event. It
// returns true when the message may be acked: on success, and also when the
// workspace has no default preset (a permanent condition we do not retry; the
// user can create a preset and job explicitly). It returns false only on a
// transient error worth retrying.
func (l *Landed) autoCreateJob(ctx context.Context, ev events.UploadLanded, _ engine.MediaInfo) bool {
	preset, err := l.sources.DefaultPreset(ctx, ev.WorkspaceID)
	if errors.Is(err, store.ErrNotFound) {
		// No preset to run: not a transient failure. Ack and move on; the
		// source is probed and a job can be created explicitly later.
		l.log.Warn("auto-create skipped: workspace has no default preset",
			"workspace", ev.WorkspaceID, "key", ev.ObjectKey)
		return true
	}
	if err != nil {
		l.log.Warn("auto-create: default preset lookup failed, will retry",
			"workspace", ev.WorkspaceID, "err", err)
		return false
	}
	j, err := l.sources.CreateJob(ctx, jobs.Job{
		WorkspaceID:     ev.WorkspaceID,
		UserID:          ev.UserID,
		PresetID:        preset.ID,
		SourceObjectKey: ev.ObjectKey,
		SourceSHA256:    ev.SHA256,
	})
	if err != nil {
		l.log.Warn("auto-create: create job failed, will retry", "key", ev.ObjectKey, "err", err)
		return false
	}
	l.log.Info("auto-created transcode job on landing",
		"job", j.ID, "workspace", ev.WorkspaceID, "preset", preset.ID, "key", ev.ObjectKey)

	// Emit job_queued metering (contract 2) and publish the queued state for
	// FT-4; both are best-effort and never fail the ack (mirrors the API path).
	if l.meter != nil {
		if err := l.meter.Emit(ctx, j.WorkspaceID, j.UserID, events.MeterJobQueued, j.ID, nil, nil); err != nil {
			l.log.Warn("auto-create: emit job_queued metering", "job", j.ID, "err", err)
		}
	}
	if l.progress != nil {
		if err := l.progress.Publish(events.JobProgress{
			JobID:       j.ID,
			State:       string(jobs.StateQueued),
			ProgressPct: j.ProgressPct,
		}); err != nil {
			l.log.Warn("auto-create: publish queued state", "job", j.ID, "err", err)
		}
	}
	if l.runner != nil {
		l.runner.Wake()
	}
	return true
}
