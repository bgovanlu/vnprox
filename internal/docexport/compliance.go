// SPDX-License-Identifier: Apache-2.0

package docexport

import (
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/compliance"
)

// compliance.go renders T-2706's compliance report. It extends this
// package's existing dual-format machinery (T-605's config doc, T-1607's
// posture report) with a third artifact rather than introducing a parallel
// renderer, and adds one thing neither of those has: a PARSER per format.
//
// WHY A PARSER. The card requires the rendered report to round-trip, and the
// central safety property — a control with no mapped evidence reports
// `unmapped`, never `pass` — is only enforceable end-to-end if the status a
// format RENDERED can be read back and compared. A renderer that quietly
// prints a green tick next to an unmapped control would satisfy any
// assertion made against the model alone. So every format here is
// (Render, Parse) and the standing test drives BOTH over the registry —
// which is also how a format added later is covered without touching the
// test: an unregistered renderer fails
// TestComplianceRenderers_RegistryCoversEveryRenderer.

// Compliance report headings, exported so the golden test asserts each
// verbatim and every renderer names a section identically.
const (
	HeadingComplianceScope    = "Report scope"
	HeadingComplianceCaveats  = "Caveats"
	HeadingComplianceSummary  = "Control summary"
	HeadingComplianceControls = "Controls"
	HeadingComplianceDetail   = "Control detail"
	HeadingComplianceUnmapped = "Unmapped checks"
	complianceDocTitle        = "vnprox compliance report"
	// complianceNoticeLead prefixes the profile's notice in every format,
	// so the artifact leads with what it is not.
	complianceNoticeLead = "This is not a certification."
	// evidenceNone is the cell content for "no evidence in this column".
	// Deliberately not an empty cell: an empty cell is indistinguishable
	// from a rendering bug.
	evidenceNone = "—"
	// evidenceSep joins evidence tokens in a single cell. Check names,
	// factor names and rule ids never contain it.
	evidenceSep = "; "
)

// ComplianceControlDigest is one control as recovered from a rendered
// report.
type ComplianceControlDigest struct {
	ID       string
	Status   compliance.Status
	Evidence []string
	Failing  []string
}

// ComplianceDigest is the machine-readable core of a rendered compliance
// report: everything a reader must be able to get back out of the artifact.
// Every format's parser returns one, and the round-trip test asserts it
// equals ComplianceDigestOf(the report that was rendered).
type ComplianceDigest struct {
	ProductVersion string
	ProfileID      string
	ProfileVersion string
	Controls       []ComplianceControlDigest
	UnmappedChecks []string
	GeneratedAt    int64
	AsOf           int64
	// NoticePresent records that the format carried the profile's
	// "this is not a certification" notice. A format that dropped it would
	// round-trip its statuses perfectly and still be the artifact the arc
	// risk register warns about.
	NoticePresent bool
}

// ComplianceDigestOf projects a report onto the digest its renderers must
// preserve.
func ComplianceDigestOf(r compliance.Report) ComplianceDigest {
	d := ComplianceDigest{
		ProductVersion: r.ProductVersion,
		ProfileID:      r.ProfileID,
		ProfileVersion: r.ProfileVersion,
		GeneratedAt:    r.GeneratedAt,
		AsOf:           r.AsOf,
		UnmappedChecks: append([]string(nil), r.UnmappedChecks...),
		NoticePresent:  strings.TrimSpace(r.Notice) != "",
	}
	for _, c := range r.Controls {
		d.Controls = append(d.Controls, ComplianceControlDigest{
			ID:     c.ID,
			Status: c.Stat,
			// Normalized to nil when empty: a parser cannot tell an empty
			// cell from an absent one, so an empty-but-non-nil slice here
			// would fail the round-trip on a distinction no reader of the
			// artifact could ever make.
			Evidence: nilIfEmpty(c.EvidenceKeys()),
			Failing:  nilIfEmpty(c.FailingEvidenceKeys()),
		})
	}
	return d
}

func nilIfEmpty(ss []string) []string {
	if len(ss) == 0 {
		return nil
	}
	return ss
}

// ComplianceRenderer is one output format of the compliance report.
type ComplianceRenderer struct {
	Render func(compliance.Report) string
	Parse  func(string) (ComplianceDigest, error)
	// Format is the `?format=` value that selects it.
	Format string
	// FuncName is the exported renderer function's name, which
	// TestComplianceRenderers_RegistryCoversEveryRenderer matches against
	// the package's source.
	FuncName    string
	ContentType string
	Extension   string
}

