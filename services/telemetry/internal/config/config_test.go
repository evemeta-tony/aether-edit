// services/telemetry/internal/config/config_test.go

package config

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/evemeta-tony/aether-edit/services/telemetry/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

// TestLoadDecodesHS256KeyBase64URL is the load-bearing interop test: it proves
// that TELEMETRY_AUTH_HS256_KEY is decoded with base64url (no padding), exactly
// the encoding the tenancy signer and services/upload use
// (base64.RawURLEncoding, see services/tenancy/config.go and
// services/upload/config.go). A padding or alphabet mismatch would pass every
// other unit test (they inject raw bytes and skip the decode) yet break token
// interop in production, so it is asserted here end to end.
func TestLoadDecodesHS256KeyBase64URL(t *testing.T) {
	// 32 raw key bytes that force the base64url alphabet: 0xFB, 0xFF encode to
	// '-' and '_' in base64url and to '+' and '/' in standard base64, so a
	// wrong decoder would produce different bytes or fail outright.
	rawKey := make([]byte, 32)
	for i := range rawKey {
		rawKey[i] = byte(0xFB + i)
	}
	keyB64 := base64.RawURLEncoding.EncodeToString(rawKey)

	t.Setenv("TELEMETRY_AUTH_HS256_KEY", keyB64)
	t.Setenv("TELEMETRY_LISTEN_ADDR", "127.0.0.1:8094")
	t.Setenv("TELEMETRY_NATS_URL", "nats://127.0.0.1:4222")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.AuthHS256Key) != len(rawKey) {
		t.Fatalf("decoded key length = %d, want %d", len(c.AuthHS256Key), len(rawKey))
	}
	for i := range rawKey {
		if c.AuthHS256Key[i] != rawKey[i] {
			t.Fatalf("decoded key byte %d = %#x, want %#x", i, c.AuthHS256Key[i], rawKey[i])
		}
	}

	// A token signed with the DECODED key must verify.
	v, err := auth.NewVerifier(c.AuthHS256Key)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	good := signHS256(t, rawKey, jwt.MapClaims{
		"sub":         "user-1",
		"workspaceId": "ws-1",
		"exp":         time.Now().Add(time.Hour).Unix(),
	})
	id, err := v.Verify(good)
	if err != nil {
		t.Fatalf("Verify(decoded-key token): %v", err)
	}
	if id.UserID != "user-1" || id.WorkspaceID != "ws-1" {
		t.Fatalf("identity = %+v", id)
	}

	// A token signed with the raw ENV STRING bytes (i.e. someone who fed the
	// key without base64url-decoding, or a service on a mismatched encoding)
	// must NOT verify. This is the "raw-string key must not verify" guarantee.
	bad := signHS256(t, []byte(keyB64), jwt.MapClaims{
		"sub":         "user-1",
		"workspaceId": "ws-1",
		"exp":         time.Now().Add(time.Hour).Unix(),
	})
	if _, err := v.Verify(bad); err == nil {
		t.Fatal("token signed with the raw env string verified; base64url decode is not load-bearing")
	}
}

// TestLoadRejectsBadHS256Key covers the validation failures on the key env var.
func TestLoadRejectsBadHS256Key(t *testing.T) {
	t.Setenv("TELEMETRY_LISTEN_ADDR", "127.0.0.1:8094")
	t.Setenv("TELEMETRY_NATS_URL", "nats://127.0.0.1:4222")

	cases := map[string]string{
		"empty":                  "",
		"not-base64url":          "not valid base64!!",
		"padded-standard-base64": base64.StdEncoding.EncodeToString(make([]byte, 32)),
		"decodes-below-32-bytes": base64.RawURLEncoding.EncodeToString(make([]byte, 16)),
	}
	for name, val := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("TELEMETRY_AUTH_HS256_KEY", val)
			if _, err := Load(); err == nil {
				t.Fatalf("Load accepted invalid key %q", val)
			}
		})
	}
}

func signHS256(t *testing.T, key []byte, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}
