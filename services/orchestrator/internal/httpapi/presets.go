// services/orchestrator/internal/httpapi/presets.go
//
// Preset endpoints. Edit semantic (documented contract of this API): a
// PATCH takes effect for jobs that START after the update commits. Jobs
// already running keep the preset snapshot taken at their start; queued
// jobs pick up the edited preset when the scheduler starts them.
package httpapi

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/jobs"
	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/store"
)

// presetBody is the POST /v1/presets body (full preset definition).
type presetBody struct {
	Name           string      `json:"name"`
	Container      string      `json:"container"`
	VideoCodec     string      `json:"videoCodec"`
	RateControl    string      `json:"rateControl"`
	CRF            int         `json:"crf"`
	BitrateKbps    int         `json:"bitrateKbps"`
	MaxBitrateKbps int         `json:"maxBitrateKbps"`
	GOPLength      int         `json:"gopLength"`
	SpeedPreset    string      `json:"speedPreset"`
	Ladder         []jobs.Rung `json:"ladder"`
}

// listPresets handles GET /v1/presets.
func (a *API) listPresets(w http.ResponseWriter, r *http.Request) {
	id, ok := a.identity(w, r)
	if !ok {
		return
	}
	list, err := a.store.ListPresets(r.Context(), id.WorkspaceID)
	if err != nil {
		a.log.Error("list presets", "err", err)
		writeError(w, http.StatusInternalServerError, "list presets failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"presets": list})
}

// getPreset handles GET /v1/presets/{id}.
func (a *API) getPreset(w http.ResponseWriter, r *http.Request) {
	id, ok := a.identity(w, r)
	if !ok {
		return
	}
	presetID := r.PathValue("id")
	if _, err := uuid.Parse(presetID); err != nil {
		writeError(w, http.StatusBadRequest, "preset id must be a uuid")
		return
	}
	p, err := a.store.GetPreset(r.Context(), id.WorkspaceID, presetID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "preset not found")
		return
	}
	if err != nil {
		a.log.Error("get preset", "err", err)
		writeError(w, http.StatusInternalServerError, "get preset failed")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// createPreset handles POST /v1/presets.
func (a *API) createPreset(w http.ResponseWriter, r *http.Request) {
	id, ok := a.identity(w, r)
	if !ok {
		return
	}
	var body presetBody
	if !decodeStrict(w, r, &body) {
		return
	}
	p := jobs.Preset{
		WorkspaceID:    id.WorkspaceID,
		Name:           body.Name,
		Container:      jobs.Container(body.Container),
		VideoCodec:     jobs.VideoCodec(body.VideoCodec),
		RateControl:    jobs.RateControlMode(body.RateControl),
		CRF:            body.CRF,
		BitrateKbps:    body.BitrateKbps,
		MaxBitrateKbps: body.MaxBitrateKbps,
		GOPLength:      body.GOPLength,
		SpeedPreset:    jobs.SpeedPreset(body.SpeedPreset),
		Ladder:         body.Ladder,
	}
	if err := p.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := a.store.CreatePreset(r.Context(), p)
	if err != nil {
		a.log.Error("create preset", "err", err)
		writeError(w, http.StatusInternalServerError, "create preset failed")
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// presetPatch is the PATCH /v1/presets/{id} body: only provided fields
// change. Cross-field constraints are validated on the merged result, so a
// rate control mode change must arrive together with consistent value
// fields or the whole patch is rejected.
type presetPatch struct {
	Name           *string      `json:"name"`
	Container      *string      `json:"container"`
	VideoCodec     *string      `json:"videoCodec"`
	RateControl    *string      `json:"rateControl"`
	CRF            *int         `json:"crf"`
	BitrateKbps    *int         `json:"bitrateKbps"`
	MaxBitrateKbps *int         `json:"maxBitrateKbps"`
	GOPLength      *int         `json:"gopLength"`
	SpeedPreset    *string      `json:"speedPreset"`
	Ladder         *[]jobs.Rung `json:"ladder"`
}

// patchPreset handles PATCH /v1/presets/{id}.
func (a *API) patchPreset(w http.ResponseWriter, r *http.Request) {
	id, ok := a.identity(w, r)
	if !ok {
		return
	}
	presetID := r.PathValue("id")
	if _, err := uuid.Parse(presetID); err != nil {
		writeError(w, http.StatusBadRequest, "preset id must be a uuid")
		return
	}
	var patch presetPatch
	if !decodeStrict(w, r, &patch) {
		return
	}
	p, err := a.store.GetPreset(r.Context(), id.WorkspaceID, presetID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "preset not found")
		return
	}
	if err != nil {
		a.log.Error("get preset for patch", "err", err)
		writeError(w, http.StatusInternalServerError, "patch failed")
		return
	}
	if patch.Name != nil {
		p.Name = *patch.Name
	}
	if patch.Container != nil {
		p.Container = jobs.Container(*patch.Container)
	}
	if patch.VideoCodec != nil {
		p.VideoCodec = jobs.VideoCodec(*patch.VideoCodec)
	}
	if patch.RateControl != nil {
		p.RateControl = jobs.RateControlMode(*patch.RateControl)
	}
	if patch.CRF != nil {
		p.CRF = *patch.CRF
	}
	if patch.BitrateKbps != nil {
		p.BitrateKbps = *patch.BitrateKbps
	}
	if patch.MaxBitrateKbps != nil {
		p.MaxBitrateKbps = *patch.MaxBitrateKbps
	}
	if patch.GOPLength != nil {
		p.GOPLength = *patch.GOPLength
	}
	if patch.SpeedPreset != nil {
		p.SpeedPreset = jobs.SpeedPreset(*patch.SpeedPreset)
	}
	if patch.Ladder != nil {
		p.Ladder = *patch.Ladder
	}
	if err := p.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := a.store.UpdatePreset(r.Context(), p)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "preset not found")
		return
	}
	if err != nil {
		a.log.Error("update preset", "err", err)
		writeError(w, http.StatusInternalServerError, "patch failed")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}
