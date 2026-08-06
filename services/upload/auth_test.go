// services/upload/auth_test.go

package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

var testAuthKey = []byte("0123456789abcdef0123456789abcdef")

// signTestToken builds a compact HS256 JWS for tests.
func signTestToken(t *testing.T, key []byte, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	signing := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(signing))
	return signing + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func validClaims(exp time.Time) map[string]any {
	return map[string]any{
		"sub":         "user-1",
		"workspaceId": "ws-1",
		"exp":         exp.Unix(),
	}
}

func TestVerifyHS256(t *testing.T) {
	now := time.Now()

	t.Run("valid token yields identity", func(t *testing.T) {
		tok := signTestToken(t, testAuthKey, validClaims(now.Add(time.Hour)))
		id, err := VerifyHS256(tok, testAuthKey, now)
		if err != nil {
			t.Fatalf("VerifyHS256: %v", err)
		}
		if id.WorkspaceID != "ws-1" || id.UserID != "user-1" {
			t.Fatalf("identity = %+v", id)
		}
	})

	t.Run("wrong key rejected", func(t *testing.T) {
		tok := signTestToken(t, []byte("another-key-another-key-another!"), validClaims(now.Add(time.Hour)))
		if _, err := VerifyHS256(tok, testAuthKey, now); err == nil {
			t.Fatal("want signature error")
		}
	})

	t.Run("expired rejected", func(t *testing.T) {
		tok := signTestToken(t, testAuthKey, validClaims(now.Add(-time.Minute)))
		if _, err := VerifyHS256(tok, testAuthKey, now); err == nil {
			t.Fatal("want expiry error")
		}
	})

	t.Run("missing exp rejected", func(t *testing.T) {
		tok := signTestToken(t, testAuthKey, map[string]any{"sub": "u", "workspaceId": "w"})
		if _, err := VerifyHS256(tok, testAuthKey, now); err == nil {
			t.Fatal("want expiry error for missing exp")
		}
	})

	t.Run("missing claims rejected", func(t *testing.T) {
		tok := signTestToken(t, testAuthKey, map[string]any{"sub": "u", "exp": now.Add(time.Hour).Unix()})
		if _, err := VerifyHS256(tok, testAuthKey, now); err == nil {
			t.Fatal("want claims error")
		}
	})

	t.Run("alg none rejected", func(t *testing.T) {
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
		payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"u","workspaceId":"w","exp":9999999999}`))
		if _, err := VerifyHS256(header+"."+payload+".", testAuthKey, now); err == nil {
			t.Fatal("want alg error")
		}
	})

	t.Run("garbage rejected", func(t *testing.T) {
		if _, err := VerifyHS256("not-a-token", testAuthKey, now); err == nil {
			t.Fatal("want format error")
		}
	})
}
