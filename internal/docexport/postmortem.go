// SPDX-License-Identifier: Apache-2.0

package docexport

import (
	"fmt"
	"html"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/incident"
	"github.com/bgovanlu/vnprox/internal/redact"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// postmortem.go renders T-4102's postmortem export: `internal/incident`'s
// Timeline turned into a filed document. It extends this package's existing
// dual-format machinery (T-605's config doc, T-1607's posture report,
// T-2706's compliance report) with a fourth artifact rather than a parallel
// renderer — one Data value, two pure rendering functions, same as the other
// three.
//
// # This is not internal/incident/export.go
//
// export.go produces a `backup.Bundle` support archive: a `.tar.gz` an
// operator attaches to a support case, through the redaction path every
// other bundle collector shares. This file produces a readable
// Markdown/HTML document through docexport's machinery instead — the two
// are unrelated deliverables that happen to read the same Timeline. Neither
// calls the other, and PostmortemDataOf never touches internal/backup.
//
// # It renders, it does not gather
//
// internal/incident's own doc comment states the contract this inherits
// exactly: a Timeline is assembled at READ time from history vnprox already
// records, and this package renders what that assembly produced — it opens
// no new source, runs no new query. PostmortemDataOf is a pure projection of
// one *incident.Timeline onto a render-format-agnostic shape.
//
// # Redaction: what is inherited, and what is not
//
// Two things happen here, both because a postmortem is a document that
// leaves the building — attached to a ticket, pasted into chat, mailed to a
// vendor:
//
//   - Every event Summary passes through redact.Scrub. Most of a Timeline's
//     summaries are built from typed fields (a finding id, a changeset
//     action, a flow's IP:port) with nothing credential-shaped in them, but
//     one source is not: `annotation` events carry an operator's own free
//     text verbatim, and a person under incident pressure pastes whatever is
//     in front of them. Scrub is pattern-based (docs/features.md and
//     internal/redact's own doc comment: "conservative in one direction
//     only ... over-match"), so it catches shapes — a PVE ticket, a PEM
//     block, a `password=...` assignment — not every possible secret. A
//     plain unlabelled string that happens to be a credential (an API key
//     with no recognizable prefix, pasted with no `key=` around it) is NOT
//     caught by this or by any redactor in this codebase; that gap is
//     structural, not a bug in this file.
//   - A diff entry's field VALUES are dropped entirely; only the NAMES of
//     the changed options survive. This is not a new decision — it is
//     export.go's own documented choice for the identical data
//     (bundleDiffEntry's "the NAME only" comment), applied here for the
//     identical reason: an interfaces(5) option value is exactly where a
//     WireGuard private key lives, and there is no way to tell a benign MTU
//     change from a credential rotation by inspecting the value. Dropping
//     the value is the only redaction that cannot be defeated by a pattern
//     redact.go has not learned yet.
//
// What this does NOT catch, stated plainly rather than left to be
// discovered: node names, interface names, IP addresses, flow byte counts,
// changeset titles and usernames are rendered in the clear — none of those
// are secrets by internal/redact's vocabulary, but a postmortem can still
// carry topology information an operator did not mean to hand to whoever
// receives the document. That is a disclosure decision for the person who
// exports it, the same one that already applies to `GET /export/doc` and
// every other docexport artifact.

// Postmortem heading constants, exported so the golden test asserts each
// verbatim and both renderers name a section identically — the same
// discipline compliance.go and markdown.go/html.go already hold.
const (
	HeadingPostmortemSummary  = "Incident summary"
	HeadingPostmortemAffected = "What was affected"
	HeadingPostmortemTimeline = "Timeline"
	HeadingPostmortemSources  = "Timeline sources"
	HeadingPostmortemFindings = "Findings raised"
	HeadingPostmortemDiff     = "Configuration changes"
	HeadingPostmortemCaveats  = "Caveats"
	postmortemDocTitle        = "vnprox incident postmortem"
)

// PostmortemEvent is one rendered timeline row. Summary has already passed
// through redact.Scrub by the time it reaches here (see PostmortemDataOf);
// every other field is a typed value copied from incident.Event verbatim.
type PostmortemEvent struct {
	Source     string
	Kind       string
	Summary    string
	Actor      string
	Node       string
	Ref        string
	Result     string
	FindingID  string
	Transition string
	At         int64
}

// PostmortemSource is one timeline source's contribution, unchanged from
// incident.SourceStatus except for the type of Source (a plain string here,
// so this package never imports incident's Source type into its render
// functions).
type PostmortemSource struct {
	Source string
	Status string
	Detail string
	Count  int
}

// PostmortemDiffEntry is one changed entity. Fields carries the NAMES of the
// options that changed and never their before/after values — see this
// file's doc comment.
type PostmortemDiffEntry struct {
	Change      string
	Ref         string
	Kind        string
	Node        string
	Name        string
	ChangesetID string
	Actor       string
	Fields      []string
	Attributed  bool
}

// PostmortemDiff is the T-2704 point-in-time diff, or the honest refusal in
// its place. Present is false exactly when the source Timeline's Diff field
// was nil — never inferred from Entries being empty, because "nothing
// changed" and "no diff could be computed" are different facts and must
// render as different sections.
type PostmortemDiff struct {
	Error          string
	ErrorCode      string
	ComparedPaths  []string
	OmittedPaths   []string
	UnmatchedNodes []string
	Entries        []PostmortemDiffEntry
	FromAt         int64
	ToAt           int64
	Added          int
	Removed        int
	Modified       int
	Unattributed   int
	Present        bool
}

// PostmortemAffected names the nodes and entities the timeline and the diff
// together touched — "what was affected", read off Node/Ref fields rather
// than re-derived from any source the Timeline did not already carry.
type PostmortemAffected struct {
	Nodes []string
	Refs  []string
}

// PostmortemData is the fully-assembled, render-format-agnostic content of
// one postmortem export. Markdown and HTML are both pure functions of a
// PostmortemData value, matching this package's existing contract for its
// other three artifacts.
type PostmortemData struct {
	Title       string
	Status      string
	OpenedBy    string
	IncidentID  string
	Affected    PostmortemAffected
	Caveats     []string
	Events      []PostmortemEvent
	Sources     []PostmortemSource
	Findings    []PostmortemEvent
	Diff        PostmortemDiff
	OpenedAt    int64
	StartedAt   int64
	EndedAt     int64
	ClosedAt    int64
	WindowFrom  int64
	WindowTo    int64
	GeneratedAt int64
	Retroactive bool
	WindowLive  bool
}

// PostmortemDataOf projects an incident Timeline onto PostmortemData.
//
// generatedAt is the render instant (unix seconds), supplied by the caller
// rather than read from time.Now here — the same "gathering takes a clock,
// rendering does not" split Service.Build/Gather already use, and it is what
// keeps this function a pure, table-testable projection.
func PostmortemDataOf(tl *incident.Timeline, generatedAt int64) PostmortemData {
	d := PostmortemData{
		IncidentID:  tl.Incident.ID,
		Title:       tl.Incident.Title,
		Status:      tl.Incident.Status,
		OpenedBy:    tl.Incident.OpenedBy,
		OpenedAt:    tl.Incident.OpenedAt,
		StartedAt:   tl.Incident.StartedAt,
		EndedAt:     tl.Incident.EndedAt,
		ClosedAt:    tl.Incident.ClosedAt,
		Retroactive: tl.Incident.Retroactive,
		WindowFrom:  tl.Window.From,
		WindowTo:    tl.Window.To,
		WindowLive:  tl.Window.Live,
		Caveats:     append([]string{}, tl.Caveats...),
		GeneratedAt: generatedAt,
	}

	nodes := map[string]bool{}
	refs := map[string]bool{}

	for _, e := range tl.Events {
		pe := PostmortemEvent{
			At:         e.At,
			Source:     string(e.Source),
			Kind:       e.Kind,
			Summary:    redact.Scrub(e.Summary),
			Actor:      e.Actor,
			Node:       e.Node,
			Ref:        e.Ref,
			Result:     e.Result,
			FindingID:  e.FindingID,
			Transition: e.Transition,
		}
		d.Events = append(d.Events, pe)
		if e.Source == incident.SourceFinding {
			d.Findings = append(d.Findings, pe)
		}
		if e.Node != "" {
			nodes[e.Node] = true
		}
		if e.Ref != "" {
			refs[e.Ref] = true
		}
	}

	for _, s := range tl.Sources {
		d.Sources = append(d.Sources, PostmortemSource{
			Source: string(s.Source), Status: s.Status, Count: s.Count, Detail: s.Detail,
		})
	}

	d.Diff = postmortemDiffOf(tl)
	for _, e := range d.Diff.Entries {
		if e.Node != "" {
			nodes[e.Node] = true
		}
		if ref := diffEntryRef(e); ref != "" {
			refs[ref] = true
		}
	}
	d.Affected = PostmortemAffected{Nodes: sortedSetKeys(nodes), Refs: sortedSetKeys(refs)}

	return d
}

func diffEntryRef(e PostmortemDiffEntry) string {
	if e.Name != "" {
		return e.Name
	}
	return e.Ref
}

func postmortemDiffOf(tl *incident.Timeline) PostmortemDiff {
	if tl.Diff == nil {
		return PostmortemDiff{Error: tl.DiffError, ErrorCode: tl.DiffErrorCode}
	}
	diff := tl.Diff
	out := PostmortemDiff{
		Present:       true,
		FromAt:        diff.From.At,
		ToAt:          diff.To.At,
		Added:         len(diff.Added),
		Removed:       len(diff.Removed),
		Modified:      len(diff.Modified),
		Unattributed:  diff.Unattributed,
		ComparedPaths: append([]string{}, diff.Coverage.Paths...),
		OmittedPaths:  append([]string{}, diff.Coverage.OmittedPaths...),
	}
	for _, n := range diff.Coverage.UnmatchedNodes {
		out.UnmatchedNodes = append(out.UnmatchedNodes, fmt.Sprintf("%s (captured only in %s)", n.Node, n.PresentIn))
	}
	out.Entries = appendDiffEntries(out.Entries, diff.Added)
	out.Entries = appendDiffEntries(out.Entries, diff.Removed)
	out.Entries = appendDiffEntries(out.Entries, diff.Modified)
	return out
}

// appendDiffEntries maps one group of topology.EntityDiff onto
// PostmortemDiffEntry, dropping every FieldChange's Before/After value —
// see this file's doc comment for why that is not optional.
func appendDiffEntries(dst []PostmortemDiffEntry, entries []topology.EntityDiff) []PostmortemDiffEntry {
	for _, en := range entries {
		fields := make([]string, 0, len(en.Fields))
		for _, f := range en.Fields {
			fields = append(fields, f.Field)
		}
		dst = append(dst, PostmortemDiffEntry{
			Change:      string(en.Change),
			Ref:         en.Ref,
			Kind:        en.Kind,
			Node:        en.Node,
			Name:        en.Name,
			Attributed:  en.Attribution.Attributed,
			ChangesetID: en.Attribution.ChangesetID,
			Actor:       en.Attribution.Actor,
			Fields:      fields,
		})
	}
	return dst
}

func sortedSetKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- Markdown -----------------------------------------------------------

// PostmortemMarkdown renders d as a standalone Markdown document.
func PostmortemMarkdown(d PostmortemData) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# %s\n\n", postmortemDocTitle)
	fmt.Fprintf(&b, "_Generated: %s (%d) for incident %s: %s_\n\n",
		stampRFC3339(d.GeneratedAt), d.GeneratedAt, mdCell(d.IncidentID), mdText(d.Title))

	writePostmortemSummaryMD(&b, d)
	writePostmortemAffectedMD(&b, d)
	writePostmortemTimelineMD(&b, d)
	writePostmortemSourcesMD(&b, d)
	writePostmortemFindingsMD(&b, d)
	writePostmortemDiffMD(&b, d)
	writePostmortemCaveatsMD(&b, d)

	return b.String()
}

