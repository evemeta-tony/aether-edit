// services/orchestrator/internal/scheduler/scheduler.go
//
// Slot-based job scheduler matching the console model: a fixed number of
// concurrent encode slots (default three, configurable). Farm-of-one (T-7):
// this scheduler drives the single local TranscodeEngine on this node; a
// multi-node scheduler that fans jobs out across a farm is explicitly
// deferred to a later work order and is NOT implemented here.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/engine"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/events"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/jobs"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/store"
)

// Store is the persistence surface the scheduler needs. The Postgres store
// implements it; tests substitute a double.
type Store interface {
	ClaimNextQueued(ctx context.Context) (jobs.Job, error)
	GetPreset(ctx context.Context, workspaceID, id string) (jobs.Preset, error)
	GetSource(ctx context.Context, workspaceID, objectKey string) (store.Source, error)
	UpdateProgress(ctx context.Context, id string, pct, fps, speedX, etaSeconds float64, outputs []jobs.OutputProgress) error
	MarkCompleted(ctx context.Context, id string, outputs []jobs.OutputProgress) (jobs.Job, error)
	MarkFailed(ctx context.Context, id string, class jobs.ErrorClass, msg string) (jobs.Job, error)
}

// ObjectStore is the object storage surface the scheduler needs. It is
// backed by the OVH S3 store: the source object is Downloaded to a local
// scratch file (ffmpeg reads local files, not S3 URLs) and encode outputs
// are uploaded from a local staging directory with PutDir.
type ObjectStore interface {
	Exists(ctx context.Context, key string) (bool, error)
	Download(ctx context.Context, key, dstPath string) error
	PutDir(ctx context.Context, keyPrefix, srcDir string) ([]string, error)
}

// ProgressSink receives job state transitions and live progress (FT-4).
type ProgressSink interface {
	Publish(ev events.JobProgress) error
}

// Meter emits contract 2 metering events.
type Meter interface {
	Emit(ctx context.Context, workspaceID, userID string, kind events.MeteringKind, jobID string, bytes *int64, encodeSeconds *float64) error
}

// Config tunes the scheduler.
type Config struct {
	// Slots is the number of concurrent encode slots (console model: 3).
	Slots int
	// PollInterval is the queue re-check period when idle.
	PollInterval time.Duration
	// StagingDir is where encode outputs are staged before moving into the
	// object store.
	StagingDir string
	// ProgressPersistEvery throttles progress writes and publishes.
	ProgressPersistEvery time.Duration
}

