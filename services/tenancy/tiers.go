// services/tenancy/tiers.go

package main

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Tier is one config-defined plan tier. Quotas are per workspace.
type Tier struct {
	// EncodeHoursPerMonth caps job encode time per UTC calendar
	// month; the metered checker denies job admission once the
	// rollup reaches it.
	EncodeHoursPerMonth float64 `yaml:"encodeHoursPerMonth"`
	// StorageBytes caps cumulative landed upload bytes.
	StorageBytes int64 `yaml:"storageBytes"`
	// MaxUploadBytes caps the declared size of a single upload
	// session.
	MaxUploadBytes int64 `yaml:"maxUploadBytes"`
	// AllowUploads and AllowJobs gate the surfaces entirely.
	AllowUploads bool `yaml:"allowUploads"`
	AllowJobs    bool `yaml:"allowJobs"`
}

// TierConfig is the on-disk plan tier file (see
// infra/tenancy/plan-tiers.yaml for the deployed shape).
type TierConfig struct {
	DefaultTier string          `yaml:"defaultTier"`
	Tiers       map[string]Tier `yaml:"tiers"`
}

// LoadTierConfig reads and validates the tier YAML at path. Unknown
// fields are rejected.
func LoadTierConfig(path string) (*TierConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("tier config: %w", err)
	}
	var cfg TierConfig
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("tier config %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("tier config %s: %w", path, err)
	}
	return &cfg, nil
}

func (c *TierConfig) validate() error {
	if len(c.Tiers) == 0 {
		return fmt.Errorf("at least one tier is required")
	}
	if c.DefaultTier == "" {
		return fmt.Errorf("defaultTier is required")
	}
	if _, ok := c.Tiers[c.DefaultTier]; !ok {
		return fmt.Errorf("defaultTier %q is not a defined tier", c.DefaultTier)
	}
	for name, t := range c.Tiers {
		if name == "" {
			return fmt.Errorf("empty tier name")
		}
		if t.EncodeHoursPerMonth < 0 {
			return fmt.Errorf("tier %q: encodeHoursPerMonth must not be negative", name)
		}
		if t.StorageBytes < 0 {
			return fmt.Errorf("tier %q: storageBytes must not be negative", name)
		}
		if t.MaxUploadBytes < 0 {
			return fmt.Errorf("tier %q: maxUploadBytes must not be negative", name)
		}
	}
	return nil
}

// Lookup returns the tier definition by name.
func (c *TierConfig) Lookup(name string) (Tier, bool) {
	t, ok := c.Tiers[name]
	return t, ok
}