func writePostmortemSummaryMD(b *strings.Builder, d PostmortemData) {
	fmt.Fprintf(b, "## %s\n\n", HeadingPostmortemSummary)
	b.WriteString("| Field | Value |\n|---|---|\n")
	fmt.Fprintf(b, "| Incident ID | %s |\n", mdCell(d.IncidentID))
	fmt.Fprintf(b, "| Title | %s |\n", mdText(d.Title))
	fmt.Fprintf(b, "| Status | %s |\n", mdCell(d.Status))
	fmt.Fprintf(b, "| Opened by | %s |\n", mdCell(d.OpenedBy))
	fmt.Fprintf(b, "| Opened at | %s |\n", stampRFC3339(d.OpenedAt))
	fmt.Fprintf(b, "| Retroactive | %s |\n", boolCell(d.Retroactive))
	fmt.Fprintf(b, "| Window start | %s |\n", stampRFC3339(d.WindowFrom))
	fmt.Fprintf(b, "| Window end | %s |\n", windowEndCell(d.WindowTo, d.WindowLive))
	fmt.Fprintf(b, "| Closed at | %s |\n", closedAtCell(d.ClosedAt))
	b.WriteString("\n")
}

func writePostmortemAffectedMD(b *strings.Builder, d PostmortemData) {
	fmt.Fprintf(b, "## %s\n\n", HeadingPostmortemAffected)
	if len(d.Affected.Nodes) == 0 && len(d.Affected.Refs) == 0 {
		fmt.Fprintf(b, "%s\n\n", noneObservedMarker)
		return
	}
	if len(d.Affected.Nodes) > 0 {
		fmt.Fprintf(b, "- Nodes: %s\n", strings.Join(d.Affected.Nodes, ", "))
	}
	if len(d.Affected.Refs) > 0 {
		fmt.Fprintf(b, "- Entities: %s\n", strings.Join(d.Affected.Refs, ", "))
	}
	b.WriteString("\n")
}

