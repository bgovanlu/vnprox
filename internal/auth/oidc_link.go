// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/store"
)

// LinkCredential is the sealed-at-rest PVE authentication material an
// OIDC-group→PVE linkage maps to (oidc_pve_links.credential_enc). Exactly one
// form is populated: a PVE API token (Token) or ticket auth
// (Username/Password/Realm). The whole struct is JSON-serialized and sealed with
// the same AES-256-GCM SessionCipher sessions.pve_ticket_enc /
// clusters.credential_enc use — the identical shape internal/federation seals
// per attached cluster, kept as its own type here to avoid an auth→federation
// import (docs/security.md: one credential-storage mechanism, not one per
// feature).
type LinkCredential struct {
	Kind     string `json:"kind"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Realm    string `json:"realm,omitempty"`
	Token    string `json:"token,omitempty"`
}

// LinkCredentialKind values.
const (
	LinkCredentialTicket = "ticket"
	LinkCredentialToken  = "token"
)

// LinkCipher is the subset of *store.SessionCipher the resolver needs to unseal
// a linkage credential — an interface so tests can substitute a fake without
// real key material (the same seam pattern internal/federation.Cipher uses).
type LinkCipher interface {
	Encrypt(plaintext []byte) ([]byte, error)
	Decrypt(sealed []byte) ([]byte, error)
}

// LinkStore is the subset of *store.OIDCPVELinkRepo the resolver reads.
type LinkStore interface {
	GetByGroup(ctx context.Context, clusterID, group string) (store.OIDCPVELink, error)
}

// SealLinkCredential JSON-marshals and seals a LinkCredential for storage in
// oidc_pve_links.credential_enc. Exported so an admin-facing linkage CRUD (and
// this package's own tests) can seal a mapped PVE credential with the daemon's
// session cipher, exactly as internal/federation seals a per-cluster credential.
func SealLinkCredential(cipher LinkCipher, cred LinkCredential) ([]byte, error) {
	plain, err := json.Marshal(cred)
	if err != nil {
		return nil, fmt.Errorf("auth: marshaling link credential: %w", err)
	}
	sealed, err := cipher.Encrypt(plain)
	if err != nil {
		return nil, fmt.Errorf("auth: sealing link credential: %w", err)
	}
	return sealed, nil
}

// StorePVELinkResolver resolves OIDC groups to a per-cluster PVE identity by
// reading the oidc_pve_links table and building a PVE client from the mapped,
// sealed credential. It implements PVELinkResolver.
//
// Scope note: this resolver builds clients against one PVE API base (the
// local/default cluster, clusterID ""). Resolving a linkage on a *non-default*
// attached federation cluster needs that cluster's own API URL from T-1201's
// registry (internal/federation.Service.ClientFor); wiring that through is a
// documented follow-up (see planning/reports/T-1207.md) — for any clusterID
// other than base's, ResolvePVE reports "no linkage" (ok=false) rather than
// guessing an endpoint.
type StorePVELinkResolver struct {
	store     LinkStore
	cipher    LinkCipher
	clusterID string
	base      pve.Config
}

// NewStorePVELinkResolver constructs a resolver. base carries the PVE API
// URL/TLS/timeout for the cluster identified by clusterID (default "" = the
// local cluster); the per-request Auth/credential fields are filled from each
// resolved linkage.
func NewStorePVELinkResolver(linkStore LinkStore, cipher LinkCipher, clusterID string, base pve.Config) *StorePVELinkResolver {
	return &StorePVELinkResolver{store: linkStore, cipher: cipher, base: base, clusterID: clusterID}
}

// ResolvePVE implements PVELinkResolver: it returns the PVE identity for the
// first of groups that has a linkage on clusterID, or ok=false when none does
// (the authn/authz split's "no linkage" case).
func (r *StorePVELinkResolver) ResolvePVE(ctx context.Context, clusterID string, groups []string) (PVEIdentity, string, bool, error) {
	if clusterID != r.clusterID {
		// See the type's scope note: only this resolver's own cluster is
		// serviceable without the federation registry's endpoint.
		return nil, "", false, nil
	}
	for _, g := range groups {
		link, err := r.store.GetByGroup(ctx, clusterID, g)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, "", false, fmt.Errorf("auth: reading oidc linkage for group %q: %w", g, err)
		}
		cred, err := r.unseal(link.CredentialEnc)
		if err != nil {
			return nil, "", false, err
		}
		client, err := r.buildClient(cred)
		if err != nil {
			return nil, "", false, err
		}
		// A ticket-mode mapped credential must log in to obtain a ticket before
		// its permissions can be read. Production linkages use a PVE API token
		// (token-mode, no login, non-expiring) — ticket-mode exists for the
		// pvemock test path, which has no API-token auth (the same dev-ticket
		// deviation the collectors use, see internal/config's [pve] doc). A
		// token-mode client needs no login: its header authenticates each call.
		if cred.Kind == LinkCredentialTicket {
			if _, _, loginErr := client.Login(ctx); loginErr != nil {
				return nil, "", false, fmt.Errorf("auth: logging in linked pve identity: %w", loginErr)
			}
		}
		return clientIdentity{c: client}, link.PVEUsername, true, nil
	}
	return nil, "", false, nil
}

func (r *StorePVELinkResolver) unseal(sealed []byte) (LinkCredential, error) {
	plain, err := r.cipher.Decrypt(sealed)
	if err != nil {
		return LinkCredential{}, fmt.Errorf("auth: unsealing link credential: %w", err)
	}
	var cred LinkCredential
	if err := json.Unmarshal(plain, &cred); err != nil {
		return LinkCredential{}, fmt.Errorf("auth: decoding link credential: %w", err)
	}
	return cred, nil
}

func (r *StorePVELinkResolver) buildClient(cred LinkCredential) (*pve.Client, error) {
	cfg := r.base
	switch cred.Kind {
	case LinkCredentialToken:
		cfg.Auth = pve.AuthAPIToken
		cfg.TokenValue = cred.Token
	case LinkCredentialTicket:
		cfg.Auth = pve.AuthTicket
		cfg.Username = cred.Username
		cfg.Password = cred.Password
		cfg.Realm = cred.Realm
	default:
		return nil, fmt.Errorf("auth: unknown link credential kind %q", cred.Kind)
	}
	client, err := pve.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("auth: building linked pve client: %w", err)
	}
	return client, nil
}
