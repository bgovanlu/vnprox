// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// This file implements T-1207's OIDC authorization-code + PKCE flow against a
// real IdP (or the mock in internal/auth/oidcmock), using only the standard
// library — no new third-party dependency (CLAUDE.md's dependency rule). It
// covers OpenID Connect discovery, the code→token exchange, and RS256 ID-token
// verification against the provider's published JWKS. Group→role mapping and
// the authn/authz split (OIDC authenticates the human; PVE authorization still
// gates every cluster-scoped action) live in oidc_caps.go / oidc_identity.go.

// ErrOIDCVerify is the sentinel every ID-token verification failure wraps —
// bad signature, wrong issuer/audience, expired, or a nonce mismatch. The
// callback handler maps it to a 401 without leaking which check failed.
var ErrOIDCVerify = errors.New("auth: oidc id-token verification failed")

// OIDCProviderConfig is the immutable, secret-free configuration of one OIDC
// relying-party integration, sourced from vnprox.toml's [oidc] section
// (issuer, clientId, scopes) plus the client secret loaded from clientSecretFile
// and vnprox's own redirect URL. It is separate from OIDCService (which adds
// the group-mapping table, the PVE-linkage resolver, and per-request state) so
// the pure protocol layer here can be unit-tested in isolation.
type OIDCProviderConfig struct {
	HTTPClient   *http.Client
	Now          func() time.Time
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	GroupsClaim  string
	Scopes       []string
}

// OIDCProvider is the protocol client: discovery + PKCE auth-URL construction +
// code exchange + RS256 ID-token verification. It caches discovery metadata and
// JWKS after the first fetch (refetching JWKS on an unknown `kid`, the standard
// key-rotation handling).
type OIDCProvider struct {
	http   *http.Client
	now    func() time.Time
	meta   *providerMetadata
	keys   map[string]*rsa.PublicKey
	cfg    OIDCProviderConfig
	scopes []string
	mu     sync.Mutex
}

type providerMetadata struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

// tokenResponse is the OIDC token endpoint's success body.
type tokenResponse struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
}

// NewOIDCProvider constructs a provider from cfg. It does not perform any
// network I/O — discovery is lazy on the first flow call so a daemon with a
// temporarily-unreachable IdP still starts (login simply fails until the IdP
// is reachable, the same posture the PVE ticket bridge already has).
func NewOIDCProvider(cfg OIDCProviderConfig) (*OIDCProvider, error) {
	if strings.TrimSpace(cfg.Issuer) == "" {
		return nil, fmt.Errorf("auth: oidc issuer is required")
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return nil, fmt.Errorf("auth: oidc clientId is required")
	}
	if strings.TrimSpace(cfg.RedirectURL) == "" {
		return nil, fmt.Errorf("auth: oidc redirectUrl is required")
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	scopes := dedupeScopes(cfg.Scopes)
	if cfg.GroupsClaim == "" {
		cfg.GroupsClaim = "groups"
	}
	return &OIDCProvider{http: hc, now: now, keys: map[string]*rsa.PublicKey{}, cfg: cfg, scopes: scopes}, nil
}

// dedupeScopes returns cfg.Scopes with "openid" guaranteed present and no
// duplicates, preserving caller order otherwise.
func dedupeScopes(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in)+1)
	add := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	add("openid")
	for _, s := range in {
		add(s)
	}
	return out
}

// discover fetches (once) the provider's OpenID configuration document.
func (p *OIDCProvider) discover(ctx context.Context) (*providerMetadata, error) {
	p.mu.Lock()
	if p.meta != nil {
		m := p.meta
		p.mu.Unlock()
		return m, nil
	}
	p.mu.Unlock()

	discoveryURL := strings.TrimRight(p.cfg.Issuer, "/") + "/.well-known/openid-configuration"
	var meta providerMetadata
	if err := p.getJSON(ctx, discoveryURL, &meta); err != nil {
		return nil, fmt.Errorf("auth: oidc discovery: %w", err)
	}
	if meta.Issuer != p.cfg.Issuer {
		return nil, fmt.Errorf("auth: oidc discovery issuer %q does not match configured issuer %q", meta.Issuer, p.cfg.Issuer)
	}
	if meta.AuthorizationEndpoint == "" || meta.TokenEndpoint == "" || meta.JWKSURI == "" {
		return nil, fmt.Errorf("auth: oidc discovery document is missing required endpoints")
	}
	p.mu.Lock()
	p.meta = &meta
	p.mu.Unlock()
	return &meta, nil
}

