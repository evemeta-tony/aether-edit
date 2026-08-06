// services/telemetry/internal/auth/auth.go

// Package auth implements the shared bearer-token middleware contract used
// by the FT-2 and FT-3 services: "Authorization: Bearer <token>" with a
// constant-time comparison, 401 JSON error on failure.
package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

// Middleware returns a middleware enforcing the shared bearer-auth contract
// with the given static token.
func Middleware(token string) func(http.Handler) http.Handler {
	want := sha256.Sum256([]byte(token))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(h, prefix) {
				unauthorized(w)
				return
			}
			got := sha256.Sum256([]byte(strings.TrimPrefix(h, prefix)))
			if subtle.ConstantTimeCompare(want[:], got[:]) != 1 {
				unauthorized(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", "Bearer")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte(`{"error":"unauthorized"}`))
}
