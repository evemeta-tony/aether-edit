// services/tenancy/server.go

package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// maxBodyBytes caps every JSON request body (S1: validate and bound
// all input at the boundary).
const maxBodyBytes = 1 << 20

// apiKeyUserPrefix marks identities that authenticated with an API key
// instead of a user session.
const apiKeyUserPrefix = "apikey:"

// Server wires the tenancy service handlers to their dependencies.
type Server struct {
	store  Store
	signer AccessSigner
	oidc   *OIDCClient
	tiers  *TierConfig
	quota  *MeteredQuota
	log    *slog.Logger

	accessTokenTTL  time.Duration
	refreshTokenTTL time.Duration
	internalToken   string
	cookieSecure    bool
	now             func() time.Time
}

// NewServer builds a Server over its dependencies.
func NewServer(store Store, signer AccessSigner, oidc *OIDCClient, tiers *TierConfig,
	quota *MeteredQuota, log *slog.Logger, accessTokenTTL, refreshTokenTTL time.Duration,
	internalToken string, cookieSecure bool) *Server {
	return &Server{
		store:           store,
		signer:          signer,
		oidc:            oidc,
		tiers:           tiers,
		quota:           quota,
		log:             log,
		accessTokenTTL:  accessTokenTTL,
		refreshTokenTTL: refreshTokenTTL,
		internalToken:   internalToken,
		cookieSecure:    cookieSecure,
		now:             time.Now,
	}
}

// Routes returns the full handler chain: request id and structured
// logging outermost, then per-group auth, then the API muxes.
func (s *Server) Routes() http.Handler {
	// Authenticated API (bearer JWT or API key).
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/me", s.handleMe)
	api.HandleFunc("POST /v1/workspaces", s.handleCreateWorkspace)
	api.HandleFunc("GET /v1/workspaces", s.handleListWorkspaces)
	api.HandleFunc("GET /v1/workspaces/active", s.handleActiveWorkspace)
	api.HandleFunc("POST /v1/workspaces/switch", s.handleSwitchWorkspace)
	api.HandleFunc("GET /v1/workspaces/{id}/members", s.handleListMembers)
	api.HandleFunc("POST /v1/workspaces/{id}/members", s.handleAddMember)
	api.HandleFunc("PATCH /v1/workspaces/{id}/members/{userId}", s.handleUpdateMemberRole)
	api.HandleFunc("DELETE /v1/workspaces/{id}/members/{userId}", s.handleRemoveMember)
	api.HandleFunc("POST /v1/apikeys", s.handleCreateAPIKey)
	api.HandleFunc("GET /v1/apikeys", s.handleListAPIKeys)
	api.HandleFunc("DELETE /v1/apikeys/{id}", s.handleRevokeAPIKey)
	api.HandleFunc("GET /v1/usage", s.handleUsage)

	// Internal service-to-service quota API (shared internal token).
	internal := http.NewServeMux()
	internal.HandleFunc("POST /internal/v1/quota/check-upload-session", s.handleQuotaCheckUpload)
	internal.HandleFunc("POST /internal/v1/quota/check-job-admission", s.handleQuotaCheckJob)

	root := http.NewServeMux()
	root.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	// Auth flow endpoints authenticate by OIDC redirect, refresh
	// token, or API key exchange; they sit outside the bearer group.
	root.HandleFunc("GET /v1/auth/login", s.handleLogin)
	root.HandleFunc("GET /v1/auth/callback", s.handleCallback)
	root.HandleFunc("POST /v1/auth/refresh", s.handleRefresh)
	root.HandleFunc("POST /v1/auth/logout", s.handleLogout)
	root.HandleFunc("POST /v1/auth/token", s.handleAPIKeyToken)
	root.Handle("/v1/", s.authMiddleware(api))
	root.Handle("/internal/v1/", s.internalAuthMiddleware(internal))
	return s.requestLogger(root)
}

// ---- context plumbing ----

type identityKey struct{}
type requestIDKey struct{}
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

// IdentityFrom returns the request identity placed by authMiddleware.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}

// ---- middleware ----

