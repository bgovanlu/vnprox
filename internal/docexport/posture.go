package docexport

import (
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/posture"
)

// posture.go extends T-605's config-documentation export machinery with a
// network-posture section (T-1607) — the management-legible progress artifact
// that turns the posture score into a trend line an operator can show someone
// else. It reuses this package's existing Markdown/HTML dual-format contract
// (the same standalone/CSP-safe HTML shell, the same heading-constant
// discipline) rather than introducing a parallel renderer, per the card.

// Posture section headings, exported so the golden test asserts each verbatim
// and both renderers name a section identically.
const (
	HeadingPosture        = "Network posture score"
	HeadingPostureFactors = "Contributing factors"
	HeadingPostureTrend   = "Score trend"
	postureDocTitle       = "vnprox network posture report"
)

// PostureTrendPoint is one historical score, oldest-to-newest, feeding the
// trend sparkline. Overall is the 0..100 score at ComputedAt.
type PostureTrendPoint struct {
	ComputedAt int64
	Overall    int
}

// PostureReport is the fully-assembled content of one posture export: the
// latest score with its named factors, plus the bounded history for the trend.
// Both renderers are pure functions of this value.
type PostureReport struct {
	Trend  []PostureTrendPoint
	Latest posture.Posture
}

// PostureMarkdown renders r as a standalone Markdown document. The factor
// table renders every factor's weight/value/score/contribution independently
// (the "never a single number with no factors" contract), and a not-evaluated
// or caveated factor is rendered as such — never silently omitted or shown as a
// clean 100.
func PostureMarkdown(r PostureReport) string {
	var b strings.Builder
	p := r.Latest

	fmt.Fprintf(&b, "# %s\n\n", postureDocTitle)
	fmt.Fprintf(&b, "_Generated: %s (%d)_\n\n", time.Unix(p.ComputedAt, 0).UTC().Format(time.RFC3339), p.ComputedAt)

	fmt.Fprintf(&b, "## %s\n\n", HeadingPosture)
	fmt.Fprintf(&b, "- **Overall: %d / 100**%s\n", p.Overall, qualifiedSuffixMD(p.Qualified))
	if p.Qualified {
		b.WriteString("- _This is a partial score: at least one dimension could not be fully evaluated (see caveats below). It must not be read as a clean bill of health._\n")
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "## %s\n\n", HeadingPostureFactors)
	b.WriteString("| Factor | Weight | Value | Score | Contribution | Evaluated | Notes |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	for _, f := range p.Factors {
		fmt.Fprintf(&b, "| %s | %d | %s | %s | %s | %s | %s |\n",
			f.Name, f.Weight, formatValue(f.Value), scoreCellMD(f), formatContribution(f),
			boolCell(f.Evaluated), mdCell(notesCell(f)))
	}
	b.WriteString("\n")

	writePostureTrendMD(&b, r.Trend)
	return b.String()
}

func writePostureTrendMD(b *strings.Builder, trend []PostureTrendPoint) {
	fmt.Fprintf(b, "## %s\n\n", HeadingPostureTrend)
	if len(trend) == 0 {
		fmt.Fprintf(b, "%s\n\n", noneObservedMarker)
		return
	}
	// A text sparkline plus the underlying points, so the Markdown form carries
	// the trend even without the HTML SVG.
	fmt.Fprintf(b, "`%s`\n\n", textSparkline(trend))
	b.WriteString("| When | Overall |\n|---|---|\n")
	for _, t := range trend {
		fmt.Fprintf(b, "| %s | %d |\n", time.Unix(t.ComputedAt, 0).UTC().Format(time.RFC3339), t.Overall)
	}
	b.WriteString("\n")
}

