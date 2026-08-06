// services/tenancy/apikeys_test.go

package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestAPIKeyLifecycle covers create, use through both auth paths,
// list, the JWT exchange endpoint, and revocation taking effect
// immediately.
func TestAPIKeyLifecycle(t *testing.T) {
	env := newTestEnv(t)
	sess := env.login("key-sub", "key@example.com", "Key")

	// Create.
	created := env.do(http.MethodPost, "/v1/apikeys", sess.AccessToken, map[string]string{"name": "ci key"})
	wantStatus(t, created, http.StatusCreated)
	var cResp struct {
		Key    string     `json:"key"`
		APIKey apiKeyView `json:"apiKey"`
	}
	decodeBody(t, created, &cResp)
	if !strings.HasPrefix(cResp.Key, apiKeyPrefix) {
		t.Fatalf("key %q missing prefix", cResp.Key)
	}
	if cResp.APIKey.WorkspaceID != sess.ActiveWorkspaceID {
		t.Fatalf("key workspace %s, want %s", cResp.APIKey.WorkspaceID, sess.ActiveWorkspaceID)
	}

	// The raw key authenticates as a bearer credential.
	usage := env.do(http.MethodGet, "/v1/usage", cResp.Key, nil)
	wantStatus(t, usage, http.StatusOK)
	var u usageResponse
	decodeBody(t, usage, &u)
	if u.WorkspaceID != sess.ActiveWorkspaceID {
		t.Fatalf("usage workspace %s, want %s", u.WorkspaceID, sess.ActiveWorkspaceID)
	}

	// And via the X-API-Key header.
	req, err := http.NewRequest(http.MethodGet, env.http.URL+"/v1/usage", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("X-API-Key", cResp.Key)
	hdrResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("x-api-key request: %v", err)
	}
	wantStatus(t, hdrResp, http.StatusOK)
	hdrResp.Body.Close()

	// API keys cannot manage workspaces (user session required).
	wsDeny := env.do(http.MethodPost, "/v1/workspaces", cResp.Key, map[string]string{"name": "nope"})
	wantStatus(t, wsDeny, http.StatusForbidden)

	// Exchange the key for a contract JWT (usable against FT-2/FT-3).
	ex := env.do(http.MethodPost, "/v1/auth/token", "", map[string]string{"apiKey": cResp.Key})
	wantStatus(t, ex, http.StatusOK)
	var exResp struct {
		AccessToken string `json:"accessToken"`
		WorkspaceID string `json:"workspaceId"`
	}
	decodeBody(t, ex, &exResp)
	id, err := env.signer.Verify(exResp.AccessToken, time.Now())
	if err != nil {
		t.Fatalf("verify exchanged token: %v", err)
	}
	if id.WorkspaceID != sess.ActiveWorkspaceID || !strings.HasPrefix(id.UserID, apiKeyUserPrefix) {
		t.Fatalf("exchanged identity %+v", id)
	}

	// List shows the key without any secret material.
	list := env.do(http.MethodGet, "/v1/apikeys", sess.AccessToken, nil)
	wantStatus(t, list, http.StatusOK)
	var lResp struct {
		APIKeys []apiKeyView `json:"apiKeys"`
	}
	decodeBody(t, list, &lResp)
	if len(lResp.APIKeys) != 1 || lResp.APIKeys[0].ID != cResp.APIKey.ID {
		t.Fatalf("list %+v", lResp)
	}

	// Revoke; the key stops working on the very next request, on
	// both the direct path and the exchange endpoint.
	rv := env.do(http.MethodDelete, "/v1/apikeys/"+cResp.APIKey.ID, sess.AccessToken, nil)
	wantStatus(t, rv, http.StatusNoContent)
	rv.Body.Close()
	postRevoke := env.do(http.MethodGet, "/v1/usage", cResp.Key, nil)
	wantStatus(t, postRevoke, http.StatusUnauthorized)
	exDeny := env.do(http.MethodPost, "/v1/auth/token", "", map[string]string{"apiKey": cResp.Key})
	wantStatus(t, exDeny, http.StatusUnauthorized)

	// Double revoke is a 404.
	rv2 := env.do(http.MethodDelete, "/v1/apikeys/"+cResp.APIKey.ID, sess.AccessToken, nil)
	wantStatus(t, rv2, http.StatusNotFound)
}

// TestAPIKeyWrongSecret rejects a syntactically valid key whose secret
// does not match, and keys from another workspace cannot be revoked.
func TestAPIKeyWrongSecret(t *testing.T) {
	env := newTestEnv(t)
	sess := env.login("key2-sub", "key2@example.com", "Key2")

	created := env.do(http.MethodPost, "/v1/apikeys", sess.AccessToken, map[string]string{"name": "k"})
	wantStatus(t, created, http.StatusCreated)
	var cResp struct {
		Key    string     `json:"key"`
		APIKey apiKeyView `json:"apiKey"`
	}
	decodeBody(t, created, &cResp)

	tampered := cResp.Key[:len(cResp.Key)-4] + "AAAA"
	deny := env.do(http.MethodGet, "/v1/usage", tampered, nil)
	wantStatus(t, deny, http.StatusUnauthorized)

	// Another workspace's admin cannot revoke this key.
	other := env.login("key3-sub", "key3@example.com", "Key3")
	cross := env.do(http.MethodDelete, "/v1/apikeys/"+cResp.APIKey.ID, other.AccessToken, nil)
	wantStatus(t, cross, http.StatusNotFound)
}

// TestArgon2Hashing pins the at-rest format and verification behavior.
func TestArgon2Hashing(t *testing.T) {
	phc, err := hashAPIKeySecret("secret-material")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(phc, "$argon2id$") {
		t.Fatalf("hash %q is not argon2id PHC", phc)
	}
	ok, err := verifyAPIKeySecret("secret-material", phc)
	if err != nil || !ok {
		t.Fatalf("verify: ok=%v err=%v", ok, err)
	}
	ok, err = verifyAPIKeySecret("wrong", phc)
	if err != nil || ok {
		t.Fatalf("verify wrong: ok=%v err=%v", ok, err)
	}
	if _, err := verifyAPIKeySecret("x", "$plain$nope"); err == nil {
		t.Fatal("malformed PHC accepted")
	}
}
