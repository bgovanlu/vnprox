package docexport

import (
	"fmt"
	"html"
	"strconv"
	"strings"

	"github.com/bgovanlu/vnprox/internal/posture"
)

// digest.go renders T-2807's scheduled digest. Like posture.go (T-1607) and
// compliance.go (T-2706) before it, it extends this package's existing
// dual-format machinery with one more artifact rather than introducing a
// parallel renderer: the same Markdown table conventions, the same
// standalone/CSP-safe HTML shell, the same heading constants a golden test
// asserts verbatim, and the same cell helpers. A digest is therefore rendered
// by code that is already under test, which is the card's stated reason for
// putting it here.
//
// THE LOAD-BEARING BEHAVIOUR IS THE QUIET FORM. A digest with nothing to
// report is ONE LINE, in both formats, and it does not render a single empty
// section. That is not a formatting nicety: a digest that sends a full
// document every week regardless of whether anything happened is a digest
// people learn to ignore, at which point the one week it matters is the week
// nobody reads it. `DigestReport.Quiet` decides, and both renderers branch on
// it before writing anything else.

// Digest section headings, exported so the golden test asserts each verbatim
// and both renderers name a section identically.
const (
	HeadingDigestPosture  = "Posture"
	HeadingDigestCapacity = "Capacity projections crossing the horizon"
	HeadingDigestDrift    = "Unresolved drift"
	HeadingDigestOpened   = "Findings opened in this period"
	HeadingDigestClosed   = "Findings closed in this period"
	digestDocTitle        = "vnprox digest"
)

// DigestQuietMaxBytes is the stated size bound on a quiet digest, in bytes,
// for the Markdown form — the form that is actually delivered (it is the body
// of the alert payload). It is a CONTRACT, not an observation: a change that
// makes a quiet digest larger than this has made the digest chatty, and
// TestDigestMarkdown_QuietPeriodIsOneLineUnderTheBound fails.
//
// A full digest is an order of magnitude larger, which is what makes the
// bound discriminating rather than decorative.
const DigestQuietMaxBytes = 200

// DigestQuietHTMLMaxBytes is the same bound for the HTML form. It is larger
// only because the standalone HTML shell (doctype, head, the inline <style>
// this package's CSP-safe contract requires) is a fixed cost every HTML
// artifact in this package pays; the DIGEST content inside it is still the
// single line, which the golden test asserts directly by requiring no <h2>
// and no <table>.
const DigestQuietHTMLMaxBytes = 1400

// DigestItem is one reported row: a finding, a drift entry, or a capacity
// projection. All four content sections share the shape because all four come
// from the same unified findings stream, and giving each its own type would
// mean four renderers that can disagree.
type DigestItem struct {
	ID       string
	Check    string
	Severity string
	Detail   string
	Nodes    []string
}

// DigestPosture is the posture half of a digest: the score with its named
// factors, and the delta since the PREVIOUS DIGEST.
//
// Scored and PreviousScored are separate booleans on purpose. "There is no
// score yet", "there is a score but no previous digest to compare it to" and
// "there is a score and the last digest carried one" are three different
// statements, and collapsing any two of them is how a first-ever digest ends
// up reporting a delta against zero — the exact failure T-2807 AC2 names.
type DigestPosture struct {
	Factors []posture.Factor
	// Overall is the current 0..100 score; meaningful only when Scored.
	Overall int
	// Previous is the score the previous digest carried; meaningful only
	// when PreviousScored.
	Previous       int
	Qualified      bool
	Scored         bool
	PreviousScored bool
}

// HasDelta reports whether a delta can honestly be shown: both this digest
// and the previous one carried a score.
func (p DigestPosture) HasDelta() bool { return p.Scored && p.PreviousScored }

// Delta is the change in score since the previous digest. Callers must check
// HasDelta first; it returns 0 otherwise, and 0 is also a legitimate delta,
// which is precisely why the check is not optional.
func (p DigestPosture) Delta() int {
	if !p.HasDelta() {
		return 0
	}
	return p.Overall - p.Previous
}

// DigestReport is the fully-assembled, render-format-agnostic content of one
// scheduled digest. Both renderers are pure functions of this value.
//
// HasBaseline records whether a PREVIOUS DIGEST exists — not whether some
// arbitrary earlier window does. PeriodStart is that previous digest's own
// end, so the window a digest reports on abuts the last one exactly and
// nothing falls between two digests unreported.
type DigestReport struct {
	Capacity    []DigestItem
	Drift       []DigestItem
	Opened      []DigestItem
	Closed      []DigestItem
	Posture     DigestPosture
	PeriodStart int64
	PeriodEnd   int64
	GeneratedAt int64
	// BaselineAt is the previous digest's PeriodEnd, 0 when HasBaseline is
	// false.
	BaselineAt  int64
	HasBaseline bool
}