func writePostmortemTimelineMD(b *strings.Builder, d PostmortemData) {
	fmt.Fprintf(b, "## %s\n\n", HeadingPostmortemTimeline)
	if len(d.Events) == 0 {
		fmt.Fprintf(b, "_No events were recorded in this incident's window. See \"%s\" below for what each "+
			"source reported._\n\n", HeadingPostmortemSources)
		return
	}
	b.WriteString("| At | Source | Kind | Summary | Actor | Node | Ref | Result |\n|---|---|---|---|---|---|---|---|\n")
	for _, e := range d.Events {
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s | %s | %s |\n",
			stampRFC3339(e.At), mdCell(e.Source), mdCell(e.Kind), mdText(e.Summary),
			mdCell(e.Actor), mdCell(e.Node), mdCell(e.Ref), mdCell(e.Result))
	}
	b.WriteString("\n")
}

func writePostmortemSourcesMD(b *strings.Builder, d PostmortemData) {
	fmt.Fprintf(b, "## %s\n\n", HeadingPostmortemSources)
	b.WriteString("| Source | Status | Count | Detail |\n|---|---|---|---|\n")
	for _, s := range d.Sources {
		fmt.Fprintf(b, "| %s | %s | %d | %s |\n", mdCell(s.Source), mdCell(s.Status), s.Count, mdCell(sourceDetailCell(s)))
	}
	b.WriteString("\n")
}

