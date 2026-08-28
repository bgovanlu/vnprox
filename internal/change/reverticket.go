// SPDX-License-Identifier: Apache-2.0

// reverticket.go implements T-1805 / roadmap-proven **D1**: the apply-time
// revert ticket that closes the one genuine hole in this engine's safety
// guarantee.
//
// The hole (found by T-502, unowned since): PVE firewall and SDN writes are
// performed with the *user's own* PVE ticket (docs/architecture.md §6 — vnprox
// deliberately cannot exceed the user's PVE ACLs). Node-file changes are
// different: `NodeAgent` writes /etc/network/interfaces and execs `ifreload`
// as root, so the daemon can restore them with no user context at all. That
// asymmetry meant a `fw.*`-only changeset which reached `awaiting_confirm` and
// then timed out — or whose daemon was killed mid-window — was **never**
// reverted, while the UI implied a safety net that did not exist.
//
// D1's answer, and the shape of this file: at apply time, seal the applying
// user's ticket into the changeset row for the duration of the commit-confirm
// window; unseal it only on that changeset's own revert path; wipe it the
// moment the changeset leaves `awaiting_confirm` by any path.
//
// The safety properties this file is responsible for, each with a test named
// in planning/reports/T-1805.md:
//
//  1. **It is a credential at rest and treated as one.** Sealed with the
//     existing internal/store.SessionCipher (AES-256-GCM) — the identical
//     primitive sessions.pve_ticket_enc and clusters.credential_enc use, never
//     a second cipher or key pair (docs/security.md). Never returned by an API
//     response, never logged, never in an audit detail.
//  2. **Its lifetime is bounded from both ends.** Wiped on confirm, on
//     rollback, on a failed apply, on discard, and on ticket expiry —
//     whichever comes first, unconditionally, not best-effort.
//  3. **It authorizes exactly one thing.** The sealed bytes are reachable only
//     through store.ChangesetRepo.RevertTicket, called only from
//     revertGatewayFor below, called only from that changeset's own revert
//     path. No route, MCP tool, or plugin capability can reach it.
//  4. **Expiry is surfaced, never swallowed.** UnattendedRevert is computed at
//     apply time and reported in the apply response, so an operator whose
//     confirm window would outlive their PVE ticket is told then — not at
//     minute 121.
//  5. **It is not a second mutation path.** The revert runs the existing
//     rollback machinery (doRollbackLocked / restoreSDN / restoreFwState) with
//     a credential it previously lacked. No new op type, no new route, nothing
//     here applies anything.

package change

import (
	"context"
	"encoding/json"
	"time"
)

// RevertTicket is the applying user's PVE credential, held for the duration
// of one changeset's commit-confirm window so its ticket-scoped ops can be
// reverted with no live session. It is JSON-encoded and then sealed; the
// plaintext never touches the store, a log line, or a response.
type RevertTicket struct {
	// Ticket is the PVE auth ticket (the PVEAuthCookie value).
	Ticket string `json:"ticket"`
	// CSRF is the matching CSRFPreventionToken, required for every mutating
	// PVE call — a revert is made entirely of mutating calls, so a ticket
	// without it is useless.
	CSRF string `json:"csrf"`
	// ExpiresAt is when PVE stops honouring Ticket (unix seconds), derived
	// from its issue time plus pve.TicketLifetime.
	ExpiresAt int64 `json:"expiresAt"`
}

// RevertTicketSource is the optional PVEGateway extension that yields the
// applying user's ticket for sealing. cmd/vnproxd's `pveGateway` (built from
// the requesting session's own *pve.Client) implements it; a gateway that does
// not — every test double, and any future gateway with no ticket of its own —
// simply means no ticket is sealed and unattended revert is reported
// unavailable, which is exactly the pre-T-1805 behaviour rather than a
// failure.
//
// Deliberately an extension of the existing PVEGateway seam rather than a new
// Apply parameter: the ticket must come from the *same* credential the apply
// itself ran under, and threading it separately would make it possible for
// those two to drift apart.
type RevertTicketSource interface {
	// RevertTicket returns the credential to seal. ok is false when this
	// gateway has no user ticket (e.g. an API-token identity).
	RevertTicket(ctx context.Context) (RevertTicket, bool)
}

