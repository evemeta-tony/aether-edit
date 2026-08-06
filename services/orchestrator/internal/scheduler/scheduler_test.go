// services/orchestrator/internal/scheduler/scheduler_test.go
//
// Scheduler tests with in-test doubles at the Store, ObjectStore and
// TranscodeEngine seams: slot accounting under concurrent completion,
// preset snapshot-at-start semantics, cancellation, and failure taxonomy
// propagation.
package scheduler

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/engine"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/events"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/jobs"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/store"
)

// ---- doubles ----

type fakeStore struct {
	mu        sync.Mutex
	queued    []jobs.Job
	presets   map[string]jobs.Preset
	sources   map[string]store.Source
	completed map[string][]jobs.OutputProgress
	failed    map[string]jobs.ErrorClass
	failMsgs  map[string]string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		presets:   map[string]jobs.Preset{},
		sources:   map[string]store.Source{},
		completed: map[string][]jobs.OutputProgress{},
		failed:    map[string]jobs.ErrorClass{},
		failMsgs:  map[string]string{},
	}
}

func (f *fakeStore) ClaimNextQueued(context.Context) (jobs.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.queued) == 0 {
		return jobs.Job{}, store.ErrNotFound
	}
	j := f.queued[0]
	f.queued = f.queued[1:]
	j.State = jobs.StateRunning
	return j, nil
}

func (f *fakeStore) GetPreset(_ context.Context, _, id string) (jobs.Preset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.presets[id]
	if !ok {
		return p, store.ErrNotFound
	}
	return p, nil
}

func (f *fakeStore) GetSource(_ context.Context, _, key string) (store.Source, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sources[key]
	if !ok {
		return s, store.ErrNotFound
	}
	return s, nil
}

func (f *fakeStore) UpdateProgress(context.Context, string, float64, float64, float64, float64, []jobs.OutputProgress) error {
	return nil
}

func (f *fakeStore) MarkCompleted(_ context.Context, id string, outputs []jobs.OutputProgress) (jobs.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completed[id] = outputs
	return jobs.Job{ID: id, State: jobs.StateCompleted}, nil
}

func (f *fakeStore) MarkFailed(_ context.Context, id string, class jobs.ErrorClass, msg string) (jobs.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed[id] = class
	f.failMsgs[id] = msg
	return jobs.Job{ID: id, State: jobs.StateFailed}, nil
}

func (f *fakeStore) enqueue(j jobs.Job) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queued = append(f.queued, j)
}

func (f *fakeStore) completedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.completed)
}

type fakeObjects struct{}

func (fakeObjects) Path(key string) (string, error) { return "/fake/" + key, nil }
func (fakeObjects) Exists(string) (bool, error)     { return true, nil }
func (fakeObjects) PutDir(prefix, _ string) ([]string, error) {
	return []string{prefix + "/artifact.mp4"}, nil
}

type startedSpec struct {
	input string
	spec  engine.OutputSpec
}

type fakeEngine struct {
	mu       sync.Mutex
	cur      int
	maxSeen  int
	release  map[string]chan struct{}
	started  chan startedSpec
	failWith error
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{
		release: map[string]chan struct{}{},
		started: make(chan startedSpec, 64),
	}
}

func (e *fakeEngine) Probe(context.Context, string) (engine.MediaInfo, error) {
	return engine.MediaInfo{DurationSeconds: 30, VideoStreams: 1}, nil
}

func (e *fakeEngine) gate(input string) chan struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	g, ok := e.release[input]
	if !ok {
		g = make(chan struct{})
		e.release[input] = g
	}
	return g
}

func (e *fakeEngine) Transcode(ctx context.Context, input string, spec engine.OutputSpec, _ func(engine.Progress)) error {
	e.mu.Lock()
	e.cur++
	if e.cur > e.maxSeen {
		e.maxSeen = e.cur
	}
	fail := e.failWith
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.cur--
		e.mu.Unlock()
	}()
	e.started <- startedSpec{input: input, spec: spec}
	if fail != nil {
		return fail
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-e.gate(input):
		return nil
	}
}

func (e *fakeEngine) concurrent() (cur, maxSeen int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cur, e.maxSeen
}

type fakeSink struct {
	mu     sync.Mutex
	states []string
}

func (s *fakeSink) Publish(ev events.JobProgress) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states = append(s.states, ev.JobID+":"+ev.State)
	return nil
}

type fakeMeter struct {
	mu    sync.Mutex
	kinds []events.MeteringKind
	enc   []float64
}

