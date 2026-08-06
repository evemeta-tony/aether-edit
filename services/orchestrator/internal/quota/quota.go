// services/orchestrator/internal/quota/quota.go
//
// File-config-backed implementation of the frozen QuotaChecker hook
// (contracts v0 item 3). It reads per-workspace limits from a mounted JSON
// config file and enforces them with typed deny reasons: a real enforcement
// path, not a noop. Job admission consumes CheckJobAdmission; the upload
// session check is implemented against the same config so this type
// satisfies the full frozen interface. FT-6 later swaps in the metered
// implementation behind the same interface.
package quota

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/evemeta-tony/aether-edit/services/contracts"
)

// Limits are per-workspace quota limits.
type Limits struct {
	// MaxActiveJobs caps jobs in state queued or running. 0 uses the
	// default; -1 means unlimited.
	MaxActiveJobs int `json:"maxActiveJobs"`
	// MaxUploadBytes caps a single upload session size hint. 0 uses the
	// default; -1 means unlimited.
	MaxUploadBytes int64 `json:"maxUploadBytes"`
}

// fileConfig is the on-disk shape.
type fileConfig struct {
	Defaults   Limits            `json:"defaults"`
	Workspaces map[string]Limits `json:"workspaces"`
}

// ActiveJobCounter reports the number of jobs in state queued or running for
// a workspace. The Postgres job store provides the production implementation.
type ActiveJobCounter func(ctx context.Context, workspaceID string) (int, error)

// Checker implements contracts.QuotaChecker from a config file.
type Checker struct {
	cfg        fileConfig
	activeJobs ActiveJobCounter
}

var _ contracts.QuotaChecker = (*Checker)(nil)

// NewFromFile loads and validates the quota config file.
func NewFromFile(path string, activeJobs ActiveJobCounter) (*Checker, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("quota config: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var cfg fileConfig
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("quota config %s: %w", path, err)
	}
	if cfg.Defaults.MaxActiveJobs == 0 {
		return nil, fmt.Errorf("quota config %s: defaults.maxActiveJobs is required (use -1 for unlimited)", path)
	}
	if cfg.Defaults.MaxUploadBytes == 0 {
		return nil, fmt.Errorf("quota config %s: defaults.maxUploadBytes is required (use -1 for unlimited)", path)
	}
	if activeJobs == nil {
		return nil, fmt.Errorf("quota: activeJobs counter is required")
	}
	return &Checker{cfg: cfg, activeJobs: activeJobs}, nil
}

// limitsFor resolves effective limits for a workspace.
func (c *Checker) limitsFor(workspaceID string) Limits {
	eff := c.cfg.Defaults
	if ws, ok := c.cfg.Workspaces[workspaceID]; ok {
		if ws.MaxActiveJobs != 0 {
			eff.MaxActiveJobs = ws.MaxActiveJobs
		}
		if ws.MaxUploadBytes != 0 {
			eff.MaxUploadBytes = ws.MaxUploadBytes
		}
	}
	return eff
}

// CheckJobAdmission implements contracts.QuotaChecker.
func (c *Checker) CheckJobAdmission(ctx context.Context, workspaceID string) (contracts.QuotaDecision, error) {
	lim := c.limitsFor(workspaceID)
	if lim.MaxActiveJobs < 0 {
		return contracts.QuotaDecision{Allowed: true}, nil
	}
	n, err := c.activeJobs(ctx, workspaceID)
	if err != nil {
		return contracts.QuotaDecision{}, fmt.Errorf("quota: count active jobs: %w", err)
	}
	if n >= lim.MaxActiveJobs {
		return contracts.QuotaDecision{
			Allowed: false,
			Reason:  fmt.Sprintf("quota_exceeded:max_active_jobs (%d of %d active)", n, lim.MaxActiveJobs),
		}, nil
	}
	return contracts.QuotaDecision{Allowed: true}, nil
}

// CheckUploadSession implements contracts.QuotaChecker.
func (c *Checker) CheckUploadSession(ctx context.Context, workspaceID string, sizeHintBytes int64) (contracts.QuotaDecision, error) {
	lim := c.limitsFor(workspaceID)
	if lim.MaxUploadBytes < 0 {
		return contracts.QuotaDecision{Allowed: true}, nil
	}
	if sizeHintBytes > lim.MaxUploadBytes {
		return contracts.QuotaDecision{
			Allowed: false,
			Reason:  fmt.Sprintf("quota_exceeded:max_upload_bytes (%d over limit %d)", sizeHintBytes, lim.MaxUploadBytes),
		}, nil
	}
	return contracts.QuotaDecision{Allowed: true}, nil
}
