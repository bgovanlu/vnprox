// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"fmt"

	"github.com/bgovanlu/vnprox/internal/pve"
)

// PVEIdentity is what this package needs from one user's authenticated PVE
// session to build a vnprox session record and derive capabilities: ticket
// login/renewal, the effective permission set, and the cluster's node list
// (for per-node capability derivation). *pve.Client (constructed in
// AuthTicket mode with that user's submitted credentials) implements this
// directly — see clientIdentity below; integration tests use exactly that
// against internal/pvemock, while handler-level unit tests substitute
// in-memory stubs (see helpers_test.go).
type PVEIdentity interface {
	// Login performs the initial PVE ticket login, validating the
	// submitted credentials (and OTP, where the realm requires one).
	Login(ctx context.Context) (ticket, csrf string, err error)
	// Renew re-issues the ticket if the client's renewal threshold has
	// elapsed; a cheap no-op otherwise.
	Renew(ctx context.Context) (ticket, csrf string, err error)
	// Permissions returns the authenticated user's effective PVE ACL
	// privilege set (GET /access/permissions).
	Permissions(ctx context.Context) (pve.Permissions, error)
	// ClusterNodes returns the cluster's member node names (GET
	// /cluster/status filtered to type "node"), used to build a per-node
	// capability map. Real PVE (and internal/pvemock) gate this on
	// Sys.Audit; a user without it gets *pve.ErrPVEDenied here, which the
	// login handler treats as "no per-node granularity available" rather
	// than a hard failure (see caps.go's BuildCapabilities empty-nodes
	// case).
	ClusterNodes(ctx context.Context) ([]string, error)
}

// IdentityFactory constructs a PVEIdentity for one login attempt. The
// production factory (NewClientIdentityFactory) builds a real ticket-mode
// *pve.Client; tests inject a factory that returns a stub or a
// fixture-backed decorator (see doc.go).
type IdentityFactory func(username, password, realm, otp string) (PVEIdentity, error)

// clientIdentity adapts a *pve.Client (AuthTicket mode) to PVEIdentity.
type clientIdentity struct {
	c *pve.Client
}

func (i clientIdentity) Login(ctx context.Context) (string, string, error) {
	return i.c.Login(ctx)
}

func (i clientIdentity) Renew(ctx context.Context) (string, string, error) {
	return i.c.Renew(ctx)
}

func (i clientIdentity) Permissions(ctx context.Context) (pve.Permissions, error) {
	return i.c.Permissions(ctx)
}

func (i clientIdentity) ClusterNodes(ctx context.Context) ([]string, error) {
	entries, err := i.c.ClusterStatus(ctx)
	if err != nil {
		return nil, err
	}
	var nodes []string
	for _, e := range entries {
		if e.Type == "node" {
			nodes = append(nodes, e.Name)
		}
	}
	return nodes, nil
}

// NewClientIdentityFactory builds the production IdentityFactory: every
// login attempt gets its own ticket-mode *pve.Client, constructed against
// base (APIURL/TLS/TicketRenewAfter carried over; Auth/Username/Password/
// Realm/OTP are set per attempt). The client is kept alive for the process
// lifetime of the resulting session by this package's renewal loop
// (renewal.go), never persisted. It renews via PVE's ticket-as-password
// shortcut and drops the plaintext password from memory once that first
// succeeds (see internal/pve/auth.go's ticketAuth doc comment); until
// then the password is held in memory only, as the fallback for a PVE
// that rejects the shortcut.
func NewClientIdentityFactory(base pve.Config) IdentityFactory {
	return func(username, password, realm, otp string) (PVEIdentity, error) {
		cfg := base
		cfg.Auth = pve.AuthTicket
		cfg.Username = username
		cfg.Password = password
		cfg.Realm = realm
		cfg.OTP = otp

		c, err := pve.New(cfg)
		if err != nil {
			return nil, fmt.Errorf("auth: building PVE client for %s: %w", username, err)
		}
		return clientIdentity{c: c}, nil
	}
}
