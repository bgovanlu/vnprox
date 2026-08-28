// SPDX-License-Identifier: Apache-2.0

package docexport

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/compliance"
)

// compliance_test.go carries T-2706's acceptance criterion 2 ("a test
// asserts `unmapped` can never be rendered as passing in any output format")
// and criterion 4 (round-trip + version/profile-version/generation-time).
//
// Both are driven from ComplianceRenderers(), so a format added later is
// covered without touching this file — and
// TestComplianceRenderers_RegistryCoversEveryRenderer makes leaving a new
// renderer out of the registry a build-breaking omission rather than a
// silent coverage hole.

func sampleReport() compliance.Report {
	return compliance.Report{
		ProductVersion: "3.0.3",
		ProfileID:      "general-network-hygiene",
		ProfileTitle:   "General network hygiene",
		ProfileVersion: "1.0.0",
		Notice:         "This report is not a certification.",
		GeneratedAt:    1_700_000_000,
		Caveats:        []string{"one caveat"},
		CheckUniverse:  "this build's check catalog",
		Summary:        compliance.Summary{Pass: 1, Fail: 1, NotEvaluated: 1, Unmapped: 1, Total: 4},
		Controls: []compliance.ControlResult{
			{
				ID: "P1", Title: "Passing control", Statement: "It passes.", Stat: compliance.StatusPass,
				Evidence: []compliance.EvidenceResult{
					{Kind: compliance.EvidenceCheck, Name: "mgmt_single_path", Stat: compliance.EvidenceSatisfied, Detail: "no open finding", Note: "why it matters"},
					{Kind: compliance.EvidencePosture, Name: "segmentation", Stat: compliance.EvidenceSatisfied, Detail: "80/100"},
				},
			},
			{
				ID: "F1", Title: "Failing control", Statement: "It fails.", Stat: compliance.StatusFail,
				Evidence: []compliance.EvidenceResult{
					{Kind: compliance.EvidenceCheck, Name: "bond_slave_down", Stat: compliance.EvidenceUnsatisfied, Detail: "1 open finding", Refs: []string{"health:bond_slave_down|b"}},
				},
			},
			{
				ID: "N1", Title: "Unevaluated control", Statement: "It could not be evaluated.", Stat: compliance.StatusNotEvaluated,
				Evidence: []compliance.EvidenceResult{
					{Kind: compliance.EvidencePolicy, Name: "tag:change-control", Stat: compliance.EvidenceNotEvaluated, Detail: "no rule carries that tag"},
				},
			},
			{
				ID: "U1", Title: "Unmapped control", Statement: "vnprox observes none of this.",
				Stat: compliance.StatusUnmapped, UnmappedReason: "vnprox does not read Proxmox user configuration.",
			},
		},
		UnmappedChecks: []string{"trunk_unused_vlans", "wan_degraded"},
	}
}