func (m *fakeMeter) Emit(_ context.Context, _, _ string, kind events.MeteringKind, _ string, _ *int64, encodeSeconds *float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.kinds = append(m.kinds, kind)
	if encodeSeconds != nil {
		m.enc = append(m.enc, *encodeSeconds)
	}
	return nil
}

// ---- helpers ----

func testPreset(gop int) jobs.Preset {
	return jobs.Preset{
		ID: "preset-1", WorkspaceID: "ws", Name: "p",
		Container: jobs.ContainerMP4, VideoCodec: jobs.CodecH264,
		RateControl: jobs.RateControlCRF, CRF: 23,
		GOPLength: gop, SpeedPreset: "p5",
		Ladder: []jobs.Rung{{Name: "720p", Width: 1280, Height: 720}},
	}
}

func testJob(i int) jobs.Job {
	return jobs.Job{
		ID: fmt.Sprintf("job-%d", i), WorkspaceID: "ws", UserID: "user",
		PresetID: "preset-1", SourceObjectKey: fmt.Sprintf("src-%d", i),
		State: jobs.StateQueued,
	}
}

func setup(t *testing.T, slots int) (*fakeStore, *fakeEngine, *fakeSink, *fakeMeter, *Scheduler, context.CancelFunc) {
	t.Helper()
	st := newFakeStore()
	st.presets["preset-1"] = testPreset(48)
	eng := newFakeEngine()
	sink := &fakeSink{}
	meter := &fakeMeter{}
	s, err := New(Config{
		Slots:                slots,
		PollInterval:         20 * time.Millisecond,
		StagingDir:           t.TempDir(),
		ProgressPersistEvery: time.Millisecond,
	}, st, fakeObjects{}, eng, sink, meter, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = s.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("scheduler did not stop")
		}
	})
	return st, eng, sink, meter, s, cancel
}

