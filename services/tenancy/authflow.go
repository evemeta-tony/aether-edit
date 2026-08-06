// services/tenancy/authflow.go

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// loginStateTTL bounds how long an OIDC redirect may stay in flight.
const loginStateTTL = 10 * time.Minute

// refreshCookieName carries the refresh token for browser flows. It is
// HttpOnly and scoped to the auth endpoints only.
const refreshCookieName = "aether_refresh"

// sessionResponse is the shape returned by callback, refresh, and
// workspace switch: everything the console needs to run.
type sessionResponse struct {
	AccessToken       string   `json:"accessToken"`
	TokenType         string   `json:"tokenType"`
	ExpiresIn         int64    `json:"expiresIn"`
	RefreshToken      string   `json:"refreshToken,omitempty"`
	User              userView `json:"user"`
	ActiveWorkspaceID string   `json:"activeWorkspaceId"`
}

type userView struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// handleLogin starts the OIDC authorization-code flow: it saves the
// single-use state (CSRF state, nonce, PKCE verifier) and redirects to
// the Google authorization endpoint.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	state, err := newOpaqueToken(32)
	if err != nil {
		s.internalError(w, r, "login state", err)
		return
	}
	nonce, err := newOpaqueToken(32)
	if err != nil {
		s.internalError(w, r, "login nonce", err)
		return
	}
	verifier, err := newOpaqueToken(48)
	if err != nil {
		s.internalError(w, r, "login pkce", err)
		return
	}
	now := s.now()
	ls := LoginState{
		State:        state,
		Nonce:        nonce,
		PKCEVerifier: verifier,
		CreatedAt:    now,
		ExpiresAt:    now.Add(loginStateTTL),
	}
	if err := s.store.SaveLoginState(r.Context(), ls); err != nil {
		s.internalError(w, r, "login state save", err)
		return
	}
	http.Redirect(w, r, s.oidc.AuthCodeURL(state, nonce, verifier), http.StatusFound)
}

// handleCallback finishes the flow: single-use state check, code
// exchange, ID token verification, user upsert (with first-login
// workspace bootstrap), and session issuance.
func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if errCode := q.Get("error"); errCode != "" {
		writeError(w, http.StatusBadRequest, "oidc_error",
			fmt.Sprintf("identity provider returned error %q", errCode))
		return
	}
	state := q.Get("state")
	code := q.Get("code")
	if state == "" || code == "" {
		writeError(w, http.StatusBadRequest, "invalid_callback", "state and code are required")
		return
	}
	now := s.now()
	ls, err := s.store.TakeLoginState(r.Context(), state, now)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusBadRequest, "invalid_state", "unknown, expired, or already used state")
			return
		}
		s.internalError(w, r, "login state take", err)
		return
	}
	rawIDToken, err := s.oidc.Exchange(r.Context(), code, ls.PKCEVerifier)
	if err != nil {
		s.log.Warn("oidc exchange failed", "err", err)
		writeError(w, http.StatusUnauthorized, "oidc_exchange_failed", "authorization code exchange failed")
		return
	}
	verified, err := s.oidc.VerifyIDToken(r.Context(), rawIDToken, ls.Nonce, now)
	if err != nil {
		s.log.Warn("id token verification failed", "err", err)
		writeError(w, http.StatusUnauthorized, "oidc_token_invalid", "id token verification failed")
		return
	}

	user, err := s.store.UpsertUserByGoogleSub(r.Context(), verified.Subject, verified.Email, verified.Name, now)
	if err != nil {
		s.internalError(w, r, "user upsert", err)
		return
	}
	if user.ActiveWorkspaceID == "" {
		ws, err := s.bootstrapWorkspace(r.Context(), user)
		if err != nil {
			s.internalError(w, r, "workspace bootstrap", err)
			return
		}
		user.ActiveWorkspaceID = ws.ID
	}

	resp, err := s.issueSession(r.Context(), user, "")
	if err != nil {
		s.internalError(w, r, "session issue", err)
		return
	}
	s.setRefreshCookie(w, resp.RefreshToken, now.Add(s.refreshTokenTTL))
	s.log.Info("login", "userId", user.ID, "workspaceId", user.ActiveWorkspaceID)
	writeJSON(w, http.StatusOK, resp)
}

