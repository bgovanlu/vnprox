// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/pve"
)

// PVELinkResolver resolves an OIDC-authenticated human to a per-cluster PVE
// authorization (T-1207's authn/authz split). Given the user's OIDC groups, it
// returns the PVEIdentity for whichever group has an admin-configured
// OIDC-group→PVE mapping on clusterID (docs/security.md's Authentication
// section; the oidc_pve_links store table). ok is false when NO group maps to a
// PVE identity on that cluster — the "no linkage" case, where the OIDC user is
// authenticated but holds zero cluster-scoped capability (writes fall back to a
// first-use PVE-credential prompt, never to the OIDC bundle alone).
type PVELinkResolver interface {
	ResolvePVE(ctx context.Context, clusterID string, groups []string) (identity PVEIdentity, pveUsername string, ok bool, err error)
}

// oidcIdentity adapts an OIDC login to the PVEIdentity interface the session /
// capability machinery already speaks, so OIDC sessions reuse the exact same
// store, cookies, expiry, and hourly re-derivation the PVE ticket bridge uses
// (T-1207 AC1/AC4). It carries:
//   - bundle: the OIDC group→role capability ceiling (capLimiter);
//   - linked: the PVE identity the user's groups map to on the target cluster
//     (nil when there is no linkage — the authn/authz split's "no PVE
//     authorization" case, which yields zero cluster-scoped capability);
//   - the OIDC provider + refresh token, for OIDC session renewal mirroring the
//     ticket bridge's ~half-life ticket renewal.
type oidcIdentity struct {
	nextRefresh time.Time
	linked      PVEIdentity
	provider    *OIDCProvider
	now         func() time.Time
	csrf        string
	refreshTok  string
	mu          sync.Mutex
	bundle      Capabilities
}

// newOIDCIdentity builds an oidcIdentity. accessTokenTTL schedules the first
// refresh at ~half its lifetime (mirroring the PVE ticket bridge's ~1h30-of-2h
// renewal); a non-positive TTL disables scheduled refresh (the session then
// lives only until its idle/hard cap, still per the session contract).
func newOIDCIdentity(provider *OIDCProvider, linked PVEIdentity, bundle Capabilities, csrf, refreshTok string, accessTokenTTL time.Duration, now func() time.Time) *oidcIdentity {
	if now == nil {
		now = time.Now
	}
	id := &oidcIdentity{
		provider: provider, linked: linked, now: now,
		csrf: csrf, refreshTok: refreshTok, bundle: bundle,
	}
	id.scheduleRefresh(accessTokenTTL)
	return id
}

func (i *oidcIdentity) scheduleRefresh(ttl time.Duration) {
	if ttl <= 0 || i.refreshTok == "" {
		i.nextRefresh = time.Time{}
		return
	}
	i.nextRefresh = i.now().Add(ttl / 2)
}

// capBundle implements capLimiter: the OIDC group bundle capping this
// identity's PVE-derived capabilities.
func (i *oidcIdentity) capBundle() Capabilities { return i.bundle }

// Login is not a valid operation for an OIDC identity — the OIDC handler builds
// the session directly from a verified ID token rather than a password login,
// so this exists only to satisfy PVEIdentity and must never be called.
func (i *oidcIdentity) Login(context.Context) (string, string, error) {
	return "", "", fmt.Errorf("auth: oidc identity does not support password login")
}

// Renew performs OIDC session renewal: when a refresh token is held and its
// scheduled half-life has elapsed, it exchanges the refresh token for a fresh
// token set (rotating the stored refresh token if the IdP issued a new one). It
// returns the session's unchanged CSRF secret and an empty PVE ticket — an OIDC
// session has no PVE ticket of its own; cluster-scoped PVE calls go out on the
// linked PVE identity (PVEClientFor). A refresh failure surfaces as an error so
// the renewal loop invalidates the session, exactly as a failed ticket renewal
// does (docs/security.md's session contract, T-1207 AC4).
func (i *oidcIdentity) Renew(ctx context.Context) (string, string, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.refreshTok == "" || i.nextRefresh.IsZero() || i.now().Before(i.nextRefresh) {
		return "", i.csrf, nil
	}
	tr, err := i.provider.Refresh(ctx, i.refreshTok)
	if err != nil {
		return "", "", fmt.Errorf("auth: oidc refresh: %w", err)
	}
	// Re-verify the refreshed ID token (issuer/audience/signature); a nonce is
	// not present on a refresh, so it is not checked here.
	if _, verr := i.provider.VerifyIDToken(ctx, tr.IDToken, ""); verr != nil {
		return "", "", verr
	}
	if tr.RefreshToken != "" {
		i.refreshTok = tr.RefreshToken
	}
	i.scheduleRefresh(time.Duration(tr.ExpiresIn) * time.Second)
	return "", i.csrf, nil
}

// Permissions delegates to the linked PVE identity, or returns an empty
// privilege set when there is no linkage — which makes BuildCapabilities derive
// an all-false capability map, so IntersectCaps (via deriveCapabilities'
// capLimiter step) leaves the OIDC user with no cluster-scoped capability. This
// is the mechanism behind T-1207 AC2's authn/authz split.
func (i *oidcIdentity) Permissions(ctx context.Context) (pve.Permissions, error) {
	if i.linked == nil {
		return pve.Permissions{}, nil
	}
	return i.linked.Permissions(ctx)
}

// ClusterNodes delegates to the linked PVE identity, or returns no nodes when
// there is no linkage (the cluster-wide "" fallback entry then represents the
// user's — empty — capability set).
func (i *oidcIdentity) ClusterNodes(ctx context.Context) ([]string, error) {
	if i.linked == nil {
		return nil, nil
	}
	return i.linked.ClusterNodes(ctx)
}

// linkedClient returns the underlying *pve.Client for the linked PVE identity,
// if any — so PVEClientFor can hand OIDC sessions a client for cluster-scoped
// PVE calls (the linked mapped credential's, since an OIDC session has no PVE
// ticket of its own).
func (i *oidcIdentity) linkedClient() (*pve.Client, bool) {
	if ci, ok := i.linked.(clientIdentity); ok {
		return ci.c, true
	}
	return nil, false
}