func writePostmortemFindingsMD(b *strings.Builder, d PostmortemData) {
	fmt.Fprintf(b, "## %s\n\n", HeadingPostmortemFindings)
	if len(d.Findings) == 0 {
		fmt.Fprintf(b, "%s\n\n", findingsEmptyReason(d))
		return
	}
	b.WriteString("| At | Finding | Transition | Summary |\n|---|---|---|---|\n")
	for _, e := range d.Findings {
		fmt.Fprintf(b, "| %s | %s | %s | %s |\n", stampRFC3339(e.At), mdCell(e.FindingID), mdCell(e.Transition), mdText(e.Summary))
	}
	b.WriteString("\n")
}

func writePostmortemDiffMD(b *strings.Builder, d PostmortemData) {
	fmt.Fprintf(b, "## %s\n\n", HeadingPostmortemDiff)
	if !d.Diff.Present {
		fmt.Fprintf(b, "No point-in-time diff covers this window: %s", oneLine(d.Diff.Error))
		if d.Diff.ErrorCode != "" {
			fmt.Fprintf(b, " (`%s`)", d.Diff.ErrorCode)
		}
		b.WriteString("\n\n")
		return
	}
	fmt.Fprintf(b, "- Compared %s to %s\n", stampRFC3339(d.Diff.FromAt), stampRFC3339(d.Diff.ToAt))
	fmt.Fprintf(b, "- Added: %d, Removed: %d, Modified: %d, Unattributed (out of band): %d\n",
		d.Diff.Added, d.Diff.Removed, d.Diff.Modified, d.Diff.Unattributed)
	if len(d.Diff.ComparedPaths) > 0 {
		fmt.Fprintf(b, "- Paths compared: %s\n", strings.Join(d.Diff.ComparedPaths, ", "))
	}
	if len(d.Diff.OmittedPaths) > 0 {
		fmt.Fprintf(b, "- Paths not compared: %s\n", strings.Join(d.Diff.OmittedPaths, ", "))
	}
	if len(d.Diff.UnmatchedNodes) > 0 {
		fmt.Fprintf(b, "- Captured at only one end of the window: %s\n", strings.Join(d.Diff.UnmatchedNodes, "; "))
	}
	b.WriteString("\n")
	if len(d.Diff.Entries) == 0 {
		fmt.Fprintf(b, "%s\n\n", noneObservedMarker)
		return
	}
	b.WriteString("| Change | Kind | Node | Name/Ref | Fields changed | Attributed to |\n|---|---|---|---|---|---|\n")
	for _, e := range d.Diff.Entries {
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s |\n",
			mdCell(e.Change), mdCell(e.Kind), mdCell(e.Node), mdCell(diffEntryRef(e)),
			mdCell(strings.Join(e.Fields, ", ")), mdCell(attributionCell(e)))
	}
	b.WriteString("\n_Field values are not shown here — only which fields changed. An interfaces(5) option " +
		"value is exactly where a credential such as a WireGuard private key lives; see internal/redact._\n\n")
}