// TestComplianceReport_RoundTripsThroughEveryParser is AC4: the rendered
// report round-trips through the parser and names the vnprox version, the
// profile version, and the generation time.
//
// BREAK IT TO SEE IT FIRE: drop the "| Profile version | … |" row from
// ComplianceMarkdown, or the vnprox.generatedAt <meta> from ComplianceHTML.
func TestComplianceReport_RoundTripsThroughEveryParser(t *testing.T) {
	report := sampleReport()
	want := ComplianceDigestOf(report)

	for _, r := range ComplianceRenderers() {
		t.Run(r.Format, func(t *testing.T) {
			out := r.Render(report)

			// The three facts the card names must be legible in the
			// artifact itself, not only recoverable by the parser. The
			// generation time counts in either of its two canonical
			// spellings: the human formats print RFC3339, the JSON form
			// prints the unix seconds its schema documents.
			for _, must := range []string{report.ProductVersion, report.ProfileVersion} {
				if !strings.Contains(out, must) {
					t.Errorf("rendered %s report does not contain %q", r.Format, must)
				}
			}
			rfc := time.Unix(report.GeneratedAt, 0).UTC().Format(time.RFC3339)
			unix := strconv.FormatInt(report.GeneratedAt, 10)
			if !strings.Contains(out, rfc) && !strings.Contains(out, unix) {
				t.Errorf("rendered %s report names the generation time neither as %q nor as %q", r.Format, rfc, unix)
			}

			got, err := r.Parse(out)
			if err != nil {
				t.Fatalf("parsing the %s report this package just rendered: %v", r.Format, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s round-trip lost information:\n got %+v\nwant %+v", r.Format, got, want)
			}
			if !got.NoticePresent {
				t.Errorf("the %s report dropped the profile's no-certification notice", r.Format)
			}
		})
	}
}

// TestComplianceReport_UnmappedNeverRendersAsPassing is AC2, driven over
// every registered format.
//
// BREAK IT TO SEE IT FIRE: make ComplianceMarkdown print
// compliance.StatusPass in the control table's status column.
func TestComplianceReport_UnmappedNeverRendersAsPassing(t *testing.T) {
	report := sampleReport()

	for _, r := range ComplianceRenderers() {
		t.Run(r.Format, func(t *testing.T) {
			digest, err := r.Parse(r.Render(report))
			if err != nil {
				t.Fatalf("parsing the %s report: %v", r.Format, err)
			}
			found := false
			for _, c := range digest.Controls {
				if c.ID != "U1" {
					continue
				}
				found = true
				if c.Status != compliance.StatusUnmapped {
					t.Errorf("%s rendered the unmapped control as %q", r.Format, c.Status)
				}
				if c.Status.IsPassing() {
					t.Errorf("%s rendered the unmapped control with a passing status (%q)", r.Format, c.Status)
				}
				if len(c.Evidence) != 0 {
					t.Errorf("%s attributed evidence %v to a control that has none", r.Format, c.Evidence)
				}
			}
			if !found {
				t.Errorf("%s dropped the unmapped control entirely; an omitted control reads as a control that does not apply", r.Format)
			}
		})
	}
}

// TestComplianceRenderers_TransmitTheStatusDistinction is the other half of
// AC2's "in any output format": a format could round-trip every status and
// still print an identical document, if its parser read a field the reader
// never sees. Flipping one control's status must change the bytes.
func TestComplianceRenderers_TransmitTheStatusDistinction(t *testing.T) {
	unmapped := sampleReport()
	passing := sampleReport()
	passing.Controls[3].Stat = compliance.StatusPass
	passing.Controls[3].UnmappedReason = ""

	for _, r := range ComplianceRenderers() {
		if r.Render(unmapped) == r.Render(passing) {
			t.Errorf("%s renders an unmapped control and a passing one identically", r.Format)
		}
	}
}

// TestComplianceMarkdown_UnmappedRowSaysUnmappedAndNotPass reads the
// artifact the way a person does: the control's own table row.
func TestComplianceMarkdown_UnmappedRowSaysUnmappedAndNotPass(t *testing.T) {
	out := ComplianceMarkdown(sampleReport())
	row := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "| U1 |") {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatal("no control-table row for U1")
	}
	if !strings.Contains(row, string(compliance.StatusUnmapped)) {
		t.Errorf("U1's row does not say %q: %q", compliance.StatusUnmapped, row)
	}
	if strings.Contains(row, "| "+string(compliance.StatusPass)+" |") {
		t.Errorf("U1's row carries a pass cell: %q", row)
	}
}

// TestComplianceHTML_VisibleStatusMatchesTheAttribute closes the gap a
// data-attribute parser opens: a format must not be able to show one status
// to a person and report another to a machine.
func TestComplianceHTML_VisibleStatusMatchesTheAttribute(t *testing.T) {
	out := ComplianceHTML(sampleReport())
	for _, m := range controlRowRe.FindAllStringSubmatchIndex(out, -1) {
		row := out[m[0]:]
		if end := strings.Index(row, "</tr>"); end >= 0 {
			row = row[:end]
		}
		status := out[m[4]:m[5]]
		if !strings.Contains(row, "<td>"+status+"</td>") {
			t.Errorf("row reports data-status=%q but its visible cells do not carry it: %q", status, row)
		}
	}
}

