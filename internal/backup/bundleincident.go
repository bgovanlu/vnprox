// SPDX-License-Identifier: Apache-2.0

// bundleincident.go is T-2804's export: "close and export produces one
// artifact — the timeline plus a support bundle — through the existing
// redaction path".
//
// The load-bearing words are "the existing redaction path". An incident
// export is not a second archive format with its own idea of what is safe; it
// is a T-1902 support bundle carrying one additional declared entry. It
// therefore inherits, without restating any of it:
//
//   - Staging, so the entry has a recorded digest and a manifest line;
//   - bundleEntrySchema/bundleFieldSchema, so every field it emits is
//     declared with a disposition and a reason, checked by reflection;
//   - sealedCollector, so the incident collector — like every other bundle
//     collector — has no way to declare that it emits a secret class;
//   - Scrub, on every string the operator or the cluster (rather than this
//     code) decided the content of.
//
// AC3 asks for exactly that: the exported artifact passes the SAME
// secret-redaction assertions as the support bundle, reusing those tests.
// bundle_test.go's AC1 scan therefore runs over both producers rather than
// getting a second, parallel copy of itself.
//
// # The one deliberate omission: field VALUES
//
// A T-2704 diff entry carries per-field before/after values, and those values
// are interfaces(5) option values — the exact place a `wireguard-private-key`
// lives. The bundle's host-network collector protects that file with
// ifaceOptionAllowlist; a diff of the same file arriving by a different route
// would walk straight past it. So the export carries the NAMES of the fields
// that changed and not their values. A reader learns "wg0's private key
// changed", which is what triage needs, and never learns what it changed to.

package backup

import (
	"context"
)

// entryBundleIncident is the export's one additional archive entry.
const entryBundleIncident = "incident/timeline.json"

// BundleIncident is `incident/timeline.json`: one investigation window and
// every event on it, already merged and ordered by internal/incident.
//
// internal/backup keeps its own copy of the shape rather than importing
// internal/incident — the same layering internal/store follows for
// internal/capture and internal/flow, and the reason this package can be
// imported by the one that produces the document without a cycle.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type BundleIncident struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	OpenedBy    string `json:"openedBy"`
	OpenedAt    int64  `json:"openedAt"`
	StartedAt   int64  `json:"startedAt"`
	EndedAt     int64  `json:"endedAt"`
	ClosedAt    int64  `json:"closedAt"`
	Retroactive bool   `json:"retroactive"`

	WindowFrom int64 `json:"windowFrom"`
	WindowTo   int64 `json:"windowTo"`
	WindowLive bool  `json:"windowLive"`

	EventCount int                    `json:"eventCount"`
	Events     []BundleIncidentEvent  `json:"events"`
	Sources    []BundleIncidentSource `json:"sources"`
	Caveats    []string               `json:"caveats"`

	DiffErrorCode string              `json:"diffErrorCode,omitempty"`
	DiffError     string              `json:"diffError,omitempty"`
	Diff          *BundleIncidentDiff `json:"diff,omitempty"`
}

// BundleIncidentEvent is one timeline row.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type BundleIncidentEvent struct {
	At          int64  `json:"at"`
	Source      string `json:"source"`
	Kind        string `json:"kind"`
	Summary     string `json:"summary"`
	Actor       string `json:"actor,omitempty"`
	Node        string `json:"node,omitempty"`
	Ref         string `json:"ref,omitempty"`
	Result      string `json:"result,omitempty"`
	ChangesetID string `json:"changesetId,omitempty"`
	CaptureID   string `json:"captureId,omitempty"`
}

// BundleIncidentSource is one source's contribution and, when it contributed
// nothing, why.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type BundleIncidentSource struct {
	Source string `json:"source"`
	Status string `json:"status"`
	Count  int    `json:"count"`
	Detail string `json:"detail,omitempty"`
}

