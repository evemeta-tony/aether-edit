// services/orchestrator/internal/httpapi/jobs.go
//
// Job endpoints: list, get, create (with quota admission), retry (failed
// only), and cancel. Cancel semantics with the frozen four-state set: a
// queued job moves directly to failed with error class internal and message
// "canceled by user"; a running job is canceled through the scheduler and
// finalized by the job runner with the same terminal shape.
package httpapi

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/events"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/jobs"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/store"
)

// listJobs handles GET /v1/jobs with an optional exact state filter.
func (a *API) listJobs(w http.ResponseWriter, r *http.Request) {
	id, ok := a.identity(w, r)
	if !ok {
		return
	}
	var stateFilter *jobs.State
	if raw := r.URL.Query().Get("state"); raw != "" {
		st := jobs.State(raw)
		if !jobs.ValidState(st) {
			writeError(w, http.StatusBadRequest, "state must be one of queued, running, completed, failed")
			return
		}
		stateFilter = &st
	}
	list, err := a.store.ListJobs(r.Context(), id.WorkspaceID, stateFilter, 200)
	if err != nil {
		a.log.Error("list jobs", "err", err)
		writeError(w, http.StatusInternalServerError, "list jobs failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": list})
}

// getJob handles GET /v1/jobs/{id}.
func (a *API) getJob(w http.ResponseWriter, r *http.Request) {
	id, ok := a.identity(w, r)
	if !ok {
		return
	}
	jobID := r.PathValue("id")
	if _, err := uuid.Parse(jobID); err != nil {
		writeError(w, http.StatusBadRequest, "job id must be a uuid")
		return
	}
	j, err := a.store.GetJob(r.Context(), id.WorkspaceID, jobID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		a.log.Error("get job", "err", err)
		writeError(w, http.StatusInternalServerError, "get job failed")
		return
	}
	writeJSON(w, http.StatusOK, j)
}

// createJobRequest is the POST /v1/jobs body.
type createJobRequest struct {
	ObjectKey string `json:"objectKey"`
	PresetID  string `json:"presetId"`
}

// createJob handles POST /v1/jobs: validate, admit via the quota hook,
// enqueue, meter.
func (a *API) createJob(w http.ResponseWriter, r *http.Request) {
	id, ok := a.identity(w, r)
	if !ok {
		return
	}
	var req createJobRequest
	if !decodeStrict(w, r, &req) {
		return
	}
	m := events.ObjectKeyPattern.FindStringSubmatch(req.ObjectKey)
	if m == nil {
		writeError(w, http.StatusBadRequest, "objectKey must match assets/<workspaceId>/sha256/<hex64>")
		return
	}
	if m[1] != id.WorkspaceID {
		writeError(w, http.StatusForbidden, "objectKey belongs to another workspace")
		return
	}
	if _, err := uuid.Parse(req.PresetID); err != nil {
		writeError(w, http.StatusBadRequest, "presetId must be a uuid")
		return
	}

	src, err := a.store.GetSource(r.Context(), id.WorkspaceID, req.ObjectKey)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusUnprocessableEntity, "source is not probed yet (no landed-object record)")
		return
	}
	if err != nil {
		a.log.Error("get source", "err", err)
		writeError(w, http.StatusInternalServerError, "source lookup failed")
		return
	}
	if _, err := a.store.GetPreset(r.Context(), id.WorkspaceID, req.PresetID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusUnprocessableEntity, "preset not found")
			return
		}
		a.log.Error("get preset", "err", err)
		writeError(w, http.StatusInternalServerError, "preset lookup failed")
		return
	}

	decision, err := a.quota.CheckJobAdmission(r.Context(), id.WorkspaceID)
	if err != nil {
		a.log.Error("quota admission", "err", err)
		writeError(w, http.StatusInternalServerError, "quota check failed")
		return
	}
	if !decision.Allowed {
		writeError(w, http.StatusTooManyRequests, decision.Reason)
		return
	}

	j, err := a.store.CreateJob(r.Context(), jobs.Job{
		WorkspaceID:     id.WorkspaceID,
		UserID:          id.UserID,
		PresetID:        req.PresetID,
		SourceObjectKey: req.ObjectKey,
		SourceSHA256:    src.SHA256,
	})
	if err != nil {
		a.log.Error("create job", "err", err)
		writeError(w, http.StatusInternalServerError, "create job failed")
		return
	}
	a.meterEmit(r.Context(), j, events.MeterJobQueued)
	a.publishState(j)
	if a.runner != nil {
		a.runner.Wake()
	}
	writeJSON(w, http.StatusCreated, j)
}