// RevertGatewayFactory rebuilds a PVEGateway from a previously sealed ticket,
// for the unattended revert paths (commit-confirm timeout, crash recovery)
// which by construction have no live session. cmd/vnproxd wires it to a
// non-renewing sealed-ticket *pve.Client (pve.Config.Ticket/CSRFToken).
//
// nil (the default, and every test that does not exercise the path) means the
// daemon has no way to act on a sealed ticket, so nothing is ever sealed —
// see Service.sealRevertTicket.
type RevertGatewayFactory interface {
	GatewayForRevertTicket(ctx context.Context, t RevertTicket) (PVEGateway, error)
}

// SecretUnsealer is SecretSealer's inverse: the seam Service uses to recover a
// sealed revert ticket. *internal/store.SessionCipher satisfies both, and
// Service type-asserts its configured Sealer to this rather than taking a
// second Config field — one cipher, one key, structurally (docs/security.md's
// standing invariant that every at-rest credential class in this product is
// sealed by the same AES-256-GCM key).
type SecretUnsealer interface {
	Decrypt(sealed []byte) ([]byte, error)
}

// UnattendedRevert is the apply-time answer to "if this changeset locks me
// out, will it revert itself — and for how long?" (docs/api.md's changesets
// section). It is computed, never persisted, and carries no credential
// material: the expiry timestamp it reports is a bound, not a secret.
type UnattendedRevert struct {
	// Reason is a short, operator-facing explanation whenever Available is
	// false or FullWindow is false. Empty otherwise. It never names or hints
	// at the credential itself.
	Reason string `json:"reason,omitempty"`
	// CoversUntil is the unix instant past which unattended revert of the
	// ticket-scoped portion is no longer possible: min(confirm deadline,
	// ticket expiry). Zero when Available is false.
	CoversUntil int64 `json:"coversUntil,omitempty"`
	// Required reports whether this changeset has any op whose revert needs
	// the user's PVE ticket at all (`fw.*`, `sdn.*`). When false the other
	// fields are moot and Available is true: node-file, WireGuard, QoS and
	// switch reverts all run through daemon-level gateways that need no user
	// context, and always have.
	Required bool `json:"required"`
	// Available reports whether an unattended revert of the ticket-scoped
	// portion is possible at all — i.e. a ticket was sealed successfully.
	Available bool `json:"available"`
	// FullWindow reports whether CoversUntil reaches the confirm deadline. A
	// false value with Available true is the "reduced coverage" case: the
	// ticket expires mid-window, so a timeout after CoversUntil reverts the
	// node-file portion but not the firewall/SDN portion.
	FullWindow bool `json:"fullWindow"`
}

// Reasons reported in UnattendedRevert.Reason. Kept as constants so the UI's
// own copy and the tests pin the same strings.
const (
	revertReasonNoSession = "no PVE session credential was available at apply time; " +
		"firewall/SDN changes in this changeset will not revert automatically"
	revertReasonSealFailed = "the revert credential could not be stored; " +
		"firewall/SDN changes in this changeset will not revert automatically"
	revertReasonNotWired = "this daemon is not configured for unattended firewall/SDN revert; " +
		"firewall/SDN changes in this changeset will not revert automatically"
	revertReasonTicketExpiresFirst = "your PVE session expires before the confirm window closes; " +
		"after that point firewall/SDN changes will no longer revert automatically"
)

// needsRevertTicket reports whether this plan carries any step whose revert
// requires the user's PVE ticket — the firewall steps (T-502) and the SDN
// stage/apply steps (T-402). Every other step kind is executed and reverted by
// a daemon-level gateway with no user context (node files, WireGuard, QoS,
// switch ports), so a plan without these needs no sealed credential at all.
//
// StepIpamAlloc is deliberately NOT in this set: `ipam.alloc.*` has no
// unattended revert path today (doRollbackLocked has never reverted it; only
// the same-request executor rollback does, which always holds a live gateway).
// Claiming coverage here would be a lie; the gap is recorded in
// planning/reports/T-1805.md rather than papered over.
func (p Plan) needsRevertTicket() bool {
	return p.hasFw() || p.hasSDN()
}

