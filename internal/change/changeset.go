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
	// StatusRequested is T-1703's request-changeset entry state: a tenant
	// member created it (POST /changesets {tenantId}) but it is blocked from
	// apply until an approver converts it to an ordinary StatusDraft
	// (POST /changesets/{id}/approve). Its ops are validated exactly like a
	// draft's at creation, but there is deliberately NO transition from
	// requested to applying/validated — the only forward edges are to draft
	// (approve) or discarded (reject), so no request-changeset can ever reach
	// apply without passing through the ordinary draft flow an approver drives.
	StatusRequested Status = "requested"
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
		// StatusRolledBack is reachable since T-2602's staged (canary) apply.
		// A staged apply PAUSES in `applying` between stages — it is neither
		// applied nor rolled back — and aborting from that pause restores
		// exactly the stages that ran. That outcome is `rolled_back` by the
		// plain meaning of both terms: what was applied was undone. Routing
		// it to `failed` instead would conflate "we stopped on purpose and
		// cleaned up" with `failed`'s own documented meaning ("an apply step
		// failed"), which is still where an abort lands when the restore
		// itself could not complete. No non-staged apply can take this edge:
		// nothing else ever pauses in `applying`.
		StatusRolledBack: true,
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
	// requested (T-1703): an approver converts it to an ordinary draft, or it
	// is rejected/withdrawn to discarded. It can NEVER go straight to
	// applying/validated — apply is only ever reachable from draft/validated,
	// so the approval step is an unbypassable gate on every request-changeset.
	StatusRequested: {
		StatusDraft:     true,
		StatusDiscarded: true,
	},
}

// AllStatuses enumerates every valid Status, for tests (changeset_test.go's
// exhaustive state x action table) and for any future caller that wants
// the canonical list.
var AllStatuses = []Status{
	StatusDraft, StatusValidated, StatusApplying, StatusAwaitingConfirm,
	StatusCommitted, StatusRolledBack, StatusFailed, StatusDiscarded,
	StatusRequested,
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
	// UnattendedRevert (T-1805) is the computed, never-persisted answer to
	// "will this changeset revert itself if it locks me out, and until when?"
	// Set on the Apply response and on a read of an awaiting_confirm
	// changeset; nil otherwise. It carries a coverage bound, never the sealed
	// PVE ticket the coverage rests on.
	UnattendedRevert *UnattendedRevert
	ID               string
	Title            string
	Author           string
	// ClusterID (T-1201) scopes this changeset to a single attached cluster.
	// '' is the implicit default/local cluster, so a single-cluster
	// deployment's changesets keep working unchanged. Set once at Create and
	// never mutated — no op type or API surface lets a changeset span
	// clusters (validate_crosscluster.go enforces this at validation time).
	ClusterID string
	// Origin records who/what staged this changeset (T-1701): OriginUI for an
	// ordinary human edit through the SPA (the default for every pre-T-1701
	// row and every Create call), OriginMCP for an AI-staged draft, OriginCLI
	// for a vnproxctl-staged one. Set once at Create/CreateWithOrigin and never
	// mutated. It is the audit-trail half of T-1701's stage-only invariant: an
	// operator reading a changeset can always tell an AI-staged draft from a
	// human one, regardless of the change engine's own uniform apply path.
	Origin string
	// OriginTokenID (T-1701) is the api_tokens.id of the bearer token that
	// staged this changeset, when Origin is OriginMCP/OriginCLI; '' for a
	// UI-originated one. It ties an AI-staged draft back to the exact
	// automation credential that produced it.
	OriginTokenID string
	// OriginTool (T-2705) names the tool that staged this changeset, when it
	// was staged by a tool with an identity of its own — the MCP surface's
	// typed staging tools set it to their own tool name
	// ("changesets.stage.bridge", …). '' for everything else (the UI, the CLI,
	// gitsync, the generic changesets.create MCP tool). Where Origin says
	// WHAT KIND of actor staged the changeset and OriginTokenID says WHICH
	// automation credential (i.e. which session), this says WHICH ACTION —
	// together they are the tag a reviewer sees on an AI-staged draft
	// (docs/api.md's changeset-provenance paragraph). Set once at create and
	// never mutated: ChangesetRepo.Update does not write the column.
	OriginTool string
	Status     Status
	Ops        []Op
	Findings   []Finding
	Plan       json.RawMessage
	ApplyLog   json.RawMessage
	// RevertTicketExpiresAt (T-1805) is when the sealed apply-time revert
	// ticket stops being usable (unix seconds), or 0 when none is sealed. It
	// is a **bound, not a credential**: the sealed ticket itself never enters
	// this struct, so nothing that renders a Changeset — no API response, no
	// MCP tool result, no plugin-visible value — can carry it. This timestamp
	// is what UnattendedRevert's coverage report is recomputed from after a
	// reload.
	RevertTicketExpiresAt int64
	CreatedAt             int64
	UpdatedAt             int64
}

// Origin values for Changeset.Origin (T-1701). OriginUI is the default for any
// changeset staged through the ordinary human path; OriginMCP marks a draft
// staged by an AI operator through internal/mcp; OriginCLI is reserved for a
// vnproxctl-staged one. The set is small and closed — the change engine's apply
// path is identical regardless of origin (a changeset is a changeset), so this
// is purely a provenance label, never a control-flow switch.
const (
	OriginUI  = "ui"
	OriginMCP = "mcp"
	OriginCLI = "cli"
	// OriginGitSync (T-2701) marks a draft opened by internal/gitsync because
	// the spec in the operator's git repository and the live cluster
	// disagreed. Like every other origin it is a provenance label, not a
	// control-flow switch: a sync draft is an ordinary draft a human reviews
	// and applies through the normal flow. It is also the key gitsync uses to
	// find its own single open draft, which is how "one open sync changeset
	// at a time" is enforced without a second table.
	OriginGitSync = "gitsync"
)

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
