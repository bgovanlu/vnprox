// SPDX-License-Identifier: Apache-2.0

// Package pve implements a typed, minimal Go client for the subset of the
// Proxmox VE API surface vnprox uses (docs/architecture.md §1, §6).
//
// Two authentication modes are supported, matching docs/security.md:
//
//   - Ticket auth (AuthTicket): the client logs in via POST /access/ticket
//     with a user's PVE credentials, then sends the resulting ticket as the
//     "PVEAuthCookie" cookie and the CSRF token as the "CSRFPreventionToken"
//     header on mutating requests, renewing the ticket in the background
//     before it goes stale (via PVE's ticket-as-password shortcut, with a
//     plaintext-password fallback — see ticketAuth in auth.go). This is the
//     mode vnprox's auth bridge (T-105) uses to perform writes with the
//     logged-in user's own PVE privileges.
//   - API-token auth (AuthAPIToken): the client sends a fixed
//     "Authorization: PVEAPIToken=user@realm!tokenid=secret" header on every
//     request. This is the mode the daemon's read-only polling identity
//     (vnprox@pve!daemon, docs/deployment.md) uses.
//
// The client exposes one typed method per PVE endpoint vnprox actually
// calls (cluster status/resources, access permissions, node network
// reads/writes/reload, guest config reads/writes, SDN reads, IPAM reads
// (list + per-instance allocation status), firewall reads, task
// get/wait) — deliberately no generic "call any PVE endpoint" escape
// hatch, so every PVE call vnprox makes is visible at a client call site
// and reviewable.
//
// Errors are mapped consistently: HTTP 401 becomes *ErrPVEAuth, 403 becomes
// *ErrPVEDenied (carrying PVE's own message), 5xx becomes *ErrPVEServer, and
// transport/network/timeout failures become *ErrPVETransport. Callers use
// errors.As to detect these.
package pve
