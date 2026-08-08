// services/telemetry/internal/auth/auth_test.go

package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// key is a 32-byte raw HS256 signing key for the tests.
var key = []byte("telemetry-auth-test-key-01234567") // 32 bytes

func sign(t *testing.T, method jwt.SigningMethod, claims jwt.MapClaims, signKey any) string {
	t.Helper()
	tok := jwt.NewWithClaims(method, claims)
	s, err := tok.SignedString(signKey)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

func TestNewVerifierRejectsShortKey(t *testing.T) {
	if _, err := NewVerifier([]byte("too-short")); err == nil {
		t.Fatal("expected error for key shorter than 32 bytes")
	}
	if _, err := NewVerifier(key); err != nil {
		t.Fatalf("unexpected error for valid key: %v", err)
	}
}

func TestVerifyValidToken(t *testing.T) {
	v, err := NewVerifier(key)
	if err != nil {
		t.Fatal(err)
	}
	tok := sign(t, jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":         "user-abc",
		"workspaceId": "ws-xyz",
		"exp":         time.Now().Add(time.Hour).Unix(),
	}, key)
	id, err := v.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if id.UserID != "user-abc" || id.WorkspaceID != "ws-xyz" {
		t.Fatalf("identity = %+v", id)
	}
}

func TestVerifyRejects(t *testing.T) {
	v, err := NewVerifier(key)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"wrong key": sign(t, jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": "u", "workspaceId": "w", "exp": time.Now().Add(time.Hour).Unix(),
		}, []byte("another-32-byte-signing-key-0123")),
		"expired": sign(t, jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": "u", "workspaceId": "w", "exp": time.Now().Add(-time.Hour).Unix(),
		}, key),
		"not yet valid": sign(t, jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": "u", "workspaceId": "w",
			"nbf": time.Now().Add(time.Hour).Unix(),
			"exp": time.Now().Add(2 * time.Hour).Unix(),
		}, key),
		"no exp": sign(t, jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": "u", "workspaceId": "w",
		}, key),
		"missing sub": sign(t, jwt.SigningMethodHS256, jwt.MapClaims{
			"workspaceId": "w", "exp": time.Now().Add(time.Hour).Unix(),
		}, key),
		"missing workspaceId": sign(t, jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": "u", "exp": time.Now().Add(time.Hour).Unix(),
		}, key),
		"garbage": "not.a.jwt",
	}
	for name, tok := range cases {
		if _, err := v.Verify(tok); err == nil {
			t.Fatalf("%s: expected verification error, got nil", name)
		}
	}
}

// TestVerifyRejectsNoneAlg ensures the alg-confusion attack (alg=none) is
// rejected: only HS256 is accepted.
func TestVerifyRejectsNoneAlg(t *testing.T) {
	v, err := NewVerifier(key)
	if err != nil {
		t.Fatal(err)
	}
	tok := sign(t, jwt.SigningMethodNone, jwt.MapClaims{
		"sub": "u", "workspaceId": "w", "exp": time.Now().Add(time.Hour).Unix(),
	}, jwt.UnsafeAllowNoneSignatureType)
	if _, err := v.Verify(tok); err == nil {
		t.Fatal("alg=none token must be rejected")
	}
}

func TestMiddleware(t *testing.T) {
	v, err := NewVerifier(key)
	if err != nil {
		t.Fatal(err)
	}
	var gotID Identity
	var gotOK bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID, gotOK = FromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	h := v.Middleware(inner)

	// No header: 401, inner not reached.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing header: code %d, want 401", rec.Code)
	}
	if gotOK {
		t.Fatal("inner handler must not run without a valid token")
	}

	// Valid token: inner reached with the identity.
	tok := sign(t, jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "user-1", "workspaceId": "ws-1", "exp": time.Now().Add(time.Hour).Unix(),
	}, key)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("valid token: code %d, want 204", rec.Code)
	}
	if !gotOK || gotID.UserID != "user-1" || gotID.WorkspaceID != "ws-1" {
		t.Fatalf("identity = %+v ok=%v", gotID, gotOK)
	}
}