// Quiet reports whether this digest has nothing to say: no capacity
// projection crossing the horizon, no unresolved drift, nothing opened,
// nothing closed, and no movement in the posture score.
//
// A posture score that MOVED is content even when nothing else did — that is
// the whole point of tracking it — so a non-zero delta makes a digest
// non-quiet. A digest with no baseline cannot have a delta and so does not
// become chatty merely by being the first one.
func (r DigestReport) Quiet() bool {
	return len(r.Capacity) == 0 && len(r.Drift) == 0 &&
		len(r.Opened) == 0 && len(r.Closed) == 0 &&
		r.Posture.Delta() == 0
}

// quietLine is the one line a quiet digest is, shared by both renderers so
// the two formats cannot say different things about the same quiet period.
func quietLine(r DigestReport) string {
	return fmt.Sprintf("%s %s -> %s: nothing to report. %s",
		digestDocTitle, stampRFC3339(r.PeriodStart), stampRFC3339(r.PeriodEnd), postureSentence(r.Posture))
}

// postureSentence states the score and what may honestly be said about its
// movement. Each of the three cases is a different sentence rather than a
// formatted 0, because "unchanged" and "not comparable" are different facts.
func postureSentence(p DigestPosture) string {
	switch {
	case !p.Scored:
		return "No posture score yet; no comparison possible."
	case !p.PreviousScored:
		return fmt.Sprintf("Posture %d/100; no previous digest to compare against.", p.Overall)
	case p.Delta() == 0:
		return fmt.Sprintf("Posture %d/100, unchanged since the last digest.", p.Overall)
	default:
		return fmt.Sprintf("Posture %d/100 (%s since the last digest).", p.Overall, signedDelta(p.Delta()))
	}
}

// signedDelta renders a delta with an explicit sign, so "+4" and "-4" are
// distinguishable at a glance and "4" is never ambiguous.
func signedDelta(d int) string {
	if d > 0 {
		return "+" + strconv.Itoa(d)
	}
	return strconv.Itoa(d)
}

