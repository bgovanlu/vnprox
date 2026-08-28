// SPDX-License-Identifier: Apache-2.0

package explain

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/runbook"
)

// severityFraming turns a Finding's Severity into the generic urgency
// clause every Explanation's WhatToDo-adjacent framing shares — computed
// once, here, rather than restated by each of the 70 templates.
var severityFraming = map[string]string{
	findings.SeverityError:   "This is an error-level finding: it names a condition vnprox treats as actively wrong, not just worth a look.",
	findings.SeverityWarning: "This is a warning-level finding: worth addressing, not (yet) an active failure.",
	findings.SeverityInfo:    "This is an info-level finding: observational, for awareness rather than action.",
}

// exemptions lists check names deliberately excluded from findingRegistry,
// each with why — the same shape internal/findings/catalog_test.go's own
// catalogExclusions table uses for the identical reason: an exemption with
// no reason is indistinguishable from an oversight.
//
//nolint:gochecknoglobals // a read-only exemption table, the same shape catalogExclusions already is
var exemptions = map[string]string{}

// Explain renders f's plain-language explanation from findingRegistry,
// keyed on f.Check — never by parsing f.Detail. Panics if f.Check is
// neither registered nor exempted: that combination means a check shipped
// in findings.AllCheckNames() without this package learning about it, which
// TestCoverage_AllCatalogChecksHaveExplainerOrExemption exists specifically
// to catch before this ever runs in production. A caller that only ever
// sees real Findings (every producer stamps a real, cataloged Check) never
// observes the panic; ExplainOK exists for a caller that wants the
// non-panicking form (e.g. handling a check name from an older/newer build
// during a rolling upgrade).
func Explain(f findings.Finding) Explanation {
	ex, ok := ExplainOK(f)
	if !ok {
		panic(fmt.Sprintf("explain: no template or exemption registered for check %q — "+
			"add one to internal/explain/registry.go or internal/explain/findings.go's exemptions", f.Check))
	}
	return ex
}

// ExplainOK is Explain's non-panicking form: ok is false when f.Check is
// neither in findingRegistry nor exemptions (a check this package's
// coverage gate has not yet learned about — see Explain's doc comment for
// why that should never happen for a real, cataloged Finding).
func ExplainOK(f findings.Finding) (Explanation, bool) {
	tmpl, ok := findingRegistry[f.Check]
	if !ok {
		return Explanation{}, false
	}

	whatToDo := tmpl.Remedy
	runbookName := ""
	if rbs := runbook.ForCheck(f.Check); len(rbs) > 0 {
		// Point at the runbook rather than restating its remediation —
		// the task card's explicit rule. Reads runbook.ForCheck fresh
		// every call, so if the runbook's own title/summary changes this
		// explanation changes with it instead of drifting from a copy.
		rb := rbs[0]
		runbookName = rb.Name
		whatToDo = fmt.Sprintf("Use the %q runbook (%s) to review and stage the fix.", rb.Title, rb.DocsLink)
	}

	return Explanation{
		Check:        f.Check,
		Severity:     f.Severity,
		What:         tmpl.What,
		WhyItMatters: strings.TrimSpace(tmpl.Why + " " + severityFraming[f.Severity]),
		WhatToDo:     whatToDo,
		RunbookName:  runbookName,
		Where:        whereClause(f),
	}, true
}

// whereClause renders which nodes and/or entities a finding concerns,
// generically, from Finding.Nodes and Finding.Refs — the "parameterized by
// the finding's Refs/Nodes" half of the task card's instruction, computed
// once here rather than duplicated across 70 templates.
func whereClause(f findings.Finding) string {
	nodes := sortedCopy(f.Nodes)
	refs := sortedCopy(f.Refs)

	switch {
	case len(nodes) == 0 && len(refs) == 0:
		return "Cluster-wide; no specific node or entity is named."
	case len(refs) > 0:
		return "Affects: " + strings.Join(refs, ", ") + "."
	default:
		return "Affects node(s): " + strings.Join(nodes, ", ") + "."
	}
}

func sortedCopy(ss []string) []string {
	out := make([]string, len(ss))
	copy(out, ss)
	sort.Strings(out)
	return out
}

// missingExplainers checks names for which allChecks has neither a
// findingRegistry template nor a documented exemptions entry. Exported for
// findings_test.go's coverage gate; kept as a plain function of its inputs
// (rather than reading the package globals directly inside the test) so the
// same check can be exercised against a deliberately incomplete check list
// without mutating the real registry — see
// TestCoverage_MissingTemplateFailsGate.
func missingExplainers(allChecks []string, registry map[string]findingTemplate, exempt map[string]string) []string {
	var out []string
	for _, c := range allChecks {
		if _, ok := registry[c]; ok {
			continue
		}
		reason, isExempt := exempt[c]
		if isExempt && strings.TrimSpace(reason) != "" {
			continue
		}
		if isExempt {
			out = append(out, fmt.Sprintf("%s: exempted with an empty reason", c))
			continue
		}
		out = append(out, fmt.Sprintf("%s: no explainer template and no exemption", c))
	}
	return out
}
