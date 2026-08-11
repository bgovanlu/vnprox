package docexport

import (
	"html"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/posture"
)

// digest_test.go covers T-2807's rendering half. The criterion with the most
// product weight is AC1 — a digest with nothing to report is ONE LINE and
// under a stated size — so that is asserted first, in both directions: the
// quiet form is bounded, and the full form is an order of magnitude past the
// bound, which is what makes the bound discriminating rather than a number
// that would pass no matter what the renderer did.

// digestFactors is a representative factor set, so a full digest carries the
// posture score's NAMED FACTORS (the card's wording) rather than a bare
// number.
func digestFactors() []posture.Factor {
	return []posture.Factor{
		{
			Name: posture.FactorSPOF, Detail: "2 single points of failure",
			Value: 2, ScorePct: 60, Weight: 30, Contribution: 20.7, Evaluated: true,
		},
		{
			Name: posture.FactorSegmentation, Detail: "3 of 4 segments isolated",
			Value: 0.75, ScorePct: 75, Weight: 25, Contribution: 21.6, Evaluated: true,
		},
		{
			Name: posture.FactorAnomalyRate, Caveat: "no baseline learned yet",
			Value: 0, ScorePct: posture.NotEvaluatedScore, Weight: 15, Evaluated: false,
		},
	}
}

func quietReport() DigestReport {
	return DigestReport{
		PeriodStart: 1750000000,
		PeriodEnd:   1750604800,
		GeneratedAt: 1750604800,
		BaselineAt:  1750000000,
		HasBaseline: true,
		Posture: DigestPosture{
			Overall: 82, Previous: 82, Scored: true, PreviousScored: true,
			Factors: digestFactors(),
		},
	}
}

func busyReport() DigestReport {
	r := quietReport()
	r.Posture.Overall = 86
	r.Capacity = []DigestItem{{
		ID: "capacity:capacity_link_forecast|iface:pve1:vmbr1", Check: "capacity_link_forecast",
		Severity: "warning", Detail: "vmbr1 projected to reach 100% in ~34 days", Nodes: []string{"pve1"},
	}}
	r.Drift = []DigestItem{{
		ID: "drift:bridge|pve2:vmbr0", Check: "bridge", Severity: "warning",
		Detail: "vmbr0 has an unapplied pending change", Nodes: []string{"pve2"},
	}}
	r.Opened = []DigestItem{{
		ID: "health:carrier_down|iface:pve1:eno2", Check: "carrier_down",
		Severity: "error", Detail: "eno2 has no carrier", Nodes: []string{"pve1"},
	}}
	r.Closed = []DigestItem{{
		ID: "health:mtu_mismatch|iface:pve3:eno1", Check: "mtu_mismatch",
		Detail: resolvedDetailForTest, Nodes: nil,
	}}
	return r
}

// resolvedDetailForTest mirrors internal/digest's own wording for a finding
// that has left the stream. Duplicated as a literal rather than imported so
// this package does not depend on its own caller.
const resolvedDetailForTest = "no longer in the findings stream"

func TestDigestMarkdown_QuietPeriodIsOneLineUnderTheBound(t *testing.T) {
	r := quietReport()
	if !r.Quiet() {
		t.Fatal("the quiet fixture is not Quiet(); the fixture is wrong, not the renderer")
	}

	doc := DigestMarkdown(r)

	if n := len(doc); n > DigestQuietMaxBytes {
		t.Errorf("a quiet digest rendered %d bytes, over the stated bound of %d:\n%s",
			n, DigestQuietMaxBytes, doc)
	}
	if lines := strings.Count(strings.TrimRight(doc, "\n"), "\n"); lines != 0 {
		t.Errorf("a quiet digest rendered %d newlines, want a single line:\n%s", lines+1, doc)
	}
	if !strings.Contains(doc, "nothing to report") {
		t.Errorf("a quiet digest does not say it has nothing to report:\n%s", doc)
	}

	// It must not MANUFACTURE content: no section heading, no table, no
	// "none observed" filler row standing in for a section that was empty.
	forbidden := []string{noneObservedMarker, "|", "# " + digestDocTitle}
	for _, heading := range []string{
		HeadingDigestPosture, HeadingDigestCapacity, HeadingDigestDrift,
		HeadingDigestOpened, HeadingDigestClosed,
	} {
		forbidden = append(forbidden, "## "+heading)
	}
	for _, f := range forbidden {
		if strings.Contains(doc, f) {
			t.Errorf("a quiet digest contains %q; it is manufacturing content:\n%s", f, doc)
		}
	}
}