// Scheduler claims queued jobs and runs them on the engine.
type Scheduler struct {
	cfg      Config
	store    Store
	objects  ObjectStore
	eng      engine.TranscodeEngine
	progress ProgressSink
	meter    Meter
	log      *slog.Logger

	sem  chan struct{}
	wake chan struct{}
	wg   sync.WaitGroup

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

// New builds a scheduler.
func New(cfg Config, st Store, objects ObjectStore, eng engine.TranscodeEngine, progress ProgressSink, meter Meter, log *slog.Logger) (*Scheduler, error) {
	if cfg.Slots < 1 {
		return nil, fmt.Errorf("scheduler: Slots must be >= 1")
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 2 * time.Second
	}
	if cfg.ProgressPersistEvery <= 0 {
		cfg.ProgressPersistEvery = 500 * time.Millisecond
	}
	if cfg.StagingDir == "" {
		return nil, fmt.Errorf("scheduler: StagingDir is required")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Scheduler{
		cfg:      cfg,
		store:    st,
		objects:  objects,
		eng:      eng,
		progress: progress,
		meter:    meter,
		log:      log,
		sem:      make(chan struct{}, cfg.Slots),
		wake:     make(chan struct{}, 1),
		cancels:  map[string]context.CancelFunc{},
	}, nil
}

// Wake nudges the scheduler to re-check the queue (called after job create
// and retry so admission-to-start latency does not wait on the poll tick).
func (s *Scheduler) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Cancel requests cancellation of a running job. It returns true when the
// job was running on this node and a cancel was delivered.
func (s *Scheduler) Cancel(jobID string) bool {
	s.mu.Lock()
	cancel, ok := s.cancels[jobID]
	s.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// Running reports how many slots are occupied (used by tests and metrics).
func (s *Scheduler) Running() int { return len(s.sem) }

// Run drives the claim loop until ctx is done, then waits for in-flight
// jobs to observe cancellation and return.
func (s *Scheduler) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.cfg.PollInterval)
	defer ticker.Stop()
	for {
		// Acquire a slot.
		select {
		case <-ctx.Done():
			s.wg.Wait()
			return ctx.Err()
		case s.sem <- struct{}{}:
		}

		j, err := s.store.ClaimNextQueued(ctx)
		if err != nil {
			<-s.sem // release the slot
			if !errors.Is(err, store.ErrNotFound) && ctx.Err() == nil {
				s.log.Error("claim queued job", "err", err)
			}
			select {
			case <-ctx.Done():
				s.wg.Wait()
				return ctx.Err()
			case <-s.wake:
			case <-ticker.C:
			}
			continue
		}

		s.wg.Add(1)
		go func(j jobs.Job) {
			defer s.wg.Done()
			defer func() {
				<-s.sem
				s.Wake()
			}()
			s.runJob(ctx, j)
		}(j)
	}
}

// publishState sends a state transition (and final progress numbers) to the
// FT-4 sink. Failures are logged, never fatal to the job.
func (s *Scheduler) publishState(j jobs.Job, state jobs.State, pct, fps, speedX, eta float64) {
	if s.progress == nil {
		return
	}
	err := s.progress.Publish(events.JobProgress{
		JobID:       j.ID,
		State:       string(state),
		FPS:         fps,
		SpeedX:      speedX,
		ETASeconds:  eta,
		ProgressPct: pct,
	})
	if err != nil {
		s.log.Warn("publish job progress", "job", j.ID, "err", err)
	}
}

// meterEmit wraps metering emission with logging (never fails the job).
func (s *Scheduler) meterEmit(ctx context.Context, j jobs.Job, kind events.MeteringKind, encodeSeconds *float64) {
	if s.meter == nil {
		return
	}
	if err := s.meter.Emit(ctx, j.WorkspaceID, j.UserID, kind, j.ID, nil, encodeSeconds); err != nil {
		s.log.Warn("emit metering event", "job", j.ID, "kind", kind, "err", err)
	}
}

// fail finalizes a job as failed and emits the side-channel events.
func (s *Scheduler) fail(ctx context.Context, j jobs.Job, class jobs.ErrorClass, msg string) {
	if _, err := s.store.MarkFailed(ctx, j.ID, class, msg); err != nil {
		s.log.Error("mark job failed", "job", j.ID, "err", err)
	}
	s.publishState(j, jobs.StateFailed, 0, 0, 0, 0)
	s.meterEmit(ctx, j, events.MeterJobFailed, nil)
	s.log.Info("job failed", "job", j.ID, "class", class, "msg", msg)
}

// runJob executes one claimed job end to end.
func (s *Scheduler) runJob(parent context.Context, j jobs.Job) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	s.mu.Lock()
	s.cancels[j.ID] = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.cancels, j.ID)
		s.mu.Unlock()
	}()

	started := time.Now()
	s.publishState(j, jobs.StateRunning, 0, 0, 0, 0)
	s.meterEmit(parent, j, events.MeterJobStarted, nil)

	// Snapshot the preset at start time: edits after this point apply only
	// to subsequently started jobs (documented preset semantic).
	preset, err := s.store.GetPreset(ctx, j.WorkspaceID, j.PresetID)
	if err != nil {
		s.fail(parent, j, jobs.ErrorInternal, fmt.Sprintf("load preset %s: %v", j.PresetID, err))
		return
	}
	src, err := s.store.GetSource(ctx, j.WorkspaceID, j.SourceObjectKey)
	if err != nil {
		s.fail(parent, j, jobs.ErrorAsset, fmt.Sprintf("source %s not probed: %v", j.SourceObjectKey, err))
		return
	}
	if ok, err := s.objects.Exists(ctx, j.SourceObjectKey); err != nil || !ok {
		s.fail(parent, j, jobs.ErrorAsset, fmt.Sprintf("source object %s missing from store", j.SourceObjectKey))
		return
	}
	// Download the source from S3 to a local scratch file: ffmpeg reads a
	// local file path, never an S3 URL, and only the derived source key is
	// ever fetched (S4). The scratch file is removed when the job returns.
	inputDir, err := os.MkdirTemp(s.cfg.StagingDir, "src-"+j.ID+"-")
	if err != nil {
		s.fail(parent, j, jobs.ErrorInternal, fmt.Sprintf("scratch dir: %v", err))
		return
	}
	defer os.RemoveAll(inputDir)
	inputPath := filepath.Join(inputDir, "source")
	if err := s.objects.Download(ctx, j.SourceObjectKey, inputPath); err != nil {
		if ctx.Err() != nil {
			s.fail(parent, j, jobs.ErrorInternal, "canceled by user")
			return
		}
		s.fail(parent, j, jobs.ErrorAsset, fmt.Sprintf("download source %s: %v", j.SourceObjectKey, err))
		return
	}

	outputs := make([]jobs.OutputProgress, len(preset.Ladder))
	for i, r := range preset.Ladder {
		outputs[i] = jobs.OutputProgress{Name: r.Name, State: "pending"}
	}

	total := float64(len(preset.Ladder))
	// lastPersist and outputs are written by onProgress without a lock. That
	// is safe today because the ffmpeg adapter calls onProgress synchronously
	// from ScanProgress on the Transcode goroutine and rungs run
	// sequentially. Any future engine adapter that emits progress from its
	// own goroutine must add synchronization here first (Argus PR#4
	// finding 6 follow-up).
	var lastPersist time.Time
	for i, rung := range preset.Ladder {
		if ctx.Err() != nil {
			s.fail(parent, j, jobs.ErrorInternal, "canceled by user")
			return
		}
		staging, err := os.MkdirTemp(s.cfg.StagingDir, "job-"+j.ID+"-")
		if err != nil {
			s.fail(parent, j, jobs.ErrorInternal, fmt.Sprintf("staging dir: %v", err))
			return
		}
		outputs[i].State = "running"

		spec := engine.OutputSpec{
			RungName:   rung.Name,
			Width:      rung.Width,
			Height:     rung.Height,
			Container:  string(preset.Container),
			VideoCodec: string(preset.VideoCodec),
			RateControl: engine.RateControl{
				Mode:           string(preset.RateControl),
				CRF:            preset.CRF,
				BitrateKbps:    preset.BitrateKbps,
				MaxBitrateKbps: preset.MaxBitrateKbps,
			},
			GOPLength:             preset.GOPLength,
			SpeedPreset:           string(preset.SpeedPreset),
			IncludeAudio:          src.MediaInfo.AudioStreams > 0,
			DestDir:               staging,
			SourceDurationSeconds: src.MediaInfo.DurationSeconds,
		}

		rungIdx := i
		onProgress := func(p engine.Progress) {
			outputs[rungIdx].ProgressPct = p.ProgressPct
			jobPct := (float64(rungIdx)*100 + p.ProgressPct) / total
			// Remaining rungs assumed to take a similar time at current speed.
			eta := p.ETASeconds
			if p.SpeedX > 0 {
				eta += (total - float64(rungIdx) - 1) * (src.MediaInfo.DurationSeconds / p.SpeedX)
			}
			now := time.Now()
			if now.Sub(lastPersist) < s.cfg.ProgressPersistEvery {
				return
			}
			lastPersist = now
			if err := s.store.UpdateProgress(parent, j.ID, jobPct, p.FPS, p.SpeedX, eta, outputs); err != nil {
				s.log.Warn("persist progress", "job", j.ID, "err", err)
			}
			s.publishState(j, jobs.StateRunning, jobPct, p.FPS, p.SpeedX, eta)
		}

		err = s.eng.Transcode(ctx, inputPath, spec, onProgress)
		if err != nil {
			os.RemoveAll(staging)
			if ctx.Err() != nil {
				s.fail(parent, j, jobs.ErrorInternal, "canceled by user")
				return
			}
			class := jobs.ErrorInternal
			var engErr *engine.Error
			if errors.As(err, &engErr) {
				class = jobs.ErrorClass(engErr.Stage)
			}
			outputs[rungIdx].State = "failed"
			s.fail(parent, j, class, fmt.Sprintf("rung %s: %v", rung.Name, err))
			return
		}

		prefix := fmt.Sprintf("outputs/%s/%s/%s", j.WorkspaceID, j.ID, rung.Name)
		keys, err := s.objects.PutDir(parent, prefix, staging)
		os.RemoveAll(staging)
		if err != nil {
			s.fail(parent, j, jobs.ErrorInternal, fmt.Sprintf("store outputs for rung %s: %v", rung.Name, err))
			return
		}
		outputs[rungIdx].State = "completed"
		outputs[rungIdx].ProgressPct = 100
		outputs[rungIdx].ObjectKey = primaryArtifact(keys, prefix, string(preset.Container), rung.Name)
	}

	if _, err := s.store.MarkCompleted(parent, j.ID, outputs); err != nil {
		s.log.Error("mark job completed", "job", j.ID, "err", err)
		return
	}
	encodeSeconds := time.Since(started).Seconds()
	s.publishState(j, jobs.StateCompleted, 100, 0, 0, 0)
	s.meterEmit(parent, j, events.MeterJobCompleted, &encodeSeconds)
	s.log.Info("job completed", "job", j.ID, "encodeSeconds", encodeSeconds)
}

// primaryArtifact picks the representative output key for an output entry:
// the muxed file for mp4, mov and webm, the playlist for hls, the manifest
// for dash; falls back to the first stored key.
func primaryArtifact(keys []string, prefix, container, rungName string) string {
	var want string
	switch container {
	case "mp4":
		want = prefix + "/" + rungName + ".mp4"
	case "mov":
		want = prefix + "/" + rungName + ".mov"
	case "webm":
		want = prefix + "/" + rungName + ".webm"
	case "hls":
		want = prefix + "/" + rungName + ".m3u8"
	case "dash":
		want = prefix + "/" + rungName + ".mpd"
	}
	for _, k := range keys {
		if k == want {
			return k
		}
	}
	if len(keys) > 0 {
		return keys[0]
	}
	return ""
}

// StagingRoot ensures and returns a staging root under dir.
func StagingRoot(dir string) (string, error) {
	p := filepath.Clean(dir)
	if err := os.MkdirAll(p, 0o755); err != nil {
		return "", err
	}
	return p, nil
}