// ComplianceRenderers is every output format the compliance report has.
//
// ADDING A FORMAT: write `func ComplianceX(r compliance.Report) string` plus
// its parser and add it here. Leaving it out of this slice fails
// TestComplianceRenderers_RegistryCoversEveryRenderer, which is what makes
// the card's "in any output format" assertion hold for formats that do not
// exist yet.
func ComplianceRenderers() []ComplianceRenderer {
	return []ComplianceRenderer{
		{
			Format: "md", FuncName: "ComplianceMarkdown",
			ContentType: "text/markdown; charset=utf-8", Extension: "md",
			Render: ComplianceMarkdown, Parse: ParseComplianceMarkdown,
		},
		{
			Format: "html", FuncName: "ComplianceHTML",
			ContentType: "text/html; charset=utf-8", Extension: "html",
			Render: ComplianceHTML, Parse: ParseComplianceHTML,
		},
		{
			Format: "json", FuncName: "ComplianceJSON",
			ContentType: "application/json; charset=utf-8", Extension: "json",
			Render: ComplianceJSON, Parse: ParseComplianceJSON,
		},
	}
}

// ComplianceRendererFor returns the renderer for format, or ok=false.
func ComplianceRendererFor(format string) (ComplianceRenderer, bool) {
	for _, r := range ComplianceRenderers() {
		if r.Format == format {
			return r, true
		}
	}
	return ComplianceRenderer{}, false
}

// ComplianceFormats lists every supported `?format=` value, sorted — for the
// API's validation error message, so it can never drift from the registry.
func ComplianceFormats() []string {
	out := make([]string, 0, 3)
	for _, r := range ComplianceRenderers() {
		out = append(out, r.Format)
	}
	sort.Strings(out)
	return out
}

// --- Markdown ---------------------------------------------------------

// ComplianceMarkdown renders r as a standalone Markdown document.
func ComplianceMarkdown(r compliance.Report) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# %s\n\n", complianceDocTitle)
	fmt.Fprintf(&b, "> **%s** %s\n\n", complianceNoticeLead, oneLine(r.Notice))

	fmt.Fprintf(&b, "## %s\n\n", HeadingComplianceScope)
	b.WriteString("| Field | Value |\n|---|---|\n")
	fmt.Fprintf(&b, "| vnprox version | %s |\n", complianceCell(r.ProductVersion))
	fmt.Fprintf(&b, "| Profile | %s |\n", complianceCell(r.ProfileID))
	fmt.Fprintf(&b, "| Profile title | %s |\n", mdCell(r.ProfileTitle))
	fmt.Fprintf(&b, "| Profile version | %s |\n", complianceCell(r.ProfileVersion))
	fmt.Fprintf(&b, "| Generated | %s |\n", stampRFC3339(r.GeneratedAt))
	fmt.Fprintf(&b, "| Generated (unix) | %d |\n", r.GeneratedAt)
	fmt.Fprintf(&b, "| Evidence as of | %s |\n", asOfLabel(r.AsOf))
	fmt.Fprintf(&b, "| Evidence as of (unix) | %d |\n", r.AsOf)
	fmt.Fprintf(&b, "| Check universe | %s |\n\n", mdCell(r.CheckUniverse))

	fmt.Fprintf(&b, "## %s\n\n", HeadingComplianceCaveats)
	if len(r.Caveats) == 0 {
		fmt.Fprintf(&b, "%s\n\n", noneObservedMarker)
	} else {
		for _, c := range r.Caveats {
			fmt.Fprintf(&b, "- %s\n", oneLine(c))
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "## %s\n\n", HeadingComplianceSummary)
	b.WriteString("| Status | Controls |\n|---|---|\n")
	for _, st := range compliance.AllStatuses {
		fmt.Fprintf(&b, "| %s | %d |\n", st, summaryCount(r.Summary, st))
	}
	fmt.Fprintf(&b, "| total | %d |\n\n", r.Summary.Total)

	fmt.Fprintf(&b, "## %s\n\n", HeadingComplianceControls)
	b.WriteString("| Control | Title | Status | Evidence | Failing evidence |\n|---|---|---|---|---|\n")
	for _, c := range r.Controls {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
			complianceCell(c.ID), mdCell(c.Title), c.Stat,
			complianceCell(joinEvidence(c.EvidenceKeys())), complianceCell(joinEvidence(c.FailingEvidenceKeys())))
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "## %s\n\n", HeadingComplianceDetail)
	for _, c := range r.Controls {
		writeComplianceControlDetailMD(&b, c)
	}

	fmt.Fprintf(&b, "## %s\n\n", HeadingComplianceUnmapped)
	if len(r.UnmappedChecks) == 0 {
		fmt.Fprintf(&b, "%s\n\n", noneObservedMarker)
		return b.String()
	}
	b.WriteString("These checks this build can emit are mapped by no control in this profile, and contribute to no control's status:\n\n")
	for _, name := range r.UnmappedChecks {
		fmt.Fprintf(&b, "- `%s`\n", name)
	}
	b.WriteString("\n")
	return b.String()
}