// TestDigestMarkdown_FullDigestIsFarPastTheQuietBound is the other half of
// AC1's bound: a bound nothing can exceed proves nothing.
func TestDigestMarkdown_FullDigestIsFarPastTheQuietBound(t *testing.T) {
	r := busyReport()
	if r.Quiet() {
		t.Fatal("the busy fixture reports Quiet(); the fixture is wrong")
	}
	doc := DigestMarkdown(r)
	if len(doc) <= DigestQuietMaxBytes {
		t.Fatalf("a full digest rendered %d bytes, within the quiet bound of %d — "+
			"the bound cannot discriminate between a quiet digest and a busy one", len(doc), DigestQuietMaxBytes)
	}
	if len(doc) < 1000 {
		t.Errorf("a full digest rendered only %d bytes; expected a substantial document:\n%s", len(doc), doc)
	}
	for _, heading := range []string{
		HeadingDigestPosture, HeadingDigestCapacity, HeadingDigestDrift,
		HeadingDigestOpened, HeadingDigestClosed,
	} {
		if !strings.Contains(doc, "## "+heading) {
			t.Errorf("full digest is missing section %q", heading)
		}
	}
	// The posture score's NAMED FACTORS, not just the number.
	for _, f := range digestFactors() {
		if !strings.Contains(doc, f.Name) {
			t.Errorf("full digest does not name posture factor %q", f.Name)
		}
	}
}

func TestDigestReport_QuietIsDecidedByContentAndScoreMovement(t *testing.T) {
	tests := []struct {
		mutet func(*DigestReport)
		name  string
		quiet bool
	}{
		{name: "nothing at all", mutet: func(*DigestReport) {}, quiet: true},
		{name: "a capacity projection", mutet: func(r *DigestReport) { r.Capacity = []DigestItem{{Check: "c"}} }},
		{name: "unresolved drift", mutet: func(r *DigestReport) { r.Drift = []DigestItem{{Check: "d"}} }},
		{name: "something opened", mutet: func(r *DigestReport) { r.Opened = []DigestItem{{Check: "o"}} }},
		{name: "something closed", mutet: func(r *DigestReport) { r.Closed = []DigestItem{{Check: "c"}} }},
		{name: "the score moved", mutet: func(r *DigestReport) { r.Posture.Overall = 90 }},
		{
			name: "the score moved but there is no baseline to move from",
			mutet: func(r *DigestReport) {
				r.Posture.Overall = 90
				r.Posture.PreviousScored = false
				r.HasBaseline = false
			},
			quiet: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := quietReport()
			tc.mutet(&r)
			if got := r.Quiet(); got != tc.quiet {
				t.Errorf("Quiet() = %v, want %v", got, tc.quiet)
			}
		})
	}
}

// TestDigestMarkdown_FirstEverDigestStatesItHasNoBaseline is AC2's
// silently-regressing direction: with no previous digest, the report must SAY
// so and must show no delta — not a delta against zero.
func TestDigestMarkdown_FirstEverDigestStatesItHasNoBaseline(t *testing.T) {
	r := busyReport()
	r.HasBaseline = false
	r.BaselineAt = 0
	r.Posture.PreviousScored = false
	r.Posture.Previous = 0

	if r.Posture.HasDelta() {
		t.Fatal("HasDelta() is true with no previous score; a delta against nothing is exactly what AC2 forbids")
	}

	doc := DigestMarkdown(r)
	if !strings.Contains(doc, "no previous digest to compare against") {
		t.Errorf("a first-ever digest does not state that it has no baseline:\n%s", doc)
	}
	if !strings.Contains(doc, "first digest for this schedule") {
		t.Errorf("a first-ever digest does not carry the no-baseline note:\n%s", doc)
	}
	// The score is 86 and a naive delta against a zero baseline would render
	// "+86 since the last digest". Neither may appear.
	for _, spurious := range []string{"+86", "since the last digest"} {
		if strings.Contains(doc, spurious) {
			t.Errorf("a first-ever digest renders %q — a delta against no baseline:\n%s", spurious, doc)
		}
	}

	// And the quiet form of the same case says it in its one line.
	quiet := quietReport()
	quiet.HasBaseline = false
	quiet.Posture.PreviousScored = false
	quietDoc := DigestMarkdown(quiet)
	if !strings.Contains(quietDoc, "no previous digest to compare against") {
		t.Errorf("a first-ever QUIET digest does not state that it has no baseline: %q", quietDoc)
	}
	if n := len(quietDoc); n > DigestQuietMaxBytes {
		t.Errorf("a first-ever quiet digest rendered %d bytes, over the bound of %d: %q",
			n, DigestQuietMaxBytes, quietDoc)
	}
}

