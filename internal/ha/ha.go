// SPDX-License-Identifier: Apache-2.0

// Package ha implements T-1704's active/standby vnproxd high availability: a
// durable, fenced leader lease that makes exactly one daemon the single writer
// for apply/confirm/rollback at any instant, state replication of the
// safety-critical app store (changesets, changeset_schedules, api_tokens, the
// audit log, and the pre-apply snapshots an in-flight rollback depends on) over
// internal/peer's TLS+HMAC channel, and VIP-or-DNS failover triggering.
//
// # Safety invariant (the single-writer / split-brain guarantee)
//
// Two vnproxd instances must NEVER both drive the same changeset's commit-
// confirm/rollback. This package guarantees single-writer for apply/confirm at
// any instant via a durable lease with a monotonic fencing token (`term`),
// not via "probably only one is active":
//
//   - Deterministic arbitration. The ha_lease record (holder, term, expiresAt)
//     is the sole source of truth for who may act. The active renews it on a
//     short interval; a standby promotes ONLY after the last-observed lease has
//     expired past a fencing margin (Manager.shouldPromote) — never on a
//     transient replication blip. Promotion always writes a STRICTLY-higher
//     term (Manager.promoteLocked); the newer term always wins.
//
//   - No double-apply, no dropped rollback. changesets.confirm_deadline and
//     changeset_schedules.window_* are already ABSOLUTE unix timestamps
//     (T-304/T-1103); they replicate verbatim. On promotion the new active
//     re-arms every timer from the persisted deadline through T-304/T-205's
//     EXISTING re-arm code path (change.Service.ArmPendingRollbacks +
//     TickSchedules, wired via the Coordinator seam) — reproducing the same
//     absolute deadline, never a fresh one. Only the current lease-term holder
//     ever executes a rollback/apply-completion decision: change.Service's
//     unattended timer callbacks consult a LeaderGuard (Manager.IsLeader) and
//     fire as a no-op when this daemon does not hold the lease.
//
//   - A demoted former-active takes no action. If a partition heals and the old
//     active discovers a newer lease term (via a received heartbeat or a push
//     ack carrying the higher term), it demotes immediately (Manager.adopt/
//     demoteLocked, which calls Coordinator.Quiesce to cancel its in-process
//     timers) and performs zero rollback/apply actions in the interim.
//
//   - Fail safe. If HA/lease state is ambiguous, the safe action is to NOT
//     drive apply/confirm: a missed confirm auto-rolls-back by design on the
//     true leader's own re-armed, absolute-deadline timer; a double-apply could
//     cut connectivity. The LeaderGuard therefore withholds on any non-leader
//     answer, and the standby withholds replication-driven writes it cannot
//     confirm are from the current-or-newer term.
//
// Reconciliation with node-local timers: this package deliberately does NOT
// fence T-304's per-node LocalTimerAgent rollback timers — those arm on each
// NODE with an absolute deadline and are each node's own authoritative safety
// net, which a coordinator failover reconciles with (re-arming the
// coordinator-side view to the same deadline) rather than fights.
package ha

import (
	"context"
	"errors"
	"time"
)

// Role is a daemon's current HA role.
type Role string

const (
	// RoleStandby is a daemon that replicates state but drives nothing — it
	// holds no valid leader lease.
	RoleStandby Role = "standby"
	// RoleActive is the single daemon currently holding the leader lease; it is
	// the sole writer for apply/confirm/rollback and the sole replication
	// pusher.
	RoleActive Role = "active"
)

// ErrNoLease is returned by a LeaseStore.Get when no lease has ever been
// recorded (a fresh install that has neither acquired nor observed one).
var ErrNoLease = errors.New("ha: no lease recorded")

// Lease is the in-memory aggregate of the ha_lease singleton row. ExpiresAt,
// AcquiredAt and UpdatedAt are ABSOLUTE unix seconds so the record survives
// replication and restart verbatim (never a relative duration re-based against
// a local clock). Term is the monotonic fencing token.
type Lease struct {
	Holder     string
	Term       int64
	ExpiresAt  int64
	AcquiredAt int64
	UpdatedAt  int64
}

// Clock is the injected time seam (T-304/T-1103's pattern): production wires
// time.Now; the two-daemon harness wires a shared, manually-advanced fake so
// failover/split-brain tests need no real sleeps.
type Clock interface {
	Now() time.Time
}

// clockFunc adapts a plain func() time.Time to Clock.
type clockFunc func() time.Time

func (f clockFunc) Now() time.Time { return f() }

// LeaseStore is the durable persistence of one daemon's best-known lease view.
// *store.HALeaseRepo satisfies this via a thin adapter (see NewStoreLeaseStore)
// so this package never imports internal/store directly.
type LeaseStore interface {
	// Get returns the persisted lease, or ErrNoLease if none has ever been
	// written.
	Get(ctx context.Context) (Lease, error)
	// Set upserts the singleton lease row.
	Set(ctx context.Context, l Lease) error
}

// Coordinator is the seam onto the change engine's EXISTING re-arm / quiesce
// machinery — this package adds no new timer path. Production wires
// change.Service (ReArm -> ArmPendingRollbacks + TickSchedules; Quiesce ->
// StopTimers) via a small adapter, keeping internal/ha free of an
// internal/change import.
type Coordinator interface {
	// ReArm re-establishes apply-engine timer state from the (replicated)
	// persisted absolute deadlines. Called exactly once on each promotion to
	// active. It must reproduce the same absolute deadlines, never fresh ones.
	ReArm(ctx context.Context) error
	// Quiesce cancels this daemon's in-process apply/confirm timers WITHOUT
	// changing any changeset's persisted status — the demote-to-standby step,
	// so a former active stops driving immediately. Idempotent.
	Quiesce()
}

// Announcer triggers the operator-provided VIP-or-DNS failover mechanism when
// this daemon becomes active. Both modes are integration points vnproxd
// triggers, never new daemon dependencies (see announce.go).
type Announcer interface {
	// Announce is called on every transition to active (and, best-effort, on
	// demotion with RoleStandby so a mode that must relinquish the VIP can). It
	// must be safe to call repeatedly with the same role.
	Announce(ctx context.Context, role Role) error
}
