// services/orchestrator/internal/consumer/landed_test.go
//
// Consumer tests for DEFECT 3 (upload->job): on a landed source the consumer
// auto-creates a queued transcode job using the workspace default preset and
// emits the job_queued metering event. Guarded by AutoCreateJobs. Doubles are
// used at the SourceStore, Meter, ProgressSink and Runner seams.
package consumer

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/engine"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/events"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/jobs"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/store"
)

type fakeSourceStore struct {
	mu            sync.Mutex
	created       []jobs.Job
	defaultPreset jobs.Preset
	noDefault     bool
	createErr     error
	defaultErr    error
	upserted      []store.Source
	nextJobID     int
	hasActive     bool
	hasActiveErr  error
}

func (f *fakeSourceStore) UpsertSource(_ context.Context, s store.Source) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserted = append(f.upserted, s)
	return nil
}

func (f *fakeSourceStore) DefaultPreset(_ context.Context, _ string) (jobs.Preset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.defaultErr != nil {
		return jobs.Preset{}, f.defaultErr
	}
	if f.noDefault {
		return jobs.Preset{}, store.ErrNotFound
	}
	return f.defaultPreset, nil
}

func (f *fakeSourceStore) HasActiveJobForSource(_ context.Context, _, _, _ string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hasActiveErr != nil {
		return false, f.hasActiveErr
	}
	return f.hasActive, nil
}

func (f *fakeSourceStore) CreateJob(_ context.Context, j jobs.Job) (jobs.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return jobs.Job{}, f.createErr
	}
	f.nextJobID++
	j.ID = "job-" + string(rune('0'+f.nextJobID))
	j.State = jobs.StateQueued
	f.created = append(f.created, j)
	return j, nil
}

type fakeMeter struct {
	mu    sync.Mutex
	kinds []events.MeteringKind
}

func (m *fakeMeter) Emit(_ context.Context, _, _ string, kind events.MeteringKind, _ string, _ *int64, _ *float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.kinds = append(m.kinds, kind)
	return nil
}

type fakeRunner struct {
	mu    sync.Mutex
	woken int
}

func (r *fakeRunner) Wake() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.woken++
}

func landedEvent() events.UploadLanded {
	return events.UploadLanded{
		UploadID:    "0190e3a0-1111-7abc-8def-0123456789ab",
		WorkspaceID: "ws1",
		UserID:      "user-7",
		ObjectKey:   "assets/ws1/sha256/" + goodSHA,
		SHA256:      goodSHA,
		SizeBytes:   1024,
		Mime:        "video/mp4",
		LandedAt:    time.Now(),
	}
}

const goodSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func testDefaultPreset() jobs.Preset {
	return jobs.Preset{
		ID: "0190e3a0-2222-7abc-8def-0123456789ab", WorkspaceID: "ws1", Name: "default",
		Container: jobs.ContainerMP4, VideoCodec: jobs.CodecH264,
		RateControl: jobs.RateControlCRF, CRF: 23, GOPLength: 48, SpeedPreset: "p5",
		Ladder: []jobs.Rung{{Name: "720p", Width: 1280, Height: 720}},
	}
}

func TestAutoCreateJobHappyPath(t *testing.T) {
	st := &fakeSourceStore{defaultPreset: testDefaultPreset()}
	meter := &fakeMeter{}
	runner := &fakeRunner{}
	l := New(Config{AutoCreateJobs: true}, st, nil, nil, meter, nil, runner, nil)

	ev := landedEvent()
	if ok := l.autoCreateJob(context.Background(), ev, engine.MediaInfo{}); !ok {
		t.Fatal("autoCreateJob returned false on the happy path")
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.created) != 1 {
		t.Fatalf("created %d jobs, want 1", len(st.created))
	}
	j := st.created[0]
	if j.WorkspaceID != ev.WorkspaceID || j.UserID != ev.UserID {
		t.Fatalf("job identity wrong: %+v", j)
	}
	if j.PresetID != testDefaultPreset().ID {
		t.Fatalf("job used preset %q, want the workspace default %q", j.PresetID, testDefaultPreset().ID)
	}
	if j.SourceObjectKey != ev.ObjectKey || j.SourceSHA256 != ev.SHA256 {
		t.Fatalf("job source wrong: %+v", j)
	}
	meter.mu.Lock()
	defer meter.mu.Unlock()
	if len(meter.kinds) != 1 || meter.kinds[0] != events.MeterJobQueued {
		t.Fatalf("metering kinds = %v, want [job_queued]", meter.kinds)
	}
	if runner.woken != 1 {
		t.Fatalf("runner woken %d times, want 1", runner.woken)
	}
}