// TestDigestMarkdown_DeltaAgainstThePreviousDigestIsSigned is AC2's other
// direction: with a baseline, the delta is shown, signed, and relative to the
// previous digest.
func TestDigestMarkdown_DeltaAgainstThePreviousDigestIsSigned(t *testing.T) {
	tests := []struct {
		name     string
		want     string
		overall  int
		previous int
	}{
		{name: "improved", overall: 86, previous: 82, want: "Posture 86/100 (+4 since the last digest)."},
		{name: "regressed", overall: 78, previous: 82, want: "Posture 78/100 (-4 since the last digest)."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := busyReport()
			r.Posture.Overall = tc.overall
			r.Posture.Previous = tc.previous

			if got := r.Posture.Delta(); got != tc.overall-tc.previous {
				t.Fatalf("Delta() = %d, want %d", got, tc.overall-tc.previous)
			}
			doc := DigestMarkdown(r)
			if !strings.Contains(doc, tc.want) {
				t.Errorf("digest does not carry %q:\n%s", tc.want, doc)
			}
		})
	}
}

func TestDigestMarkdown_NoPostureScoreIsSaidRatherThanShownAsZero(t *testing.T) {
	r := busyReport()
	r.Posture.Scored = false
	r.Posture.Factors = nil

	doc := DigestMarkdown(r)
	if !strings.Contains(doc, "No posture score yet") {
		t.Errorf("a digest with no posture score does not say so:\n%s", doc)
	}
	if strings.Contains(doc, "Posture 0/100") {
		t.Errorf("a digest with no posture score rendered it as zero:\n%s", doc)
	}
}

func TestDigestHTML_QuietPeriodCarriesNoSections(t *testing.T) {
	doc := DigestHTML(quietReport())

	if n := len(doc); n > DigestQuietHTMLMaxBytes {
		t.Errorf("a quiet HTML digest rendered %d bytes, over the stated bound of %d", n, DigestQuietHTMLMaxBytes)
	}
	if strings.Contains(doc, "<h2>") || strings.Contains(doc, "<table>") {
		t.Errorf("a quiet HTML digest carries a section heading or a table:\n%s", doc)
	}
	if !strings.Contains(doc, "nothing to report") {
		t.Errorf("a quiet HTML digest does not say it has nothing to report:\n%s", doc)
	}
	if !strings.Contains(doc, `data-digest-quiet="1"`) {
		t.Errorf("a quiet HTML digest is not machine-identifiable as quiet:\n%s", doc)
	}

	// Both formats must say the SAME thing about the same quiet period —
	// modulo HTML escaping, which is why the comparison escapes rather than
	// loosens.
	line := html.EscapeString(strings.TrimSpace(DigestMarkdown(quietReport())))
	if !strings.Contains(doc, line) {
		t.Errorf("the HTML quiet form does not carry the Markdown quiet line %q", line)
	}
}

func TestDigestHTML_FullDigestNamesEverySectionAndIsSelfContained(t *testing.T) {
	doc := DigestHTML(busyReport())
	if len(doc) <= DigestQuietHTMLMaxBytes {
		t.Fatalf("a full HTML digest rendered %d bytes, within the quiet bound of %d — "+
			"the bound cannot discriminate", len(doc), DigestQuietHTMLMaxBytes)
	}
	for _, heading := range []string{
		HeadingDigestPosture, HeadingDigestCapacity, HeadingDigestDrift,
		HeadingDigestOpened, HeadingDigestClosed,
	} {
		if !strings.Contains(doc, "<h2>"+heading+"</h2>") {
			t.Errorf("full HTML digest is missing section %q", heading)
		}
	}
	// The same CSP-style check compliance.go's own golden test makes: an
	// export that reaches out to the network is not a standalone artifact.
	for _, external := range []string{"<script", "<link", "src=", "http://", "https://cdn"} {
		if strings.Contains(doc, external) {
			t.Errorf("HTML digest contains an external reference %q; it must be self-contained", external)
		}
	}
}

func TestDigestPeriodLabel_NamesTheWindowOnce(t *testing.T) {
	got := DigestPeriodLabel(quietReport())
	want := "2025-06-15T15:06:40Z -> 2025-06-22T15:06:40Z"
	if got != want {
		t.Errorf("DigestPeriodLabel = %q, want %q", got, want)
	}
}
