// SPDX-License-Identifier: Apache-2.0

package compliance

import "sort"

// Status is a control's reported state. FOUR values, and the distinction
// between the last two is the whole point of this package:
//
//   - StatusPass       — every mapped evidence item was evaluated and satisfied.
//   - StatusFail       — at least one mapped evidence item was evaluated and is not satisfied.
//   - StatusNotEvaluated — the control HAS a mapping, but at least one item could not
//     be evaluated (a posture factor posture itself could not assess, a policy rule
//     that is not installed, a rule that has never matched anything). Absence of
//     evidence is not evidence of compliance.
//   - StatusUnmapped   — the control has NO mapped evidence at all. vnprox has
//     nothing to say about it.
//
// StatusUnmapped and StatusNotEvaluated are never passing. IsPassing is the
// single predicate every renderer and every caller must use to decide
// whether a control "passed"; nothing else in this repo may compare against
// StatusPass directly.
type Status string

const (
	StatusPass         Status = "pass"
	StatusFail         Status = "fail"
	StatusNotEvaluated Status = "not_evaluated"
	StatusUnmapped     Status = "unmapped"
)

// AllStatuses is every Status, in report order (worst-to-best is not the
// order: this is the order a summary table lists them).
//
//nolint:gochecknoglobals // a read-only vocabulary table
var AllStatuses = []Status{StatusPass, StatusFail, StatusNotEvaluated, StatusUnmapped}

// IsPassing reports whether s means "this control passed". Exactly one
// status does.
//
// This is deliberately a function on the type rather than a `== StatusPass`
// written at each call site: T-2706's acceptance criterion 2 asserts an
// unmapped control can never be rendered as passing in ANY output format,
// and that assertion is only enforceable if there is one place that decides.
func (s Status) IsPassing() bool { return s == StatusPass }

// EvidenceStatus is one evidence item's state. Same three-way honesty as
// Status, minus "unmapped" (an evidence item is by definition mapped).
type EvidenceStatus string

const (
	EvidenceSatisfied    EvidenceStatus = "satisfied"
	EvidenceUnsatisfied  EvidenceStatus = "unsatisfied"
	EvidenceNotEvaluated EvidenceStatus = "not_evaluated"
)

// EvidenceResult is one evaluated evidence item: what was mapped, what was
// found, and where it came from.
type EvidenceResult struct {
	Kind EvidenceKind   `json:"kind"`
	Name string         `json:"name"`
	Stat EvidenceStatus `json:"status"`
	// Detail is the human-readable finding: "no open finding for check
	// mgmt_single_path", "posture factor segmentation scored 41/100,
	// below the profile's minimum of 70", "policy rule no-flat-vlan has
	// never matched an op in 214 evaluations".
	Detail string `json:"detail"`
	// Note is the profile author's own argument for why this item
	// evidences the control, carried through verbatim.
	Note string `json:"note,omitempty"`
	// Refs names the concrete records behind Detail — finding ids, policy
	// rule ids — so a reader can go look.
	Refs []string `json:"refs,omitempty"`
}

// Key is the "kind:name" token a rendered report carries for this item.
func (e EvidenceResult) Key() string { return string(e.Kind) + ":" + e.Name }

// ControlResult is one evaluated control.
//
//nolint:govet // fieldalignment: wire shape; field order is the documented JSON contract (docs/api.md's ComplianceReport).
type ControlResult struct {
	ID        string           `json:"id"`
	Title     string           `json:"title"`
	Statement string           `json:"statement"`
	Stat      Status           `json:"status"`
	Evidence  []EvidenceResult `json:"evidence,omitempty"`
	// UnmappedReason is the profile's stated reason this control has no
	// evidence (StatusUnmapped only).
	UnmappedReason string `json:"unmappedReason,omitempty"`
}

// EvidenceKeys returns every mapped evidence item's "kind:name" token, in
// profile order. AC1's "each check named as evidence".
func (c ControlResult) EvidenceKeys() []string {
	out := make([]string, 0, len(c.Evidence))
	for _, e := range c.Evidence {
		out = append(out, e.Key())
	}
	return out
}

// FailingEvidenceKeys returns the tokens of the evidence items that are NOT
// satisfied, in profile order. AC3's "the failing check is named".
func (c ControlResult) FailingEvidenceKeys() []string {
	out := make([]string, 0, len(c.Evidence))
	for _, e := range c.Evidence {
		if e.Stat == EvidenceUnsatisfied {
			out = append(out, e.Key())
		}
	}
	return out
}

// UnevaluatedEvidenceKeys returns the tokens of the evidence items that
// could not be evaluated.
func (c ControlResult) UnevaluatedEvidenceKeys() []string {
	out := make([]string, 0, len(c.Evidence))
	for _, e := range c.Evidence {
		if e.Stat == EvidenceNotEvaluated {
			out = append(out, e.Key())
		}
	}
	return out
}

// Summary is the per-status control count.
type Summary struct {
	Pass         int `json:"pass"`
	Fail         int `json:"fail"`
	NotEvaluated int `json:"notEvaluated"`
	Unmapped     int `json:"unmapped"`
	Total        int `json:"total"`
}

// Report is one fully-assembled compliance report: render-format-agnostic,
// so Markdown, HTML and JSON are all pure functions of this value (the same
// contract docexport.Data and docexport.PostureReport already establish).
//
//nolint:govet // fieldalignment: wire shape; field order is the documented JSON contract (docs/api.md's ComplianceReport).
type Report struct {
	// ProductVersion is the vnprox build that produced the report.
	ProductVersion string `json:"productVersion"`
	ProfileID      string `json:"profileId"`
	ProfileTitle   string `json:"profileTitle"`
	ProfileVersion string `json:"profileVersion"`
	// Notice is the profile's standing statement of what this report does
	// not claim. Every renderer emits it.
	Notice string `json:"notice"`
	// GeneratedAt is when the report was produced (unix seconds).
	GeneratedAt int64 `json:"generatedAt"`
	// AsOf is the point in time the evidence describes (unix seconds), or
	// 0 for a live report. A non-zero AsOf means the evidence was
	// reconstructed from retained history and is weaker — see Caveats.
	AsOf int64 `json:"asOf,omitempty"`
	// Caveats are report-wide honesty statements: what this particular
	// report could not establish. Never empty for a historical report.
	Caveats  []string        `json:"caveats,omitempty"`
	Summary  Summary         `json:"summary"`
	Controls []ControlResult `json:"controls"`
	// UnmappedChecks is every check in the catalog (or observed in the
	// evidence) that NO control in this profile maps. T-2706 acceptance
	// criterion 6: adding a check without mapping it must be reported, not
	// ignored — a control cannot silently lose a dimension.
	UnmappedChecks []string `json:"unmappedChecks,omitempty"`
	// CheckUniverse states where UnmappedChecks was computed from, so the
	// list's completeness is legible rather than assumed.
	CheckUniverse string `json:"checkUniverse"`
}

// summarize counts controls by status.
func summarize(controls []ControlResult) Summary {
	var s Summary
	for _, c := range controls {
		s.Total++
		switch c.Stat {
		case StatusPass:
			s.Pass++
		case StatusFail:
			s.Fail++
		case StatusNotEvaluated:
			s.NotEvaluated++
		case StatusUnmapped:
			s.Unmapped++
		}
	}
	return s
}

func sortedUnique(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