func writeComplianceControlDetailMD(b *strings.Builder, c compliance.ControlResult) {
	fmt.Fprintf(b, "### %s — %s\n\n", c.ID, c.Title)
	fmt.Fprintf(b, "- **Status: %s**\n", c.Stat)
	fmt.Fprintf(b, "- Statement: %s\n", oneLine(c.Statement))
	if c.Stat == compliance.StatusUnmapped {
		fmt.Fprintf(b, "- vnprox maps no evidence to this control: %s\n\n", oneLine(c.UnmappedReason))
		return
	}
	b.WriteString("\n| Evidence | Status | Detail | Refs |\n|---|---|---|---|\n")
	for _, e := range c.Evidence {
		detail := e.Detail
		if e.Note != "" {
			detail += " (profile note: " + e.Note + ")"
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s |\n",
			mdCell(e.Key()), e.Stat, mdCell(oneLine(detail)), mdCell(joinEvidence(e.Refs)))
	}
	b.WriteString("\n")
}

// ParseComplianceMarkdown recovers the digest from a Markdown report.
func ParseComplianceMarkdown(doc string) (ComplianceDigest, error) {
	var d ComplianceDigest
	lines := strings.Split(doc, "\n")

	scope := map[string]string{}
	section := ""
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "## ") {
			section = strings.TrimPrefix(line, "## ")
			continue
		}
		if strings.HasPrefix(line, "> **"+complianceNoticeLead+"**") {
			d.NoticePresent = true
			continue
		}
		switch section {
		case HeadingComplianceScope:
			if cells, ok := mdRow(line, 2); ok {
				scope[cells[0]] = cells[1]
			}
		case HeadingComplianceControls:
			cells, ok := mdRow(line, 5)
			if !ok || cells[0] == "Control" || strings.HasPrefix(cells[0], "---") {
				continue
			}
			d.Controls = append(d.Controls, ComplianceControlDigest{
				ID:       cells[0],
				Status:   compliance.Status(cells[2]),
				Evidence: splitEvidence(cells[3]),
				Failing:  splitEvidence(cells[4]),
			})
		case HeadingComplianceUnmapped:
			if strings.HasPrefix(line, "- `") && strings.HasSuffix(line, "`") {
				d.UnmappedChecks = append(d.UnmappedChecks, strings.Trim(strings.TrimPrefix(line, "- "), "`"))
			}
		}
	}

	d.ProductVersion = scope["vnprox version"]
	d.ProfileID = scope["Profile"]
	d.ProfileVersion = scope["Profile version"]
	var err error
	if d.GeneratedAt, err = parseUnixCell(scope, "Generated (unix)"); err != nil {
		return ComplianceDigest{}, err
	}
	if d.AsOf, err = parseUnixCell(scope, "Evidence as of (unix)"); err != nil {
		return ComplianceDigest{}, err
	}
	if len(d.Controls) == 0 {
		return ComplianceDigest{}, fmt.Errorf("docexport: parsing compliance Markdown: no control rows under %q", HeadingComplianceControls)
	}
	return d, nil
}

// mdRow splits a Markdown table row into exactly want cells, or reports
// ok=false for anything that is not one (including the |---|---| separator).
func mdRow(line string, want int) ([]string, bool) {
	if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
		return nil, false
	}
	parts := strings.Split(strings.Trim(line, "|"), "|")
	if len(parts) != want {
		return nil, false
	}
	out := make([]string, want)
	for i, p := range parts {
		out[i] = strings.TrimSpace(p)
		if strings.Trim(out[i], "-") == "" && out[i] != "" {
			return nil, false
		}
	}
	return out, true
}

