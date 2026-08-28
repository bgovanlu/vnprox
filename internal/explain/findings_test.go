// SPDX-License-Identifier: Apache-2.0

package explain

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/runbook"
)

// --- the coverage gate ------------------------------------------------------

// TestCoverage_AllCatalogChecksHaveExplainerOrExemption is the real gate:
// every check name findings.AllCheckNames() can emit today must resolve to
// either a findingRegistry template or a documented exemptions entry. This
// is the test that fails when a check ships with neither — see
// TestCoverage_MissingTemplateFailsGate for proof the mechanism actually
// catches that.
func TestCoverage_AllCatalogChecksHaveExplainerOrExemption(t *testing.T) {
	missing := missingExplainers(findings.AllCheckNames(), findingRegistry, exemptions)
	for _, m := range missing {
		t.Error(m)
	}
}

// TestCoverage_RegistryHasNoStaleOrDuplicateEntries is the reverse
// direction: findingRegistry must not carry an entry for a check name that
// findings.AllCheckNames() no longer lists (a check renamed or removed
// without its template following), and exemptions must not name a check
// that also has a template (an exemption that can never fire, which would
// hide a real gap if the template were later deleted).
func TestCoverage_RegistryHasNoStaleOrDuplicateEntries(t *testing.T) {
	known := map[string]bool{}
	for _, c := range findings.AllCheckNames() {
		known[c] = true
	}
	for c := range findingRegistry {
		if !known[c] {
			t.Errorf("findingRegistry has a template for %q, which findings.AllCheckNames() no longer lists", c)
		}
		if _, exempt := exemptions[c]; exempt {
			t.Errorf("%q is both templated and exempted; drop the exemption", c)
		}
	}
	for c := range exemptions {
		if !known[c] {
			t.Errorf("exemptions names %q, which findings.AllCheckNames() no longer lists", c)
		}
	}
}

// TestCoverage_MissingTemplateFailsGate is the task card's explicit "a
// check with no template must fail the gate" case, proven against the
// missingExplainers mechanism directly (rather than by mutating the real,
// package-level findingRegistry, which every other test in this package
// also relies on being complete) — a synthetic check name that is neither
// registered nor exempted must be reported.
func TestCoverage_MissingTemplateFailsGate(t *testing.T) {
	const madeUpCheck = "totally_unregistered_check_name"
	missing := missingExplainers([]string{madeUpCheck}, findingRegistry, exemptions)
	if len(missing) != 1 {
		t.Fatalf("missingExplainers(%q) = %v, want exactly one report", madeUpCheck, missing)
	}
	if !strings.Contains(missing[0], madeUpCheck) {
		t.Errorf("missingExplainers report %q does not name the offending check", missing[0])
	}

	// Control: a check that IS registered must NOT be reported missing.
	for c := range findingRegistry {
		if got := missingExplainers([]string{c}, findingRegistry, exemptions); len(got) != 0 {
			t.Errorf("missingExplainers([%q]) = %v, want none — %q is registered", c, got, c)
		}
		break // one is enough to prove the control leg; the full sweep is TestCoverage_AllCatalogChecksHaveExplainerOrExemption's job
	}

	// An exemption with a non-empty reason must not be reported missing;
	// one with an empty reason must be.
	reasoned := map[string]string{madeUpCheck: "test fixture: documented reason"}
	if got := missingExplainers([]string{madeUpCheck}, findingRegistry, reasoned); len(got) != 0 {
		t.Errorf("missingExplainers with a reasoned exemption = %v, want none", got)
	}
	unreasoned := map[string]string{madeUpCheck: "  "}
	if got := missingExplainers([]string{madeUpCheck}, findingRegistry, unreasoned); len(got) != 1 {
		t.Errorf("missingExplainers with an empty-reason exemption = %v, want exactly one report", got)
	}
}

// TestCoverage_EveryExemptionHasANonEmptyReason mirrors
// internal/findings/catalog_test.go's own rule for catalogExclusions: "we
// could not be bothered" is not a valid exemption reason, so an empty one
// fails here directly rather than only being caught incidentally by the
// gate above.
func TestCoverage_EveryExemptionHasANonEmptyReason(t *testing.T) {
	for check, reason := range exemptions {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("exemptions[%q] has an empty reason", check)
		}
	}
}

// --- rendering ---------------------------------------------------------------

// TestExplain_EveryRegisteredCheckRendersNonEmpty renders every
// findingRegistry template through Explain, across every severity value and
// with/without Nodes and Refs populated (the "absent optional fields" case
// the task card calls out), and requires What/Why/WhatToDo/Where to all be
// non-empty, and that Explain never panics doing so.
func TestExplain_EveryRegisteredCheckRendersNonEmpty(t *testing.T) {
	severities := []string{findings.SeverityError, findings.SeverityWarning, findings.SeverityInfo, "unknown-severity"}
	shapes := []struct {
		name  string
		nodes []string
		refs  []string
	}{
		{"nodes and refs", []string{"pve1", "pve2"}, []string{"bridge:pve1:vmbr0"}},
		{"nodes only", []string{"pve1"}, nil},
		{"refs only", nil, []string{"bond:pve1:bond0"}},
		{"neither (cluster-wide)", nil, nil},
	}

	for check := range findingRegistry {
		for _, sev := range severities {
			for _, shape := range shapes {
				t.Run(check+"/"+sev+"/"+shape.name, func(t *testing.T) {
					f := findings.Finding{Check: check, Severity: sev, Nodes: shape.nodes, Refs: shape.refs}
					func() {
						defer func() {
							if r := recover(); r != nil {
								t.Fatalf("Explain panicked: %v", r)
							}
						}()
						got := Explain(f)
						if got.What == "" {
							t.Error("What is empty")
						}
						if got.WhyItMatters == "" {
							t.Error("WhyItMatters is empty")
						}
						if got.WhatToDo == "" {
							t.Error("WhatToDo is empty")
						}
						if got.Where == "" {
							t.Error("Where is empty")
						}
						if got.Check != check {
							t.Errorf("Check = %q, want %q", got.Check, check)
						}
					}()
				})
			}
		}
	}
}

