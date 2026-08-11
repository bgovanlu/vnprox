package incident

import (
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/store"
)

// Source is the closed vocabulary of timeline event sources — the five the
// card names, plus the operator's own annotations.
//
// It is a contract, not an internal detail: web/src/incidents renders per
// source, and the export declares its fields by these names.
type Source string

const (
	SourceFinding    Source = "finding"
	SourceChangeset  Source = "changeset"
	SourceDiagnosis  Source = "diagnosis"
	SourceCapture    Source = "capture"
	SourceFlow       Source = "flow"
	SourceAnnotation Source = "annotation"
)

// Sources returns the five machine sources plus annotations, in a fixed
// order. Used by the timeline assembly, by the per-source status list, and by
// the tests that assert all five are represented.
func Sources() []Source {
	return []Source{SourceFinding, SourceChangeset, SourceDiagnosis, SourceCapture, SourceFlow, SourceAnnotation}
}

// Status values for one source's contribution to a timeline.
const (
	// StatusOK: the source was queried and returned everything in the window.
	StatusOK = "ok"
	// StatusUnavailable: no such source is wired on this node (e.g. no flow
	// listener). Distinct from "ok with zero events", which is a fact about
	// the window; this is a fact about the daemon.
	StatusUnavailable = "unavailable"
	// StatusError: the source was queried and failed. The rest of the
	// timeline is still returned — one dead source degrades only its own
	// events, the same convention GET /flows' partial/failedNodes follows.
	StatusError = "error"
	// StatusTruncated: the source returned as many events as the cap allows
	// and there may be more.
	StatusTruncated = "truncated"
)

// Incident is one investigation window.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type Incident struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	// OpenedBy/OpenedAt describe the RECORD; StartedAt/EndedAt describe the
	// WINDOW. `openedAt > startedAt` is exactly what a retroactive incident
	// looks like, and the UI shows it as such.
	OpenedBy  string `json:"openedBy"`
	OpenedAt  int64  `json:"openedAt"`
	StartedAt int64  `json:"startedAt"`
	// EndedAt is the inclusive end of the window; 0 means "runs to now".
	EndedAt  int64 `json:"endedAt,omitempty"`
	ClosedAt int64 `json:"closedAt,omitempty"`
	// Retroactive is derived, not stored: the record was created after the
	// window it describes had already begun.
	Retroactive bool         `json:"retroactive"`
	Annotations []Annotation `json:"annotations"`
}

// Annotation is one operator observation on an incident's timeline.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type Annotation struct {
	ID     string `json:"id"`
	Author string `json:"author"`
	Body   string `json:"body"`
	At     int64  `json:"at"`
}

// Event is one entry on the merged timeline.
//
// The typed optional fields are deliberately flat rather than a per-source
// union: a timeline is read top to bottom by a human under time pressure, and
// every renderer of it (the UI, the export, a future MCP read tool) wants
// `at`, `source` and `summary` in the same place regardless of which
// subsystem produced the row.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type Event struct {
	// ID is stable for the same underlying row: `<source>:<key>`. It is the
	// tie-break in the chronological sort, so two events sharing a timestamp
	// always come back in the same order.
	ID      string `json:"id"`
	At      int64  `json:"at"`
	Source  Source `json:"source"`
	Kind    string `json:"kind"`
	Summary string `json:"summary"`

	Actor  string `json:"actor,omitempty"`
	Node   string `json:"node,omitempty"`
	Ref    string `json:"ref,omitempty"`
	Result string `json:"result,omitempty"`

	FindingID    string `json:"findingId,omitempty"`
	Transition   string `json:"transition,omitempty"`
	ChangesetID  string `json:"changesetId,omitempty"`
	Action       string `json:"action,omitempty"`
	CaptureID    string `json:"captureId,omitempty"`
	AnnotationID string `json:"annotationId,omitempty"`
}

// SourceStatus reports what one source contributed, and why it contributed
// nothing when it did.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type SourceStatus struct {
	Source Source `json:"source"`
	Status string `json:"status"`
	Count  int    `json:"count"`
	Detail string `json:"detail,omitempty"`
}

// Window is the resolved time range a timeline was assembled over. `to` is
// always concrete — an open incident resolves it to the assembly instant, so
// a timeline and the diff beside it describe the same range.
type Window struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
	// Live is true when the incident's own window is open-ended and To was
	// resolved from the clock rather than stored.
	Live bool `json:"live"`
}

// Timeline is GET /incidents/{id}/timeline's response: the incident, every
// event in its window in strict chronological order, what changed across the
// window per T-2704, and an honest account of what was not covered.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type Timeline struct {
	Incident Incident       `json:"incident"`
	Window   Window         `json:"window"`
	Events   []Event        `json:"events"`
	Sources  []SourceStatus `json:"sources"`

	// Diff is the T-2704 point-in-time diff across the window, or nil when
	// it could not be computed.
	Diff *change.TopologyDiff `json:"diff,omitempty"`
	// DiffError is the change engine's own refusal message when Diff is nil.
	// It is surfaced verbatim because it names the snapshots that DO exist,
	// which is both the explanation and the fix. An empty diff is never
	// substituted for it: "nothing changed" and "vnprox cannot see that far
	// back" are different statements.
	DiffError string `json:"diffError,omitempty"`
	// DiffErrorCode is docs/api.md's stable error code for that refusal
	// (`no_snapshot_in_range`, `validation_failed`, `apply_unavailable`,
	// `internal_error`), so a client can style it without parsing prose.
	DiffErrorCode string `json:"diffErrorCode,omitempty"`

	// Caveats are derived statements about what this timeline does not
	// establish — never a fixed list, always computed from what the sources
	// actually reported, so a caveat cannot outlive the limit it describes.
	Caveats []string `json:"caveats"`
}

// fromStoreIncident converts a store row into the API shape. Annotations are
// attached by the caller, which is the only party that knows whether it needs
// them.
func fromStoreIncident(row store.Incident) Incident {
	return Incident{
		ID:          row.ID,
		Title:       row.Title,
		Status:      row.Status,
		OpenedBy:    row.OpenedBy,
		OpenedAt:    row.OpenedAt,
		StartedAt:   row.StartedAt,
		EndedAt:     row.EndedAt,
		ClosedAt:    row.ClosedAt,
		Retroactive: row.OpenedAt > row.StartedAt,
		Annotations: []Annotation{},
	}
}

func fromStoreAnnotation(row store.IncidentAnnotation) Annotation {
	return Annotation{ID: row.ID, Author: row.Author, Body: row.Body, At: row.At}
}
