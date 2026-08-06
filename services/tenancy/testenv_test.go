// services/tenancy/testenv_test.go

package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// fakeIssuer is an httptest OIDC identity provider: discovery, JWKS,
// and a token endpoint that enforces client credentials and PKCE and
// signs RS256 ID tokens (a test double standing in for Google).
type fakeIssuer struct {
	srv      *httptest.Server
	key      *rsa.PrivateKey
	clientID string
	secret   string
	redirect string

	mu    sync.Mutex
	codes map[string]fakeCode
}

type fakeCode struct {
	Nonce     string
	Challenge string
	Sub       string
	Email     string
	Verified  bool
	Name      string
}

func newFakeIssuer(t *testing.T, clientID, secret, redirect string) *fakeIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa key: %v", err)
	}
	fi := &fakeIssuer{key: key, clientID: clientID, secret: secret, redirect: redirect, codes: map[string]fakeCode{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 fi.srv.URL,
			"authorization_endpoint": fi.srv.URL + "/authorize",
			"token_endpoint":         fi.srv.URL + "/token",
			"jwks_uri":               fi.srv.URL + "/jwks",
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, r *http.Request) {
		pub := &fi.key.PublicKey
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{
				"kty": "RSA",
				"kid": "test-key",
				"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
			}},
		})
	})
	mux.HandleFunc("POST /token", fi.handleToken)
	fi.srv = httptest.NewServer(mux)
	t.Cleanup(fi.srv.Close)
	return fi
}

// NewCode registers an authorization code as if the user had consented
// at the authorize endpoint.
func (f *fakeIssuer) NewCode(t *testing.T, nonce, challenge, sub, email, name string, verified bool) string {
	t.Helper()
	code := fmt.Sprintf("code-%d", time.Now().UnixNano())
	f.mu.Lock()
	f.codes[code] = fakeCode{Nonce: nonce, Challenge: challenge, Sub: sub, Email: email, Verified: verified, Name: name}
	f.mu.Unlock()
	return code
}

