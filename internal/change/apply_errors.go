package change

import (
	"fmt"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

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
// the apply engine cannot yet execute (the guest/SDN-write/ipam op
// families, pending their pve.Client write methods — fw.* is executable as
// of T-502). It is raised at plan time — before any mutation — so an
// un-executable changeset is rejected up front rather than partially
// applied. The API layer maps it to a 422.
type ErrUnsupportedOp struct {
	OpType OpType
}

func (e *ErrUnsupportedOp) Error() string {
	return fmt.Sprintf("change: apply engine does not yet execute op type %q", e.OpType)
}

// ErrFwRuleNotFound is returned by PVEGateway.FirewallRuleFields (and
// surfaced by fw.rule.move's apply-time revalidation) when the position it
// was asked about no longer holds any rule.
type ErrFwRuleNotFound struct {
	Ref inventory.Ref
	Pos int
}

func (e *ErrFwRuleNotFound) Error() string {
	return fmt.Sprintf("change: no rule at pos %d in ruleset %s", e.Pos, e.Ref)
}

// ErrFwPositionChanged is T-502 acceptance criterion 3: fw.rule.move's
// apply-time revalidation found that the rule now at FromPos no longer
// matches what the client observed when the move was drafted (the fixture
// shifted between draft and apply — someone else's concurrent edit, or a
// stale UI). The apply step fails with this error rather than silently
// moving whatever now happens to occupy that position; the API layer
// surfaces it distinctly (not just a generic apply failure) so the UI can
// prompt the user to refresh the ruleset and retry instead of quietly
// reporting "apply failed".
type ErrFwPositionChanged struct {
	Ref  inventory.Ref
	Want FwRuleFields
	Got  FwRuleFields
	Pos  int
}

func (e *ErrFwPositionChanged) Error() string {
	return fmt.Sprintf("change: firewall ruleset %s changed since this move was drafted: the rule at pos %d no longer matches what was expected there — refresh and retry", e.Ref, e.Pos)
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

// ErrSDNZoneUnhealthy is returned by the sdn_apply step when the underlying
// PVE task itself succeeded but T-402's post-apply verification finds a
// zone unhealthy on one of its member nodes (docs/features/sdn.md §4:
// "post-apply verification that each node's status reports the zone
// healthy ... failures link straight to the failing node's task log") — a
// deliberately distinct failure mode from the PVE task itself failing
// (which surfaces as whatever error the gateway's WaitTask/*pve.ErrPVE*
// call returned). Node/Detail/Status are the failing zone's first non-ok
// entry; UPID/TaskNode identify the apply task itself for the task-log
// deep link even though the task "succeeded".
type ErrSDNZoneUnhealthy struct {
	Zone     string
	Node     string
	Status   string
	Detail   string
	UPID     string
	TaskNode string
}

func (e *ErrSDNZoneUnhealthy) Error() string {
	return fmt.Sprintf("change: sdn zone %q reports %s on node %s after apply (task %s on %s): %s",
		e.Zone, e.Status, e.Node, e.UPID, e.TaskNode, e.Detail)
}
