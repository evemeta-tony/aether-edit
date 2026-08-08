// services/telemetry/internal/auth/auth.go

// Package auth implements the HS256 JWT bearer-token middleware shared by the
// aether-edit services (FT-6a tenancy is the signer). Requests carry
// "Authorization: Bearer <JWT>"; tokens are HS256-signed with the workspace
// signing key, and their claims provide the user id (sub) and the workspace id
// (workspaceId). Tokens without a valid signature, without exp, expired, not
// yet valid (nbf), or missing either required claim are rejected with a 401
// JSON error. Nothing is coerced. The signing key is the base64url-decoded
// bytes of the env value, matching tenancy/config.go and upload/config.go (S5:
// no secrets in the repo).
package auth

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// Identity is the authenticated caller extracted from the bearer token.
type Identity struct {
	UserID      string
	WorkspaceID string
}

type ctxKey struct{}

// FromContext returns the Identity set by Middleware.
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(ctxKey{}).(Identity)
	return id, ok
}

// Verifier validates HS256 bearer tokens against a shared signing key.
type Verifier struct {
	key []byte
	log *slog.Logger
}

// NewVerifier builds a Verifier. The key is the raw (already base64url-decoded)
// HMAC key; it must be at least 32 bytes, matching the other services.
func NewVerifier(key []byte) (*Verifier, error) {
	if len(key) < 32 {
		return nil, fmt.Errorf("auth: HS256 key must be at least 32 bytes")
	}
	return &Verifier{key: key, log: slog.Default()}, nil
}

// WithLogger returns the Verifier with its debug logger set. Auth failures are
// logged at debug level (reason only, never the token) so an operator can tell
// an attack from clock skew or a key-rotation break; the client still receives
// only a flat 401 with no failure detail.
func (v *Verifier) WithLogger(log *slog.Logger) *Verifier {
	if log != nil {
		v.log = log
	}
	return v
}

// claims is the expected token claim set.
type claims struct {
	WorkspaceID string `json:"workspaceId"`
	jwt.RegisteredClaims
}

// Verify parses and validates a compact JWT and extracts the identity. Only
// HS256 is accepted; exp is required and enforced, and nbf is enforced by
// golang-jwt when present.
func (v *Verifier) Verify(token string) (Identity, error) {
	var c claims
	parsed, err := jwt.ParseWithClaims(token, &c, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method %s", t.Method.Alg())
		}
		return v.key, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		// Tokens must carry exp; without this option golang-jwt v5 accepts
		// unbounded-lifetime tokens.
		jwt.WithExpirationRequired())
	if err != nil {
		return Identity{}, err
	}
	if !parsed.Valid {
		return Identity{}, fmt.Errorf("invalid token")
	}
	if c.Subject == "" {
		return Identity{}, fmt.Errorf("token missing sub claim")
	}
	if c.WorkspaceID == "" {
		return Identity{}, fmt.Errorf("token missing workspaceId claim")
	}
	return Identity{UserID: c.Subject, WorkspaceID: c.WorkspaceID}, nil
}

// Middleware enforces bearer auth and injects the Identity into the request
// context.
func (v *Verifier) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(h, prefix) {
			v.log.Debug("auth: missing bearer token", "path", r.URL.Path, "remote", r.RemoteAddr)
			unauthorized(w)
			return
		}
		id, err := v.Verify(strings.TrimPrefix(h, prefix))
		if err != nil {
			v.log.Debug("auth: token rejected", "path", r.URL.Path, "remote", r.RemoteAddr, "reason", err)
			unauthorized(w)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	})
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", "Bearer")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte(`{"error":"unauthorized"}`))
}
