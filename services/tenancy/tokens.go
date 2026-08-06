// services/tenancy/tokens.go

package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// tokenIssuer names this service in the iss claim. FT-2 and FT-3
// middlewares do not require iss; it is informational until the
// asymmetric upgrade.
const tokenIssuer = "aether-tenancy"

// AccessSigner mints and verifies access tokens. It is an interface so
// the recorded follow-up (asymmetric RS256/EdDSA keys published over
// JWKS) swaps in without touching call sites; hs256Signer implements
// the frozen HS256 shared-key contract that the FT-2 and FT-3
// middlewares verify today.
type AccessSigner interface {
	// Alg returns the JWS algorithm name (for logs and the future
	// JWKS document).
	Alg() string
	// Mint issues a short-lived access token carrying the frozen
	// claims contract: sub (user id), workspaceId, exp, nbf.
	Mint(userID, workspaceID string, now time.Time, ttl time.Duration) (string, error)
	// Verify validates a token and returns the caller identity.
	Verify(token string, now time.Time) (Identity, error)
}

// Identity is the authenticated caller extracted from an access token
// or an API key. For API key callers UserID is "apikey:<key id>".
type Identity struct {
	UserID      string
	WorkspaceID string
}

// accessClaims is the frozen claims contract plus registered claims.
type accessClaims struct {
	WorkspaceID string `json:"workspaceId"`
	jwt.RegisteredClaims
}

// hs256Signer signs with the shared HMAC key every FT service holds.
type hs256Signer struct {
	key []byte
}

var _ AccessSigner = (*hs256Signer)(nil)

// newHS256Signer builds the signer; the key must be at least 32 bytes.
func newHS256Signer(key []byte) (*hs256Signer, error) {
	if len(key) < 32 {
		return nil, fmt.Errorf("hs256 signer: key must be at least 32 bytes")
	}
	return &hs256Signer{key: key}, nil
}

func (s *hs256Signer) Alg() string { return "HS256" }

func (s *hs256Signer) Mint(userID, workspaceID string, now time.Time, ttl time.Duration) (string, error) {
	if userID == "" || workspaceID == "" {
		return "", fmt.Errorf("mint: userID and workspaceID are required")
	}
	claims := accessClaims{
		WorkspaceID: workspaceID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			ID:        uuid.NewString(),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(s.key)
	if err != nil {
		return "", fmt.Errorf("mint: %w", err)
	}
	return signed, nil
}

func (s *hs256Signer) Verify(token string, now time.Time) (Identity, error) {
	var c accessClaims
	_, err := jwt.ParseWithClaims(token, &c, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method %s", t.Method.Alg())
		}
		return s.key, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithTimeFunc(func() time.Time { return now }),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return Identity{}, fmt.Errorf("verify: %w", err)
	}
	if c.Subject == "" || c.WorkspaceID == "" {
		return Identity{}, fmt.Errorf("verify: missing sub or workspaceId claim")
	}
	return Identity{UserID: c.Subject, WorkspaceID: c.WorkspaceID}, nil
}

// newOpaqueToken returns cryptographically random URL-safe material of
// nBytes entropy (refresh tokens, OIDC state and nonce, API key
// secrets).
func newOpaqueToken(nBytes int) (string, error) {
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("token entropy: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// hashOpaqueToken is the at-rest form of refresh tokens: SHA-256, hex.
// A 256-bit random token needs no memory-hard hash; preimage search is
// infeasible, and a plain digest keeps refresh verification cheap. API
// keys use argon2id instead (apikeys.go) because their format is
// long-lived and user-held.
func hashOpaqueToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