// BundleIncidentDiff is the T-2704 point-in-time diff across the window,
// summarised. Counts and coverage, plus one row per difference.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type BundleIncidentDiff struct {
	FromAt         int64                     `json:"fromAt"`
	ToAt           int64                     `json:"toAt"`
	FromSnapshotID string                    `json:"fromSnapshotId,omitempty"`
	ToSnapshotID   string                    `json:"toSnapshotId,omitempty"`
	Added          int                       `json:"addedCount"`
	Removed        int                       `json:"removedCount"`
	Modified       int                       `json:"modifiedCount"`
	Unattributed   int                       `json:"unattributedCount"`
	ComparedPaths  []string                  `json:"comparedPaths"`
	OmittedPaths   []string                  `json:"omittedPaths,omitempty"`
	UnmatchedNodes []string                  `json:"unmatchedNodes,omitempty"`
	Entries        []BundleIncidentDiffEntry `json:"entries"`
}

// BundleIncidentDiffEntry is one difference. Fields names, never field
// values — see this file's header for why.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape.
type BundleIncidentDiffEntry struct {
	Change      string   `json:"change"`
	Ref         string   `json:"ref"`
	Kind        string   `json:"kind"`
	Node        string   `json:"node,omitempty"`
	Name        string   `json:"name,omitempty"`
	Attributed  bool     `json:"attributed"`
	ChangesetID string   `json:"changesetId,omitempty"`
	Actor       string   `json:"actor,omitempty"`
	Fields      []string `json:"changedFields"`
}

// incidentCollector stages the incident document. Like every other bundle
// collector it is a bundleCollector — no Emits method — and it runs only when
// an incident was supplied, so an ordinary `vnproxctl support-bundle` is
// byte-for-byte what it always was.
type incidentCollector struct {
	opts *BundleOptions
}

func (c incidentCollector) Name() string { return "incident" }

func (c incidentCollector) Collect(_ context.Context, st *Staging) error {
	if c.opts.Incident == nil {
		return nil
	}
	return writeJSONEntry(st, entryBundleIncident, RoleMeta, scrubIncident(*c.opts.Incident))
}

// scrubIncident applies the dispositions bundleFieldSchema declares for these
// types. It is a pure function over the document so the redaction is
// testable on its own, and so the collector has no branch in it.
func scrubIncident(in BundleIncident) BundleIncident {
	out := in
	out.Title = Scrub(in.Title)
	out.OpenedBy = Scrub(in.OpenedBy)
	out.DiffError = Scrub(in.DiffError)

	out.Caveats = make([]string, 0, len(in.Caveats))
	for _, c := range in.Caveats {
		out.Caveats = append(out.Caveats, Scrub(c))
	}

	out.Events = make([]BundleIncidentEvent, 0, len(in.Events))
	for _, e := range in.Events {
		e.Summary = Scrub(e.Summary)
		e.Actor = Scrub(e.Actor)
		e.Node = Scrub(e.Node)
		e.Ref = Scrub(e.Ref)
		out.Events = append(out.Events, e)
	}
	out.EventCount = len(out.Events)

	out.Sources = make([]BundleIncidentSource, 0, len(in.Sources))
	for _, s := range in.Sources {
		s.Detail = Scrub(s.Detail)
		out.Sources = append(out.Sources, s)
	}

	if in.Diff != nil {
		d := *in.Diff
		d.UnmatchedNodes = scrubEach(in.Diff.UnmatchedNodes)
		d.Entries = make([]BundleIncidentDiffEntry, 0, len(in.Diff.Entries))
		for _, e := range in.Diff.Entries {
			e.Ref = Scrub(e.Ref)
			e.Node = Scrub(e.Node)
			e.Name = Scrub(e.Name)
			e.Actor = Scrub(e.Actor)
			e.Fields = scrubEach(e.Fields)
			d.Entries = append(d.Entries, e)
		}
		out.Diff = &d
	}
	return out
}

func scrubEach(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, Scrub(s))
	}
	return out
}