func writePostmortemCaveatsMD(b *strings.Builder, d PostmortemData) {
	fmt.Fprintf(b, "## %s\n\n", HeadingPostmortemCaveats)
	if len(d.Caveats) == 0 {
		fmt.Fprintf(b, "%s\n\n", noneObservedMarker)
		return
	}
	for _, c := range d.Caveats {
		fmt.Fprintf(b, "- %s\n", oneLine(c))
	}
	b.WriteString("\n")
}

// --- HTML -----------------------------------------------------------------

// PostmortemHTML renders d as a standalone HTML document — every byte
// inline, matching this package's existing CSP-friendly contract.
func PostmortemHTML(d PostmortemData) string {
	var b strings.Builder

	b.WriteString("<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\">\n")
	fmt.Fprintf(&b, "<title>%s</title>\n", html.EscapeString(postmortemDocTitle))
	b.WriteString(htmlStyle)
	b.WriteString("</head><body>\n")
	fmt.Fprintf(&b, "<h1>%s</h1>\n", html.EscapeString(postmortemDocTitle))
	fmt.Fprintf(&b, "<p class=\"generated\">Generated: %s (%d) for incident %s: %s</p>\n",
		html.EscapeString(stampRFC3339(d.GeneratedAt)), d.GeneratedAt, html.EscapeString(d.IncidentID), html.EscapeString(d.Title))

	writePostmortemSummaryHTML(&b, d)
	writePostmortemAffectedHTML(&b, d)
	writePostmortemTimelineHTML(&b, d)
	writePostmortemSourcesHTML(&b, d)
	writePostmortemFindingsHTML(&b, d)
	writePostmortemDiffHTML(&b, d)
	writePostmortemCaveatsHTML(&b, d)

	b.WriteString("</body></html>\n")
	return b.String()
}

func writePostmortemSummaryHTML(b *strings.Builder, d PostmortemData) {
	fmt.Fprintf(b, "<h2>%s</h2>\n", html.EscapeString(HeadingPostmortemSummary))
	b.WriteString("<table><tbody>\n")
	writeScopeRowHTML(b, "Incident ID", d.IncidentID)
	writeScopeRowHTML(b, "Title", d.Title)
	writeScopeRowHTML(b, "Status", d.Status)
	writeScopeRowHTML(b, "Opened by", d.OpenedBy)
	writeScopeRowHTML(b, "Opened at", stampRFC3339(d.OpenedAt))
	fmt.Fprintf(b, "<tr><td>Retroactive</td><td>%s</td></tr>\n", boolCell(d.Retroactive))
	writeScopeRowHTML(b, "Window start", stampRFC3339(d.WindowFrom))
	writeScopeRowHTML(b, "Window end", windowEndCell(d.WindowTo, d.WindowLive))
	writeScopeRowHTML(b, "Closed at", closedAtCell(d.ClosedAt))
	b.WriteString("</tbody></table>\n")
}

func writePostmortemAffectedHTML(b *strings.Builder, d PostmortemData) {
	fmt.Fprintf(b, "<h2>%s</h2>\n", html.EscapeString(HeadingPostmortemAffected))
	if len(d.Affected.Nodes) == 0 && len(d.Affected.Refs) == 0 {
		writeNoneHTML(b)
		return
	}
	b.WriteString("<ul>\n")
	if len(d.Affected.Nodes) > 0 {
		fmt.Fprintf(b, "<li>Nodes: %s</li>\n", html.EscapeString(strings.Join(d.Affected.Nodes, ", ")))
	}
	if len(d.Affected.Refs) > 0 {
		fmt.Fprintf(b, "<li>Entities: %s</li>\n", html.EscapeString(strings.Join(d.Affected.Refs, ", ")))
	}
	b.WriteString("</ul>\n")
}

