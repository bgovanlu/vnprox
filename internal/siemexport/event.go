// SPDX-License-Identifier: Apache-2.0

package siemexport

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/redact"
)

// Kind names which of the two source streams an Event came from.
type Kind string

const (
	KindAudit   Kind = "audit"
	KindFinding Kind = "finding"
)

// Severity is the normalized three-level severity every Event carries,
// regardless of Kind — see doc.go's field-mapping section.
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
	SeverityInfo    = "info"
)

// Transition names why a finding Event was exported — this package's own
// addition, independent of internal/findings.TransitionKind (see doc.go's
// "Field mapping" section for why).
const (
	TransitionNew      = "new"
	TransitionChanged  = "changed"
	TransitionResolved = "resolved"
)

// Event is one exported record, already fully redacted — see doc.go. It is
// deliberately a flat struct with both audit-only and finding-only fields
// rather than an interface/union: every Sink implementation renders the
// same struct regardless of Kind, and a flat struct is what lets
// event_test.go and every Sink's own test assert on it directly without a
// type switch.
//
// audit row / finding transition, never in a tight loop) — field order
// here groups "common", "audit", "finding" for readability, matching this
// package's doc.go section order.
//
//nolint:govet // fieldalignment: not a hot allocation path (one Event per
type Event struct {
	Kind     Kind
	At       time.Time
	Severity string

	// Audit fields (Kind == KindAudit only).
	AuditID     int64
	Username    string
	Action      string
	Target      string
	ChangesetID string
	Result      string
	IP          string
	Detail      json.RawMessage // redacted (redact.JSON)

	// Finding fields (Kind == KindFinding only).
	FindingID     string
	Source        string
	Check         string
	Transition    string
	Nodes         []string
	Refs          []string
	FindingDetail string // redacted (redact.Scrub)
}

// AuditInput is the minimal audit-row shape ExportAudit needs, decoupled
// from internal/store.AuditEntry the same way internal/findings'
// FindingEventRecorder decouples that package from internal/store — this
// package must not import internal/store (cmd/vnproxd's composition root
// does that conversion; see its siemexport.go).
//
// never a hot allocation path — field order mirrors store.AuditEntry's own
// documented column order for readability.
//
//nolint:govet // fieldalignment: wire-shape input DTO, one per audit row,
type AuditInput struct {
	ID          int64
	At          int64 // unix seconds
	Username    string
	Action      string
	Target      string
	ChangesetID string
	Result      string
	IP          string
	// DetailJSON is the raw detail_json column text, or "" when the row has
	// none. Redacted by NewAuditEvent via redact.JSON before it ever
	// becomes part of an Event.
	DetailJSON string
}

// FindingInput is the minimal finding shape ExportFinding needs, decoupled
// from internal/findings.Finding for the same reason AuditInput is
// decoupled from store.AuditEntry.
//
// transition, never a hot allocation path — field order mirrors
// findings.Finding's own documented field order for readability.
//
//nolint:govet // fieldalignment: wire-shape input DTO, one per finding
type FindingInput struct {
	ID       string
	Source   string
	Check    string
	Severity string
	Detail   string
	Nodes    []string
	Refs     []string
	// Transition is one of the Transition* constants above, set by
	// cmd/vnproxd's siemFindingsTracker (event.go's own doc comment).
	Transition string
	// At is unix seconds; 0 means "now" (NewFindingEvent's zero-value
	// convention — internal/findings.Finding carries no timestamp of its
	// own, so the tracker stamps the instant it observed the transition).
	At int64
}

// auditSeverity maps AuditEntry.Result to Event.Severity. Result is a
// small, existing vocabulary (docs/security.md's Audit section: "success",
// "denied", "error", and a handful of change-engine outcome strings) — this
// switch is intentionally permissive (substring-free, case-insensitive
// exact match on the handful of values that mean "this was refused or
// failed") rather than an exhaustive enum, so an outcome string this
// package has never seen still degrades to "info" rather than panicking or
// needing a matching update here.
func auditSeverity(result string) string {
	switch strings.ToLower(strings.TrimSpace(result)) {
	case "denied", "blocked", "refused":
		return SeverityWarning
	case "error", "failed", "failure":
		return SeverityError
	default:
		return SeverityInfo
	}
}

// normalizeFindingSeverity passes through internal/findings' own
// error|warning|info vocabulary, and folds anything else to "info" —
// belt-and-braces against a future severity string this package has not
// been told about, the same "never panic on an unrecognized value" stance
// internal/findings.severityAtLeast itself takes for the same input.
func normalizeFindingSeverity(sev string) string {
	switch sev {
	case SeverityError, SeverityWarning, SeverityInfo:
		return sev
	default:
		return SeverityInfo
	}
}

// NewAuditEvent builds a redacted Event from in. Every free-text field is
// passed through redact.Scrub and DetailJSON through redact.JSON — the
// only place in this package an audit Event is constructed, so there is no
// call path that can produce an unredacted one.
func NewAuditEvent(in AuditInput) Event {
	var detail json.RawMessage
	if in.DetailJSON != "" {
		detail = redact.JSON([]byte(in.DetailJSON))
	}
	return Event{
		Kind:        KindAudit,
		At:          time.Unix(in.At, 0).UTC(),
		Severity:    auditSeverity(in.Result),
		AuditID:     in.ID,
		Username:    redact.Scrub(in.Username),
		Action:      redact.Scrub(in.Action),
		Target:      redact.Scrub(in.Target),
		ChangesetID: in.ChangesetID,
		Result:      in.Result,
		IP:          in.IP,
		Detail:      detail,
	}
}

// NewFindingEvent builds a redacted Event from in. Detail is passed through
// redact.Scrub; Nodes/Refs are scrubbed entry-by-entry too (defense in
// depth — neither is documented to ever carry a credential, but both are
// free strings a producer could in principle put anything in).
func NewFindingEvent(in FindingInput) Event {
	at := time.Now().UTC()
	if in.At != 0 {
		at = time.Unix(in.At, 0).UTC()
	}
	return Event{
		Kind:          KindFinding,
		At:            at,
		Severity:      normalizeFindingSeverity(in.Severity),
		FindingID:     in.ID,
		Source:        in.Source,
		Check:         in.Check,
		Transition:    in.Transition,
		Nodes:         scrubAll(in.Nodes),
		Refs:          scrubAll(in.Refs),
		FindingDetail: redact.Scrub(in.Detail),
	}
}

func scrubAll(ss []string) []string {
	if ss == nil {
		return nil
	}
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = redact.Scrub(s)
	}
	return out
}