func parseUnixCell(scope map[string]string, key string) (int64, error) {
	raw, ok := scope[key]
	if !ok {
		return 0, fmt.Errorf("docexport: parsing compliance report: scope table has no %q row", key)
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("docexport: parsing compliance report's %q (%q): %w", key, raw, err)
	}
	return n, nil
}

// --- HTML -------------------------------------------------------------

// ComplianceHTML renders r as a standalone HTML document — every byte
// inline, no external reference, matching this package's existing
// CSP-friendly contract.
//
// Each control row carries machine-readable `data-` attributes alongside its
// visible cells, and ParseComplianceHTML reads those. The golden test
// additionally asserts the VISIBLE status cell equals the attribute, so this
// format cannot show one status to a person and report another to a machine.
func ComplianceHTML(r compliance.Report) string {
	var b strings.Builder

	b.WriteString("<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\">\n")
	fmt.Fprintf(&b, "<title>%s</title>\n", html.EscapeString(complianceDocTitle))
	writeMeta(&b, "vnprox.productVersion", r.ProductVersion)
	writeMeta(&b, "vnprox.profileId", r.ProfileID)
	writeMeta(&b, "vnprox.profileVersion", r.ProfileVersion)
	writeMeta(&b, "vnprox.generatedAt", strconv.FormatInt(r.GeneratedAt, 10))
	writeMeta(&b, "vnprox.asOf", strconv.FormatInt(r.AsOf, 10))
	b.WriteString(htmlStyle)
	b.WriteString("</head><body>\n")
	fmt.Fprintf(&b, "<h1>%s</h1>\n", html.EscapeString(complianceDocTitle))
	fmt.Fprintf(&b, "<p class=\"none\" data-notice=\"1\"><strong>%s</strong> %s</p>\n",
		html.EscapeString(complianceNoticeLead), html.EscapeString(oneLine(r.Notice)))

	fmt.Fprintf(&b, "<h2>%s</h2>\n", html.EscapeString(HeadingComplianceScope))
	b.WriteString("<table><tbody>\n")
	writeScopeRowHTML(&b, "vnprox version", r.ProductVersion)
	writeScopeRowHTML(&b, "Profile", r.ProfileID)
	writeScopeRowHTML(&b, "Profile title", r.ProfileTitle)
	writeScopeRowHTML(&b, "Profile version", r.ProfileVersion)
	writeScopeRowHTML(&b, "Generated", stampRFC3339(r.GeneratedAt))
	writeScopeRowHTML(&b, "Evidence as of", asOfLabel(r.AsOf))
	writeScopeRowHTML(&b, "Check universe", r.CheckUniverse)
	b.WriteString("</tbody></table>\n")

	fmt.Fprintf(&b, "<h2>%s</h2>\n", html.EscapeString(HeadingComplianceCaveats))
	if len(r.Caveats) == 0 {
		writeNoneHTML(&b)
	} else {
		b.WriteString("<ul>\n")
		for _, c := range r.Caveats {
			fmt.Fprintf(&b, "<li>%s</li>\n", html.EscapeString(oneLine(c)))
		}
		b.WriteString("</ul>\n")
	}

	fmt.Fprintf(&b, "<h2>%s</h2>\n", html.EscapeString(HeadingComplianceSummary))
	b.WriteString("<table><thead><tr><th>Status</th><th>Controls</th></tr></thead><tbody>\n")
	for _, st := range compliance.AllStatuses {
		fmt.Fprintf(&b, "<tr><td>%s</td><td>%d</td></tr>\n", html.EscapeString(string(st)), summaryCount(r.Summary, st))
	}
	fmt.Fprintf(&b, "<tr><td>total</td><td>%d</td></tr>\n</tbody></table>\n", r.Summary.Total)

	fmt.Fprintf(&b, "<h2>%s</h2>\n", html.EscapeString(HeadingComplianceControls))
	b.WriteString("<table><thead><tr><th>Control</th><th>Title</th><th>Status</th><th>Evidence</th><th>Failing evidence</th></tr></thead><tbody>\n")
	for _, c := range r.Controls {
		fmt.Fprintf(&b,
			"<tr data-control=\"%s\" data-status=\"%s\" data-evidence=\"%s\" data-failing=\"%s\">"+
				"<td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>\n",
			html.EscapeString(c.ID), html.EscapeString(string(c.Stat)),
			html.EscapeString(strings.Join(c.EvidenceKeys(), evidenceSep)),
			html.EscapeString(strings.Join(c.FailingEvidenceKeys(), evidenceSep)),
			html.EscapeString(c.ID), html.EscapeString(c.Title), html.EscapeString(string(c.Stat)),
			html.EscapeString(joinEvidence(c.EvidenceKeys())), html.EscapeString(joinEvidence(c.FailingEvidenceKeys())))
	}
	b.WriteString("</tbody></table>\n")

	fmt.Fprintf(&b, "<h2>%s</h2>\n", html.EscapeString(HeadingComplianceDetail))
	for _, c := range r.Controls {
		writeComplianceControlDetailHTML(&b, c)
	}

	fmt.Fprintf(&b, "<h2>%s</h2>\n", html.EscapeString(HeadingComplianceUnmapped))
	if len(r.UnmappedChecks) == 0 {
		writeNoneHTML(&b)
	} else {
		b.WriteString("<p>These checks this build can emit are mapped by no control in this profile, and contribute to no control's status:</p>\n<ul>\n")
		for _, name := range r.UnmappedChecks {
			fmt.Fprintf(&b, "<li data-unmapped-check=\"%s\">%s</li>\n", html.EscapeString(name), html.EscapeString(name))
		}
		b.WriteString("</ul>\n")
	}

	b.WriteString("</body></html>\n")
	return b.String()
}

