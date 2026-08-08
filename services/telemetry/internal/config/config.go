// services/telemetry/internal/config/config.go

// Package config loads and validates the telemetry service configuration
// from the environment. Invalid values are rejected at startup, never
// coerced.
package config

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"time"
)

// Config is the validated service configuration.
type Config struct {
	// ListenAddr is the host:port the HTTP server binds.
	ListenAddr string
	// AuthHS256Key is the base64url-decoded HMAC key used to verify the
	// HS256 JWT bearer tokens minted by the tenancy signer. It is decoded
	// from TELEMETRY_AUTH_HS256_KEY (base64url, no padding) and must be at
	// least 32 bytes, matching upload/config.go and tenancy/config.go.
	AuthHS256Key []byte
	// NATSURL is the NATS server URL (nats:// or tls://).
	NATSURL string
	// StreamBufferSize is the per-connection SSE buffer (16..4096 events).
	StreamBufferSize int
	// HeartbeatInterval is the SSE heartbeat comment cadence.
	HeartbeatInterval time.Duration
	// SampleInterval is the hardware sampling cadence (contract 4: 1 Hz).
	SampleInterval time.Duration
}

// Load reads configuration from the environment and validates every field.
func Load() (Config, error) {
	c := Config{
		ListenAddr:        getenv("TELEMETRY_LISTEN_ADDR", "127.0.0.1:8094"),
		NATSURL:           getenv("TELEMETRY_NATS_URL", "nats://127.0.0.1:4222"),
		StreamBufferSize:  256,
		HeartbeatInterval: 15 * time.Second,
		SampleInterval:    time.Second,
	}
	host, port, err := net.SplitHostPort(c.ListenAddr)
	if err != nil || host == "" || port == "" {
		return Config{}, fmt.Errorf("TELEMETRY_LISTEN_ADDR %q is not a valid host:port", c.ListenAddr)
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return Config{}, fmt.Errorf("TELEMETRY_LISTEN_ADDR port %q is not a valid port number", port)
	}
	keyB64 := os.Getenv("TELEMETRY_AUTH_HS256_KEY")
	if keyB64 == "" {
		return Config{}, fmt.Errorf("TELEMETRY_AUTH_HS256_KEY is required")
	}
	key, err := base64.RawURLEncoding.DecodeString(keyB64)
	if err != nil {
		return Config{}, fmt.Errorf("TELEMETRY_AUTH_HS256_KEY must be base64url without padding: %w", err)
	}
	if len(key) < 32 {
		return Config{}, fmt.Errorf("TELEMETRY_AUTH_HS256_KEY must decode to at least 32 bytes")
	}
	c.AuthHS256Key = key
	u, err := url.Parse(c.NATSURL)
	if err != nil || (u.Scheme != "nats" && u.Scheme != "tls") || u.Host == "" {
		return Config{}, fmt.Errorf("TELEMETRY_NATS_URL %q must be a nats:// or tls:// URL", c.NATSURL)
	}
	if raw := os.Getenv("TELEMETRY_STREAM_BUFFER"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 16 || n > 4096 {
			return Config{}, fmt.Errorf("TELEMETRY_STREAM_BUFFER %q must be an integer in [16,4096]", raw)
		}
		c.StreamBufferSize = n
	}
	return c, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
