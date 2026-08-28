// SPDX-License-Identifier: Apache-2.0

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

// ErrApprovalRequired is returned by Apply when this deployment's policy
// ([changesets] approval_required = true) requires an explicit review
// approval before a changeset may apply, and none is currently recorded
// (never approved, awaiting a decision, or the last decision was a
// rejection, or a subsequent edit cleared a prior approval — see
// review.go's ApprovalConfig/ClearApproval doc comments). This is a new,
// additive apply-refusal error, distinct from every existing one
// (docs/api.md's changeset-apply error list): it does not repurpose
// validation_failed (the changeset may be perfectly valid) or
// invalid_transition (the ordinary status state machine is untouched by
// approval — review.go's package doc comment) — approval is an orthogonal
// gate the state machine doesn't know about. The API layer maps it to a
// new, documented 422 with the stable code approval_required.
type ErrApprovalRequired struct {
	ID string
}

func (e *ErrApprovalRequired) Error() string {
	return fmt.Sprintf("change: changeset %s requires review approval before it can be applied", e.ID)
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
