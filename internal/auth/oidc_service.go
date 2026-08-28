// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"sync"
	"time"
)

// OIDCService bundles everything the /auth/oidc/* handlers need: the protocol
// provider (oidc.go), the group→role mapping table (oidc_caps.go), the
// per-cluster PVE-linkage resolver (oidc_identity.go), and the short-lived
// server-side state for in-flight authorization requests (PKCE verifier + nonce
// keyed by the opaque state value). It is optional on a Service — nil disables
// OIDC entirely, so a deployment with no [oidc] section is unaffected, exactly
// as a nil Tokens repo disables bearer auth.
type OIDCService struct {
	resolver  PVELinkResolver
	provider  *OIDCProvider
	now       func() time.Time
	pending   map[string]pendingAuth
	clusterID string
	mappings  []GroupMapping
	ttl       time.Duration
	mu        sync.Mutex
}

// pendingAuth is the server-side state for one in-flight authorization request,
// keyed by the opaque `state` value returned to the browser and echoed back on
// the callback. Held server-side (not in a cookie) so the PKCE verifier and
// nonce never leave the daemon.
type pendingAuth struct {
	createdAt time.Time
	verifier  string
	nonce     string
}

// OIDCConfig configures an OIDCService. Provider is required; Resolver may be
// nil (every OIDC login then has no PVE linkage — authenticated but zero
// cluster-scoped capability, the fail-closed default of the authn/authz split).
// ClusterID is the federation cluster OIDC logins authorize against (default
// "" = the local/default cluster); PendingTTL bounds how long an authorization
// request may sit unredeemed (default 10m).
type OIDCConfig struct {
	Resolver   PVELinkResolver
	Provider   *OIDCProvider
	Now        func() time.Time
	ClusterID  string
	Mappings   []GroupMapping
	PendingTTL time.Duration
}

// NewOIDCService constructs an OIDCService. Provider is required.
func NewOIDCService(cfg OIDCConfig) (*OIDCService, error) {
	if cfg.Provider == nil {
		return nil, fmt.Errorf("auth: OIDCConfig.Provider is required")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	ttl := cfg.PendingTTL
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &OIDCService{
		provider:  cfg.Provider,
		resolver:  cfg.Resolver,
		now:       now,
		pending:   make(map[string]pendingAuth),
		mappings:  cfg.Mappings,
		clusterID: cfg.ClusterID,
		ttl:       ttl,
	}, nil
}

// begin generates a fresh state/nonce/PKCE verifier+challenge, records the
// pending authorization server-side, and returns the state and S256 challenge
// for the authorize-URL. It opportunistically sweeps expired pending entries so
// an abandoned login flow never accumulates.
func (o *OIDCService) begin() (state, nonce, challenge string, err error) {
	state, err = randomToken()
	if err != nil {
		return "", "", "", err
	}
	nonce, err = randomToken()
	if err != nil {
		return "", "", "", err
	}
	verifier, err := randomToken()
	if err != nil {
		return "", "", "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])

	now := o.now()
	o.mu.Lock()
	for k, p := range o.pending {
		if now.Sub(p.createdAt) > o.ttl {
			delete(o.pending, k)
		}
	}
	o.pending[state] = pendingAuth{createdAt: now, verifier: verifier, nonce: nonce}
	o.mu.Unlock()
	return state, nonce, challenge, nil
}

// redeem consumes the pending authorization for state (single-use: it is
// removed whether or not it had expired), returning its verifier and nonce. ok
// is false for an unknown or expired state.
func (o *OIDCService) redeem(state string) (verifier, nonce string, ok bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	p, found := o.pending[state]
	if !found {
		return "", "", false
	}
	delete(o.pending, state)
	if o.now().Sub(p.createdAt) > o.ttl {
		return "", "", false
	}
	return p.verifier, p.nonce, true
}

// randomToken returns 32 bytes of CSPRNG entropy, base64url-encoded — used for
// the state, nonce, and PKCE code verifier (all opaque, single-use values).
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: generating oidc token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
