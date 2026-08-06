// services/upload/config.go

package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
)

// ChunkSizeBytes is the fixed chunk size handed to clients at session
// creation: 64 MiB.
const ChunkSizeBytes int64 = 64 << 20

// Config holds all runtime configuration for the upload service. Every
// value comes from the environment (or an EnvironmentFile under
// systemd); no secrets live in the repo.
type Config struct {
	ListenAddr string

	DatabaseURL string

	S3Endpoint  string
	S3Region    string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string
	S3PathStyle bool

	NATSURL string

	QuotaConfigPath string

	// AuthHS256Key is the base64url (no padding) encoded HMAC key used
	// to verify bearer tokens.
	AuthHS256Key []byte

	// MaxInflightBytes is the backpressure ceiling: the total number of
	// chunk body bytes the service will hold in flight at once.
	MaxInflightBytes int64

	// MaxObjectBytes caps the declared size of a single upload session
	// at the service boundary (quota config may be tighter).
	MaxObjectBytes int64
}

// LoadConfig reads configuration from the environment and validates it.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		ListenAddr:      envDefault("UPLOAD_LISTEN_ADDR", "127.0.0.1:5301"),
		DatabaseURL:     os.Getenv("UPLOAD_DATABASE_URL"),
		S3Endpoint:      os.Getenv("UPLOAD_S3_ENDPOINT"),
		S3Region:        envDefault("UPLOAD_S3_REGION", "gra"),
		S3Bucket:        os.Getenv("UPLOAD_S3_BUCKET"),
		S3AccessKey:     os.Getenv("UPLOAD_S3_ACCESS_KEY"),
		S3SecretKey:     os.Getenv("UPLOAD_S3_SECRET_KEY"),
		NATSURL:         envDefault("UPLOAD_NATS_URL", "nats://127.0.0.1:4222"),
		QuotaConfigPath: os.Getenv("UPLOAD_QUOTA_CONFIG_PATH"),
	}

	var err error
	if cfg.S3PathStyle, err = envBool("UPLOAD_S3_PATH_STYLE", true); err != nil {
		return nil, err
	}
	if cfg.MaxInflightBytes, err = envInt64("UPLOAD_MAX_INFLIGHT_BYTES", 1<<30); err != nil {
		return nil, err
	}
	if cfg.MaxObjectBytes, err = envInt64("UPLOAD_MAX_OBJECT_BYTES", 1<<40); err != nil {
		return nil, err
	}

	keyB64 := os.Getenv("UPLOAD_AUTH_HS256_KEY")
	if keyB64 == "" {
		return nil, fmt.Errorf("config: UPLOAD_AUTH_HS256_KEY is required")
	}
	key, err := base64.RawURLEncoding.DecodeString(keyB64)
	if err != nil {
		return nil, fmt.Errorf("config: UPLOAD_AUTH_HS256_KEY must be base64url without padding: %w", err)
	}
	if len(key) < 32 {
		return nil, fmt.Errorf("config: UPLOAD_AUTH_HS256_KEY must decode to at least 32 bytes")
	}
	cfg.AuthHS256Key = key

	for name, val := range map[string]string{
		"UPLOAD_DATABASE_URL":      cfg.DatabaseURL,
		"UPLOAD_S3_ENDPOINT":       cfg.S3Endpoint,
		"UPLOAD_S3_BUCKET":         cfg.S3Bucket,
		"UPLOAD_S3_ACCESS_KEY":     cfg.S3AccessKey,
		"UPLOAD_S3_SECRET_KEY":     cfg.S3SecretKey,
		"UPLOAD_QUOTA_CONFIG_PATH": cfg.QuotaConfigPath,
	} {
		if val == "" {
			return nil, fmt.Errorf("config: %s is required", name)
		}
	}
	if cfg.MaxInflightBytes < ChunkSizeBytes {
		return nil, fmt.Errorf("config: UPLOAD_MAX_INFLIGHT_BYTES must be at least one chunk (%d)", ChunkSizeBytes)
	}
	if cfg.MaxObjectBytes <= 0 {
		return nil, fmt.Errorf("config: UPLOAD_MAX_OBJECT_BYTES must be positive")
	}
	return cfg, nil
}

func envDefault(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func envBool(name string, def bool) (bool, error) {
	v := os.Getenv(name)
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("config: %s must be a boolean: %w", name, err)
	}
	return b, nil
}

func envInt64(name string, def int64) (int64, error) {
	v := os.Getenv(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be an integer: %w", name, err)
	}
	return n, nil
}