// bootstrapWorkspace creates the first-login personal workspace on the
// default plan tier and makes the user its owner and it their active
// workspace.
func (s *Server) bootstrapWorkspace(ctx context.Context, user User) (Workspace, error) {
	name := user.Name
	if at := strings.IndexByte(user.Email, '@'); name == user.Email && at > 0 {
		name = user.Email[:at]
	}
	ws := Workspace{
		ID:        uuid.NewString(),
		Name:      name + "'s workspace",
		PlanTier:  s.tiers.DefaultTier,
		CreatedBy: user.ID,
		CreatedAt: s.now(),
	}
	if err := s.store.CreateWorkspace(ctx, ws, user.ID); err != nil {
		return Workspace{}, err
	}
	if err := s.store.SetActiveWorkspace(ctx, user.ID, ws.ID); err != nil {
		return Workspace{}, err
	}
	return ws, nil
}

// issueSession mints an access token for the user's active workspace
// plus a fresh refresh token. A non-empty familyID continues an
// existing rotation family; empty starts a new one.
func (s *Server) issueSession(ctx context.Context, user User, familyID string) (sessionResponse, error) {
	now := s.now()
	if user.ActiveWorkspaceID == "" {
		return sessionResponse{}, fmt.Errorf("user %s has no active workspace", user.ID)
	}
	if _, err := s.store.GetMembership(ctx, user.ActiveWorkspaceID, user.ID); err != nil {
		return sessionResponse{}, fmt.Errorf("active workspace membership: %w", err)
	}
	access, err := s.signer.Mint(user.ID, user.ActiveWorkspaceID, now, s.accessTokenTTL)
	if err != nil {
		return sessionResponse{}, err
	}
	rawRefresh, err := newOpaqueToken(32)
	if err != nil {
		return sessionResponse{}, err
	}
	if familyID == "" {
		familyID = uuid.NewString()
	}
	rt := RefreshToken{
		ID:        uuid.NewString(),
		UserID:    user.ID,
		FamilyID:  familyID,
		TokenHash: hashOpaqueToken(rawRefresh),
		CreatedAt: now,
		ExpiresAt: now.Add(s.refreshTokenTTL),
	}
	if err := s.store.CreateRefreshToken(ctx, rt); err != nil {
		return sessionResponse{}, err
	}
	return sessionResponse{
		AccessToken:       access,
		TokenType:         "Bearer",
		ExpiresIn:         int64(s.accessTokenTTL.Seconds()),
		RefreshToken:      rawRefresh,
		User:              userView{ID: user.ID, Email: user.Email, Name: user.Name},
		ActiveWorkspaceID: user.ActiveWorkspaceID,
	}, nil
}

type refreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}

// takeRefreshToken pulls the raw refresh token from the JSON body or,
// failing that, the auth cookie.
func (s *Server) takeRefreshToken(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r.Header.Get("Content-Type") != "" && r.ContentLength != 0 {
		var req refreshRequest
		if !decodeJSONBody(w, r, &req) {
			return "", false
		}
		if req.RefreshToken != "" {
			return req.RefreshToken, true
		}
	}
	if c, err := r.Cookie(refreshCookieName); err == nil && c.Value != "" {
		return c.Value, true
	}
	writeError(w, http.StatusBadRequest, "missing_refresh_token", "refresh token required in body or cookie")
	return "", false
}