// PostureHTML renders r as a standalone HTML document — every byte inline (the
// same <style> block, no <script>/<link>/external src the config-doc export
// uses), the trend as an inline <svg> sparkline. The golden test greps for
// external references as a CSP-style check.
func PostureHTML(r PostureReport) string {
	var b strings.Builder
	p := r.Latest

	b.WriteString("<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\">\n")
	fmt.Fprintf(&b, "<title>%s</title>\n", html.EscapeString(postureDocTitle))
	b.WriteString(htmlStyle)
	b.WriteString("</head><body>\n")
	fmt.Fprintf(&b, "<h1>%s</h1>\n", html.EscapeString(postureDocTitle))
	fmt.Fprintf(&b, "<p class=\"generated\">Generated: %s (%d)</p>\n",
		html.EscapeString(time.Unix(p.ComputedAt, 0).UTC().Format(time.RFC3339)), p.ComputedAt)

	fmt.Fprintf(&b, "<h2>%s</h2>\n", html.EscapeString(HeadingPosture))
	fmt.Fprintf(&b, "<p><strong>Overall: %d / 100</strong>%s</p>\n", p.Overall, qualifiedSuffixHTML(p.Qualified))
	if p.Qualified {
		b.WriteString("<p class=\"none\">This is a partial score: at least one dimension could not be fully evaluated (see caveats below). It must not be read as a clean bill of health.</p>\n")
	}

	fmt.Fprintf(&b, "<h2>%s</h2>\n", html.EscapeString(HeadingPostureFactors))
	b.WriteString("<table><thead><tr><th>Factor</th><th>Weight</th><th>Value</th><th>Score</th><th>Contribution</th><th>Evaluated</th><th>Notes</th></tr></thead><tbody>\n")
	for _, f := range p.Factors {
		fmt.Fprintf(&b, "<tr><td>%s</td><td>%d</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>\n",
			html.EscapeString(f.Name), f.Weight, html.EscapeString(formatValue(f.Value)),
			html.EscapeString(scoreCellMD(f)), html.EscapeString(formatContribution(f)),
			boolCell(f.Evaluated), htmlCell(notesCell(f)))
	}
	b.WriteString("</tbody></table>\n")

	writePostureTrendHTML(&b, r.Trend)

	b.WriteString("</body></html>\n")
	return b.String()
}

func writePostureTrendHTML(b *strings.Builder, trend []PostureTrendPoint) {
	fmt.Fprintf(b, "<h2>%s</h2>\n", html.EscapeString(HeadingPostureTrend))
	if len(trend) == 0 {
		writeNoneHTML(b)
		return
	}
	b.WriteString("<div class=\"topology-svg\">\n")
	b.WriteString(postureSparklineSVG(trend))
	b.WriteString("\n</div>\n")
}

// postureSparklineSVG renders the trend as an inline SVG polyline over a fixed
// 0..100 y-axis, so successive scores are directly comparable. Fully
// self-contained (no external refs), matching the export's standalone contract.
func postureSparklineSVG(trend []PostureTrendPoint) string {
	const w, h, pad = 480, 80, 6
	if len(trend) == 1 {
		// A single point: render it as a dot rather than a degenerate line.
		y := sparkY(trend[0].Overall, h, pad)
		return fmt.Sprintf(
			`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" class="sparkline" width="%d" height="%d">`+
				`<circle cx="%d" cy="%d" r="3" fill="#0f172a"></circle></svg>`,
			w, h, w, h, w/2, y)
	}
	var pts strings.Builder
	n := len(trend)
	for i, t := range trend {
		x := pad + (w-2*pad)*i/(n-1)
		y := sparkY(t.Overall, h, pad)
		if i > 0 {
			pts.WriteByte(' ')
		}
		fmt.Fprintf(&pts, "%d,%d", x, y)
	}
	return fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" class="sparkline" width="%d" height="%d">`+
			`<polyline fill="none" stroke="#0f172a" stroke-width="2" points="%s"></polyline></svg>`,
		w, h, w, h, pts.String())
}

// sparkY maps a 0..100 score onto the SVG y-axis (0 at bottom, 100 at top).
func sparkY(score, h, pad int) int {
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return pad + (h-2*pad)*(100-score)/100
}

// textSparkline renders the trend as a run of block characters, one per point,
// for the Markdown form.
func textSparkline(trend []PostureTrendPoint) string {
	blocks := []rune("▁▂▃▄▅▆▇█")
	var b strings.Builder
	for _, t := range trend {
		s := t.Overall
		if s < 0 {
			s = 0
		}
		if s > 100 {
			s = 100
		}
		idx := s * (len(blocks) - 1) / 100
		b.WriteRune(blocks[idx])
	}
	return b.String()
}

// scoreCellMD renders a factor's sub-score, showing "n/a" for a not-evaluated
// factor (ScorePct == NotEvaluatedScore) rather than a misleading number.
func scoreCellMD(f posture.Factor) string {
	if !f.Evaluated || f.ScorePct == posture.NotEvaluatedScore {
		return "n/a"
	}
	return strconv.Itoa(f.ScorePct) + "/100"
}

// notesCell surfaces a factor's caveat (the honesty channel) if present, else
// its detail.
func notesCell(f posture.Factor) string {
	if f.Caveat != "" {
		return f.Caveat
	}
	return f.Detail
}

func formatValue(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func formatContribution(f posture.Factor) string {
	if !f.Evaluated {
		return "0"
	}
	return strconv.FormatFloat(f.Contribution, 'f', 1, 64)
}

func qualifiedSuffixMD(qualified bool) string {
	if qualified {
		return " _(partial / qualified)_"
	}
	return ""
}

func qualifiedSuffixHTML(qualified bool) string {
	if qualified {
		return ` <span class="none">(partial / qualified)</span>`
	}
	return ""
}
