// services/orchestrator/internal/httpapi/api_test.go
//
// API tests with doubles at the Store, Runner, Meter and quota seams: auth
// enforcement, strict input validation (S1), quota admission denial, retry
// and cancel semantics, and preset CRUD validation.
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/evemeta-tony/aether-edit/services/contracts"

	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/auth"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/events"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/jobs"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/store"
)

const (
	testSecret = "test-secret-0123456789abcdef"
	testWS     = "ws1"
	goodSHA    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	goodKey    = "assets/ws1/sha256/" + goodSHA
	presetID   = "0190e3a0-2222-7abc-8def-0123456789ab"
	jobID      = "0190e3a0-3333-7abc-8def-0123456789ab"
)

// fakeStore implements Store.
type fakeStore struct {
	sources map[string]store.Source
	presets map[string]jobs.Preset
	jobsMap map[string]jobs.Job
	created []jobs.Job
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		sources: map[string]store.Source{},
		presets: map[string]jobs.Preset{},
		jobsMap: map[string]jobs.Job{},
	}
}

func (f *fakeStore) GetSource(_ context.Context, ws, key string) (store.Source, error) {
	s, ok := f.sources[ws+"/"+key]
	if !ok {
		return s, store.ErrNotFound
	}
	return s, nil
}

func (f *fakeStore) CreateJob(_ context.Context, j jobs.Job) (jobs.Job, error) {
	j.ID = jobID
	j.State = jobs.StateQueued
	j.CreatedAt = time.Now()
	f.created = append(f.created, j)
	f.jobsMap[j.ID] = j
	return j, nil
}

func (f *fakeStore) GetJob(_ context.Context, ws, id string) (jobs.Job, error) {
	j, ok := f.jobsMap[id]
	if !ok || j.WorkspaceID != ws {
		return j, store.ErrNotFound
	}
	return j, nil
}

func (f *fakeStore) ListJobs(_ context.Context, ws string, state *jobs.State, _ int) ([]jobs.Job, error) {
	out := []jobs.Job{}
	for _, j := range f.jobsMap {
		if j.WorkspaceID != ws {
			continue
		}
		if state != nil && j.State != *state {
			continue
		}
		out = append(out, j)
	}
	return out, nil
}

func (f *fakeStore) RetryJob(_ context.Context, ws, id string) (jobs.Job, error) {
	j, ok := f.jobsMap[id]
	if !ok || j.WorkspaceID != ws {
		return j, store.ErrNotFound
	}
	if j.State != jobs.StateFailed {
		return j, store.ErrConflict
	}
	j.State = jobs.StateQueued
	f.jobsMap[id] = j
	return j, nil
}

func (f *fakeStore) CancelQueued(_ context.Context, ws, id string) (jobs.Job, error) {
	j, ok := f.jobsMap[id]
	if !ok || j.WorkspaceID != ws {
		return j, store.ErrNotFound
	}
	if j.State != jobs.StateQueued {
		return j, store.ErrConflict
	}
	j.State = jobs.StateFailed
	j.ErrorClass = jobs.ErrorInternal
	j.ErrorMessage = "canceled by user"
	f.jobsMap[id] = j
	return j, nil
}

func (f *fakeStore) CreatePreset(_ context.Context, p jobs.Preset) (jobs.Preset, error) {
	p.ID = presetID
	f.presets[p.ID] = p
	return p, nil
}

func (f *fakeStore) GetPreset(_ context.Context, ws, id string) (jobs.Preset, error) {
	p, ok := f.presets[id]
	if !ok || p.WorkspaceID != ws {
		return p, store.ErrNotFound
	}
	return p, nil
}