// retryJob handles POST /v1/jobs/{id}/retry: failed -> queued only.
func (a *API) retryJob(w http.ResponseWriter, r *http.Request) {
	id, ok := a.identity(w, r)
	if !ok {
		return
	}
	jobID := r.PathValue("id")
	if _, err := uuid.Parse(jobID); err != nil {
		writeError(w, http.StatusBadRequest, "job id must be a uuid")
		return
	}
	// Retry re-admits a job into the queued+running set, so it runs the same
	// quota admission gate as create; otherwise a workspace at its active-job
	// cap could re-admit failed jobs past the cap (Argus PR#4 finding 8).
	decision, err := a.quota.CheckJobAdmission(r.Context(), id.WorkspaceID)
	if err != nil {
		a.log.Error("quota admission on retry", "err", err)
		writeError(w, http.StatusInternalServerError, "quota check failed")
		return
	}
	if !decision.Allowed {
		writeError(w, http.StatusTooManyRequests, decision.Reason)
		return
	}
	j, err := a.store.RetryJob(r.Context(), id.WorkspaceID, jobID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "only failed jobs can be retried")
		return
	}
	if err != nil {
		a.log.Error("retry job", "err", err)
		writeError(w, http.StatusInternalServerError, "retry failed")
		return
	}
	a.meterEmit(r.Context(), j, events.MeterJobQueued)
	a.publishState(j)
	if a.runner != nil {
		a.runner.Wake()
	}
	writeJSON(w, http.StatusOK, j)
}

// cancelJob handles DELETE /v1/jobs/{id}.
func (a *API) cancelJob(w http.ResponseWriter, r *http.Request) {
	id, ok := a.identity(w, r)
	if !ok {
		return
	}
	jobID := r.PathValue("id")
	if _, err := uuid.Parse(jobID); err != nil {
		writeError(w, http.StatusBadRequest, "job id must be a uuid")
		return
	}
	j, err := a.store.GetJob(r.Context(), id.WorkspaceID, jobID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		a.log.Error("get job for cancel", "err", err)
		writeError(w, http.StatusInternalServerError, "cancel failed")
		return
	}
	switch j.State {
	case jobs.StateRunning:
		// Farm-of-one: the running job lives in this process; deliver the
		// cancel to the scheduler and let the runner finalize the row.
		// 202 means "cancel delivered", not "job will end failed": if the
		// encode finishes in the window between this read and the cancel
		// reaching the runner, the terminal state is completed. The DB
		// transition guards make an illegal completed -> failed flip
		// impossible; callers re-fetch for the terminal state.
		if a.runner != nil && a.runner.Cancel(j.ID) {
			writeJSON(w, http.StatusAccepted, map[string]string{"status": "cancel requested"})
			return
		}
		// The job finished (or was picked up elsewhere) between the read
		// and the cancel; report the conflict honestly.
		writeError(w, http.StatusConflict, "job is not cancelable right now, re-fetch its state")
	case jobs.StateQueued:
		canceled, err := a.store.CancelQueued(r.Context(), id.WorkspaceID, jobID)
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "job left queued state, re-fetch its state")
			return
		}
		if err != nil {
			a.log.Error("cancel queued job", "err", err)
			writeError(w, http.StatusInternalServerError, "cancel failed")
			return
		}
		a.meterEmit(r.Context(), canceled, events.MeterJobFailed)
		a.publishState(canceled)
		writeJSON(w, http.StatusOK, canceled)
	default:
		writeError(w, http.StatusConflict, "completed or failed jobs cannot be canceled")
	}
}
