package change

import "encoding/json"

// Status is a changeset's lifecycle state, per docs/data-model.md §2's
// documented column comment and docs/architecture.md §4's lifecycle
// diagram.
type Status string

const (
	// StatusDraft is the initial, freely-editable state: ops may be
	// replaced (PUT), the draft may be discarded (DELETE), or it may be
	// validated or sent straight to apply.
	StatusDraft Status = "draft"
	// StatusValidated means the last validation run (T-202) found no
	// blocking errors against the ops as they stood at that time. Any
	// further edit invalidates this back to StatusDraft (see Editable/the
	// transition table below) since the state may have moved.
	StatusValidated Status = "validated"
	// StatusApplying is set for the duration of T-205's apply-step
	// execution.
	StatusApplying Status = "applying"
	// StatusAwaitingConfirm is the commit-confirm window: apply succeeded,
	// a rollback deadline is armed, and the changeset commits only if the
	// user confirms before it elapses.
	StatusAwaitingConfirm Status = "awaiting_confirm"
	// StatusCommitted is terminal: the change is permanent (though still
	// eligible for a manual rollback that creates a new, separate
	// restoring changeset per docs/features/change-management.md §4 — that
	// is not a status transition of the committed changeset itself).
	StatusCommitted Status = "committed"
	// StatusRolledBack is terminal: either the confirm deadline elapsed
	// with no confirmation, or the apply failed and was rolled back mid-
	// flight (see StatusFailed for the "couldn't even fully roll back"
	// case T-205 distinguishes).
	StatusRolledBack Status = "rolled_back"
	// StatusFailed is terminal: an apply step failed. (T-205 defines
	// exactly what does and doesn't route here vs. StatusRolledBack.)
	StatusFailed Status = "failed"
	// StatusDiscarded is terminal: the draft was deleted before ever being
	// applied.
	StatusDiscarded Status = "discarded"
)

// allowedTransitions is the full legal (from -> to) status graph. Only
// draft and validated are ever mutable/discardable (Editable, below);
// committed/rolled_back/failed/discarded are all terminal historical
// records (audit trail, time-machine snapshots) that nothing may
// transition out of again. Rollback of an already-committed changeset
// (docs/features/change-management.md §4: "offered for 7 days ... creates
// a new restoring changeset via the normal flow") is deliberately NOT a
// transition of the committed changeset itself in this table — it
// produces a brand new Changeset (T-205's responsibility) that goes
// through this same table from StatusDraft, rather than mutating the
// original's Status.
var allowedTransitions = map[Status]map[Status]bool{
	StatusDraft: {
		StatusValidated: true,
		StatusApplying:  true,
		StatusDiscarded: true,
	},
	StatusValidated: {
		StatusDraft:     true,
		StatusApplying:  true,
		StatusDiscarded: true,
	},
	StatusApplying: {
		StatusAwaitingConfirm: true,
		StatusFailed:          true,
	},
	StatusAwaitingConfirm: {
		StatusCommitted:  true,
		StatusRolledBack: true,
		// StatusFailed is reachable when an auto/manual rollback of the applied
		// change could not fully restore every node (the "couldn't even fully
		// roll back" case this type's StatusFailed/StatusRolledBack doc
		// comments reserve for T-205). Added by T-205; see planning/reports/
		// T-205.md's deviation note.
		StatusFailed: true,
	},
	StatusCommitted:  {},
	StatusRolledBack: {},
	StatusFailed:     {},
	StatusDiscarded:  {},
}

// AllStatuses enumerates every valid Status, for tests (changeset_test.go's
// exhaustive state x action table) and for any future caller that wants
// the canonical list.
var AllStatuses = []Status{
	StatusDraft, StatusValidated, StatusApplying, StatusAwaitingConfirm,
	StatusCommitted, StatusRolledBack, StatusFailed, StatusDiscarded,
}

// Severity is a validation Finding's severity (docs/api.md's finding
// shape).
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// Finding is one validation result, per docs/api.md's documented shape:
// `{severity, code, message, ref?, fix?}`. T-202 is responsible for
// actually producing these; this package only defines the wire type so the
// changeset aggregate and the draft CRUD API have a stable "findings"
// field from day one, and T-202 has a type to populate rather than
// inventing its own.
type Finding struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	Ref      string   `json:"ref,omitempty"`
	Fix      []Op     `json:"fix,omitempty"`
}

// Changeset is the in-memory aggregate this package operates on — the
// typed counterpart of store.Changeset's flat/JSON-string row shape (see
// service.go's toStoreRow/fromStoreRow).
type Changeset struct {
	ConfirmDeadline *int64
	ID              string
	Title           string
	Author          string
	Status          Status
	Ops             []Op
	Findings        []Finding
	Plan            json.RawMessage
	ApplyLog        json.RawMessage
	CreatedAt       int64
	UpdatedAt       int64
}

// CanTransition reports whether moving from c's current Status to to is
// legal per allowedTransitions.
func (c Changeset) CanTransition(to Status) bool {
	return allowedTransitions[c.Status][to]
}

// Transition moves c to the given status, updating UpdatedAt, if and only
// if the transition is legal; otherwise it returns *ErrIllegalTransition
// and leaves c unmodified.
func (c *Changeset) Transition(to Status, nowUnix int64) error {
	if !c.CanTransition(to) {
		return &ErrIllegalTransition{From: c.Status, To: to}
	}
	c.Status = to
	c.UpdatedAt = nowUnix
	return nil
}

// Editable reports whether the changeset's ops may still be replaced or
// the changeset discarded outright (draft CRUD's PUT/DELETE): only draft
// and validated changesets qualify — every other status is either a
// terminal historical record or mid-flight apply that draft CRUD must
// never touch.
func (c Changeset) Editable() bool {
	return c.Status == StatusDraft || c.Status == StatusValidated
}