func TestAutoCreateJobNoDefaultPresetAcks(t *testing.T) {
	st := &fakeSourceStore{noDefault: true}
	meter := &fakeMeter{}
	l := New(Config{AutoCreateJobs: true}, st, nil, nil, meter, nil, nil, nil)

	// No default preset is a permanent condition: return true (ack) but do not
	// create a job or meter.
	if ok := l.autoCreateJob(context.Background(), landedEvent(), engine.MediaInfo{}); !ok {
		t.Fatal("autoCreateJob must ack (return true) when no default preset exists")
	}
	if len(st.created) != 0 {
		t.Fatalf("created %d jobs, want 0", len(st.created))
	}
	if len(meter.kinds) != 0 {
		t.Fatalf("metered %v, want none", meter.kinds)
	}
}

func TestAutoCreateJobTransientCreateErrorRetries(t *testing.T) {
	st := &fakeSourceStore{defaultPreset: testDefaultPreset(), createErr: errors.New("db down")}
	l := New(Config{AutoCreateJobs: true}, st, nil, nil, &fakeMeter{}, nil, nil, nil)

	// A create failure is transient: return false so the message is NAKed.
	if ok := l.autoCreateJob(context.Background(), landedEvent(), engine.MediaInfo{}); ok {
		t.Fatal("autoCreateJob must return false (retry) on a create error")
	}
}

func TestAutoCreateJobIdempotentOnRedelivery(t *testing.T) {
	// A non-failed job already exists for this source+preset (the situation a
	// crash-then-JetStream-redelivery produces): the consumer must NOT create a
	// second job, must NOT re-meter job_queued, and must ack (return true).
	st := &fakeSourceStore{defaultPreset: testDefaultPreset(), hasActive: true}
	meter := &fakeMeter{}
	runner := &fakeRunner{}
	l := New(Config{AutoCreateJobs: true}, st, nil, nil, meter, nil, runner, nil)

	if ok := l.autoCreateJob(context.Background(), landedEvent(), engine.MediaInfo{}); !ok {
		t.Fatal("autoCreateJob must ack (return true) when a job already exists")
	}
	if len(st.created) != 0 {
		t.Fatalf("created %d jobs on redelivery, want 0", len(st.created))
	}
	if len(meter.kinds) != 0 {
		t.Fatalf("metered %v on redelivery, want none", meter.kinds)
	}
	if runner.woken != 0 {
		t.Fatalf("runner woken %d times on redelivery, want 0", runner.woken)
	}
}

func TestAutoCreateJobActiveLookupErrorRetries(t *testing.T) {
	st := &fakeSourceStore{defaultPreset: testDefaultPreset(), hasActiveErr: errors.New("db down")}
	l := New(Config{AutoCreateJobs: true}, st, nil, nil, &fakeMeter{}, nil, nil, nil)

	// The idempotency lookup failing is transient: return false so the message
	// is NAKed and no job is created.
	if ok := l.autoCreateJob(context.Background(), landedEvent(), engine.MediaInfo{}); ok {
		t.Fatal("autoCreateJob must return false (retry) on an active-job lookup error")
	}
	if len(st.created) != 0 {
		t.Fatalf("created %d jobs after lookup error, want 0", len(st.created))
	}
}