func writePostmortemTimelineHTML(b *strings.Builder, d PostmortemData) {
	fmt.Fprintf(b, "<h2>%s</h2>\n", html.EscapeString(HeadingPostmortemTimeline))
	if len(d.Events) == 0 {
		fmt.Fprintf(b, "<p class=\"none\">No events were recorded in this incident's window. See &ldquo;%s&rdquo; "+
			"below for what each source reported.</p>\n", html.EscapeString(HeadingPostmortemSources))
		return
	}
	b.WriteString("<table><thead><tr><th>At</th><th>Source</th><th>Kind</th><th>Summary</th><th>Actor</th><th>Node</th><th>Ref</th><th>Result</th></tr></thead><tbody>\n")
	for _, e := range d.Events {
		fmt.Fprintf(b, "<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>\n",
			html.EscapeString(stampRFC3339(e.At)), html.EscapeString(e.Source), html.EscapeString(e.Kind), htmlCell(e.Summary),
			htmlCell(e.Actor), htmlCell(e.Node), htmlCell(e.Ref), htmlCell(e.Result))
	}
	b.WriteString("</tbody></table>\n")
}

func writePostmortemSourcesHTML(b *strings.Builder, d PostmortemData) {
	fmt.Fprintf(b, "<h2>%s</h2>\n", html.EscapeString(HeadingPostmortemSources))
	b.WriteString("<table><thead><tr><th>Source</th><th>Status</th><th>Count</th><th>Detail</th></tr></thead><tbody>\n")
	for _, s := range d.Sources {
		fmt.Fprintf(b, "<tr><td>%s</td><td>%s</td><td>%d</td><td>%s</td></tr>\n",
			html.EscapeString(s.Source), html.EscapeString(s.Status), s.Count, htmlCell(sourceDetailCell(s)))
	}
	b.WriteString("</tbody></table>\n")
}

func writePostmortemFindingsHTML(b *strings.Builder, d PostmortemData) {
	fmt.Fprintf(b, "<h2>%s</h2>\n", html.EscapeString(HeadingPostmortemFindings))
	if len(d.Findings) == 0 {
		fmt.Fprintf(b, "<p class=\"none\">%s</p>\n", html.EscapeString(findingsEmptyReason(d)))
		return
	}
	b.WriteString("<table><thead><tr><th>At</th><th>Finding</th><th>Transition</th><th>Summary</th></tr></thead><tbody>\n")
	for _, e := range d.Findings {
		fmt.Fprintf(b, "<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>\n",
			html.EscapeString(stampRFC3339(e.At)), html.EscapeString(e.FindingID), html.EscapeString(e.Transition), htmlCell(e.Summary))
	}
	b.WriteString("</tbody></table>\n")
}

