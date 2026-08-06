// services/tenancy/apikeys.go

package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/argon2"
)

// API key format: "aek_<key id>_<secret>", where the key id is a
// hyphenated uuid (no underscores, so the id/secret split is
// unambiguous) and the secret is 32 bytes of entropy encoded
// base64url. Only the argon2id hash of the secret is stored.
const apiKeyPrefix = "aek_"

// argon2id parameters (OWASP minimum profile: 19 MiB, t=2, p=1). They
// are recorded inside each PHC hash string, so future parameter bumps
// verify old hashes transparently.
const (
	argonTime    uint32 = 2
	argonMemory  uint32 = 19 * 1024
	argonThreads uint8  = 1
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

// hashAPIKeySecret returns a PHC-format argon2id string for secret.
func hashAPIKeySecret(secret string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("apikey salt: %w", err)
	}
	digest := argon2.IDKey([]byte(secret), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(digest)), nil
}

// verifyAPIKeySecret checks secret against a PHC argon2id string in
// constant time over the digest.
func verifyAPIKeySecret(secret, phc string) (bool, error) {
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, fmt.Errorf("apikey hash: not an argon2id PHC string")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, fmt.Errorf("apikey hash: bad version field: %w", err)
	}
	if version != argon2.Version {
		return false, fmt.Errorf("apikey hash: unsupported argon2 version %d", version)
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false, fmt.Errorf("apikey hash: bad parameter field: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("apikey hash: bad salt: %w", err)
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("apikey hash: bad digest: %w", err)
	}
	got := argon2.IDKey([]byte(secret), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// mintAPIKey builds a fresh key id and raw key string.
func mintAPIKey() (id, raw, secret string, err error) {
	id = uuid.NewString()
	secret, err = newOpaqueToken(32)
	if err != nil {
		return "", "", "", err
	}
	return id, apiKeyPrefix + id + "_" + secret, secret, nil
}

// splitAPIKey parses "aek_<id>_<secret>". ok is false for anything
// that does not look like an API key.
func splitAPIKey(raw string) (id, secret string, ok bool) {
	if !strings.HasPrefix(raw, apiKeyPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(raw, apiKeyPrefix)
	id, secret, found := strings.Cut(rest, "_")
	if !found || id == "" || secret == "" {
		return "", "", false
	}
	return id, secret, true
}

// verifyAPIKeyString resolves a raw API key to its stored record,
// enforcing revocation. It returns ErrNotFound for unknown, malformed,
// revoked, or mismatched keys so callers cannot distinguish them.
func (s *Server) verifyAPIKeyString(ctx context.Context, raw string) (APIKey, error) {
	id, secret, ok := splitAPIKey(raw)
	if !ok {
		return APIKey{}, ErrNotFound
	}
	key, err := s.store.GetAPIKey(ctx, id)
	if err != nil {
		return APIKey{}, err
	}
	if key.RevokedAt != nil {
		return APIKey{}, ErrNotFound
	}
	match, err := verifyAPIKeySecret(secret, key.SecretHash)
	if err != nil {
		return APIKey{}, fmt.Errorf("apikey verify: %w", err)
	}
	if !match {
		return APIKey{}, ErrNotFound
	}
	if err := s.store.TouchAPIKey(ctx, key.ID, s.now()); err != nil {
		s.log.Warn("apikey touch failed", "keyId", key.ID, "err", err)
	}
	return key, nil
}

// ---- HTTP handlers ----

type createAPIKeyRequest struct {
	Name string `json:"name"`
}

type apiKeyView struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspaceId"`
	Name        string     `json:"name"`
	CreatedBy   string     `json:"createdBy"`
	CreatedAt   time.Time  `json:"createdAt"`
	LastUsedAt  *time.Time `json:"lastUsedAt,omitempty"`
	RevokedAt   *time.Time `json:"revokedAt,omitempty"`
}

func viewAPIKey(k APIKey) apiKeyView {
	return apiKeyView{
		ID: k.ID, WorkspaceID: k.WorkspaceID, Name: k.Name,
		CreatedBy: k.CreatedBy, CreatedAt: k.CreatedAt,
		LastUsedAt: k.LastUsedAt, RevokedAt: k.RevokedAt,
	}
}

// handleCreateAPIKey mints a key for the active workspace. The raw key
// appears once in this response and is never retrievable again.
// Requires the admin role or better.
func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if !s.requireRole(w, r, id.WorkspaceID, id.UserID, RoleAdmin) {
		return
	}
	var req createAPIKeyRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 128 {
		writeError(w, http.StatusBadRequest, "invalid_name", "name is required and at most 128 characters")
		return
	}
	keyID, raw, secret, err := mintAPIKey()
	if err != nil {
		s.internalError(w, r, "apikey mint", err)
		return
	}
	hash, err := hashAPIKeySecret(secret)
	if err != nil {
		s.internalError(w, r, "apikey hash", err)
		return
	}
	rec := APIKey{
		ID:          keyID,
		WorkspaceID: id.WorkspaceID,
		Name:        req.Name,
		SecretHash:  hash,
		CreatedBy:   id.UserID,
		CreatedAt:   s.now(),
	}
	if err := s.store.CreateAPIKey(r.Context(), rec); err != nil {
		s.internalError(w, r, "apikey create", err)
		return
	}
	s.log.Info("apikey created", "workspaceId", id.WorkspaceID, "keyId", keyID, "by", id.UserID)
	writeJSON(w, http.StatusCreated, map[string]any{
		"key":    raw,
		"apiKey": viewAPIKey(rec),
	})
}

// handleListAPIKeys lists keys for the active workspace (hashes never
// leave the store). Any member may list.
func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if !s.requireRole(w, r, id.WorkspaceID, id.UserID, RoleMember) {
		return
	}
	keys, err := s.store.ListAPIKeys(r.Context(), id.WorkspaceID)
	if err != nil {
		s.internalError(w, r, "apikey list", err)
		return
	}
	views := make([]apiKeyView, 0, len(keys))
	for _, k := range keys {
		views = append(views, viewAPIKey(k))
	}
	writeJSON(w, http.StatusOK, map[string]any{"apiKeys": views})
}

// handleRevokeAPIKey revokes a key in the active workspace. Requires
// the admin role or better. Revocation is immediate: the auth
// middleware refuses revoked keys on the next request.
func (s *Server) handleRevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if !s.requireRole(w, r, id.WorkspaceID, id.UserID, RoleAdmin) {
		return
	}
	keyID := r.PathValue("id")
	if keyID == "" {
		writeError(w, http.StatusBadRequest, "invalid_key_id", "key id is required")
		return
	}
	if err := s.store.RevokeAPIKey(r.Context(), id.WorkspaceID, keyID, s.now()); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "key_not_found", "no such api key in this workspace")
			return
		}
		s.internalError(w, r, "apikey revoke", err)
		return
	}
	s.log.Info("apikey revoked", "workspaceId", id.WorkspaceID, "keyId", keyID, "by", id.UserID)
	w.WriteHeader(http.StatusNoContent)
}