// sealRevertTicket captures the applying user's PVE ticket from pveGW and
// seals it into the changeset row for the duration of this apply, returning
// the coverage report the apply response carries. It is called once per apply,
// **before the first mutating step**, so a daemon killed mid-apply
// (recoverInterruptedApply) finds the credential too — not only one killed
// mid-window.
//
// Every failure mode degrades to "unattended revert unavailable, and the
// operator is told why" rather than failing the apply: refusing to apply a
// firewall change because a credential could not be *cached* would be a worse
// outcome than the pre-T-1805 status quo, which is exactly this minus the
// warning.
func (s *Service) sealRevertTicket(ctx context.Context, id string, plan Plan, pveGW PVEGateway, deadline int64) UnattendedRevert {
	if !plan.needsRevertTicket() {
		// Nothing in this changeset needs a user ticket to revert; the
		// daemon-level machinery covers the whole window on its own.
		return UnattendedRevert{Required: false, Available: true, CoversUntil: deadline, FullWindow: true}
	}

	out := UnattendedRevert{Required: true}

	if s.revertGateways == nil {
		out.Reason = revertReasonNotWired
		return out
	}
	unsealer, ok := s.sealer.(SecretUnsealer)
	if !ok || s.sealer == nil {
		// No cipher, or one that cannot reverse itself: sealing would produce
		// bytes nothing could ever open. Fail closed and say so.
		out.Reason = revertReasonNotWired
		return out
	}
	_ = unsealer // presence-checked here so the failure is reported at apply time, not at revert time.

	src, ok := pveGW.(RevertTicketSource)
	if !ok || pveGW == nil {
		out.Reason = revertReasonNoSession
		return out
	}
	ticket, ok := src.RevertTicket(ctx)
	if !ok || ticket.Ticket == "" || ticket.CSRF == "" {
		out.Reason = revertReasonNoSession
		return out
	}

	plaintext, err := json.Marshal(ticket)
	if err != nil {
		s.log.Error("change: encoding revert ticket", "changeset_id", id, "error", err)
		out.Reason = revertReasonSealFailed
		return out
	}
	sealed, err := s.sealer.Encrypt(plaintext)
	if err != nil {
		// Deliberately no error detail beyond the cipher's own message, and
		// never the plaintext.
		s.log.Error("change: sealing revert ticket", "changeset_id", id, "error", err)
		out.Reason = revertReasonSealFailed
		return out
	}
	if err := s.repo.SealRevertTicket(ctx, id, sealed, ticket.ExpiresAt); err != nil {
		s.log.Error("change: storing sealed revert ticket", "changeset_id", id, "error", err)
		out.Reason = revertReasonSealFailed
		return out
	}

	out.Available = true
	out.CoversUntil = deadline
	out.FullWindow = true
	if ticket.ExpiresAt > 0 && ticket.ExpiresAt < deadline {
		out.CoversUntil = ticket.ExpiresAt
		out.FullWindow = false
		out.Reason = revertReasonTicketExpiresFirst
	}
	s.log.Info("change: sealed revert ticket for the commit-confirm window",
		"changeset_id", id, "covers_until", out.CoversUntil, "full_window", out.FullWindow)
	return out
}

// unattendedRevertFor recomputes the coverage report for an already-applied
// changeset from data alone — its ops, its confirm deadline, and the (stored,
// non-secret) sealed-ticket expiry. It is what GET /changesets/{id} reports
// after a page reload, so the operator sees the same coverage statement the
// apply response made rather than losing it with the response body.
//
// Returns nil for a changeset that is not in its commit-confirm window: there
// is nothing to promise about a draft or a terminal record.
func (s *Service) unattendedRevertFor(c Changeset) *UnattendedRevert {
	if c.Status != StatusAwaitingConfirm || c.ConfirmDeadline == nil {
		return nil
	}
	plan, err := BuildPlan(c.Ops)
	if err != nil {
		return nil
	}
	out := revertCoverage(plan, *c.ConfirmDeadline, c.RevertTicketExpiresAt)
	return &out
}

// revertCoverage computes the coverage report from data alone: what the plan
// needs a ticket for, when the window closes, and when the sealed ticket (if
// any) stops being usable. Shared by unattendedRevertFor above and by
// T-2602's staged-apply promotion (apply_staged.go), which must report the
// same coverage the apply response promised at the START of the sequence —
// continuing a staged apply neither seals a new ticket nor extends the old
// one, so the answer must be recomputed from the same three inputs rather
// than re-derived independently.
func revertCoverage(plan Plan, deadline, ticketExpiresAt int64) UnattendedRevert {
	if !plan.needsRevertTicket() {
		return UnattendedRevert{Required: false, Available: true, CoversUntil: deadline, FullWindow: true}
	}
	out := UnattendedRevert{Required: true}
	if ticketExpiresAt == 0 {
		out.Reason = revertReasonNoSession
		return out
	}
	out.Available = true
	out.CoversUntil = deadline
	out.FullWindow = true
	if ticketExpiresAt < deadline {
		out.CoversUntil = ticketExpiresAt
		out.FullWindow = false
		out.Reason = revertReasonTicketExpiresFirst
	}
	return out
}

