// SPDX-License-Identifier: Apache-2.0

// tcmirror_expiry.go implements T-4014's "a mirror session must have a
// maximum duration and must stop itself" requirement, WITHOUT adding a
// second mutation path (CLAUDE.md's change-engine invariant, D4).
//
// # The mechanism, and why it is not a new mutation path
//
// tc.mirror.create's MaxDurationSec (params_tcmirror.go) is validated at
// stage time (validate_schema.go: required, positive; validate_safety.go:
// never exceeding the server's configured ceiling). Once that changeset
// commits (Confirm), the session's tc/clsact/mirred state is live on its
// node with a durable expires_at deadline recorded in its
// tc_mirror_sessions row (TcMirrorGateway.ApplyTcMirrorOp).
//
// RunTcMirrorSweep ticks (like internal/capture's RunSweepLoop) and, on
// each tick, asks the store for every ACTIVE session whose expires_at has
// passed (DueForExpiry). For each one, expireTcMirrorSession does exactly
// what an operator clicking "stop this session" would do: it drafts an
// ORDINARY tc.mirror.delete changeset (Service.Create), applies it
// (Service.Apply), and confirms it (Service.Confirm) — the same three
// calls docs/api.md documents for a manual delete, attributed to
// systemTcMirrorActor instead of a human. This is the identical pattern
// createRestoringDraft (apply.go) already uses to draft a system-initiated
// inverse changeset for a rollback-past-window, and the identical
// unattended-action shape autorollback.go's own timer-driven revert and
// schedule.go's fireSchedule both use — "the daemon calls its own public
// Create/Apply/Confirm API on a changeset it drafted, attributed to a
// system actor" is already this codebase's established idiom for "this
// needs to happen without an operator", not a new mechanism invented for
// this card. The delete changeset goes through the FULL stage->validate->
// diff->apply lifecycle like any other — a truly nonexistent/already-gone
// session's delete fails cleanly and audibly (see expireTcMirrorSession's
// own error handling) rather than silently succeeding.
//
// # Why this is not T-1103's scheduler (schedule.go)
//
// T-1103 stages a changeset now and applies it at a FUTURE, human-chosen
// window with missed-window policy, callback tokens, and a
// touchesMgmtPath unattended-scheduling gate — machinery built for "apply
// this later, safely, unattended". A mirror session's expiry is a single
// fixed instant already known and validated at create time, and the
// teardown must run the MOMENT that instant passes (not "the next
// convenient tick within a window") — so this reuses capture's simpler
// "tick, prime immediately on startup, act now" sweep shape instead,
// exactly as T-4014's card directs ("reusing capture's bounding and audit
// discipline" while the mechanism itself is an ordinary changeset op).
//
// # Daemon restart mid-session
//
// RunTcMirrorSweep calls TickTcMirrorSessions once, eagerly, BEFORE
// starting its ticker (mirroring RunSweepLoop's "primes immediately...
// without waiting a full interval" and schedule.go's identical startup
// tick) — so a session whose expires_at passed while the daemon was down
// is found via DueForExpiry and torn down the instant the daemon comes
// back up, before serving any other traffic. There is no window in which
// an orphaned mirror silently duplicates traffic indefinitely: the
// longest it can outlive its bound is one missed sweep interval, and a
// fresh daemon start closes even that gap immediately. This is the same
// durability guarantee capture_sessions' retention sweep gives an
// orphaned capture file, applied to a live tc/clsact/mirred session
// instead of a file.

package change

import (
	"context"
	"fmt"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

// systemTcMirrorActor is the audit/apply attribution for the expiry
// sweep's own unattended actions (mirrors systemScheduleActor/
// systemRollbackActor's identical naming convention).
const systemTcMirrorActor = "system:tcmirror"

// DefaultTcMirrorSweepInterval is how often RunTcMirrorSweep re-scans for
// due sessions in production. A minute, not capture's 15 — a mirror
// session copies live production traffic, so the acceptable "how late can
// the stop be" window is tighter than a capture file's retention sweep.
const DefaultTcMirrorSweepInterval = time.Minute

// RunTcMirrorSweep runs TickTcMirrorSessions every interval until ctx is
// cancelled — the supervised, owned expiry goroutine (docs/development.md's
// "every goroutine has an owner and a shutdown path"), mirroring
// capture.Coordinator.RunSweepLoop exactly, including priming immediately
// so a daemon restart expires long-overdue sessions without waiting a full
// interval (see this file's own doc comment). A no-op forever if
// TcMirrorSessions was never configured (tcMirrorSess is nil) — the same
// "absent feature is a no-op" degradation every other optional store in
// Config uses.
func (s *Service) RunTcMirrorSweep(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = DefaultTcMirrorSweepInterval
	}
	s.TickTcMirrorSessions(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.TickTcMirrorSessions(ctx)
		}
	}
}