// TestExplainOK_UnregisteredCheckReturnsFalse asserts the non-panicking
// form reports ok=false rather than a zero-value Explanation masquerading
// as a real one.
func TestExplainOK_UnregisteredCheckReturnsFalse(t *testing.T) {
	_, ok := ExplainOK(findings.Finding{Check: "not_a_real_check"})
	if ok {
		t.Error("ExplainOK reported ok=true for an unregistered check")
	}
}

// TestExplain_PanicsOnUnregisteredCheck pins Explain's documented panic
// behavior — it exists so a caller that only ever sees real, cataloged
// Findings gets a direct value rather than an (Explanation, bool) pair to
// unwrap at every call site, with the coverage gate above making the panic
// path unreachable for any check that ships.
func TestExplain_PanicsOnUnregisteredCheck(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Explain did not panic on an unregistered check")
		}
	}()
	Explain(findings.Finding{Check: "not_a_real_check"})
}

// --- runbook deference -------------------------------------------------------

// TestExplain_DefersToRunbookRatherThanDuplicatingRemediation is the task
// card's central rule for the three checks with a built-in runbook
// (internal/runbook/catalog.go): WhatToDo must name the runbook rather than
// restate its Summary, and RunbookName must be set. checkedRunbookChecks is
// asserted against runbook.Runbooks() directly (not hand-listed) so this
// test keeps covering every built-in runbook automatically if a fourth one
// is ever added.
func TestExplain_DefersToRunbookRatherThanDuplicatingRemediation(t *testing.T) {
	rbs := runbook.Runbooks()
	if len(rbs) == 0 {
		t.Fatal("runbook.Runbooks() returned none; this test cannot prove anything")
	}
	for _, rb := range rbs {
		t.Run(rb.CheckName, func(t *testing.T) {
			f := findings.Finding{Check: rb.CheckName, Severity: findings.SeverityWarning, Nodes: []string{"pve1"}}
			got := Explain(f)
			if got.RunbookName != rb.Name {
				t.Errorf("RunbookName = %q, want %q", got.RunbookName, rb.Name)
			}
			if strings.Contains(got.WhatToDo, rb.Summary) {
				t.Errorf("WhatToDo restates the runbook's own Summary verbatim, rather than pointing at it: %q", got.WhatToDo)
			}
			if !strings.Contains(got.WhatToDo, rb.Title) {
				t.Errorf("WhatToDo = %q, want it to name the runbook's Title %q", got.WhatToDo, rb.Title)
			}
		})
	}
}

// TestExplain_NonRunbookCheckHasNoRunbookName is the control for the test
// above: a check with no built-in runbook must render RunbookName == "" and
// a WhatToDo that came from the template's own Remedy text, not an empty
// string standing in for a runbook pointer.
func TestExplain_NonRunbookCheckHasNoRunbookName(t *testing.T) {
	runbookChecks := map[string]bool{}
	for _, rb := range runbook.Runbooks() {
		runbookChecks[rb.CheckName] = true
	}

	found := 0
	for check, tmpl := range findingRegistry {
		if runbookChecks[check] {
			continue
		}
		found++
		f := findings.Finding{Check: check, Severity: findings.SeverityWarning}
		got := Explain(f)
		if got.RunbookName != "" {
			t.Errorf("%s: RunbookName = %q, want empty (no built-in runbook)", check, got.RunbookName)
		}
		if got.WhatToDo != tmpl.Remedy {
			t.Errorf("%s: WhatToDo = %q, want the template's own Remedy text %q", check, got.WhatToDo, tmpl.Remedy)
		}
		if got.WhatToDo == "" {
			t.Errorf("%s: WhatToDo is empty for a non-runbook check", check)
		}
	}
	if found == 0 {
		t.Fatal("found zero non-runbook checks in findingRegistry; the control leg proves nothing")
	}
}

// --- Where clause -------------------------------------------------------------

func TestWhereClause(t *testing.T) {
	cases := []struct {
		name  string
		want  string
		nodes []string
		refs  []string
	}{
		{"neither", "Cluster-wide; no specific node or entity is named.", nil, nil},
		{"nodes only", "Affects node(s): pve1, pve2.", []string{"pve2", "pve1"}, nil},
		{"refs only", "Affects: bond:pve1:bond0.", nil, []string{"bond:pve1:bond0"}},
		{"both prefers refs", "Affects: bond:pve1:bond0.", []string{"pve1"}, []string{"bond:pve1:bond0"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := whereClause(findings.Finding{Nodes: tc.nodes, Refs: tc.refs})
			if got != tc.want {
				t.Errorf("whereClause() = %q, want %q", got, tc.want)
			}
		})
	}
}
