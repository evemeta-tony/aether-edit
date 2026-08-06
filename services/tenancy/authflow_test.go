// services/tenancy/authflow_test.go

package main

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

// TestOIDCLoginFlow drives the full authorization-code flow against
// the fake issuer: login redirect, callback exchange, ID token
// verification, first-login workspace bootstrap, and session issue.
func TestOIDCLoginFlow(t *testing.T) {
	env := newTestEnv(t)
	sess := env.login("google-sub-1", "tony@example.com", "Tony")

	if sess.AccessToken == "" || sess.RefreshToken == "" {
		t.Fatalf("session missing tokens: %+v", sess)
	}
	if sess.User.Email != "tony@example.com" {
		t.Fatalf("user email %q", sess.User.Email)
	}
	if sess.ActiveWorkspaceID == "" {
		t.Fatal("no active workspace bootstrapped on first login")
	}
	// The minted access token satisfies the frozen claims contract:
	// sub and workspaceId extractable by the shared HS256 verifier.
	id, err := env.signer.Verify(sess.AccessToken, time.Now())
	if err != nil {
		t.Fatalf("verify access token: %v", err)
	}
	if id.UserID != sess.User.ID || id.WorkspaceID != sess.ActiveWorkspaceID {
		t.Fatalf("claims %+v do not match session %+v", id, sess)
	}
	// Expired token is rejected.
	if _, err := env.signer.Verify(sess.AccessToken, time.Now().Add(16*time.Minute)); err == nil {
		t.Fatal("expired access token verified")
	}
	// Second login with the same Google sub reuses the user and
	// workspace instead of bootstrapping another.
	sess2 := env.login("google-sub-1", "tony@example.com", "Tony")
	if sess2.User.ID != sess.User.ID {
		t.Fatalf("second login created a new user: %s vs %s", sess2.User.ID, sess.User.ID)
	}
	if sess2.ActiveWorkspaceID != sess.ActiveWorkspaceID {
		t.Fatalf("second login changed the active workspace")
	}
}

// TestOIDCStateSingleUse verifies the state is single use and unknown
// or replayed states are refused.
func TestOIDCStateSingleUse(t *testing.T) {
	env := newTestEnv(t)

	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := hc.Get(env.http.URL + "/v1/auth/login")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	q := loc.Query()
	code := env.issuer.NewCode(t, q.Get("nonce"), q.Get("code_challenge"), "s", "s@example.com", "S", true)

	first, err := http.Get(env.http.URL + "/v1/auth/callback?state=" + url.QueryEscape(q.Get("state")) + "&code=" + url.QueryEscape(code))
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first callback status %d", first.StatusCode)
	}
	// Replay of the same state must fail.
	second, err := http.Get(env.http.URL + "/v1/auth/callback?state=" + url.QueryEscape(q.Get("state")) + "&code=x")
	if err != nil {
		t.Fatalf("callback replay: %v", err)
	}
	wantStatus(t, second, http.StatusBadRequest)
	// A state the service never issued must fail.
	unknown, err := http.Get(env.http.URL + "/v1/auth/callback?state=forged&code=x")
	if err != nil {
		t.Fatalf("callback forged: %v", err)
	}
	wantStatus(t, unknown, http.StatusBadRequest)
}

// TestOIDCRejectsUnverifiedEmail refuses ID tokens whose email is not
// verified by the provider.
func TestOIDCRejectsUnverifiedEmail(t *testing.T) {
	env := newTestEnv(t)
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := hc.Get(env.http.URL + "/v1/auth/login")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	q := loc.Query()
	code := env.issuer.NewCode(t, q.Get("nonce"), q.Get("code_challenge"), "s2", "bad@example.com", "Bad", false)
	cb, err := http.Get(env.http.URL + "/v1/auth/callback?state=" + url.QueryEscape(q.Get("state")) + "&code=" + url.QueryEscape(code))
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	wantStatus(t, cb, http.StatusUnauthorized)
}

// TestRefreshRotation exercises rotation, reuse detection with family
// revocation, and logout revocation.
func TestRefreshRotation(t *testing.T) {
	env := newTestEnv(t)
	sess := env.login("google-sub-r", "r@example.com", "R")

	// First refresh rotates: new access and refresh tokens.
	r1 := env.do(http.MethodPost, "/v1/auth/refresh", "", map[string]string{"refreshToken": sess.RefreshToken})
	wantStatus(t, r1, http.StatusOK)
	var s1 sessionResponse
	decodeBody(t, r1, &s1)
	if s1.RefreshToken == sess.RefreshToken {
		t.Fatal("refresh token not rotated")
	}
	if s1.AccessToken == "" {
		t.Fatal("no access token from refresh")
	}

	// Reusing the consumed token is theft evidence: 401 and the
	// whole family is revoked, including the fresh token.
	reuse := env.do(http.MethodPost, "/v1/auth/refresh", "", map[string]string{"refreshToken": sess.RefreshToken})
	wantStatus(t, reuse, http.StatusUnauthorized)
	after := env.do(http.MethodPost, "/v1/auth/refresh", "", map[string]string{"refreshToken": s1.RefreshToken})
	wantStatus(t, after, http.StatusUnauthorized)

	// A fresh login then logout: the refresh token is revoked server
	// side and refuses to rotate.
	sess2 := env.login("google-sub-r", "r@example.com", "R")
	lo := env.do(http.MethodPost, "/v1/auth/logout", "", map[string]string{"refreshToken": sess2.RefreshToken})
	wantStatus(t, lo, http.StatusNoContent)
	lo.Body.Close()
	post := env.do(http.MethodPost, "/v1/auth/refresh", "", map[string]string{"refreshToken": sess2.RefreshToken})
	wantStatus(t, post, http.StatusUnauthorized)
}

// TestAuthMiddlewareRejections covers missing, malformed, and
// wrong-key bearer tokens on the protected surface.
func TestAuthMiddlewareRejections(t *testing.T) {
	env := newTestEnv(t)

	none := env.do(http.MethodGet, "/v1/workspaces", "", nil)
	wantStatus(t, none, http.StatusUnauthorized)

	junk := env.do(http.MethodGet, "/v1/workspaces", "not-a-jwt", nil)
	wantStatus(t, junk, http.StatusUnauthorized)

	other, err := newHS256Signer([]byte("ffffffffffffffffffffffffffffffff"))
	if err != nil {
		t.Fatalf("other signer: %v", err)
	}
	forged, err := other.Mint("user", "ws", time.Now(), time.Minute)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	bad := env.do(http.MethodGet, "/v1/workspaces", forged, nil)
	wantStatus(t, bad, http.StatusUnauthorized)
}
