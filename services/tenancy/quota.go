// services/tenancy/quota.go

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/evemeta-tony/aether-edit/services/contracts"
)

// Additional typed denial reasons introduced by the metered checker.
// The ConfigQuota reasons (contracts.Reason*) stay stable per the
// frozen contract; these extend the set.
const (
	ReasonStorageExceeded      = "quota_storage_exceeded"
	ReasonEncodeHoursExhausted = "quota_encode_hours_exhausted"
	ReasonTierUnknown          = "quota_tier_unknown"
)

// MeteredQuota is the FT-6a QuotaChecker: it resolves the workspace's
// plan tier from the tenancy store and checks the metering rollups
// against the tier's limits. It satisfies the frozen
// contracts.QuotaChecker interface and is intended to replace
// ConfigQuota at deploy (FT-2 and FT-3 reach it over the internal HTTP
// quota API via the quotaclient package; ConfigQuota remains the
// file-config fallback).
type MeteredQuota struct {
	store Store
	tiers *TierConfig
	now   func() time.Time
}

var _ contracts.QuotaChecker = (*MeteredQuota)(nil)

// NewMeteredQuota builds the checker.
func NewMeteredQuota(store Store, tiers *TierConfig) *MeteredQuota {
	return &MeteredQuota{store: store, tiers: tiers, now: time.Now}
}

// resolveTier loads the workspace and its tier. A missing workspace or
// an undefined tier is a denial, not an error: enforcement stays
// fail-closed without turning bad references into 5xx storms.
func (q *MeteredQuota) resolveTier(ctx context.Context, workspaceID string) (Tier, *contracts.QuotaDecision, error) {
	ws, err := q.store.GetWorkspace(ctx, workspaceID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Tier{}, &contracts.QuotaDecision{Allowed: false, Reason: contracts.ReasonWorkspaceUnknown}, nil
		}
		return Tier{}, nil, fmt.Errorf("quota workspace lookup: %w", err)
	}
	tier, ok := q.tiers.Lookup(ws.PlanTier)
	if !ok {
		return Tier{}, &contracts.QuotaDecision{Allowed: false, Reason: ReasonTierUnknown}, nil
	}
	return tier, nil, nil
}

// CheckUploadSession enforces the tier's upload gate, per-session size
// cap, and cumulative storage quota against the metering rollups.
func (q *MeteredQuota) CheckUploadSession(ctx context.Context, workspaceID string, sizeHintBytes int64) (contracts.QuotaDecision, error) {
	if workspaceID == "" {
		return contracts.QuotaDecision{Allowed: false, Reason: contracts.ReasonWorkspaceUnknown}, nil
	}
	if sizeHintBytes < 0 {
		return contracts.QuotaDecision{}, fmt.Errorf("quota: negative sizeHintBytes %d", sizeHintBytes)
	}
	tier, deny, err := q.resolveTier(ctx, workspaceID)
	if err != nil {
		return contracts.QuotaDecision{}, err
	}
	if deny != nil {
		return *deny, nil
	}
	if !tier.AllowUploads {
		return contracts.QuotaDecision{Allowed: false, Reason: contracts.ReasonUploadsDisabled}, nil
	}
	if tier.MaxUploadBytes > 0 && sizeHintBytes > tier.MaxUploadBytes {
		return contracts.QuotaDecision{Allowed: false, Reason: contracts.ReasonUploadSizeExceeded}, nil
	}
	if tier.StorageBytes > 0 {
		used, err := q.store.SumStorageBytes(ctx, workspaceID)
		if err != nil {
			return contracts.QuotaDecision{}, fmt.Errorf("quota storage rollup: %w", err)
		}
		if used+sizeHintBytes > tier.StorageBytes {
			return contracts.QuotaDecision{Allowed: false, Reason: ReasonStorageExceeded}, nil
		}
	}
	return contracts.QuotaDecision{Allowed: true}, nil
}

// CheckJobAdmission enforces the tier's job gate and the monthly
// encode-hours quota against the metering rollups.
func (q *MeteredQuota) CheckJobAdmission(ctx context.Context, workspaceID string) (contracts.QuotaDecision, error) {
	if workspaceID == "" {
		return contracts.QuotaDecision{Allowed: false, Reason: contracts.ReasonWorkspaceUnknown}, nil
	}
	tier, deny, err := q.resolveTier(ctx, workspaceID)
	if err != nil {
		return contracts.QuotaDecision{}, err
	}
	if deny != nil {
		return *deny, nil
	}
	if !tier.AllowJobs {
		return contracts.QuotaDecision{Allowed: false, Reason: contracts.ReasonJobsDisabled}, nil
	}
	if tier.EncodeHoursPerMonth > 0 {
		month := monthOf(q.now().UTC())
		rollup, err := q.store.GetRollup(ctx, workspaceID, month)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return contracts.QuotaDecision{}, fmt.Errorf("quota encode rollup: %w", err)
		}
		if rollup.EncodeSeconds >= tier.EncodeHoursPerMonth*3600 {
			return contracts.QuotaDecision{Allowed: false, Reason: ReasonEncodeHoursExhausted}, nil
		}
	}
	return contracts.QuotaDecision{Allowed: true}, nil
}

// monthOf formats t as the rollup month key "YYYY-MM" (UTC).
func monthOf(t time.Time) string {
	return t.UTC().Format("2006-01")
}

// ---- internal HTTP quota API (transport for the frozen interface) ----

type quotaUploadRequest struct {
	WorkspaceID   string `json:"workspaceId"`
	SizeHintBytes int64  `json:"sizeHintBytes"`
}

type quotaJobRequest struct {
	WorkspaceID string `json:"workspaceId"`
}

type quotaDecisionView struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}

// handleQuotaCheckUpload serves CheckUploadSession over HTTP for FT-2.
func (s *Server) handleQuotaCheckUpload(w http.ResponseWriter, r *http.Request) {
	var req quotaUploadRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.WorkspaceID == "" {
		writeError(w, http.StatusBadRequest, "invalid_workspace_id", "workspaceId is required")
		return
	}
	if req.SizeHintBytes < 0 {
		writeError(w, http.StatusBadRequest, "invalid_size_hint", "sizeHintBytes must not be negative")
		return
	}
	d, err := s.quota.CheckUploadSession(r.Context(), req.WorkspaceID, req.SizeHintBytes)
	if err != nil {
		s.internalError(w, r, "quota upload check", err)
		return
	}
	writeJSON(w, http.StatusOK, quotaDecisionView{Allowed: d.Allowed, Reason: d.Reason})
}

// handleQuotaCheckJob serves CheckJobAdmission over HTTP for FT-3.
func (s *Server) handleQuotaCheckJob(w http.ResponseWriter, r *http.Request) {
	var req quotaJobRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.WorkspaceID == "" {
		writeError(w, http.StatusBadRequest, "invalid_workspace_id", "workspaceId is required")
		return
	}
	d, err := s.quota.CheckJobAdmission(r.Context(), req.WorkspaceID)
	if err != nil {
		s.internalError(w, r, "quota job check", err)
		return
	}
	writeJSON(w, http.StatusOK, quotaDecisionView{Allowed: d.Allowed, Reason: d.Reason})
}