// revertGatewayFor unseals this changeset's revert ticket and builds a
// PVEGateway from it — the *only* call site of store.ChangesetRepo.RevertTicket
// in the whole codebase, reached only from doRollbackLocked and
// recoverInterruptedApply, and only for the changeset being reverted.
//
// It refuses an already-expired ticket (and wipes it on the way out): a dead
// credential has no business staying at rest, and attempting the revert with
// it would produce a confusing PVE 401 instead of a clear "coverage had
// already lapsed" log line.
func (s *Service) revertGatewayFor(ctx context.Context, id string) (PVEGateway, bool) {
	if s.revertGateways == nil {
		return nil, false
	}
	unsealer, ok := s.sealer.(SecretUnsealer)
	if !ok {
		return nil, false
	}
	sealed, expiresAt, err := s.repo.RevertTicket(ctx, id)
	if err != nil {
		s.log.Error("change: reading sealed revert ticket", "changeset_id", id, "error", err)
		return nil, false
	}
	if len(sealed) == 0 {
		return nil, false
	}
	if expiresAt > 0 && s.now().Unix() >= expiresAt {
		s.log.Warn("change: sealed revert ticket had already expired; the firewall/SDN portion of this changeset cannot be reverted unattended",
			"changeset_id", id, "expired_at", expiresAt)
		s.wipeRevertTicket(ctx, id)
		return nil, false
	}
	plaintext, err := unsealer.Decrypt(sealed)
	if err != nil {
		s.log.Error("change: unsealing revert ticket", "changeset_id", id, "error", err)
		return nil, false
	}
	var ticket RevertTicket
	if err = json.Unmarshal(plaintext, &ticket); err != nil {
		s.log.Error("change: decoding unsealed revert ticket", "changeset_id", id, "error", err)
		return nil, false
	}
	gw, err := s.revertGateways.GatewayForRevertTicket(ctx, ticket)
	if err != nil || gw == nil {
		s.log.Error("change: building revert gateway from sealed ticket", "changeset_id", id, "error", err)
		return nil, false
	}
	s.log.Info("change: reverting firewall/SDN state with the sealed apply-time ticket", "changeset_id", id)
	return gw, true
}

// wipeRevertTicket removes the sealed ticket for id. It is called on **every**
// exit from the commit-confirm window — confirm, manual rollback, auto
// rollback, a rollback that only partly succeeded, a failed apply, crash
// recovery, and discard — and is deliberately unconditional and idempotent: a
// changeset that never had a ticket is a no-op, and a failure to wipe is
// logged loudly rather than swallowed, because "the credential is still there"
// is the one outcome this function exists to prevent.
func (s *Service) wipeRevertTicket(ctx context.Context, id string) {
	if s.repo == nil || id == "" {
		return
	}
	if err := s.repo.WipeRevertTicket(ctx, id); err != nil {
		s.log.Error("change: wiping sealed revert ticket", "changeset_id", id, "error", err)
	}
}

// SweepExpiredRevertTickets clears every sealed revert ticket whose PVE ticket
// has expired, regardless of what state its changeset is in. Called at daemon
// startup (ArmPendingRollbacks) so a ticket that expired while the daemon was
// down does not survive the restart, and safe to call on any schedule.
func (s *Service) SweepExpiredRevertTickets(ctx context.Context) {
	if s.repo == nil {
		return
	}
	n, err := s.repo.WipeExpiredRevertTickets(ctx, s.now().Unix())
	if err != nil {
		s.log.Error("change: sweeping expired revert tickets", "error", err)
		return
	}
	if n > 0 {
		s.log.Info("change: wiped expired sealed revert tickets", "count", n)
	}
}

// TicketExpiryFrom derives a RevertTicket's ExpiresAt from the instant the
// ticket was issued and PVE's documented ticket lifetime. Exported for
// cmd/vnproxd's gateway adapter (which holds the issue time from the live
// *pve.Client) so the arithmetic lives in exactly one place.
func TicketExpiryFrom(issuedAt time.Time, lifetime time.Duration) int64 {
	if issuedAt.IsZero() || lifetime <= 0 {
		return 0
	}
	return issuedAt.Add(lifetime).Unix()
}