// TickTcMirrorSessions expires every active tc.mirror session whose
// expires_at has passed (exported, unexported-Tick-style, so tests call it
// directly against an injected Clock/store — no sleeps needed, matching
// schedule.go's TickSchedules convention). A no-op if TcMirrorSessions was
// never configured.
func (s *Service) TickTcMirrorSessions(ctx context.Context) {
	if s.tcMirrorSess == nil {
		return
	}
	due, err := s.tcMirrorSess.DueForExpiry(ctx, s.now().Unix())
	if err != nil {
		s.log.Error("change: listing due-for-expiry tc.mirror sessions", "error", err)
		return
	}
	for _, sess := range due {
		s.expireTcMirrorSession(ctx, sess)
	}
}

// expireTcMirrorSession tears down one overdue session by drafting,
// applying, and confirming an ordinary tc.mirror.delete changeset
// attributed to systemTcMirrorActor — see this file's top doc comment for
// why this is not a second mutation path. It always writes an explicit
// tcmirror.expire audit row of its own (rich per-session detail: node,
// source/dest iface, how long it ran) — capture's exact
// audit-every-start/stop discipline, applied here — distinct from (and in
// addition to) the ordinary changeset.create/apply/confirm audit rows
// Service.Create/Apply/Confirm already write for the delete changeset
// itself.
func (s *Service) expireTcMirrorSession(ctx context.Context, sess store.TcMirrorSession) {
	target := inventory.Ref{Kind: inventory.KindTcMirror, Node: sess.Node, ID: sess.ID}
	detail := map[string]any{
		"sessionId":      sess.ID,
		"node":           sess.Node,
		"sourceIface":    sess.SourceIface,
		"destIface":      sess.DestIface,
		"maxDurationSec": sess.MaxDurationSec,
		"startedAt":      sess.StartedAt,
		"expiresAt":      sess.ExpiresAt,
	}

	draft, err := s.Create(ctx, systemTcMirrorActor, expireChangesetTitle(sess), []Op{
		{Type: OpTcMirrorDelete, Target: target, Params: &TcMirrorDeleteParams{}},
	})
	if err != nil {
		s.tcMirrorExpireFailed(ctx, sess, detail, fmt.Errorf("drafting delete changeset: %w", err))
		return
	}
	detail["changesetId"] = draft.ID

	if _, err := s.Apply(ctx, draft.ID, systemTcMirrorActor, nil, 0); err != nil {
		s.tcMirrorExpireFailed(ctx, sess, detail, fmt.Errorf("applying delete changeset %s: %w", draft.ID, err))
		return
	}
	if _, err := s.Confirm(ctx, draft.ID, systemTcMirrorActor); err != nil {
		s.tcMirrorExpireFailed(ctx, sess, detail, fmt.Errorf("confirming delete changeset %s: %w", draft.ID, err))
		return
	}

	s.appendAudit(ctx, systemTcMirrorActor, "tcmirror.expire", "ok", sess.ID, detail)
	s.log.Info("change: tc.mirror session expired and torn down", "session", sess.ID, "node", sess.Node, "changeset_id", draft.ID)
}

// tcMirrorExpireFailed records a failed expiry attempt — audited (never
// silent) and logged, so an operator can see and manually intervene rather
// than the session quietly continuing to mirror past its bound. It does
// NOT mark the store row expired: the row stays "active" and past its
// expires_at, so the NEXT sweep tick retries it (DueForExpiry's own
// query), until it either succeeds or an operator steps in — the same
// "keep retrying, never silently give up" stance capture.Sweep takes on a
// purge failure.
func (s *Service) tcMirrorExpireFailed(ctx context.Context, sess store.TcMirrorSession, detail map[string]any, err error) {
	detail["error"] = err.Error()
	s.appendAudit(ctx, systemTcMirrorActor, "tcmirror.expire", "error", sess.ID, detail)
	s.log.Error("change: expiring tc.mirror session", "session", sess.ID, "node", sess.Node, "error", err)
}

// expireChangesetTitle names the system-drafted delete changeset — visible
// in the ordinary changeset list/review UI like any other, so an operator
// browsing changesets sees WHY this one exists rather than an unexplained
// system-authored delete.
func expireChangesetTitle(sess store.TcMirrorSession) string {
	return fmt.Sprintf("tc.mirror session %s expired (max duration %ds reached)", sess.ID, sess.MaxDurationSec)
}
