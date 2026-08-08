// services/orchestrator/internal/auth/auth_test.go
//
// Auth verifier tests conforming to the frozen auth contract: the env secret
// is a base64url (RawURLEncoding, no padding) string; the HMAC key is the
// decoded bytes. A token signed with the DECODED key must verify; a token
// signed with the raw base64url string treated as bytes must NOT verify. This
// is the exact defect being fixed: the old verifier used []byte(secret) and so
// rejected every tenancy-issued token.
package auth

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// makeKey returns (base64urlString, decodedBytes) for a 32-byte key.
func makeKey() (string, []byte) {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	return base64.RawURLEncoding.EncodeToString(raw), raw
}

func signToken(t *testing.T, key []byte, sub, ws string, exp time.Time) string {
	t.Helper()
	claims := claims{
		WorkspaceID: ws,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   sub,
			ExpiresAt: jwt.NewNumericDate(exp),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return s
}

func TestVerifierAcceptsTokenSignedWithDecodedKey(t *testing.T) {
	b64, decoded := makeKey()
	v, err := NewVerifier(b64)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	tok := signToken(t, decoded, "user-1", "ws-1", time.Now().Add(time.Hour))
	id, err := v.Verify(tok)
	if err != nil {
		t.Fatalf("verify tenancy-style token: %v", err)
	}
	if id.UserID != "user-1" || id.WorkspaceID != "ws-1" {
		t.Fatalf("identity = %+v, want user-1/ws-1", id)
	}
}

func TestVerifierRejectsTokenSignedWithRawStringKey(t *testing.T) {
	b64, _ := makeKey()
	v, err := NewVerifier(b64)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	// The old, defective behavior signed with the raw base64url string bytes
	// (i.e. []byte(secret)). Such a token must be rejected now.
	tok := signToken(t, []byte(b64), "user-1", "ws-1", time.Now().Add(time.Hour))
	if _, err := v.Verify(tok); err == nil {
		t.Fatal("token signed with the raw-string key must not verify")
	}
}

func TestNewVerifierRejectsBadSecret(t *testing.T) {
	if _, err := NewVerifier(""); err == nil {
		t.Fatal("empty secret must be rejected")
	}
	if _, err := NewVerifier("not base64url with spaces!!"); err == nil {
		t.Fatal("non base64url secret must be rejected")
	}
	// A valid base64url string that decodes to fewer than 32 bytes.
	short := base64.RawURLEncoding.EncodeToString([]byte("tooshort"))
	if _, err := NewVerifier(short); err == nil {
		t.Fatal("secret decoding to < 32 bytes must be rejected")
	}
}

func TestVerifierEnforcesExpiry(t *testing.T) {
	b64, decoded := makeKey()
	v, err := NewVerifier(b64)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	tok := signToken(t, decoded, "user-1", "ws-1", time.Now().Add(-time.Minute))
	if _, err := v.Verify(tok); err == nil {
		t.Fatal("expired token must be rejected")
	}
}