// TestComplianceHTML_IsSelfContained mirrors this package's existing
// CSP-style check: the export must reference nothing off-document.
func TestComplianceHTML_IsSelfContained(t *testing.T) {
	out := ComplianceHTML(sampleReport())
	for _, forbidden := range []string{"<script", "<link", "src=\"http", "href=\"http", "@import", "url("} {
		if strings.Contains(out, forbidden) {
			t.Errorf("the compliance HTML export contains %q; it must be standalone", forbidden)
		}
	}
}

func TestComplianceMarkdown_NamesEverySection(t *testing.T) {
	out := ComplianceMarkdown(sampleReport())
	htmlOut := ComplianceHTML(sampleReport())
	for _, heading := range []string{
		HeadingComplianceScope, HeadingComplianceCaveats, HeadingComplianceSummary,
		HeadingComplianceControls, HeadingComplianceDetail, HeadingComplianceUnmapped,
	} {
		if !strings.Contains(out, "## "+heading) {
			t.Errorf("Markdown export is missing section %q", heading)
		}
		if !strings.Contains(htmlOut, ">"+heading+"</h2>") {
			t.Errorf("HTML export is missing section %q", heading)
		}
	}
}

func TestComplianceReport_UnmappedChecksAreListedInEveryFormat(t *testing.T) {
	report := sampleReport()
	for _, r := range ComplianceRenderers() {
		digest, err := r.Parse(r.Render(report))
		if err != nil {
			t.Fatalf("parsing the %s report: %v", r.Format, err)
		}
		if !reflect.DeepEqual(digest.UnmappedChecks, report.UnmappedChecks) {
			t.Errorf("%s lost the unmapped-check list: got %v, want %v", r.Format, digest.UnmappedChecks, report.UnmappedChecks)
		}
	}
}

func TestComplianceRendererFor(t *testing.T) {
	for _, format := range ComplianceFormats() {
		if _, ok := ComplianceRendererFor(format); !ok {
			t.Errorf("ComplianceFormats() lists %q but ComplianceRendererFor does not resolve it", format)
		}
	}
	if _, ok := ComplianceRendererFor("pdf"); ok {
		t.Error("ComplianceRendererFor resolved a format that does not exist")
	}
}

// TestComplianceRenderers_RegistryCoversEveryRenderer is what makes "in any
// output format" hold for formats that do not exist yet: every exported
// `func …(compliance.Report) string` in this package must be registered, so
// the tests above drive it automatically.
//
// BREAK IT TO SEE IT FIRE: add
// `func ComplianceCSV(r compliance.Report) string { return "" }` to
// compliance.go without adding it to ComplianceRenderers().
func TestComplianceRenderers_RegistryCoversEveryRenderer(t *testing.T) {
	registered := map[string]bool{}
	for _, r := range ComplianceRenderers() {
		registered[r.FuncName] = true
		if r.Render == nil || r.Parse == nil {
			t.Errorf("renderer %q is registered without both a Render and a Parse", r.Format)
		}
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading this package's directory: %v", err)
	}
	fset := token.NewFileSet()
	found := 0
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, name, nil, 0)
		if parseErr != nil {
			t.Fatalf("parsing %s: %v", name, parseErr)
		}
		for _, decl := range file.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			if !isFunc || fn.Recv != nil || !fn.Name.IsExported() || !rendersComplianceReport(fn) {
				continue
			}
			found++
			if !registered[fn.Name.Name] {
				t.Errorf("%s renders a compliance.Report but is not in ComplianceRenderers(); "+
					"an unregistered format is not covered by the unmapped-never-passes assertion",
					fn.Name.Name)
			}
		}
	}
	if found != len(registered) {
		t.Errorf("the source scan found %d compliance renderers but %d are registered", found, len(registered))
	}
}

// rendersComplianceReport reports whether fn has the renderer signature
// `func(compliance.Report) string`.
func rendersComplianceReport(fn *ast.FuncDecl) bool {
	if fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
		return false
	}
	sel, isSel := fn.Type.Params.List[0].Type.(*ast.SelectorExpr)
	if !isSel || sel.Sel.Name != "Report" {
		return false
	}
	pkg, isIdent := sel.X.(*ast.Ident)
	if !isIdent || pkg.Name != "compliance" {
		return false
	}
	if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
		return false
	}
	res, isResIdent := fn.Type.Results.List[0].Type.(*ast.Ident)
	return isResIdent && res.Name == "string"
}
