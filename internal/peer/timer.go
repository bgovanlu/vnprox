package peer

import (
	"context"
	"errors"
)

// TimerStatus is one node_timer's lifecycle state, mirrored 1:1 from
// internal/store's node_timers.status constants so this package's wire type
// doesn't have to import internal/store (the reverse-only-dependency rule
// this package's HostWriter doc comment already establishes for
// internal/change applies just as much to internal/store).
type TimerStatus string

const (
	// TimerArmed means this node has a pending rollback deadline for the
	// changeset and will self-restore if it elapses uncancelled.
	TimerArmed TimerStatus = "armed"
	// TimerCancelled means the coordinator confirmed the changeset before
	// the deadline; this node's timer was stopped and it will not restore.
	TimerCancelled TimerStatus = "cancelled"
	// TimerRolledBack means the deadline elapsed uncancelled and this node
	// successfully restored its pre-apply state.
	TimerRolledBack TimerStatus = "rolled_back"
	// TimerRollbackFailed means the deadline elapsed uncancelled and this
	// node's self-restore itself failed (Error carries the detail) — the
	// "couldn't even fully roll back" case, surfaced to the coordinator's
	// reconciliation pass.
	TimerRollbackFailed TimerStatus = "rollback_failed"
)

// TimerRecord is the wire/return shape for every /api/peer/timer/* route:
// this node's current knowledge of one changeset's rollback timer on it.
type TimerRecord struct {
	ChangesetID string      `json:"changesetId"`
	Node        string      `json:"node"`
	Status      TimerStatus `json:"status"`
	Error       string      `json:"error,omitempty"`
	Deadline    int64       `json:"deadline"`
	ArmedAt     int64       `json:"armedAt"`
	ResolvedAt  int64       `json:"resolvedAt,omitempty"`
}

// ErrTimerNotFound is returned by TimerAgent.TimerStatus (and surfaced by
// Client.TimerStatus as a *ResponseError with this code) when this node was
// never asked to arm a timer for the given (changesetId, node) — distinct
// from ErrPeerUnreachable: the peer answered, it just has no record. A
// coordinator's reconciliation pass treats this as "this node was never
// reached before whatever else resolved the changeset" rather than "still
// unknown, keep retrying".
var ErrTimerNotFound = errors.New("peer: no timer recorded for that changeset/node")

// errCodeTimerNotFound is ErrTimerNotFound's wire error code
// (docs/api.md's global error-code convention).
const errCodeTimerNotFound = "timer_not_found"

// TimerAgent is the local-timer protocol's node-side dependency (T-304,
// docs/features/change-management.md §4): arm/cancel/inspect *this* node's
// own persisted rollback deadline for one changeset. It is a small,
// T-304-owned interface for the same reason HostWriter is: this package
// never imports internal/change, so the concrete implementation
// (internal/change's LocalTimerAgent) lives on the other side of the
// dependency and is handed in structurally.
type TimerAgent interface {
	// ArmTimer persists content as node's pre-apply state to restore and
	// arms a real timer for deadline (unix seconds): if CancelTimer is not
	// called first, the implementation restores node from content and
	// reloads when the timer fires. Arming the same (changesetID, node)
	// again (e.g. a retried call) replaces the prior record.
	ArmTimer(ctx context.Context, changesetID, node, content string, deadline int64) (TimerRecord, error)

	// CancelTimer stops the timer for (changesetID, node) if it is still
	// armed, moving it to TimerCancelled. Cancelling an unknown or already-
	// resolved timer is not an error (idempotent): it returns the timer's
	// current record (or ErrTimerNotFound if this node never armed one).
	CancelTimer(ctx context.Context, changesetID, node string) (TimerRecord, error)

	// TimerStatus returns the current record for (changesetID, node), or
	// ErrTimerNotFound if this node never armed one — the coordinator's
	// reconciliation-on-reconnect read.
	TimerStatus(ctx context.Context, changesetID, node string) (TimerRecord, error)
}
