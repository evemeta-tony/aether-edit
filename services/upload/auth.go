// services/upload/auth.go

package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Identity is the authenticated caller extracted from the bearer token.
type Identity struct {
	WorkspaceID string
	UserID      string
}

type identityKey struct{}

// requestIDKey carries the generated request id through the context.
type requestIDKey struct{}

// logStateKey carries a mutable logState so inner middleware can hand
// the authenticated identity back to the outer request logger.
type logStateKey struct{}

type logState struct {
	mu sync.Mutex
	id *Identity
}

func (l *logState) setIdentity(id Identity) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.id = &id
}

func (l *logState) identity() *Identity {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.id
}

// IdentityFrom returns the request identity placed by AuthMiddleware.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}

// Verification errors.
var (
	errTokenFormat    = errors.New("malformed token")
	errTokenAlg       = errors.New("unsupported token algorithm")
	errTokenSignature = errors.New("bad token signature")
	errTokenExpired   = errors.New("token expired")
	errTokenClaims    = errors.New("missing required claims")
)

type tokenHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

type tokenClaims struct {
	Sub         string `json:"sub"`
	WorkspaceID string `json:"workspaceId"`
	Exp         int64  `json:"exp"`
	Nbf         int64  `json:"nbf"`
}

// VerifyHS256 validates a compact JWS signed with HS256 against key and
// returns the caller identity. The full OIDC issuer wiring is FT6/S5
// scope; this middleware contract is real on its own: it rejects
// missing, malformed, expired, and wrongly signed tokens and extracts
// workspaceId and userId claims.
func VerifyHS256(token string, key []byte, now time.Time) (Identity, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Identity{}, errTokenFormat
	}
	headerRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Identity{}, errTokenFormat
	}
	var hdr tokenHeader
	if err := json.Unmarshal(headerRaw, &hdr); err != nil {
		return Identity{}, errTokenFormat
	}
	if hdr.Alg != "HS256" {
		return Identity{}, errTokenAlg
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Identity{}, errTokenFormat
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return Identity{}, errTokenSignature
	}

	claimsRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Identity{}, errTokenFormat
	}
	var claims tokenClaims
	if err := json.Unmarshal(claimsRaw, &claims); err != nil {
		return Identity{}, errTokenFormat
	}
	if claims.Exp == 0 || now.Unix() >= claims.Exp {
		return Identity{}, errTokenExpired
	}
	if claims.Nbf != 0 && now.Unix() < claims.Nbf {
		return Identity{}, errTokenExpired
	}
	if claims.Sub == "" || claims.WorkspaceID == "" {
		return Identity{}, errTokenClaims
	}
	return Identity{WorkspaceID: claims.WorkspaceID, UserID: claims.Sub}, nil
}

// AuthMiddleware validates the Authorization bearer token with the
// configured HS256 key and stores the Identity in the request context.
func AuthMiddleware(key []byte, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(header, prefix) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
			return
		}
		id, err := VerifyHS256(strings.TrimPrefix(header, prefix), key, time.Now())
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid bearer token")
			return
		}
		if state, ok := r.Context().Value(logStateKey{}).(*logState); ok {
			state.setIdentity(id)
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, id)))
	})
}