func waitStarted(t *testing.T, eng *fakeEngine, n int) []startedSpec {
	t.Helper()
	var got []startedSpec
	deadline := time.After(5 * time.Second)
	for len(got) < n {
		select {
		case s := <-eng.started:
			got = append(got, s)
		case <-deadline:
			t.Fatalf("timed out waiting for %d starts, got %d", n, len(got))
		}
	}
	return got
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// ---- tests ----

func TestSlotAccountingUnderConcurrentCompletion(t *testing.T) {
	st, eng, _, _, _, _ := setup(t, 3)
	for i := 0; i < 6; i++ {
		s := testJob(i)
		st.sources[s.SourceObjectKey] = store.Source{
			ObjectKey: s.SourceObjectKey, WorkspaceID: "ws",
			MediaInfo: engine.MediaInfo{DurationSeconds: 30, VideoStreams: 1, AudioStreams: 1},
		}
		st.enqueue(s)
	}

	started := waitStarted(t, eng, 3)
	// All three slots busy; no fourth start may occur while they block.
	time.Sleep(100 * time.Millisecond)
	if cur, _ := eng.concurrent(); cur != 3 {
		t.Fatalf("concurrent = %d, want 3", cur)
	}
	select {
	case s := <-eng.started:
		t.Fatalf("fourth job %s started with all slots busy", s.input)
	default:
	}

	// Complete one job; exactly one more must start (slot handoff).
	close(eng.gate(started[0].input))
	next := waitStarted(t, eng, 1)
	if next[0].input == started[0].input {
		t.Fatal("released job restarted")
	}

	// Complete several concurrently; remaining jobs flow through.
	close(eng.gate(started[1].input))
	close(eng.gate(started[2].input))
	close(eng.gate(next[0].input))
	rest := waitStarted(t, eng, 2)
	for _, r := range rest {
		close(eng.gate(r.input))
	}
	waitFor(t, "all 6 completions", func() bool { return st.completedCount() == 6 })
	if _, maxSeen := eng.concurrent(); maxSeen > 3 {
		t.Fatalf("max concurrent = %d, exceeded 3 slots", maxSeen)
	}
}

func TestPresetSnapshotAtStart(t *testing.T) {
	st, eng, _, _, sched, _ := setup(t, 1)
	j0, j1 := testJob(0), testJob(1)
	for _, j := range []jobs.Job{j0, j1} {
		st.sources[j.SourceObjectKey] = store.Source{
			ObjectKey: j.SourceObjectKey, WorkspaceID: "ws",
			MediaInfo: engine.MediaInfo{DurationSeconds: 30, VideoStreams: 1},
		}
	}
	st.enqueue(j0)
	sched.Wake()
	first := waitStarted(t, eng, 1)[0]
	if first.spec.GOPLength != 48 {
		t.Fatalf("first job GOP = %d, want 48", first.spec.GOPLength)
	}

	// Edit the preset while job-0 is mid-encode, then queue job-1.
	st.mu.Lock()
	st.presets["preset-1"] = testPreset(120)
	st.mu.Unlock()
	st.enqueue(j1)

	// Finishing job-0 frees the slot; job-1 must start with the EDITED
	// preset while job-0 ran to completion on its original snapshot.
	close(eng.gate(first.input))
	second := waitStarted(t, eng, 1)[0]
	if second.spec.GOPLength != 120 {
		t.Fatalf("subsequently started job GOP = %d, want 120 (edit applies)", second.spec.GOPLength)
	}
	close(eng.gate(second.input))
	waitFor(t, "both completions", func() bool { return st.completedCount() == 2 })
}

func TestCancelRunningJob(t *testing.T) {
	st, eng, _, _, sched, _ := setup(t, 1)
	j := testJob(0)
	st.sources[j.SourceObjectKey] = store.Source{
		ObjectKey: j.SourceObjectKey, WorkspaceID: "ws",
		MediaInfo: engine.MediaInfo{DurationSeconds: 30, VideoStreams: 1},
	}
	st.enqueue(j)
	sched.Wake()
	waitStarted(t, eng, 1)

	if !sched.Cancel(j.ID) {
		t.Fatal("Cancel must find the running job")
	}
	waitFor(t, "job failed", func() bool {
		st.mu.Lock()
		defer st.mu.Unlock()
		_, ok := st.failed[j.ID]
		return ok
	})
	st.mu.Lock()
	if st.failed[j.ID] != jobs.ErrorInternal || st.failMsgs[j.ID] != "canceled by user" {
		st.mu.Unlock()
		t.Fatalf("cancel shape wrong: class=%s msg=%q", st.failed[j.ID], st.failMsgs[j.ID])
	}
	st.mu.Unlock()
	// Once the runner finishes unwinding, the cancel handle must be gone.
	waitFor(t, "cancel handle removal", func() bool { return !sched.Cancel(j.ID) })
}

func TestEngineFailureMapsTaxonomyAndMeters(t *testing.T) {
	st, eng, sink, meter, sched, _ := setup(t, 1)
	eng.failWith = &engine.Error{Stage: engine.StageDecode, Message: "bitstream corrupt"}
	j := testJob(0)
	st.sources[j.SourceObjectKey] = store.Source{
		ObjectKey: j.SourceObjectKey, WorkspaceID: "ws",
		MediaInfo: engine.MediaInfo{DurationSeconds: 30, VideoStreams: 1},
	}
	st.enqueue(j)
	sched.Wake()
	waitFor(t, "job failed", func() bool {
		st.mu.Lock()
		defer st.mu.Unlock()
		return st.failed[j.ID] == jobs.ErrorDecode
	})
	waitFor(t, "job_failed metering", func() bool {
		meter.mu.Lock()
		defer meter.mu.Unlock()
		for _, k := range meter.kinds {
			if k == events.MeterJobFailed {
				return true
			}
		}
		return false
	})
	sink.mu.Lock()
	defer sink.mu.Unlock()
	found := false
	for _, s := range sink.states {
		if s == j.ID+":failed" {
			found = true
		}
	}
	if !found {
		t.Errorf("failed state transition not published: %v", sink.states)
	}
}

func TestCompletionEmitsEncodeSeconds(t *testing.T) {
	st, eng, _, meter, sched, _ := setup(t, 1)
	j := testJob(0)
	st.sources[j.SourceObjectKey] = store.Source{
		ObjectKey: j.SourceObjectKey, WorkspaceID: "ws",
		MediaInfo: engine.MediaInfo{DurationSeconds: 30, VideoStreams: 1},
	}
	st.enqueue(j)
	sched.Wake()
	s := waitStarted(t, eng, 1)[0]
	close(eng.gate(s.input))
	waitFor(t, "completion", func() bool { return st.completedCount() == 1 })
	waitFor(t, "encodeSeconds metering", func() bool {
		meter.mu.Lock()
		defer meter.mu.Unlock()
		return len(meter.enc) == 1 && meter.enc[0] >= 0
	})
	st.mu.Lock()
	defer st.mu.Unlock()
	outs := st.completed[j.ID]
	if len(outs) != 1 || outs[0].State != "completed" || outs[0].ObjectKey == "" {
		t.Fatalf("output progress wrong: %+v", outs)
	}
}
