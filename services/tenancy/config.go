// services/tenancy/config.go

package main

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"time"
)

// Config holds all runtime configuration for the tenancy service. Every
// value comes from the environment (or an EnvironmentFile under
// systemd); no secrets live in the repo.
type Config struct {
	ListenAddr string

	DatabaseURL string
	NATSURL     string

	// AuthHS256Key is the base64url (no padding) encoded HMAC key used
	// to sign access tokens. It is the SAME shared key the FT-2 and
	// FT-3 middlewares verify with (frozen auth contract, HS256 first;
	// the asymmetric JWKS upgrade is a recorded follow-up).
	AuthHS256Key []byte

	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration

	// OIDC settings for the Google identity provider (AM-3: GCP is
	// identity only). Issuer defaults to Google; client id, secret,
	// and redirect URL are deploy-time values.
	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCRedirectURL  string

	// TiersConfigPath points at the plan tier YAML (see tiers.go).
	TiersConfigPath string

	// InternalToken authenticates service-to-service calls to the
	// /internal/v1 quota endpoints (FT-2 and FT-3 via quotaclient).
	InternalToken string

	// CookieSecure controls the Secure attribute on the refresh token
	// cookie. Only ever false for plain-HTTP local development.
	CookieSecure bool
}

// LoadConfig reads configuration from the environment and validates it.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		ListenAddr:       envDefault("TENANCY_LISTEN_ADDR", "127.0.0.1:5401"),
		DatabaseURL:      os.Getenv("TENANCY_DATABASE_URL"),
		NATSURL:          envDefault("TENANCY_NATS_URL", "nats://127.0.0.1:4222"),
		OIDCIssuer:       envDefault("TENANCY_OIDC_ISSUER", "https://accounts.google.com"),
		OIDCClientID:     os.Getenv("TENANCY_OIDC_CLIENT_ID"),
		OIDCClientSecret: os.Getenv("TENANCY_OIDC_CLIENT_SECRET"),
		OIDCRedirectURL:  os.Getenv("TENANCY_OIDC_REDIRECT_URL"),
		TiersConfigPath:  os.Getenv("TENANCY_TIERS_CONFIG_PATH"),
		InternalToken:    os.Getenv("TENANCY_INTERNAL_TOKEN"),
	}

	var err error
	if cfg.AccessTokenTTL, err = envDuration("TENANCY_ACCESS_TOKEN_TTL", 15*time.Minute); err != nil {
		return nil, err
	}
	if cfg.RefreshTokenTTL, err = envDuration("TENANCY_REFRESH_TOKEN_TTL", 720*time.Hour); err != nil {
		return nil, err
	}
	if cfg.CookieSecure, err = envBool("TENANCY_COOKIE_SECURE", true); err != nil {
		return nil, err
	}

	keyB64 := os.Getenv("TENANCY_AUTH_HS256_KEY")
	if keyB64 == "" {
		return nil, fmt.Errorf("config: TENANCY_AUTH_HS256_KEY is required")
	}
	key, err := base64.RawURLEncoding.DecodeString(keyB64)
	if err != nil {
		return nil, fmt.Errorf("config: TENANCY_AUTH_HS256_KEY must be base64url without padding: %w", err)
	}
	if len(key) < 32 {
		return nil, fmt.Errorf("config: TENANCY_AUTH_HS256_KEY must decode to at least 32 bytes")
	}
	cfg.AuthHS256Key = key

	for name, val := range map[string]string{
		"TENANCY_DATABASE_URL":       cfg.DatabaseURL,
		"TENANCY_OIDC_CLIENT_ID":     cfg.OIDCClientID,
		"TENANCY_OIDC_CLIENT_SECRET": cfg.OIDCClientSecret,
		"TENANCY_OIDC_REDIRECT_URL":  cfg.OIDCRedirectURL,
		"TENANCY_TIERS_CONFIG_PATH":  cfg.TiersConfigPath,
		"TENANCY_INTERNAL_TOKEN":     cfg.InternalToken,
	} {
		if val == "" {
			return nil, fmt.Errorf("config: %s is required", name)
		}
	}
	if len(cfg.InternalToken) < 16 {
		return nil, fmt.Errorf("config: TENANCY_INTERNAL_TOKEN must be at least 16 characters")
	}
	if _, _, err := net.SplitHostPort(cfg.ListenAddr); err != nil {
		return nil, fmt.Errorf("config: TENANCY_LISTEN_ADDR %q is not host:port", cfg.ListenAddr)
	}
	for name, raw := range map[string]string{
		"TENANCY_OIDC_ISSUER":       cfg.OIDCIssuer,
		"TENANCY_OIDC_REDIRECT_URL": cfg.OIDCRedirectURL,
	} {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return nil, fmt.Errorf("config: %s %q is not an absolute http(s) URL", name, raw)
		}
	}
	if cfg.AccessTokenTTL <= 0 || cfg.AccessTokenTTL > time.Hour {
		return nil, fmt.Errorf("config: TENANCY_ACCESS_TOKEN_TTL must be positive and at most 1h (short-lived tokens)")
	}
	if cfg.RefreshTokenTTL <= cfg.AccessTokenTTL {
		return nil, fmt.Errorf("config: TENANCY_REFRESH_TOKEN_TTL must exceed the access token TTL")
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

func envDuration(name string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(name)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be a Go duration: %w", name, err)
	}
	return d, nil
}
