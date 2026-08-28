// SPDX-License-Identifier: Apache-2.0

package pve

import (
	"context"
	"errors"
)

// ErrNotTicketAuth is returned by Client.Login and Client.Renew when the
// client was constructed with AuthAPIToken instead of AuthTicket. Only a
// ticket-mode client has a PVE ticket/CSRF pair to (re-)issue; internal/auth
// (T-105, the PVE credential bridge) always constructs a ticket-mode client
// per user login, so this only fires on programmer error.
var ErrNotTicketAuth = errors.New("pve: client is not configured for AuthTicket")

// Login performs a PVE ticket login (POST /access/ticket) immediately,
// regardless of whether a previously issued ticket is still within its
// renewal threshold. Ordinary API calls authenticate lazily on first use
// (see ticketAuth.prepare); internal/auth's login handler needs this eager
// form instead, so it can tell "bad credentials" (this call fails) apart
// from "credentials are fine but this particular read/write is denied by
// the user's own PVE ACLs" (a later call fails with *ErrPVEDenied).
//
// It returns the freshly issued ticket and CSRF token, which internal/auth
// stores encrypted at rest (docs/security.md "Authentication") rather than
// the user's password.
func (c *Client) Login(ctx context.Context) (ticket, csrf string, err error) {
	ta, ok := c.auth.(*ticketAuth)
	if !ok {
		return "", "", ErrNotTicketAuth
	}
	ta.mu.Lock()
	defer ta.mu.Unlock()
	if err := ta.login(ctx, c); err != nil {
		return "", "", err
	}
	return ta.ticket, ta.csrf, nil
}

// Renew runs the same renew-if-due check Client.do performs before every
// ordinary request (comparing time.Since(issuedAt) to
// Config.TicketRenewAfter), without also making an unrelated API call.
// internal/auth's background renewal loop calls this on a timer so a
// session's PVE ticket stays fresh even during a period where the user
// issues no other requests (docs/security.md: "vnproxd renews at ~1h30
// while the session is active" — "active" meaning the vnprox session, not
// necessarily a live HTTP request). It is a cheap no-op when renewal isn't
// yet due.
//
// It returns the current (possibly just-renewed) ticket and CSRF token.
func (c *Client) Renew(ctx context.Context) (ticket, csrf string, err error) {
	ta, ok := c.auth.(*ticketAuth)
	if !ok {
		return "", "", ErrNotTicketAuth
	}
	if err := ta.prepare(ctx, c); err != nil {
		return "", "", err
	}
	ta.mu.Lock()
	defer ta.mu.Unlock()
	return ta.ticket, ta.csrf, nil
}