func (f *fakeStore) ListPresets(_ context.Context, ws string) ([]jobs.Preset, error) {
	out := []jobs.Preset{}
	for _, p := range f.presets {
		if p.WorkspaceID == ws {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakeStore) UpdatePreset(_ context.Context, p jobs.Preset) (jobs.Preset, error) {
	if _, ok := f.presets[p.ID]; !ok {
		return p, store.ErrNotFound
	}
	f.presets[p.ID] = p
	return p, nil
}

// fakeRunner implements Runner.
type fakeRunner struct {
	woken    int
	cancels  []string
	canCancl bool
}

func (r *fakeRunner) Wake() { r.woken++ }
func (r *fakeRunner) Cancel(id string) bool {
	r.cancels = append(r.cancels, id)
	return r.canCancl
}

// fakeQuota implements contracts.QuotaChecker.
type fakeQuota struct {
	allow  bool
	reason string
}

func (q fakeQuota) CheckUploadSession(context.Context, string, int64) (contracts.QuotaDecision, error) {
	return contracts.QuotaDecision{Allowed: true}, nil
}

func (q fakeQuota) CheckJobAdmission(context.Context, string) (contracts.QuotaDecision, error) {
	return contracts.QuotaDecision{Allowed: q.allow, Reason: q.reason}, nil
}

// fakeMeter implements Meter.
type fakeMeter struct{ kinds []events.MeteringKind }

func (m *fakeMeter) Emit(_ context.Context, _, _ string, kind events.MeteringKind, _ string, _ *int64, _ *float64) error {
	m.kinds = append(m.kinds, kind)
	return nil
}

// fakeSink implements ProgressSink.
type fakeSink struct{ states []string }

func (s *fakeSink) Publish(ev events.JobProgress) error {
	s.states = append(s.states, ev.JobID+":"+ev.State)
	return nil
}

func token(t *testing.T, ws string) string {
	t.Helper()
	claims := jwt.MapClaims{
		"sub":         "user-7",
		"workspaceId": ws,
		"exp":         time.Now().Add(time.Hour).Unix(),
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

type fixture struct {
	store  *fakeStore
	runner *fakeRunner
	meter  *fakeMeter
	sink   *fakeSink
	srv    *httptest.Server
}

func newFixture(t *testing.T, quota contracts.QuotaChecker) *fixture {
	t.Helper()
	st := newFakeStore()
	st.sources[testWS+"/"+goodKey] = store.Source{ObjectKey: goodKey, WorkspaceID: testWS, SHA256: goodSHA}
	p := jobs.Preset{
		ID: presetID, WorkspaceID: testWS, Name: "p",
		Container: jobs.ContainerMP4, VideoCodec: jobs.CodecH264,
		RateControl: jobs.RateControlCRF, CRF: 23, GOPLength: 48, SpeedPreset: "p5",
		Ladder: []jobs.Rung{{Name: "720p", Width: 1280, Height: 720}},
	}
	st.presets[presetID] = p
	runner := &fakeRunner{}
	meter := &fakeMeter{}
	sink := &fakeSink{}
	v, err := auth.NewVerifier(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	api := New(st, runner, quota, meter, sink, nil)
	srv := httptest.NewServer(api.Handler(v))
	t.Cleanup(srv.Close)
	return &fixture{store: st, runner: runner, meter: meter, sink: sink, srv: srv}
}

func (f *fixture) do(t *testing.T, method, path, tok, body string) (*http.Response, string) {
	t.Helper()
	var req *http.Request
	var err error
	if body != "" {
		req, err = http.NewRequest(method, f.srv.URL+path, strings.NewReader(body))
	} else {
		req, err = http.NewRequest(method, f.srv.URL+path, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp, sb.String()
}

func TestAuthRequired(t *testing.T) {
	f := newFixture(t, fakeQuota{allow: true})
	resp, _ := f.do(t, "GET", "/v1/jobs", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: status %d, want 401", resp.StatusCode)
	}
	bad := token(t, testWS) + "tampered"
	resp, _ = f.do(t, "GET", "/v1/jobs", bad, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad token: status %d, want 401", resp.StatusCode)
	}
}

func TestCreateJobHappyPath(t *testing.T) {
	f := newFixture(t, fakeQuota{allow: true})
	resp, body := f.do(t, "POST", "/v1/jobs", token(t, testWS),
		`{"objectKey":"`+goodKey+`","presetId":"`+presetID+`"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, body %s", resp.StatusCode, body)
	}
	if len(f.store.created) != 1 {
		t.Fatal("job not created")
	}
	j := f.store.created[0]
	if j.WorkspaceID != testWS || j.UserID != "user-7" || j.SourceSHA256 != goodSHA {
		t.Errorf("created job wrong: %+v", j)
	}
	if f.runner.woken != 1 {
		t.Error("scheduler not woken")
	}
	if len(f.meter.kinds) != 1 || f.meter.kinds[0] != events.MeterJobQueued {
		t.Errorf("metering kinds = %v", f.meter.kinds)
	}
	if len(f.sink.states) != 1 || !strings.HasSuffix(f.sink.states[0], ":queued") {
		t.Errorf("progress states = %v", f.sink.states)
	}
}

func TestCreateJobValidation(t *testing.T) {
	f := newFixture(t, fakeQuota{allow: true})
	tok := token(t, testWS)
	cases := []struct {
		name string
		body string
		want int
	}{
		{"unknown field", `{"objectKey":"` + goodKey + `","presetId":"` + presetID + `","nice":1}`, 400},
		{"bad key shape", `{"objectKey":"assets/ws1/md5/abc","presetId":"` + presetID + `"}`, 400},
		{"foreign workspace key", `{"objectKey":"assets/ws2/sha256/` + goodSHA + `","presetId":"` + presetID + `"}`, 403},
		{"bad preset id", `{"objectKey":"` + goodKey + `","presetId":"nope"}`, 400},
		{"unprobed source", `{"objectKey":"assets/ws1/sha256/` + strings.Repeat("b", 64) + `","presetId":"` + presetID + `"}`, 422},
		{"trailing data", `{"objectKey":"` + goodKey + `","presetId":"` + presetID + `"} extra`, 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := f.do(t, "POST", "/v1/jobs", tok, tc.body)
			if resp.StatusCode != tc.want {
				t.Fatalf("status %d, want %d (body %s)", resp.StatusCode, tc.want, body)
			}
		})
	}
	if len(f.store.created) != 0 {
		t.Error("invalid requests must not create jobs")
	}
}

func TestCreateJobAdmissionDenied(t *testing.T) {
	f := newFixture(t, fakeQuota{allow: false, reason: "quota_exceeded:max_active_jobs (3 of 3 active)"})
	resp, body := f.do(t, "POST", "/v1/jobs", token(t, testWS),
		`{"objectKey":"`+goodKey+`","presetId":"`+presetID+`"}`)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status %d, want 429 (body %s)", resp.StatusCode, body)
	}
	if !strings.Contains(body, "quota_exceeded:max_active_jobs") {
		t.Errorf("deny reason not surfaced: %s", body)
	}
	if len(f.store.created) != 0 {
		t.Error("denied admission must not create a job")
	}
	if len(f.meter.kinds) != 0 {
		t.Error("denied admission must not emit job_queued")
	}
}

func TestRetrySemantics(t *testing.T) {
	f := newFixture(t, fakeQuota{allow: true})
	tok := token(t, testWS)
	f.store.jobsMap[jobID] = jobs.Job{ID: jobID, WorkspaceID: testWS, State: jobs.StateFailed}
	resp, _ := f.do(t, "POST", "/v1/jobs/"+jobID+"/retry", tok, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("retry failed job: status %d", resp.StatusCode)
	}
	// Now queued: a second retry must conflict.
	resp, _ = f.do(t, "POST", "/v1/jobs/"+jobID+"/retry", tok, "")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("retry non-failed job: status %d, want 409", resp.StatusCode)
	}
	resp, _ = f.do(t, "POST", "/v1/jobs/"+strings.Replace(jobID, "3333", "9999", 1)+"/retry", tok, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("retry unknown job: status %d, want 404", resp.StatusCode)
	}
}

func TestCancelSemantics(t *testing.T) {
	f := newFixture(t, fakeQuota{allow: true})
	tok := token(t, testWS)

	f.store.jobsMap[jobID] = jobs.Job{ID: jobID, WorkspaceID: testWS, State: jobs.StateQueued}
	resp, body := f.do(t, "DELETE", "/v1/jobs/"+jobID, tok, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel queued: status %d", resp.StatusCode)
	}
	if !strings.Contains(body, "canceled by user") {
		t.Errorf("cancel body missing terminal shape: %s", body)
	}

	// Terminal jobs cannot be canceled.
	resp, _ = f.do(t, "DELETE", "/v1/jobs/"+jobID, tok, "")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("cancel failed job: status %d, want 409", resp.StatusCode)
	}

	// Running jobs cancel through the scheduler.
	f.runner.canCancl = true
	f.store.jobsMap[jobID] = jobs.Job{ID: jobID, WorkspaceID: testWS, State: jobs.StateRunning}
	resp, _ = f.do(t, "DELETE", "/v1/jobs/"+jobID, tok, "")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("cancel running: status %d, want 202", resp.StatusCode)
	}
	if len(f.runner.cancels) != 1 || f.runner.cancels[0] != jobID {
		t.Errorf("scheduler cancel not delivered: %v", f.runner.cancels)
	}

	// Workspace scoping: another workspace's token sees 404.
	resp, _ = f.do(t, "DELETE", "/v1/jobs/"+jobID, token(t, "ws2"), "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-workspace cancel: status %d, want 404", resp.StatusCode)
	}
}

func TestListJobsStateFilterValidation(t *testing.T) {
	f := newFixture(t, fakeQuota{allow: true})
	resp, _ := f.do(t, "GET", "/v1/jobs?state=paused", token(t, testWS), "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad state filter: status %d, want 400", resp.StatusCode)
	}
	f.store.jobsMap[jobID] = jobs.Job{ID: jobID, WorkspaceID: testWS, State: jobs.StateFailed}
	resp, body := f.do(t, "GET", "/v1/jobs?state=failed", token(t, testWS), "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, jobID) {
		t.Fatalf("state filter list: status %d body %s", resp.StatusCode, body)
	}
}

func TestPresetCRUDAndPatchValidation(t *testing.T) {
	f := newFixture(t, fakeQuota{allow: true})
	tok := token(t, testWS)

	resp, body := f.do(t, "POST", "/v1/presets", tok, `{
		"name": "hls-hevc", "container": "hls", "videoCodec": "hevc",
		"rateControl": "vbr", "bitrateKbps": 4000, "maxBitrateKbps": 6000,
		"gopLength": 96, "speedPreset": "p4",
		"ladder": [{"name": "1080p", "width": 1920, "height": 1080}]
	}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create preset: status %d body %s", resp.StatusCode, body)
	}

	resp, _ = f.do(t, "POST", "/v1/presets", tok, `{
		"name": "bad", "container": "webm", "videoCodec": "h264",
		"rateControl": "crf", "crf": 23, "gopLength": 48, "speedPreset": "p5",
		"ladder": [{"name": "1080p", "width": 1920, "height": 1080}]
	}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid preset accepted: status %d", resp.StatusCode)
	}

	// Patch: consistent rate control change passes.
	resp, body = f.do(t, "PATCH", "/v1/presets/"+presetID, tok,
		`{"rateControl": "cbr", "crf": 0, "bitrateKbps": 5000, "maxBitrateKbps": 0}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch preset: status %d body %s", resp.StatusCode, body)
	}
	var patched jobs.Preset
	if err := json.Unmarshal([]byte(body), &patched); err != nil {
		t.Fatal(err)
	}
	if patched.RateControl != jobs.RateControlCBR || patched.BitrateKbps != 5000 {
		t.Errorf("patched preset wrong: %+v", patched)
	}

	// Patch: inconsistent merge is rejected as a whole.
	resp, _ = f.do(t, "PATCH", "/v1/presets/"+presetID, tok, `{"rateControl": "crf"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("inconsistent patch accepted: status %d", resp.StatusCode)
	}

	// Patch: unknown field rejected.
	resp, _ = f.do(t, "PATCH", "/v1/presets/"+presetID, tok, `{"surprise": true}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown patch field accepted: status %d", resp.StatusCode)
	}
}
