// Package oidcmock is a self-contained mock OpenID Connect provider for
// exercising internal/auth's OIDC SSO flow (T-1207) without a live IdP. It
// serves the discovery document, a JWKS, and a token endpoint over an
// httptest.Server, and issues RS256-signed ID tokens with caller-configurable
// group claims. It deliberately uses only the standard library — the same
// no-new-dependency bar internal/auth's own OIDC client holds.
//
// It is not a conformance-complete IdP: it implements exactly the
// authorization-code + PKCE and refresh-token grants vnprox uses, verifying the
// PKCE S256 challenge and the client id, and nothing more. Real-IdP claim-shape
// and refresh-token variance is a needs-hardware-validation item
// (planning/reports/needs-hardware-validation.md).
package oidcmock

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"
)

// User is the identity a minted authorization code resolves to — the claims the
// issued ID token will carry.
type User struct {
	Subject           string
	PreferredUsername string
	Email             string
	Groups            []string
}

// Provider is a running mock IdP. Close it when the test finishes.
type Provider struct {
	Server   *httptest.Server
	key      *rsa.PrivateKey
	now      func() time.Time
	codes    map[string]pendingGrant
	refresh  map[string]User
	clientID string
	kid      string
	tokenTTL time.Duration
	mu       sync.Mutex
}

type pendingGrant struct {
	challenge string
	nonce     string
	user      User
}

// Option configures a Provider.
type Option func(*Provider)

// WithNow overrides the provider's clock (for exp/ttl tests).
func WithNow(now func() time.Time) Option { return func(p *Provider) { p.now = now } }

// WithTokenTTL sets the issued access/ID token lifetime (default 1h); the token
// response's expires_in reflects it, driving vnprox's refresh scheduling.
func WithTokenTTL(ttl time.Duration) Option { return func(p *Provider) { p.tokenTTL = ttl } }

// New starts a mock IdP that issues tokens for clientID. The caller must call
// Close (or use t.Cleanup) to shut down the server.
func New(clientID string, opts ...Option) (*Provider, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("oidcmock: generating key: %w", err)
	}
	p := &Provider{
		key:      key,
		now:      time.Now,
		codes:    map[string]pendingGrant{},
		refresh:  map[string]User{},
		clientID: clientID,
		kid:      "mock-key-1",
		tokenTTL: time.Hour,
	}
	for _, o := range opts {
		o(p)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", p.handleDiscovery)
	mux.HandleFunc("/jwks", p.handleJWKS)
	mux.HandleFunc("/token", p.handleToken)
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	p.Server = httptest.NewServer(mux)
	return p, nil
}

// Close shuts the mock IdP down.
func (p *Provider) Close() { p.Server.Close() }

// Issuer is the mock's issuer URL (its server base), to use as the [oidc]
// issuer in the relying-party config under test.
func (p *Provider) Issuer() string { return p.Server.URL }

// HTTPClient returns a client able to reach the mock (its httptest server is
// plain HTTP, so any client works; returned for symmetry with a TLS harness).
func (p *Provider) HTTPClient() *http.Client { return p.Server.Client() }

// IssueCode mints an authorization code bound to nonce (from the authorize
// request), the PKCE S256 challenge (also from the authorize request), and the
// user identity the resulting ID token will describe — simulating the user
// consenting at the IdP. The returned code is what the relying party then
// exchanges at the token endpoint.
func (p *Provider) IssueCode(nonce, challenge string, u User) (string, error) {
	code, err := randToken()
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	p.codes[code] = pendingGrant{challenge: challenge, user: u, nonce: nonce}
	p.mu.Unlock()
	return code, nil
}

func (p *Provider) handleDiscovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]string{
		"issuer":                 p.Server.URL,
		"authorization_endpoint": p.Server.URL + "/authorize",
		"token_endpoint":         p.Server.URL + "/token",
		"jwks_uri":               p.Server.URL + "/jwks",
	})
}

func (p *Provider) handleJWKS(w http.ResponseWriter, _ *http.Request) {
	pub := p.key.Public().(*rsa.PublicKey)
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes())
	writeJSON(w, map[string]any{
		"keys": []map[string]string{
			{"kty": "RSA", "kid": p.kid, "alg": "RS256", "use": "sig", "n": n, "e": e},
		},
	})
}

func (p *Provider) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		tokenError(w, "invalid_request")
		return
	}
	if r.Form.Get("client_id") != p.clientID {
		tokenError(w, "invalid_client")
		return
	}
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		p.handleAuthCodeGrant(w, r)
	case "refresh_token":
		p.handleRefreshGrant(w, r)
	default:
		tokenError(w, "unsupported_grant_type")
	}
}

func (p *Provider) handleAuthCodeGrant(w http.ResponseWriter, r *http.Request) {
	code := r.Form.Get("code")
	p.mu.Lock()
	grant, ok := p.codes[code]
	delete(p.codes, code) // single-use
	p.mu.Unlock()
	if !ok {
		tokenError(w, "invalid_grant")
		return
	}
	// PKCE S256 verification: sha256(code_verifier) must equal the challenge
	// the authorize request carried.
	verifier := r.Form.Get("code_verifier")
	sum := sha256.Sum256([]byte(verifier))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != grant.challenge {
		tokenError(w, "invalid_grant")
		return
	}
	p.issueTokens(w, grant.user, grant.nonce, true)
}

func (p *Provider) handleRefreshGrant(w http.ResponseWriter, r *http.Request) {
	rt := r.Form.Get("refresh_token")
	p.mu.Lock()
	user, ok := p.refresh[rt]
	delete(p.refresh, rt) // rotate: a used refresh token is invalidated
	p.mu.Unlock()
	if !ok {
		tokenError(w, "invalid_grant")
		return
	}
	// A refreshed ID token carries no nonce (there was no fresh authorize
	// request), matching real-IdP behavior.
	p.issueTokens(w, user, "", false)
}

func (p *Provider) issueTokens(w http.ResponseWriter, u User, nonce string, includeNonce bool) {
	idToken, err := p.signIDToken(u, nonce, includeNonce)
	if err != nil {
		tokenError(w, "server_error")
		return
	}
	newRefresh, err := randToken()
	if err != nil {
		tokenError(w, "server_error")
		return
	}
	p.mu.Lock()
	p.refresh[newRefresh] = u
	p.mu.Unlock()
	writeJSON(w, map[string]any{
		"access_token":  "mock-access-token",
		"token_type":    "Bearer",
		"id_token":      idToken,
		"refresh_token": newRefresh,
		"expires_in":    int64(p.tokenTTL / time.Second),
	})
}

func (p *Provider) signIDToken(u User, nonce string, includeNonce bool) (string, error) {
	header := map[string]string{"alg": "RS256", "kid": p.kid, "typ": "JWT"}
	now := p.now()
	payload := map[string]any{
		"iss":                p.Server.URL,
		"sub":                u.Subject,
		"aud":                p.clientID,
		"exp":                now.Add(p.tokenTTL).Unix(),
		"iat":                now.Unix(),
		"preferred_username": u.PreferredUsername,
		"email":              u.Email,
		"groups":             u.Groups,
	}
	if includeNonce && nonce != "" {
		payload["nonce"] = nonce
	}
	hb, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	pb, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	signingInput := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(pb)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func tokenError(w http.ResponseWriter, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func randToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("oidcmock: rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
