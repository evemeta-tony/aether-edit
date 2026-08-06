// services/contracts/configquota.go

package contracts

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Typed denial reasons returned by ConfigQuota. FT6's metered
// implementation may add reasons but must keep these stable.
const (
	ReasonUploadSizeExceeded = "quota_upload_size_exceeded"
	ReasonUploadsDisabled    = "quota_uploads_disabled"
	ReasonJobsDisabled       = "quota_jobs_disabled"
	ReasonWorkspaceUnknown   = "quota_workspace_unknown"
)

// WorkspaceQuota holds the per workspace limits. Nil fields inherit
// from the defaults block.
type WorkspaceQuota struct {
	// MaxUploadBytes caps the declared size of a single upload
	// session. Zero means uploads are disabled for the workspace.
	MaxUploadBytes *int64 `yaml:"maxUploadBytes" json:"maxUploadBytes"`
	// AllowUploads gates upload session creation entirely.
	AllowUploads *bool `yaml:"allowUploads" json:"allowUploads"`
	// AllowJobs gates job admission.
	AllowJobs *bool `yaml:"allowJobs" json:"allowJobs"`
}

// QuotaConfigFile is the on disk shape of the mounted quota config.
type QuotaConfigFile struct {
	// DenyUnknownWorkspaces, when true, denies any workspace that has
	// no explicit entry under Workspaces.
	DenyUnknownWorkspaces bool                      `yaml:"denyUnknownWorkspaces" json:"denyUnknownWorkspaces"`
	Defaults              WorkspaceQuota            `yaml:"defaults" json:"defaults"`
	Workspaces            map[string]WorkspaceQuota `yaml:"workspaces" json:"workspaces"`
}

// ConfigQuota is the shipping QuotaChecker implementation: it reads per
// workspace limits from a mounted YAML or JSON config file and enforces
// them. Denials carry a typed reason. FT6 later swaps in the metered
// implementation behind the same interface.
type ConfigQuota struct {
	cfg QuotaConfigFile
}

var _ QuotaChecker = (*ConfigQuota)(nil)

// LoadConfigQuota reads and validates the quota config at path. The
// format is chosen by extension: .yaml or .yml parses as YAML, .json
// parses as JSON. Unknown fields are rejected.
func LoadConfigQuota(path string) (*ConfigQuota, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("quota config: %w", err)
	}
	var cfg QuotaConfigFile
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".yaml", ".yml":
		dec := yaml.NewDecoder(strings.NewReader(string(raw)))
		dec.KnownFields(true)
		if err := dec.Decode(&cfg); err != nil {
			return nil, fmt.Errorf("quota config %s: %w", path, err)
		}
	case ".json":
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&cfg); err != nil {
			return nil, fmt.Errorf("quota config %s: %w", path, err)
		}
	default:
		return nil, fmt.Errorf("quota config %s: unsupported extension %q (want .yaml, .yml, or .json)", path, ext)
	}
	return NewConfigQuota(cfg)
}

// NewConfigQuota validates cfg and returns a ConfigQuota over it.
func NewConfigQuota(cfg QuotaConfigFile) (*ConfigQuota, error) {
	if err := validateWorkspaceQuota("defaults", cfg.Defaults); err != nil {
		return nil, err
	}
	for name, wq := range cfg.Workspaces {
		if name == "" {
			return nil, fmt.Errorf("quota config: empty workspace id key")
		}
		if err := validateWorkspaceQuota(name, wq); err != nil {
			return nil, err
		}
	}
	return &ConfigQuota{cfg: cfg}, nil
}

func validateWorkspaceQuota(name string, wq WorkspaceQuota) error {
	if wq.MaxUploadBytes != nil && *wq.MaxUploadBytes < 0 {
		return fmt.Errorf("quota config: workspace %q: maxUploadBytes must not be negative", name)
	}
	return nil
}

// effective resolves the quota for workspaceID, layering the workspace
// entry over the defaults. known reports whether an explicit entry
// exists.
func (c *ConfigQuota) effective(workspaceID string) (eff WorkspaceQuota, known bool) {
	eff = c.cfg.Defaults
	wq, ok := c.cfg.Workspaces[workspaceID]
	if !ok {
		return eff, false
	}
	if wq.MaxUploadBytes != nil {
		eff.MaxUploadBytes = wq.MaxUploadBytes
	}
	if wq.AllowUploads != nil {
		eff.AllowUploads = wq.AllowUploads
	}
	if wq.AllowJobs != nil {
		eff.AllowJobs = wq.AllowJobs
	}
	return eff, true
}

// CheckUploadSession implements QuotaChecker. It runs before an upload
// session is created; sizeHintBytes is the client declared object size.
func (c *ConfigQuota) CheckUploadSession(ctx context.Context, workspaceID string, sizeHintBytes int64) (QuotaDecision, error) {
	if err := ctx.Err(); err != nil {
		return QuotaDecision{}, err
	}
	eff, known := c.effective(workspaceID)
	if c.cfg.DenyUnknownWorkspaces && !known {
		return QuotaDecision{Allowed: false, Reason: ReasonWorkspaceUnknown}, nil
	}
	if eff.AllowUploads != nil && !*eff.AllowUploads {
		return QuotaDecision{Allowed: false, Reason: ReasonUploadsDisabled}, nil
	}
	if eff.MaxUploadBytes != nil {
		if *eff.MaxUploadBytes == 0 {
			return QuotaDecision{Allowed: false, Reason: ReasonUploadsDisabled}, nil
		}
		if sizeHintBytes > *eff.MaxUploadBytes {
			return QuotaDecision{Allowed: false, Reason: ReasonUploadSizeExceeded}, nil
		}
	}
	return QuotaDecision{Allowed: true}, nil
}

// CheckJobAdmission implements QuotaChecker.
func (c *ConfigQuota) CheckJobAdmission(ctx context.Context, workspaceID string) (QuotaDecision, error) {
	if err := ctx.Err(); err != nil {
		return QuotaDecision{}, err
	}
	eff, known := c.effective(workspaceID)
	if c.cfg.DenyUnknownWorkspaces && !known {
		return QuotaDecision{Allowed: false, Reason: ReasonWorkspaceUnknown}, nil
	}
	if eff.AllowJobs != nil && !*eff.AllowJobs {
		return QuotaDecision{Allowed: false, Reason: ReasonJobsDisabled}, nil
	}
	return QuotaDecision{Allowed: true}, nil
}
