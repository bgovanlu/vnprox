package diagnose

// StepStatus is one ladder step's outcome classification (docs/api.md's
// Diagnosis section) — part of the stable, machine-consumable contract
// T-1701's MCP AI operator will drive next arc. The vocabulary is closed:
// a step either genuinely ran (StatusRan, even if what it found was "could
// not reach the guest agent" — the honesty-contract convention this phase's
// other routes already established, docs/features/firewall.md §6), was not
// applicable to this target and never attempted (StatusSkipped, always
// carrying a human-readable Summary reason), or failed unexpectedly at the
// ladder-orchestration level itself (StatusError — reserved for a bug in
// the step's own wiring, not for an honest "could not attempt" outcome,
// which is StatusRan).
type StepStatus string

const (
	StatusRan     StepStatus = "ran"
	StatusSkipped StepStatus = "skipped"
	StatusError   StepStatus = "error"
)

// StepResult is one entry of Result.Steps — Ladder.Run's per-step record.
type StepResult struct {
	// Detail is the step's own response shape, verbatim (e.g. a
	// simulateResponse, a verifyObserved, a guestInteriorResponse, a
	// conntrackListResponse, or a capture.Group) — the same JSON body the
	// step's own underlying route would itself return, never
	// re-summarized/lossy. Omitted (nil) for a skipped/errored step, which
	// has no detail to show beyond Summary.
	Detail  any        `json:"detail,omitempty"`
	Name    string     `json:"name"`
	Status  StepStatus `json:"status"`
	Summary string     `json:"summary"`
	RanAt   int64      `json:"ranAt"`
}

// Confidence is Verdict's closed vocabulary for how much the ladder's
// overall run actually established about the target.
const (
	ConfidenceHigh   = "high"
	ConfidenceMedium = "medium"
	ConfidenceLow    = "low"
	ConfidenceNone   = "none"
)

// Verdict is the ladder's single readable conclusion — advisory only.
// T-1307's card: "every verdict links a human-confirm fix, never
// auto-remediates" — SuggestedFixRef, when present, is a pointer at an
// EXISTING fixable finding's own POST /findings/{id}/fix link (the same
// changeset that finding's own fix endpoint would produce directly), never
// a new auto-apply mechanism this package invents.
type Verdict struct {
	Summary          string   `json:"summary"`
	Confidence       string   `json:"confidence"`
	SuggestedFixRef  string   `json:"suggestedFixRef,omitempty"`
	LinkedFindingIDs []string `json:"linkedFindingIds"`
}

// Result is POST /diagnose's response body (docs/api.md's Diagnosis
// section) — a stable, machine-consumable shape: this is the scaffolding
// T-1701's MCP AI operator drives next arc, so its field names are a
// contract, not an internal detail (see ladder_test.go's schema/golden
// test).
type Result struct {
	Verdict Verdict      `json:"verdict"`
	Target  string       `json:"target"`
	Steps   []StepResult `json:"steps"`
}
