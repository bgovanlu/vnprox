// SPDX-License-Identifier: Apache-2.0

package change

// StepStatus is one apply-log entry's outcome.
type StepStatus string

const (
	// StepPending means the step has not started (used only transiently; a
	// persisted log never leaves a step Pending once execution moves past it).
	StepPending StepStatus = "pending"
	// StepOK means the step completed successfully.
	StepOK StepStatus = "ok"
	// StepFailed means the step itself failed — this is the step the apply
	// log pinpoints (ApplyLog.FailedStep points here).
	StepFailed StepStatus = "failed"
	// StepSkipped means the step never ran because an earlier step failed.
	StepSkipped StepStatus = "skipped"
	// StepRolledBack means a previously-OK step was undone during rollback.
	StepRolledBack StepStatus = "rolled_back"
)

// StepLog is one step's execution record in changesets.apply_log_json.
type StepLog struct {
	Kind    StepKind   `json:"kind"`
	Node    string     `json:"node,omitempty"`
	Summary string     `json:"summary"`
	Status  StepStatus `json:"status"`
	Error   string     `json:"error,omitempty"`
	// TaskUPID is the PVE task identifier a StepSDNApply step ran as
	// (docs/features/sdn.md §4: "failures link straight to the failing
	// node's task log") — populated as soon as the underlying PUT
	// /cluster/sdn task starts, even if the step goes on to fail (either
	// the task itself failing, or T-402's post-apply health verification
	// failing after a successful task). Node is set to the task's own node
	// (parsed from the UPID) for the same step, so the pair (Node,
	// TaskUPID) is enough for the UI to deep-link to that node's task log.
	TaskUPID  string `json:"taskUpid,omitempty"`
	StartedAt int64  `json:"startedAt,omitempty"`
	EndedAt   int64  `json:"endedAt,omitempty"`
	Index     int    `json:"index"`
}

// RollbackLog is one rollback action's record (restoring a node's file,
// re-running a reload, etc.).
type RollbackLog struct {
	Node    string     `json:"node,omitempty"`
	Summary string     `json:"summary"`
	Status  StepStatus `json:"status"`
	Error   string     `json:"error,omitempty"`
	At      int64      `json:"at,omitempty"`
}

// ApplyLog is the full per-step execution record of an apply attempt
// (changesets.apply_log_json, docs/data-model.md §2). It pinpoints the exact
// failed step (FailedStep) and preserves the rollback trail for diagnosis
// (docs/features/change-management.md §4: "the failure step preserved for
// diagnosis").
type ApplyLog struct {
	RolledBackBy string `json:"rolledBackBy,omitempty"`
	FailedStep   *int   `json:"failedStep,omitempty"`
	// AutoRollback (T-2603) names the finding that rolled this changeset back
	// inside its commit-confirm window, or nil for every other outcome. It is
	// written BEFORE the restore runs (autorollback.go's
	// recordAutoRollbackTrigger), so an operator reading a changeset that
	// rolled back on its own is never told merely that something went wrong.
	AutoRollback *AutoRollbackTrigger `json:"autoRollback,omitempty"`
	Steps        []StepLog            `json:"steps"`
	Rollback     []RollbackLog        `json:"rollback,omitempty"`
	// NodeTimers is T-304's per-node local-timer bookkeeping: one entry per
	// node the coordinator armed a distributed rollback timer on, updated as
	// its fate becomes known (fanned-out cancel on confirm, best-effort
	// restore-and-cancel on rollback, or — for a node the coordinator lost
	// contact with — left "unknown" until a later Reconcile call resolves
	// it). This is the per-node detail docs/features/change-management.md
	// §4 and the T-304 card's AC2 ("reconciliation ... marks the changeset
	// rolled_back with per-node detail") refer to.
	NodeTimers []NodeTimerLog `json:"nodeTimers,omitempty"`
}

// NodeTimerStatus mirrors internal/peer.TimerStatus plus the
// coordinator-only NodeTimerUnknown state (a node whose resolution the
// coordinator could not observe directly and has not yet reconciled).
type NodeTimerStatus string

const (
	NodeTimerStatusArmed          NodeTimerStatus = "armed"
	NodeTimerStatusCancelled      NodeTimerStatus = "cancelled"
	NodeTimerStatusRolledBack     NodeTimerStatus = "rolled_back"
	NodeTimerStatusRollbackFailed NodeTimerStatus = "rollback_failed"
	// NodeTimerStatusUnknown means the coordinator could not reach this node
	// to arm/cancel/observe its timer (peer.ErrPeerUnreachable) — the node's
	// own local timer is the only thing guaranteeing its safety until
	// Reconcile resolves this entry.
	NodeTimerStatusUnknown NodeTimerStatus = "unknown"
)

// NodeTimerLog is one node's distributed-rollback-timer bookkeeping entry
// within an ApplyLog.
type NodeTimerLog struct {
	Node       string          `json:"node"`
	Status     NodeTimerStatus `json:"status"`
	Error      string          `json:"error,omitempty"`
	Deadline   int64           `json:"deadline,omitempty"`
	ResolvedAt int64           `json:"resolvedAt,omitempty"`
}

// systemRollbackActor is the audit/apply-log attribution for an automatic
// (confirm-timeout) rollback — not the original user (docs/features/
// change-management.md §4; T-205 card: "attributed system:rollback").
const systemRollbackActor = "system:rollback"
