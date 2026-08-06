// services/orchestrator/internal/httpapi/api.go
//
// HTTP API for the transcode job service. All input is validated at the
// boundary against strict schemas (S1): unknown fields, wrong types, and
// out-of-range values are rejected with 4xx, never coerced. Every route is
// behind the bearer auth middleware (same contract as FT-2); all reads and
// writes are scoped to the caller's workspace claim.
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/evemeta-tony/aether-edit/services/contracts"

	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/auth"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/events"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/jobs"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/store"
)

// Store is the persistence surface the API needs. *store.Postgres
// implements it; tests substitute a double.
type Store interface {
	GetSource(ctx context.Context, workspaceID, objectKey string) (store.Source, error)
	CreateJob(ctx context.Context, j jobs.Job) (jobs.Job, error)
	GetJob(ctx context.Context, workspaceID, id string) (jobs.Job, error)
	ListJobs(ctx context.Context, workspaceID string, state *jobs.State, limit int) ([]jobs.Job, error)
	RetryJob(ctx context.Context, workspaceID, id string) (jobs.Job, error)
	CancelQueued(ctx context.Context, workspaceID, id string) (jobs.Job, error)
	CreatePreset(ctx context.Context, p jobs.Preset) (jobs.Preset, error)
	GetPreset(ctx context.Context, workspaceID, id string) (jobs.Preset, error)
	ListPresets(ctx context.Context, workspaceID string) ([]jobs.Preset, error)
	UpdatePreset(ctx context.Context, p jobs.Preset) (jobs.Preset, error)
}

// Runner is the scheduler surface the API needs (wake on enqueue, cancel of
// running jobs on this farm-of-one node).
type Runner interface {
	Wake()
	Cancel(jobID string) bool
}

// Meter emits contract 2 metering events.
type Meter interface {
	Emit(ctx context.Context, workspaceID, userID string, kind events.MeteringKind, jobID string, bytes *int64, encodeSeconds *float64) error
}

// ProgressSink publishes job state transitions for FT-4.
type ProgressSink interface {
	Publish(ev events.JobProgress) error
}

// API bundles the handler dependencies.
type API struct {
	store    Store
	runner   Runner
	quota    contracts.QuotaChecker
	meter    Meter
	progress ProgressSink
	log      *slog.Logger
}

// New builds the API.
func New(st Store, runner Runner, quota contracts.QuotaChecker, meter Meter, progress ProgressSink, log *slog.Logger) *API {
	if log == nil {
		log = slog.Default()
	}
	return &API{store: st, runner: runner, quota: quota, meter: meter, progress: progress, log: log}
}

// Handler mounts all routes behind the auth middleware.
func (a *API) Handler(v *auth.Verifier) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/jobs", a.listJobs)
	mux.HandleFunc("POST /v1/jobs", a.createJob)
	mux.HandleFunc("GET /v1/jobs/{id}", a.getJob)
	mux.HandleFunc("POST /v1/jobs/{id}/retry", a.retryJob)
	mux.HandleFunc("DELETE /v1/jobs/{id}", a.cancelJob)
	mux.HandleFunc("GET /v1/presets", a.listPresets)
	mux.HandleFunc("POST /v1/presets", a.createPreset)
	mux.HandleFunc("GET /v1/presets/{id}", a.getPreset)
	mux.HandleFunc("PATCH /v1/presets/{id}", a.patchPreset)
	return v.Middleware(mux)
}

// identity extracts the authenticated identity; the middleware guarantees
// presence, so absence is a 500.
func (a *API) identity(w http.ResponseWriter, r *http.Request) (auth.Identity, bool) {
	id, ok := auth.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing identity")
		return auth.Identity{}, false
	}
	return id, true
}

// writeJSON writes v with status code.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error body.
func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// decodeStrict reads the body (capped at 1 MiB) into v, rejecting unknown
// fields and trailing data.
func decodeStrict(w http.ResponseWriter, r *http.Request, v any) bool {
	body := http.MaxBytesReader(w, r.Body, 1<<20)
	data, err := readAll(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unreadable request body")
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return false
	}
	if dec.More() {
		writeError(w, http.StatusBadRequest, "invalid request body: trailing data")
		return false
	}
	return true
}

func readAll(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(r)
	return buf.Bytes(), err
}

// publishState best-effort publishes a job state transition for FT-4.
func (a *API) publishState(j jobs.Job) {
	if a.progress == nil {
		return
	}
	err := a.progress.Publish(events.JobProgress{
		JobID:       j.ID,
		State:       string(j.State),
		ProgressPct: j.ProgressPct,
	})
	if err != nil {
		a.log.Warn("publish job state", "job", j.ID, "err", err)
	}
}

// meterEmit best-effort emits a metering event.
func (a *API) meterEmit(ctx context.Context, j jobs.Job, kind events.MeteringKind) {
	if a.meter == nil {
		return
	}
	if err := a.meter.Emit(ctx, j.WorkspaceID, j.UserID, kind, j.ID, nil, nil); err != nil {
		a.log.Warn("emit metering event", "job", j.ID, "kind", kind, "err", err)
	}
}