func writeComplianceControlDetailHTML(b *strings.Builder, c compliance.ControlResult) {
	fmt.Fprintf(b, "<h3>%s — %s</h3>\n", html.EscapeString(c.ID), html.EscapeString(c.Title))
	fmt.Fprintf(b, "<p><strong>Status: %s</strong></p>\n", html.EscapeString(string(c.Stat)))
	fmt.Fprintf(b, "<p>%s</p>\n", html.EscapeString(oneLine(c.Statement)))
	if c.Stat == compliance.StatusUnmapped {
		fmt.Fprintf(b, "<p class=\"none\">vnprox maps no evidence to this control: %s</p>\n",
			html.EscapeString(oneLine(c.UnmappedReason)))
		return
	}
	b.WriteString("<table><thead><tr><th>Evidence</th><th>Status</th><th>Detail</th><th>Refs</th></tr></thead><tbody>\n")
	for _, e := range c.Evidence {
		detail := e.Detail
		if e.Note != "" {
			detail += " (profile note: " + e.Note + ")"
		}
		fmt.Fprintf(b, "<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>\n",
			html.EscapeString(e.Key()), html.EscapeString(string(e.Stat)),
			html.EscapeString(oneLine(detail)), html.EscapeString(joinEvidence(e.Refs)))
	}
	b.WriteString("</tbody></table>\n")
}

func writeMeta(b *strings.Builder, name, content string) {
	fmt.Fprintf(b, "<meta name=\"%s\" content=\"%s\">\n", html.EscapeString(name), html.EscapeString(content))
}

func writeScopeRowHTML(b *strings.Builder, field, value string) {
	fmt.Fprintf(b, "<tr><td>%s</td><td>%s</td></tr>\n", html.EscapeString(field), html.EscapeString(value))
}

//nolint:gochecknoglobals // compiled once; these describe this file's own emitted shape
var (
	metaRe          = regexp.MustCompile(`<meta name="([^"]+)" content="([^"]*)">`)
	controlRowRe    = regexp.MustCompile(`<tr data-control="([^"]*)" data-status="([^"]*)" data-evidence="([^"]*)" data-failing="([^"]*)">`)
	unmappedCheckRe = regexp.MustCompile(`<li data-unmapped-check="([^"]*)">`)
	noticeRe        = regexp.MustCompile(`<p class="none" data-notice="1">`)
)

