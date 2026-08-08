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
	// S3Endpoint/Region/Bucket/AccessKey/SecretKey/PathStyle configure the
	// OVH S3 (S3-compatible) object store shared with the FT-2 upload service.
	// Sources are READ at assets/<workspaceId>/sha256/<hex64> and ladder
	// outputs are WRITTEN to outputs/<workspaceId>/<jobId>/... in this bucket.
	S3Endpoint  string
	S3Region    string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string
	S3PathStyle bool
	// ScratchDir is the LOCAL temp directory where source objects are
	// downloaded from S3 for ffprobe/ffmpeg and where encode outputs are
	// staged before upload. It is NOT the object store; it is ephemeral
	// per-node scratch space.
	ScratchDir string
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
	// JWTSecret verifies API bearer tokens (required, env only). It is the
	// base64url (RawURLEncoding, no padding) encoding of the shared HMAC key;
	// set it to the SAME string as the tenancy signer's
	// TENANCY_AUTH_HS256_KEY (frozen auth contract). The auth package decodes
	// it into the HMAC key.
	JWTSecret string
	// AutoCreateJobs enables auto-creating a transcode job on the landed
	// object event (using the workspace default preset). Default true; set
	// ORCH_AUTOCREATE_JOBS=false to switch to an explicit-action model where
	// jobs are created only via POST /v1/jobs.
	AutoCreateJobs bool
}

// FromEnv loads configuration from the environment.
func FromEnv() (Config, error) {
	cfg := Config{
		HTTPAddr:              getDefault("ORCH_HTTP_ADDR", "127.0.0.1:5203"),
		DatabaseURL:           os.Getenv("ORCH_DATABASE_URL"),
		NATSURL:               os.Getenv("ORCH_NATS_URL"),
		S3Endpoint:            os.Getenv("ORCH_S3_ENDPOINT"),
		S3Region:              os.Getenv("ORCH_S3_REGION"),
		S3Bucket:              os.Getenv("ORCH_S3_BUCKET"),
		S3AccessKey:           os.Getenv("ORCH_S3_ACCESS_KEY"),
		S3SecretKey:           os.Getenv("ORCH_S3_SECRET_KEY"),
		ScratchDir:            getDefault("ORCH_SCRATCH_DIR", "/var/tmp/aether-orchestrator"),
		FFmpegPath:            os.Getenv("ORCH_FFMPEG_PATH"),
		FFprobePath:           os.Getenv("ORCH_FFPROBE_PATH"),
		SchedulerSlots:        3,
		SchedulerPollInterval: 2 * time.Second,
		QuotaConfigPath:       os.Getenv("ORCH_QUOTA_CONFIG"),
		JWTSecret:             os.Getenv("ORCH_JWT_SECRET"),
		AutoCreateJobs:        true,
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
	if v := os.Getenv("ORCH_S3_PATH_STYLE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return cfg, fmt.Errorf("ORCH_S3_PATH_STYLE must be a boolean, got %q", v)
		}
		cfg.S3PathStyle = b
	}
	if v := os.Getenv("ORCH_AUTOCREATE_JOBS"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return cfg, fmt.Errorf("ORCH_AUTOCREATE_JOBS must be a boolean, got %q", v)
		}
		cfg.AutoCreateJobs = b
	}
	missing := []string{}
	if cfg.DatabaseURL == "" {
		missing = append(missing, "ORCH_DATABASE_URL")
	}
	if cfg.NATSURL == "" {
		missing = append(missing, "ORCH_NATS_URL")
	}
	if cfg.S3Endpoint == "" {
		missing = append(missing, "ORCH_S3_ENDPOINT")
	}
	if cfg.S3Region == "" {
		missing = append(missing, "ORCH_S3_REGION")
	}
	if cfg.S3Bucket == "" {
		missing = append(missing, "ORCH_S3_BUCKET")
	}
	if cfg.S3AccessKey == "" {
		missing = append(missing, "ORCH_S3_ACCESS_KEY")
	}
	if cfg.S3SecretKey == "" {
		missing = append(missing, "ORCH_S3_SECRET_KEY")
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
