// SPDX-License-Identifier: Apache-2.0

package drift

import (
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/xnode"
)

// driftFindings adapts the neutral cross-node comparison results from
// internal/xnode (the single shared implementation this package and
// internal/change both call) into this package's Finding wire type. drift
// keeps its historical severities (bridge/MTU divergence warn, SDN
// realization errors) and prefixes its fixing-changeset titles with "drift:";
// the comparison itself, and the change.Op fix construction, are shared —
// internal/change raises the same divergences to blocking errors from the
// same xnode data.
func driftFindings(divs []xnode.Divergence) []Finding {
	out := make([]Finding, 0, len(divs))
	for _, d := range divs {
		out = append(out, driftFinding(d))
	}
	return out
}

func driftFinding(d xnode.Divergence) Finding {
	sev := SeverityWarning
	if d.Family == xnode.FamilySDNRealization {
		sev = SeverityError
	}
	f := newFinding(d.Family, sev, d.Detail, d.Nodes, d.Refs)
	if len(d.Fixes) > 0 {
		f = f.withFix("drift: "+d.FixTitle, change.CrossNodeFixOps(d.Fixes))
	}
	return f
}
