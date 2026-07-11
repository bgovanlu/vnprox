package change

import "fmt"

// ErrChangesetLocked is returned by Apply when another changeset is already
// in flight (status applying or awaiting_confirm) cluster-wide: only one
// changeset may hold the advisory single-applier lock at a time
// (docs/architecture.md §4: "one changeset may be in applying state
// cluster-wide at a time"). The API layer maps it to a 409 with the stable
// code `changeset_locked` (docs/api.md's error-code list).
type ErrChangesetLocked struct {
	// HeldBy is the id of the changeset currently holding the apply lock.
	HeldBy string
}

func (e *ErrChangesetLocked) Error() string {
	return fmt.Sprintf("change: apply lock held by changeset %s", e.HeldBy)
}

// ErrApplyNotConfigured is returned by Apply/Confirm/Rollback when the
// Service was built without the apply-engine dependencies (NodeAgent,
// SnapshotRepo) — e.g. a validation-only Service in a unit test. It lets the
// API layer distinguish "this build can't apply" from a real apply failure.
type ErrApplyNotConfigured struct{}

func (e *ErrApplyNotConfigured) Error() string {
	return "change: apply engine is not configured on this Service"
}

// ErrUnsupportedOp is returned by the planner when a changeset contains an op
// the T-205 apply engine cannot yet execute (the guest/SDN-write/fw/ipam op
// families, pending their pve.Client write methods). It is raised at plan
// time — before any mutation — so an un-executable changeset is rejected up
// front rather than partially applied. The API layer maps it to a 422.
type ErrUnsupportedOp struct {
	OpType OpType
}

func (e *ErrUnsupportedOp) Error() string {
	return fmt.Sprintf("change: apply engine does not yet execute op type %q", e.OpType)
}

// ErrNotConfirmable is returned by Confirm when the changeset is not in the
// awaiting_confirm window (already committed, already rolled back, or never
// applied), and by Rollback when the changeset is in no rollback-eligible
// state.
type ErrNotConfirmable struct {
	ID     string
	Status Status
}

func (e *ErrNotConfirmable) Error() string {
	return fmt.Sprintf("change: changeset %s in status %s cannot be confirmed/rolled back", e.ID, e.Status)
}

// ErrIncompatiblePeer is returned by Apply when the plan would coordinate a
// step against a cluster peer whose wire-protocol version this daemon
// cannot safely talk to, or that peer is unreachable for the compatibility
// check itself (docs/architecture.md §5: "a daemon refuses to coordinate
// changes involving a peer with an incompatible schema version (upgrade
// prompt in UI)"). This check runs in beginApply, before any snapshot is
// captured or any step executes, so an incompatible/unreachable peer never
// results in a partial apply — the changeset stays in its pre-apply status
// entirely untouched and can be retried once every affected node is
// upgraded (or reachable). The API layer maps it to a 409 with the stable
// code `peer_incompatible` (docs/api.md's error-code list already reserves
// this code).
type ErrIncompatiblePeer struct {
	Err  error
	Node string
}

func (e *ErrIncompatiblePeer) Error() string {
	return fmt.Sprintf("change: cannot coordinate apply with node %s: %v", e.Node, e.Err)
}

func (e *ErrIncompatiblePeer) Unwrap() error { return e.Err }

// ErrRollbackWindowExpired is returned by Rollback when a committed
// changeset is older than the manual-rollback window (docs/features/
// change-management.md §4: "Manual rollback of a committed changeset is
// offered for 7 days" — audit phase-2 finding F-10). The window matches the
// snapshot-retention pin (config [retention].snapshot_pin_days), since
// beyond it the pre-apply snapshot the restoring draft is built from is no
// longer guaranteed to exist. The API layer maps it to a 409
// invalid_transition-style conflict with the stable code
// `rollback_window_expired`.
type ErrRollbackWindowExpired struct {
	ID          string
	CommittedAt int64
	WindowDays  int
}

func (e *ErrRollbackWindowExpired) Error() string {
	return fmt.Sprintf("change: changeset %s was committed more than %d days ago; the manual-rollback window has expired (restore from a snapshot instead)", e.ID, e.WindowDays)
}