// DigestMarkdown renders r as a standalone Markdown document — and, for a
// quiet period, as a single line with no sections at all.
func DigestMarkdown(r DigestReport) string {
	if r.Quiet() {
		return quietLine(r) + "\n"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", digestDocTitle)
	fmt.Fprintf(&b, "_Period: %s -> %s; generated %s_\n\n",
		stampRFC3339(r.PeriodStart), stampRFC3339(r.PeriodEnd), stampRFC3339(r.GeneratedAt))
	if !r.HasBaseline {
		fmt.Fprintf(&b, "%s\n\n", digestNoBaselineNote)
	}

	fmt.Fprintf(&b, "## %s\n\n", HeadingDigestPosture)
	fmt.Fprintf(&b, "- %s\n", postureSentence(r.Posture))
	if r.Posture.Scored && r.Posture.Qualified {
		b.WriteString("- _This is a partial score: at least one dimension could not be fully evaluated. It must not be read as a clean bill of health._\n")
	}
	b.WriteString("\n")
	writeDigestFactorsMD(&b, r.Posture)

	writeDigestItemsMD(&b, HeadingDigestCapacity, r.Capacity)
	writeDigestItemsMD(&b, HeadingDigestDrift, r.Drift)
	writeDigestItemsMD(&b, HeadingDigestOpened, r.Opened)
	writeDigestItemsMD(&b, HeadingDigestClosed, r.Closed)
	return b.String()
}

// digestNoBaselineNote is the first-ever digest's own statement, in both
// formats. It exists so a reader is told why no delta is shown, rather than
// being left to infer it from a missing number.
const digestNoBaselineNote = "_This is the first digest for this schedule: there is no previous digest to compare against, so no deltas are shown._"

func writeDigestFactorsMD(b *strings.Builder, p DigestPosture) {
	if !p.Scored || len(p.Factors) == 0 {
		return
	}
	b.WriteString("| Factor | Weight | Value | Score | Contribution | Evaluated | Notes |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	for _, f := range p.Factors {
		fmt.Fprintf(b, "| %s | %d | %s | %s | %s | %s | %s |\n",
			f.Name, f.Weight, formatValue(f.Value), scoreCellMD(f), formatContribution(f),
			boolCell(f.Evaluated), mdCell(notesCell(f)))
	}
	b.WriteString("\n")
}

func writeDigestItemsMD(b *strings.Builder, heading string, items []DigestItem) {
	fmt.Fprintf(b, "## %s\n\n", heading)
	if len(items) == 0 {
		fmt.Fprintf(b, "%s\n\n", noneObservedMarker)
		return
	}
	b.WriteString("| Check | Severity | Detail | Nodes |\n|---|---|---|---|\n")
	for _, it := range items {
		fmt.Fprintf(b, "| %s | %s | %s | %s |\n",
			mdCell(it.Check), mdCell(it.Severity), mdCell(oneLine(it.Detail)), mdCell(strings.Join(it.Nodes, ", ")))
	}
	b.WriteString("\n")
}

// DigestHTML renders r as a standalone HTML document — every byte inline, no
// external reference, matching this package's CSP-friendly contract. A quiet
// period renders the same single line as the Markdown form, inside the shell
// and with no section heading and no table.
func DigestHTML(r DigestReport) string {
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\">\n")
	fmt.Fprintf(&b, "<title>%s</title>\n", html.EscapeString(digestDocTitle))
	b.WriteString(htmlStyle)
	b.WriteString("</head><body>\n")

	if r.Quiet() {
		fmt.Fprintf(&b, "<p class=\"none\" data-digest-quiet=\"1\">%s</p>\n", html.EscapeString(quietLine(r)))
		b.WriteString("</body></html>\n")
		return b.String()
	}

	fmt.Fprintf(&b, "<h1>%s</h1>\n", html.EscapeString(digestDocTitle))
	fmt.Fprintf(&b, "<p class=\"generated\">Period: %s -> %s; generated %s</p>\n",
		html.EscapeString(stampRFC3339(r.PeriodStart)), html.EscapeString(stampRFC3339(r.PeriodEnd)),
		html.EscapeString(stampRFC3339(r.GeneratedAt)))
	if !r.HasBaseline {
		fmt.Fprintf(&b, "<p class=\"none\" data-digest-no-baseline=\"1\">%s</p>\n",
			html.EscapeString(oneLine(strings.Trim(digestNoBaselineNote, "_"))))
	}

	fmt.Fprintf(&b, "<h2>%s</h2>\n", html.EscapeString(HeadingDigestPosture))
	fmt.Fprintf(&b, "<p>%s</p>\n", html.EscapeString(postureSentence(r.Posture)))
	if r.Posture.Scored && r.Posture.Qualified {
		b.WriteString("<p class=\"none\">This is a partial score: at least one dimension could not be fully evaluated. It must not be read as a clean bill of health.</p>\n")
	}
	writeDigestFactorsHTML(&b, r.Posture)

	writeDigestItemsHTML(&b, HeadingDigestCapacity, r.Capacity)
	writeDigestItemsHTML(&b, HeadingDigestDrift, r.Drift)
	writeDigestItemsHTML(&b, HeadingDigestOpened, r.Opened)
	writeDigestItemsHTML(&b, HeadingDigestClosed, r.Closed)

	b.WriteString("</body></html>\n")
	return b.String()
}

func writeDigestFactorsHTML(b *strings.Builder, p DigestPosture) {
	if !p.Scored || len(p.Factors) == 0 {
		return
	}
	b.WriteString("<table><thead><tr><th>Factor</th><th>Weight</th><th>Value</th><th>Score</th>" +
		"<th>Contribution</th><th>Evaluated</th><th>Notes</th></tr></thead><tbody>\n")
	for _, f := range p.Factors {
		fmt.Fprintf(b, "<tr><td>%s</td><td>%d</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>\n",
			html.EscapeString(f.Name), f.Weight, html.EscapeString(formatValue(f.Value)),
			html.EscapeString(scoreCellMD(f)), html.EscapeString(formatContribution(f)),
			boolCell(f.Evaluated), htmlCell(notesCell(f)))
	}
	b.WriteString("</tbody></table>\n")
}

func writeDigestItemsHTML(b *strings.Builder, heading string, items []DigestItem) {
	fmt.Fprintf(b, "<h2>%s</h2>\n", html.EscapeString(heading))
	if len(items) == 0 {
		writeNoneHTML(b)
		return
	}
	b.WriteString("<table><thead><tr><th>Check</th><th>Severity</th><th>Detail</th><th>Nodes</th></tr></thead><tbody>\n")
	for _, it := range items {
		fmt.Fprintf(b, "<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>\n",
			htmlCell(it.Check), htmlCell(it.Severity), htmlCell(oneLine(it.Detail)),
			htmlCell(strings.Join(it.Nodes, ", ")))
	}
	b.WriteString("</tbody></table>\n")
}

// DigestPeriodLabel renders a digest's window for a log line or an alert
// title. Exported because the delivery side names the period too, and two
// spellings of the same window is how a reader ends up unsure whether they
// are looking at one digest or two.
func DigestPeriodLabel(r DigestReport) string {
	return stampRFC3339(r.PeriodStart) + " -> " + stampRFC3339(r.PeriodEnd)
}