// ParseComplianceHTML recovers the digest from an HTML report.
func ParseComplianceHTML(doc string) (ComplianceDigest, error) {
	var d ComplianceDigest
	meta := map[string]string{}
	for _, m := range metaRe.FindAllStringSubmatch(doc, -1) {
		meta[m[1]] = html.UnescapeString(m[2])
	}
	d.ProductVersion = meta["vnprox.productVersion"]
	d.ProfileID = meta["vnprox.profileId"]
	d.ProfileVersion = meta["vnprox.profileVersion"]
	var err error
	if d.GeneratedAt, err = parseUnixMeta(meta, "vnprox.generatedAt"); err != nil {
		return ComplianceDigest{}, err
	}
	if d.AsOf, err = parseUnixMeta(meta, "vnprox.asOf"); err != nil {
		return ComplianceDigest{}, err
	}
	d.NoticePresent = noticeRe.MatchString(doc)

	for _, m := range controlRowRe.FindAllStringSubmatch(doc, -1) {
		d.Controls = append(d.Controls, ComplianceControlDigest{
			ID:       html.UnescapeString(m[1]),
			Status:   compliance.Status(html.UnescapeString(m[2])),
			Evidence: splitEvidence(html.UnescapeString(m[3])),
			Failing:  splitEvidence(html.UnescapeString(m[4])),
		})
	}
	if len(d.Controls) == 0 {
		return ComplianceDigest{}, fmt.Errorf("docexport: parsing compliance HTML: no control rows found")
	}
	for _, m := range unmappedCheckRe.FindAllStringSubmatch(doc, -1) {
		d.UnmappedChecks = append(d.UnmappedChecks, html.UnescapeString(m[1]))
	}
	return d, nil
}

func parseUnixMeta(meta map[string]string, name string) (int64, error) {
	raw, ok := meta[name]
	if !ok {
		return 0, fmt.Errorf("docexport: parsing compliance HTML: no <meta name=%q>", name)
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("docexport: parsing compliance HTML's %q (%q): %w", name, raw, err)
	}
	return n, nil
}

// --- JSON -------------------------------------------------------------

// ComplianceJSON renders r as indented JSON — the machine-readable form, and
// the body GET /compliance/{profile} serves.
func ComplianceJSON(r compliance.Report) string {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		// compliance.Report is a closed struct of plain types; this is
		// unreachable. Reported rather than swallowed, and deliberately
		// not a partial document.
		return fmt.Sprintf("{\"error\":%q}\n", "docexport: marshaling compliance report: "+err.Error())
	}
	return string(b) + "\n"
}

// ParseComplianceJSON recovers the digest from a JSON report.
func ParseComplianceJSON(doc string) (ComplianceDigest, error) {
	var r compliance.Report
	dec := json.NewDecoder(strings.NewReader(doc))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return ComplianceDigest{}, fmt.Errorf("docexport: parsing compliance JSON: %w", err)
	}
	if len(r.Controls) == 0 {
		return ComplianceDigest{}, fmt.Errorf("docexport: parsing compliance JSON: document has no controls")
	}
	return ComplianceDigestOf(r), nil
}

// --- shared -----------------------------------------------------------

func summaryCount(s compliance.Summary, st compliance.Status) int {
	switch st {
	case compliance.StatusPass:
		return s.Pass
	case compliance.StatusFail:
		return s.Fail
	case compliance.StatusNotEvaluated:
		return s.NotEvaluated
	case compliance.StatusUnmapped:
		return s.Unmapped
	default:
		return 0
	}
}

func joinEvidence(keys []string) string {
	if len(keys) == 0 {
		return evidenceNone
	}
	return strings.Join(keys, evidenceSep)
}

func splitEvidence(cell string) []string {
	cell = strings.TrimSpace(cell)
	if cell == "" || cell == evidenceNone {
		return nil
	}
	parts := strings.Split(cell, strings.TrimSpace(evidenceSep))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func stampRFC3339(unix int64) string {
	return time.Unix(unix, 0).UTC().Format(time.RFC3339)
}

// asOfLabel renders the evidence scope. A live report says so in words
// rather than showing the epoch, which reads as 1970.
func asOfLabel(asOf int64) string {
	if asOf == 0 {
		return "live (current state)"
	}
	return stampRFC3339(asOf)
}

// oneLine collapses a profile's folded YAML text into a single line, so a
// table cell or a list item cannot be broken in half by a newline.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// complianceCell renders a Markdown cell ParseComplianceMarkdown must
// recover VERBATIM: version strings, profile ids, control ids and evidence
// "kind:name" tokens.
//
// Unlike mdCell it does not turn an empty value into "-" (that would
// round-trip an absent version as the literal "-"), and it does not escape
// "|" — a "\|" is indistinguishable from the cell delimiter on the way back
// in, so a stray pipe is replaced instead. Every vocabulary this is used for
// is closed and pipe-free; the replacement exists so a violation is visible
// rather than corrupting the row.
func complianceCell(s string) string {
	return strings.ReplaceAll(oneLine(s), "|", "/")
}