func (f *fakeIssuer) handleToken(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	form, err := url.ParseQuery(string(body))
	if err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if form.Get("grant_type") != "authorization_code" ||
		form.Get("client_id") != f.clientID ||
		form.Get("client_secret") != f.secret ||
		form.Get("redirect_uri") != f.redirect {
		http.Error(w, "invalid client", http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	rec, ok := f.codes[form.Get("code")]
	delete(f.codes, form.Get("code"))
	f.mu.Unlock()
	if !ok {
		http.Error(w, "invalid code", http.StatusBadRequest)
		return
	}
	sum := sha256.Sum256([]byte(form.Get("code_verifier")))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != rec.Challenge {
		http.Error(w, "pkce mismatch", http.StatusBadRequest)
		return
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":            f.srv.URL,
		"aud":            f.clientID,
		"sub":            rec.Sub,
		"email":          rec.Email,
		"email_verified": rec.Verified,
		"name":           rec.Name,
		"nonce":          rec.Nonce,
		"iat":            now.Unix(),
		"exp":            now.Add(5 * time.Minute).Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "test-key"
	signed, err := tok.SignedString(f.key)
	if err != nil {
		http.Error(w, "sign", http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"id_token": signed, "access_token": "unused", "token_type": "Bearer"})
}

// testEnv bundles a fully wired Server over the memStore and fake
// issuer, exposed through httptest.
type testEnv struct {
	t      *testing.T
	store  *memStore
	server *Server
	http   *httptest.Server
	issuer *fakeIssuer
	signer *hs256Signer
	tiers  *TierConfig
}

const (
	testClientID      = "test-client-id"
	testClientSecret  = "test-client-secret"
	testInternalToken = "internal-token-0123456789"
)

var testHS256Key = []byte("0123456789abcdef0123456789abcdef")

func testTiers(t *testing.T) *TierConfig {
	t.Helper()
	cfg := &TierConfig{
		DefaultTier: "demo",
		Tiers: map[string]Tier{
			"demo": {
				EncodeHoursPerMonth: 2,       // 7200 encode-seconds
				StorageBytes:        1 << 20, // 1 MiB
				MaxUploadBytes:      1 << 18, // 256 KiB
				AllowUploads:        true,
				AllowJobs:           true,
			},
			"locked": {
				EncodeHoursPerMonth: 1,
				StorageBytes:        1 << 20,
				MaxUploadBytes:      1 << 18,
				AllowUploads:        false,
				AllowJobs:           false,
			},
		},
	}
	if err := cfg.validate(); err != nil {
		t.Fatalf("test tiers invalid: %v", err)
	}
	return cfg
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	store := newMemStore()
	tiers := testTiers(t)
	signer, err := newHS256Signer(testHS256Key)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	redirect := "http://client.example/v1/auth/callback"
	issuer := newFakeIssuer(t, testClientID, testClientSecret, redirect)
	oidc, err := NewOIDCClient(t.Context(), issuer.srv.URL, testClientID, testClientSecret, redirect, issuer.srv.Client())
	if err != nil {
		t.Fatalf("oidc client: %v", err)
	}
	quota := NewMeteredQuota(store, tiers)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(store, signer, oidc, tiers, quota, log,
		15*time.Minute, 30*24*time.Hour, testInternalToken, false)
	hs := httptest.NewServer(srv.Routes())
	t.Cleanup(hs.Close)
	return &testEnv{t: t, store: store, server: srv, http: hs, issuer: issuer, signer: signer, tiers: tiers}
}

// login runs the whole OIDC flow for the given Google identity and
// returns the parsed session response.
func (e *testEnv) login(sub, email, name string) sessionResponse {
	e.t.Helper()
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := hc.Get(e.http.URL + "/v1/auth/login")
	if err != nil {
		e.t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		e.t.Fatalf("login: status %d, want 302", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		e.t.Fatalf("login redirect: %v", err)
	}
	q := loc.Query()
	state, nonce, challenge := q.Get("state"), q.Get("nonce"), q.Get("code_challenge")
	if state == "" || nonce == "" || challenge == "" || q.Get("code_challenge_method") != "S256" {
		e.t.Fatalf("login redirect missing params: %v", loc)
	}
	code := e.issuer.NewCode(e.t, nonce, challenge, sub, email, name, true)
	cb, err := http.Get(e.http.URL + "/v1/auth/callback?state=" + url.QueryEscape(state) + "&code=" + url.QueryEscape(code))
	if err != nil {
		e.t.Fatalf("callback: %v", err)
	}
	defer cb.Body.Close()
	if cb.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(cb.Body)
		e.t.Fatalf("callback: status %d: %s", cb.StatusCode, raw)
	}
	var sess sessionResponse
	if err := json.NewDecoder(cb.Body).Decode(&sess); err != nil {
		e.t.Fatalf("callback decode: %v", err)
	}
	return sess
}

// do issues an authenticated JSON request against the test server.
func (e *testEnv) do(method, path, bearer string, body any) *http.Response {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("marshal body: %v", err)
		}
		rd = strings.NewReader(string(raw))
	}
	req, err := http.NewRequest(method, e.http.URL+path, rd)
	if err != nil {
		e.t.Fatalf("request: %v", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("do %s %s: %v", method, path, err)
	}
	return resp
}

// doInternal posts a raw JSON body to an internal endpoint with the
// shared internal token.
func (e *testEnv) doInternal(t *testing.T, path, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.http.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("internal request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", testInternalToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("internal do %s: %v", path, err)
	}
	return resp
}

// decode reads a JSON response body into v and closes it.
func decodeBody(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

// wantStatus asserts the response status and drains the body on
// mismatch for a readable failure.
func wantStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("status %d, want %d: %s", resp.StatusCode, want, raw)
	}
}