// AuthCodeURL builds the authorization-request URL for the code+PKCE flow.
// challenge is the S256 code challenge derived from the caller-held verifier;
// state and nonce are the caller's own single-use CSRF/replay tokens.
func (p *OIDCProvider) AuthCodeURL(ctx context.Context, state, nonce, challenge string) (string, error) {
	meta, err := p.discover(ctx)
	if err != nil {
		return "", err
	}
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", p.cfg.ClientID)
	q.Set("redirect_uri", p.cfg.RedirectURL)
	q.Set("scope", strings.Join(p.scopes, " "))
	q.Set("state", state)
	q.Set("nonce", nonce)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	sep := "?"
	if strings.Contains(meta.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	return meta.AuthorizationEndpoint + sep + q.Encode(), nil
}

// Exchange trades an authorization code (plus the PKCE verifier) for tokens at
// the token endpoint.
func (p *OIDCProvider) Exchange(ctx context.Context, code, verifier string) (tokenResponse, error) {
	meta, err := p.discover(ctx)
	if err != nil {
		return tokenResponse{}, err
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", p.cfg.RedirectURL)
	form.Set("client_id", p.cfg.ClientID)
	form.Set("code_verifier", verifier)
	if p.cfg.ClientSecret != "" {
		form.Set("client_secret", p.cfg.ClientSecret)
	}
	return p.postToken(ctx, meta.TokenEndpoint, form)
}

// Refresh exchanges a refresh token for a fresh token set (OIDC session
// renewal, mirroring the PVE ticket bridge's ~1h30 ticket renewal — see
// oidc_identity.go). An IdP that rotates refresh tokens returns a new one in
// RefreshToken; the caller must persist whichever it gets back.
func (p *OIDCProvider) Refresh(ctx context.Context, refreshToken string) (tokenResponse, error) {
	meta, err := p.discover(ctx)
	if err != nil {
		return tokenResponse{}, err
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", p.cfg.ClientID)
	if p.cfg.ClientSecret != "" {
		form.Set("client_secret", p.cfg.ClientSecret)
	}
	return p.postToken(ctx, meta.TokenEndpoint, form)
}

func (p *OIDCProvider) postToken(ctx context.Context, endpoint string, form url.Values) (tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, fmt.Errorf("auth: oidc token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("auth: oidc token endpoint: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return tokenResponse{}, fmt.Errorf("auth: reading oidc token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return tokenResponse{}, fmt.Errorf("auth: oidc token endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return tokenResponse{}, fmt.Errorf("auth: decoding oidc token response: %w", err)
	}
	if tr.IDToken == "" {
		return tokenResponse{}, fmt.Errorf("auth: oidc token response carried no id_token")
	}
	return tr, nil
}

// oidcClaims is the subset of ID-token claims vnprox reads. Groups is filled by
// VerifyIDToken from the configured GroupsClaim (via the raw claim map), not
// from a fixed field name, so a non-standard claim name still works.
type oidcClaims struct {
	Issuer            string
	Subject           string
	PreferredUsername string
	Email             string
	Nonce             string
	Groups            []string
	Expiry            int64
	IssuedAt          int64
}

// Username is the human identity vnprox records for the session: preferred_username
// when present, else the subject (always present per OIDC Core).
func (c oidcClaims) Username() string {
	if c.PreferredUsername != "" {
		return c.PreferredUsername
	}
	return c.Subject
}

// VerifyIDToken parses and cryptographically verifies rawIDToken: RS256
// signature against the provider's JWKS, `iss` equal to the configured issuer,
// `aud` containing the client id, unexpired (with a small clock-skew leeway),
// and — when expectNonce is non-empty — a matching `nonce`. Every failure wraps
// ErrOIDCVerify.
func (p *OIDCProvider) VerifyIDToken(ctx context.Context, rawIDToken, expectNonce string) (oidcClaims, error) {
	parts := strings.Split(rawIDToken, ".")
	if len(parts) != 3 {
		return oidcClaims{}, fmt.Errorf("%w: token is not a JWS", ErrOIDCVerify)
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return oidcClaims{}, fmt.Errorf("%w: bad header encoding", ErrOIDCVerify)
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if jerr := json.Unmarshal(headerJSON, &header); jerr != nil {
		return oidcClaims{}, fmt.Errorf("%w: bad header json", ErrOIDCVerify)
	}
	if header.Alg != "RS256" {
		// vnprox deliberately supports only RS256 (the OIDC-mandatory-to-implement
		// asymmetric alg); refusing "none"/HS* here closes the classic
		// alg-confusion downgrade rather than trusting the token's own header.
		return oidcClaims{}, fmt.Errorf("%w: unsupported alg %q (only RS256)", ErrOIDCVerify, header.Alg)
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return oidcClaims{}, fmt.Errorf("%w: bad signature encoding", ErrOIDCVerify)
	}
	key, err := p.publicKey(ctx, header.Kid)
	if err != nil {
		return oidcClaims{}, fmt.Errorf("%w: %v", ErrOIDCVerify, err)
	}
	signingInput := []byte(parts[0] + "." + parts[1])
	digest := sha256.Sum256(signingInput)
	if verr := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig); verr != nil {
		return oidcClaims{}, fmt.Errorf("%w: bad signature", ErrOIDCVerify)
	}

	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return oidcClaims{}, fmt.Errorf("%w: bad payload encoding", ErrOIDCVerify)
	}
	claims, err := p.parseClaims(payloadJSON)
	if err != nil {
		return oidcClaims{}, err
	}
	if claims.Issuer != p.cfg.Issuer {
		return oidcClaims{}, fmt.Errorf("%w: issuer %q != %q", ErrOIDCVerify, claims.Issuer, p.cfg.Issuer)
	}
	if expectNonce != "" && claims.Nonce != expectNonce {
		return oidcClaims{}, fmt.Errorf("%w: nonce mismatch", ErrOIDCVerify)
	}
	const leeway = 60 * time.Second
	if claims.Expiry > 0 && p.now().After(time.Unix(claims.Expiry, 0).Add(leeway)) {
		return oidcClaims{}, fmt.Errorf("%w: token expired", ErrOIDCVerify)
	}
	return claims, nil
}

// parseClaims decodes the payload, validating audience and extracting the
// configurable groups claim.
func (p *OIDCProvider) parseClaims(payloadJSON []byte) (oidcClaims, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(payloadJSON, &raw); err != nil {
		return oidcClaims{}, fmt.Errorf("%w: bad payload json", ErrOIDCVerify)
	}
	var c oidcClaims
	stringClaim(raw, "iss", &c.Issuer)
	stringClaim(raw, "sub", &c.Subject)
	stringClaim(raw, "preferred_username", &c.PreferredUsername)
	stringClaim(raw, "email", &c.Email)
	stringClaim(raw, "nonce", &c.Nonce)
	intClaim(raw, "exp", &c.Expiry)
	intClaim(raw, "iat", &c.IssuedAt)

	if !audienceContains(raw["aud"], p.cfg.ClientID) {
		return oidcClaims{}, fmt.Errorf("%w: audience does not contain client id %q", ErrOIDCVerify, p.cfg.ClientID)
	}
	c.Groups = stringOrStringSlice(raw[p.cfg.GroupsClaim])
	return c, nil
}

func stringClaim(raw map[string]json.RawMessage, name string, dst *string) {
	if v, ok := raw[name]; ok {
		_ = json.Unmarshal(v, dst)
	}
}

func intClaim(raw map[string]json.RawMessage, name string, dst *int64) {
	if v, ok := raw[name]; ok {
		_ = json.Unmarshal(v, dst)
	}
}

// audienceContains reports whether the `aud` claim (a string or array of
// strings per OIDC Core §2) includes want.
func audienceContains(raw json.RawMessage, want string) bool {
	for _, a := range stringOrStringSlice(raw) {
		if a == want {
			return true
		}
	}
	return false
}

// stringOrStringSlice decodes a JSON value that may be a single string or an
// array of strings into a slice (nil for absent/null/other shapes).
func stringOrStringSlice(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return []string{one}
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		return many
	}
	return nil
}

// publicKey returns the RSA public key for kid, fetching/refreshing the JWKS on
// a cache miss (standard key-rotation handling: an unknown kid triggers exactly
// one refetch).
func (p *OIDCProvider) publicKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	p.mu.Lock()
	if key, ok := p.keys[kid]; ok {
		p.mu.Unlock()
		return key, nil
	}
	p.mu.Unlock()

	if err := p.refreshJWKS(ctx); err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if key, ok := p.keys[kid]; ok {
		return key, nil
	}
	// A token with no kid against a single-key JWKS still verifies.
	if kid == "" && len(p.keys) == 1 {
		for _, key := range p.keys {
			return key, nil
		}
	}
	return nil, fmt.Errorf("no signing key for kid %q", kid)
}

type jwksDoc struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func (p *OIDCProvider) refreshJWKS(ctx context.Context) error {
	meta, err := p.discover(ctx)
	if err != nil {
		return err
	}
	var doc jwksDoc
	if err := p.getJSON(ctx, meta.JWKSURI, &doc); err != nil {
		return fmt.Errorf("fetching jwks: %w", err)
	}
	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" {
			continue
		}
		pub, err := rsaPublicKeyFromJWK(k)
		if err != nil {
			return err
		}
		keys[k.Kid] = pub
	}
	if len(keys) == 0 {
		return fmt.Errorf("jwks carried no usable RSA keys")
	}
	p.mu.Lock()
	p.keys = keys
	p.mu.Unlock()
	return nil
}

func rsaPublicKeyFromJWK(k jwkKey) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("decoding jwk modulus: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("decoding jwk exponent: %w", err)
	}
	e := new(big.Int).SetBytes(eBytes)
	if !e.IsInt64() || e.Int64() < 2 || e.Int64() > (1<<31-1) {
		return nil, fmt.Errorf("jwk exponent out of range")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(e.Int64())}, nil
}

func (p *OIDCProvider) getJSON(ctx context.Context, endpoint string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("building request for %s: %w", endpoint, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("requesting %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %d", endpoint, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("reading %s: %w", endpoint, err)
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("decoding %s: %w", endpoint, err)
	}
	return nil
}
