// services/tenancy/oidc.go

package main

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// OIDCVerifiedIdentity is what a verified Google ID token yields.
type OIDCVerifiedIdentity struct {
	Subject string
	Email   string
	Name    string
}

// OIDCClient runs the authorization-code flow against the configured
// issuer (Google in production, AM-3; an httptest fake in tests). It
// discovers endpoints from /.well-known/openid-configuration and
// verifies RS256 ID tokens against the issuer JWKS.
type OIDCClient struct {
	issuer       string
	clientID     string
	clientSecret string
	redirectURL  string
	hc           *http.Client

	authorizationEndpoint string
	tokenEndpoint         string
	jwksURI               string

	mu          sync.Mutex
	keys        map[string]*rsa.PublicKey
	keysFetched time.Time
}

// jwksMaxAge bounds how long a cached JWKS is trusted before refetch.
const jwksMaxAge = time.Hour

type discoveryDoc struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

// NewOIDCClient fetches the issuer discovery document and validates it.
func NewOIDCClient(ctx context.Context, issuer, clientID, clientSecret, redirectURL string, hc *http.Client) (*OIDCClient, error) {
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	wellKnown := strings.TrimSuffix(issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnown, nil)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc discovery: %s returned %d", wellKnown, resp.StatusCode)
	}
	var doc discoveryDoc
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("oidc discovery: decode: %w", err)
	}
	if doc.Issuer != strings.TrimSuffix(issuer, "/") && doc.Issuer != issuer {
		return nil, fmt.Errorf("oidc discovery: issuer mismatch: configured %q, document %q", issuer, doc.Issuer)
	}
	if doc.AuthorizationEndpoint == "" || doc.TokenEndpoint == "" || doc.JWKSURI == "" {
		return nil, fmt.Errorf("oidc discovery: document missing required endpoints")
	}
	return &OIDCClient{
		issuer:                doc.Issuer,
		clientID:              clientID,
		clientSecret:          clientSecret,
		redirectURL:           redirectURL,
		hc:                    hc,
		authorizationEndpoint: doc.AuthorizationEndpoint,
		tokenEndpoint:         doc.TokenEndpoint,
		jwksURI:               doc.JWKSURI,
		keys:                  map[string]*rsa.PublicKey{},
	}, nil
}

// AuthCodeURL builds the Google authorization redirect with state,
// nonce, and a PKCE S256 challenge.
func (c *OIDCClient) AuthCodeURL(state, nonce, pkceVerifier string) string {
	challenge := sha256.Sum256([]byte(pkceVerifier))
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {c.clientID},
		"redirect_uri":          {c.redirectURL},
		"scope":                 {"openid email profile"},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
	}
	sep := "?"
	if strings.Contains(c.authorizationEndpoint, "?") {
		sep = "&"
	}
	return c.authorizationEndpoint + sep + q.Encode()
}

type tokenResponse struct {
	IDToken string `json:"id_token"`
}

// Exchange redeems the authorization code at the token endpoint and
// returns the raw ID token.
func (c *OIDCClient) Exchange(ctx context.Context, code, pkceVerifier string) (string, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"redirect_uri":  {c.redirectURL},
		"code_verifier": {pkceVerifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("oidc exchange: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("oidc exchange: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("oidc exchange: read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oidc exchange: token endpoint returned %d", resp.StatusCode)
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("oidc exchange: decode: %w", err)
	}
	if tr.IDToken == "" {
		return "", fmt.Errorf("oidc exchange: response carries no id_token")
	}
	return tr.IDToken, nil
}

// idTokenClaims is the subset of Google ID token claims we consume.
type idTokenClaims struct {
	Nonce         string `json:"nonce"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	jwt.RegisteredClaims
}

// VerifyIDToken validates signature (RS256 against the issuer JWKS),
// issuer, audience, expiry, nonce, and email verification, and returns
// the verified identity.
func (c *OIDCClient) VerifyIDToken(ctx context.Context, rawToken, expectedNonce string, now time.Time) (OIDCVerifiedIdentity, error) {
	var claims idTokenClaims
	_, err := jwt.ParseWithClaims(rawToken, &claims, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodRS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method %s", t.Method.Alg())
		}
		kid, _ := t.Header["kid"].(string)
		return c.keyForKid(ctx, kid)
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithTimeFunc(func() time.Time { return now }),
		jwt.WithExpirationRequired(),
		jwt.WithIssuer(c.issuer),
		jwt.WithAudience(c.clientID),
	)
	if err != nil {
		return OIDCVerifiedIdentity{}, fmt.Errorf("id token: %w", err)
	}
	if claims.Nonce == "" || claims.Nonce != expectedNonce {
		return OIDCVerifiedIdentity{}, fmt.Errorf("id token: nonce mismatch")
	}
	if claims.Subject == "" {
		return OIDCVerifiedIdentity{}, fmt.Errorf("id token: missing sub")
	}
	if claims.Email == "" || !claims.EmailVerified {
		return OIDCVerifiedIdentity{}, fmt.Errorf("id token: email missing or unverified")
	}
	name := claims.Name
	if name == "" {
		name = claims.Email
	}
	return OIDCVerifiedIdentity{Subject: claims.Subject, Email: claims.Email, Name: name}, nil
}

// keyForKid returns the RSA public key for kid, refetching the JWKS
// when the kid is unknown or the cache is stale.
func (c *OIDCClient) keyForKid(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	c.mu.Lock()
	key, ok := c.keys[kid]
	stale := time.Since(c.keysFetched) > jwksMaxAge
	c.mu.Unlock()
	if ok && !stale {
		return key, nil
	}
	if err := c.fetchJWKS(ctx); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key, ok = c.keys[kid]
	if !ok {
		return nil, fmt.Errorf("jwks: no key with kid %q", kid)
	}
	return key, nil
}

type jwksDoc struct {
	Keys []struct {
		Kty string `json:"kty"`
		Kid string `json:"kid"`
		N   string `json:"n"`
		E   string `json:"e"`
	} `json:"keys"`
}

// fetchJWKS refreshes the RSA key cache from the issuer JWKS endpoint.
func (c *OIDCClient) fetchJWKS(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.jwksURI, nil)
	if err != nil {
		return fmt.Errorf("jwks: %w", err)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwks: %s returned %d", c.jwksURI, resp.StatusCode)
	}
	var doc jwksDoc
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return fmt.Errorf("jwks: decode: %w", err)
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			return fmt.Errorf("jwks: key %s: bad modulus: %w", k.Kid, err)
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			return fmt.Errorf("jwks: key %s: bad exponent: %w", k.Kid, err)
		}
		e := new(big.Int).SetBytes(eBytes)
		if !e.IsInt64() || e.Int64() <= 1 || e.Int64() > 1<<31 {
			return fmt.Errorf("jwks: key %s: exponent out of range", k.Kid)
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(e.Int64())}
	}
	if len(keys) == 0 {
		return fmt.Errorf("jwks: document carries no usable RSA keys")
	}
	c.mu.Lock()
	c.keys = keys
	c.keysFetched = time.Now()
	c.mu.Unlock()
	return nil
}
