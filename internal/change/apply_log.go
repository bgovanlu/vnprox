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
	Kind      StepKind   `json:"kind"`
	Node      string     `json:"node,omitempty"`
	Summary   string     `json:"summary"`
	Status    StepStatus `json:"status"`
	Error     string     `json:"error,omitempty"`
	Index     int        `json:"index"`
	StartedAt int64      `json:"startedAt,omitempty"`
	EndedAt   int64      `json:"endedAt,omitempty"`
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
	FailedStep   *int          `json:"failedStep,omitempty"`
	RolledBackBy string        `json:"rolledBackBy,omitempty"`
	Steps        []StepLog     `json:"steps"`
	Rollback     []RollbackLog `json:"rollback,omitempty"`
}

// systemRollbackActor is the audit/apply-log attribution for an automatic
// (confirm-timeout) rollback — not the original user (docs/features/
// change-management.md §4; T-205 card: "attributed system:rollback").
const systemRollbackActor = "system:rollback"
