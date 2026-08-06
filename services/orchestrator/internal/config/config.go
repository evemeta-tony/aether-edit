// services/orchestrator/internal/config/config.go
//
// Environment-driven service configuration. Values are validated strictly;
// a bad value is a startup error, never silently defaulted when the value
// is present. Secrets (the JWT secret) arrive only via environment (S5).
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config is the orchestrator service configuration.
type Config struct {
	// HTTPAddr is the API listen address (default 127.0.0.1:5203).
	HTTPAddr string
	// DatabaseURL is the Postgres connection string (required).
	DatabaseURL string
	// NATSURL is the NATS server URL (required).
	NATSURL string
	// ObjectStoreRoot is the filesystem object store root shared with the
	// upload service on this node (required).
	ObjectStoreRoot string
	// StagingDir is where encodes are staged before landing in the store.
	StagingDir string
	// FFmpegPath and FFprobePath locate the AM-5 binaries (required).
	FFmpegPath  string
	FFprobePath string
	// SchedulerSlots is the concurrent encode slot count (default 3, the
	// console model).
	SchedulerSlots int
	// SchedulerPollInterval is the idle queue poll period.
	SchedulerPollInterval time.Duration
	// QuotaConfigPath is the mounted quota limits file (required).
	QuotaConfigPath string
	// JWTSecret signs and verifies API bearer tokens (required, env only).
	JWTSecret string
}

// FromEnv loads configuration from the environment.
func FromEnv() (Config, error) {
	cfg := Config{
		HTTPAddr:              getDefault("ORCH_HTTP_ADDR", "127.0.0.1:5203"),
		DatabaseURL:           os.Getenv("ORCH_DATABASE_URL"),
		NATSURL:               os.Getenv("ORCH_NATS_URL"),
		ObjectStoreRoot:       os.Getenv("ORCH_OBJECT_STORE_ROOT"),
		StagingDir:            getDefault("ORCH_STAGING_DIR", "/var/tmp/aether-orchestrator"),
		FFmpegPath:            os.Getenv("ORCH_FFMPEG_PATH"),
		FFprobePath:           os.Getenv("ORCH_FFPROBE_PATH"),
		SchedulerSlots:        3,
		SchedulerPollInterval: 2 * time.Second,
		QuotaConfigPath:       os.Getenv("ORCH_QUOTA_CONFIG"),
		JWTSecret:             os.Getenv("ORCH_JWT_SECRET"),
	}
	if v := os.Getenv("ORCH_SCHEDULER_SLOTS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 64 {
			return cfg, fmt.Errorf("ORCH_SCHEDULER_SLOTS must be an integer 1..64, got %q", v)
		}
		cfg.SchedulerSlots = n
	}
	if v := os.Getenv("ORCH_SCHEDULER_POLL_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 100*time.Millisecond {
			return cfg, fmt.Errorf("ORCH_SCHEDULER_POLL_INTERVAL must be a duration >= 100ms, got %q", v)
		}
		cfg.SchedulerPollInterval = d
	}
	missing := []string{}
	if cfg.DatabaseURL == "" {
		missing = append(missing, "ORCH_DATABASE_URL")
	}
	if cfg.NATSURL == "" {
		missing = append(missing, "ORCH_NATS_URL")
	}
	if cfg.ObjectStoreRoot == "" {
		missing = append(missing, "ORCH_OBJECT_STORE_ROOT")
	}
	if cfg.FFmpegPath == "" {
		missing = append(missing, "ORCH_FFMPEG_PATH")
	}
	if cfg.FFprobePath == "" {
		missing = append(missing, "ORCH_FFPROBE_PATH")
	}
	if cfg.QuotaConfigPath == "" {
		missing = append(missing, "ORCH_QUOTA_CONFIG")
	}
	if cfg.JWTSecret == "" {
		missing = append(missing, "ORCH_JWT_SECRET")
	}
	if len(missing) > 0 {
		return cfg, fmt.Errorf("missing required environment: %v", missing)
	}
	return cfg, nil
}

func getDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
