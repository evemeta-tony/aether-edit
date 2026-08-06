// services/orchestrator/internal/auth/auth.go
//
// Bearer-token auth middleware following the FT-2 contract: requests carry
// Authorization: Bearer <JWT>; claims provide the user id (sub) and the
// workspace id (workspaceId). Tokens are HS256-signed with a shared secret
// supplied via environment (S5: no secrets in the repo). Requests without a
// valid token and both claims are rejected with 401; nothing is coerced.
package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// Identity is the authenticated caller.
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

// Verifier validates bearer tokens.
type Verifier struct {
	secret []byte
}

// NewVerifier builds a Verifier; the secret must be non-empty.
func NewVerifier(secret string) (*Verifier, error) {
	if len(secret) < 16 {
		return nil, fmt.Errorf("auth: JWT secret must be at least 16 bytes")
	}
	return &Verifier{secret: []byte(secret)}, nil
}

// claims is the expected token claim set.
type claims struct {
	WorkspaceID string `json:"workspaceId"`
	jwt.RegisteredClaims
}

// Verify parses and validates a compact JWT and extracts the identity.
func (v *Verifier) Verify(token string) (Identity, error) {
	var c claims
	parsed, err := jwt.ParseWithClaims(token, &c, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method %s", t.Method.Alg())
		}
		return v.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		// Tokens must carry exp; without this option golang-jwt v5 accepts
		// unbounded-lifetime tokens (Argus PR#4 finding 13).
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
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, `{"error":"missing bearer token"}`, http.StatusUnauthorized)
			return
		}
		id, err := v.Verify(strings.TrimPrefix(h, prefix))
		if err != nil {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, `{"error":"invalid bearer token"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	})
}