// authMiddleware accepts either a bearer JWT (Authorization: Bearer
// <jwt>) or a workspace API key (Authorization: Bearer aek_... or the
// X-API-Key header) and stores the resolved Identity in the context.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ""
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			token = strings.TrimPrefix(h, "Bearer ")
		} else if k := r.Header.Get("X-API-Key"); k != "" {
			token = k
		}
		if token == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token or api key")
			return
		}
		var id Identity
		if strings.HasPrefix(token, apiKeyPrefix) {
			key, err := s.verifyAPIKeyString(r.Context(), token)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					writeError(w, http.StatusUnauthorized, "unauthorized", "invalid api key")
				} else {
					s.internalError(w, r, "apikey auth", err)
				}
				return
			}
			id = Identity{UserID: apiKeyUserPrefix + key.ID, WorkspaceID: key.WorkspaceID}
		} else {
			var err error
			id, err = s.signer.Verify(token, s.now())
			if err != nil {
				writeError(w, http.StatusUnauthorized, "unauthorized", "invalid bearer token")
				return
			}
		}
		if state, ok := r.Context().Value(logStateKey{}).(*logState); ok {
			state.setIdentity(id)
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, id)))
	})
}

// internalAuthMiddleware guards the service-to-service quota endpoints
// with the shared internal token.
func (s *Server) internalAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("X-Internal-Token")
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.internalToken)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid internal token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requestLogger assigns a request id, echoes it as X-Request-Id, and
// emits one structured line per request with workspace and user ids
// when the call authenticated.
func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := uuid.NewString()
		w.Header().Set("X-Request-Id", requestID)
		state := &logState{}
		ctx := context.WithValue(r.Context(), requestIDKey{}, requestID)
		ctx = context.WithValue(ctx, logStateKey{}, state)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := s.now()
		next.ServeHTTP(rec, r.WithContext(ctx))
		attrs := []any{
			"requestId", requestID,
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"durationMs", time.Since(start).Milliseconds(),
		}
		if id := state.identity(); id != nil {
			attrs = append(attrs, "workspaceId", id.WorkspaceID, "userId", id.UserID)
		}
		s.log.Info("request", attrs...)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// ---- handler helpers ----

// requireIdentity pulls the authenticated identity or writes 401.
func (s *Server) requireIdentity(w http.ResponseWriter, r *http.Request) (Identity, bool) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no identity")
		return Identity{}, false
	}
	return id, true
}

// requireUser is requireIdentity plus a check that the caller is a
// human session, not an API key (workspace and key management need a
// user identity for role checks and attribution).
func (s *Server) requireUser(w http.ResponseWriter, r *http.Request) (Identity, bool) {
	id, ok := s.requireIdentity(w, r)
	if !ok {
		return Identity{}, false
	}
	if strings.HasPrefix(id.UserID, apiKeyUserPrefix) {
		writeError(w, http.StatusForbidden, "user_required", "this endpoint requires a user session, not an api key")
		return Identity{}, false
	}
	return id, true
}

// roleRank orders roles for minimum-role checks.
func roleRank(role string) int {
	switch role {
	case RoleOwner:
		return 3
	case RoleAdmin:
		return 2
	case RoleMember:
		return 1
	default:
		return 0
	}
}

// requireRole verifies the user holds at least minRole in the
// workspace, writing 403/404 as appropriate. Returns true on success.
func (s *Server) requireRole(w http.ResponseWriter, r *http.Request, workspaceID, userID, minRole string) bool {
	m, err := s.store.GetMembership(r.Context(), workspaceID, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusForbidden, "not_a_member", "caller is not a member of this workspace")
			return false
		}
		s.internalError(w, r, "membership lookup", err)
		return false
	}
	if roleRank(m.Role) < roleRank(minRole) {
		writeError(w, http.StatusForbidden, "insufficient_role",
			fmt.Sprintf("this action requires the %s role or better", minRole))
		return false
	}
	return true
}

// decodeJSONBody strictly decodes a bounded JSON body into v. It
// rejects unknown fields, trailing data, and oversized bodies, and
// writes the error response itself; callers bail out on false.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON with known fields only")
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must contain a single JSON object")
		return false
	}
	return true
}

// internalError logs the failure with the request id and returns an
// opaque 500.
func (s *Server) internalError(w http.ResponseWriter, r *http.Request, what string, err error) {
	requestID, _ := r.Context().Value(requestIDKey{}).(string)
	s.log.Error(what, "requestId", requestID, "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "internal error")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