// handleRefresh rotates the refresh token: the presented token is
// single use; presenting an already-used token is treated as theft
// evidence and revokes the whole family (server-side revocation).
func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	raw, ok := s.takeRefreshToken(w, r)
	if !ok {
		return
	}
	now := s.now()
	rt, err := s.store.GetRefreshTokenByHash(r.Context(), hashOpaqueToken(raw))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "invalid_refresh_token", "unknown refresh token")
			return
		}
		s.internalError(w, r, "refresh lookup", err)
		return
	}
	switch {
	case rt.RevokedAt != nil:
		writeError(w, http.StatusUnauthorized, "refresh_revoked", "refresh token has been revoked")
		return
	case rt.UsedAt != nil:
		// Rotation reuse: revoke the family and refuse.
		if err := s.store.RevokeRefreshFamily(r.Context(), rt.FamilyID, now); err != nil {
			s.internalError(w, r, "refresh family revoke", err)
			return
		}
		s.log.Warn("refresh token reuse detected; family revoked", "userId", rt.UserID, "familyId", rt.FamilyID)
		writeError(w, http.StatusUnauthorized, "refresh_reused", "refresh token reuse detected; session revoked")
		return
	case now.After(rt.ExpiresAt):
		writeError(w, http.StatusUnauthorized, "refresh_expired", "refresh token expired")
		return
	}
	if err := s.store.MarkRefreshTokenUsed(r.Context(), rt.ID, now); err != nil {
		s.internalError(w, r, "refresh mark used", err)
		return
	}
	user, err := s.store.GetUser(r.Context(), rt.UserID)
	if err != nil {
		s.internalError(w, r, "refresh user lookup", err)
		return
	}
	resp, err := s.issueSession(r.Context(), user, rt.FamilyID)
	if err != nil {
		s.internalError(w, r, "session issue", err)
		return
	}
	s.setRefreshCookie(w, resp.RefreshToken, now.Add(s.refreshTokenTTL))
	writeJSON(w, http.StatusOK, resp)
}

// handleLogout revokes the presented refresh token's whole family and
// clears the cookie. Access tokens expire on their own short TTL.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	raw, ok := s.takeRefreshToken(w, r)
	if !ok {
		return
	}
	rt, err := s.store.GetRefreshTokenByHash(r.Context(), hashOpaqueToken(raw))
	if err != nil && !errors.Is(err, ErrNotFound) {
		s.internalError(w, r, "logout lookup", err)
		return
	}
	if err == nil {
		if err := s.store.RevokeRefreshFamily(r.Context(), rt.FamilyID, s.now()); err != nil {
			s.internalError(w, r, "logout revoke", err)
			return
		}
		s.log.Info("logout", "userId", rt.UserID, "familyId", rt.FamilyID)
	}
	s.setRefreshCookie(w, "", time.Unix(0, 0))
	w.WriteHeader(http.StatusNoContent)
}

type apiKeyTokenRequest struct {
	APIKey string `json:"apiKey"`
}

// handleAPIKeyToken exchanges a workspace API key for a short-lived
// access token, so API key holders can call FT-2 and FT-3, whose
// middlewares verify the shared HS256 claims contract.
func (s *Server) handleAPIKeyToken(w http.ResponseWriter, r *http.Request) {
	raw := r.Header.Get("X-API-Key")
	if raw == "" && r.ContentLength != 0 {
		var req apiKeyTokenRequest
		if !decodeJSONBody(w, r, &req) {
			return
		}
		raw = req.APIKey
	}
	if raw == "" {
		writeError(w, http.StatusBadRequest, "missing_api_key", "api key required in X-API-Key header or body")
		return
	}
	key, err := s.verifyAPIKeyString(r.Context(), raw)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid api key")
			return
		}
		s.internalError(w, r, "apikey token", err)
		return
	}
	access, err := s.signer.Mint(apiKeyUserPrefix+key.ID, key.WorkspaceID, s.now(), s.accessTokenTTL)
	if err != nil {
		s.internalError(w, r, "apikey token mint", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"accessToken": access,
		"tokenType":   "Bearer",
		"expiresIn":   int64(s.accessTokenTTL.Seconds()),
		"workspaceId": key.WorkspaceID,
	})
}

// setRefreshCookie writes (or clears) the HttpOnly refresh cookie.
func (s *Server) setRefreshCookie(w http.ResponseWriter, value string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    value,
		Path:     "/v1/auth",
		Expires:  expires,
		HttpOnly: true,
		Secure:   s.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}