func writePostmortemDiffHTML(b *strings.Builder, d PostmortemData) {
	fmt.Fprintf(b, "<h2>%s</h2>\n", html.EscapeString(HeadingPostmortemDiff))
	if !d.Diff.Present {
		fmt.Fprintf(b, "<p class=\"none\">No point-in-time diff covers this window: %s", html.EscapeString(oneLine(d.Diff.Error)))
		if d.Diff.ErrorCode != "" {
			fmt.Fprintf(b, " (<code>%s</code>)", html.EscapeString(d.Diff.ErrorCode))
		}
		b.WriteString("</p>\n")
		return
	}
	b.WriteString("<ul>\n")
	fmt.Fprintf(b, "<li>Compared %s to %s</li>\n", html.EscapeString(stampRFC3339(d.Diff.FromAt)), html.EscapeString(stampRFC3339(d.Diff.ToAt)))
	fmt.Fprintf(b, "<li>Added: %d, Removed: %d, Modified: %d, Unattributed (out of band): %d</li>\n",
		d.Diff.Added, d.Diff.Removed, d.Diff.Modified, d.Diff.Unattributed)
	if len(d.Diff.ComparedPaths) > 0 {
		fmt.Fprintf(b, "<li>Paths compared: %s</li>\n", html.EscapeString(strings.Join(d.Diff.ComparedPaths, ", ")))
	}
	if len(d.Diff.OmittedPaths) > 0 {
		fmt.Fprintf(b, "<li>Paths not compared: %s</li>\n", html.EscapeString(strings.Join(d.Diff.OmittedPaths, ", ")))
	}
	if len(d.Diff.UnmatchedNodes) > 0 {
		fmt.Fprintf(b, "<li>Captured at only one end of the window: %s</li>\n", html.EscapeString(strings.Join(d.Diff.UnmatchedNodes, "; ")))
	}
	b.WriteString("</ul>\n")
	if len(d.Diff.Entries) == 0 {
		writeNoneHTML(b)
		return
	}
	b.WriteString("<table><thead><tr><th>Change</th><th>Kind</th><th>Node</th><th>Name/Ref</th><th>Fields changed</th><th>Attributed to</th></tr></thead><tbody>\n")
	for _, e := range d.Diff.Entries {
		fmt.Fprintf(b, "<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>\n",
			html.EscapeString(e.Change), htmlCell(e.Kind), htmlCell(e.Node), htmlCell(diffEntryRef(e)),
			htmlCell(strings.Join(e.Fields, ", ")), htmlCell(attributionCell(e)))
	}
	b.WriteString("</tbody></table>\n")
	b.WriteString("<p class=\"none\">Field values are not shown here — only which fields changed. An " +
		"interfaces(5) option value is exactly where a credential such as a WireGuard private key lives; " +
		"see internal/redact.</p>\n")
}

func writePostmortemCaveatsHTML(b *strings.Builder, d PostmortemData) {
	fmt.Fprintf(b, "<h2>%s</h2>\n", html.EscapeString(HeadingPostmortemCaveats))
	if len(d.Caveats) == 0 {
		writeNoneHTML(b)
		return
	}
	b.WriteString("<ul>\n")
	for _, c := range d.Caveats {
		fmt.Fprintf(b, "<li>%s</li>\n", html.EscapeString(oneLine(c)))
	}
	b.WriteString("</ul>\n")
}

// --- shared cell helpers ---------------------------------------------------

// windowEndCell renders a window's inclusive end, mapping the 0 sentinel
// (endedAt<=0, live true) to an explicit "runs to now" rather than an epoch
// timestamp that would read as 1970.
func windowEndCell(endedAt int64, live bool) string {
	if endedAt <= 0 || live {
		return "runs to now (live)"
	}
	return stampRFC3339(endedAt)
}

// closedAtCell renders an incident's close time, or an explicit "not closed"
// for one still open — never a bare dash, which would be indistinguishable
// from a rendering bug.
func closedAtCell(closedAt int64) string {
	if closedAt <= 0 {
		return "not closed"
	}
	return stampRFC3339(closedAt)
}

// sourceDetailCell is the honesty channel this card asks for: an
// operator-facing sentence distinguishing "queried and found nothing" from
// "not wired on this node" from "queried and failed" — never a bare status
// word, and never an empty cell standing in for any of them.
func sourceDetailCell(s PostmortemSource) string {
	switch s.Status {
	case incident.StatusUnavailable:
		return "unavailable on this node: " + s.Detail
	case incident.StatusError:
		return "query failed, events may be incomplete: " + s.Detail
	case incident.StatusTruncated:
		return "truncated: " + s.Detail
	default: // StatusOK
		if s.Count == 0 {
			return fmt.Sprintf("no %s events were recorded in this window", s.Source)
		}
		return "-"
	}
}

// findingsEmptyReason distinguishes "no findings were raised" (the finding
// source was queried and returned nothing) from "finding history is not
// available on this node" or "could not be read" — the same distinction
// sourceDetailCell makes for the sources table, restated here because the
// findings section is read on its own by an operator jumping straight to
// "what broke".
func findingsEmptyReason(d PostmortemData) string {
	for _, s := range d.Sources {
		if s.Source != string(incident.SourceFinding) {
			continue
		}
		switch s.Status {
		case incident.StatusUnavailable:
			return "finding history is not available on this node: " + s.Detail
		case incident.StatusError:
			return "finding history could not be read for this window: " + s.Detail
		}
	}
	return "no findings were raised in this window"
}

func attributionCell(e PostmortemDiffEntry) string {
	if !e.Attributed {
		return "unattributed (made out of band)"
	}
	if e.ChangesetID != "" {
		return e.ChangesetID
	}
	if e.Actor != "" {
		return e.Actor
	}
	return "-"
}
