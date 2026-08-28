// SPDX-License-Identifier: Apache-2.0

package findings

import (
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/drift"
)

// DriftProvider is the subset of *drift.Service Engine needs: the current
// findings list and the fixing-changeset op lookup by finding id. Declared
// as an interface (the same seam pattern internal/api uses for its own
// DriftService) so this package's dependency on drift's concrete type stays
// a two-method seam, and so tests can substitute a stub.
type DriftProvider interface {
	Findings() []drift.Finding
	FixOps(id string) (ops []change.Op, title string, ok bool)
}

// driftDocsLink is the fallback remediation for a drift finding with no
// computable fix (docs/features/monitoring.md §5: "a fixing changeset where
// computable, docs link otherwise").
const driftDocsLink = "docs/features/topology.md#6-drift-detection"

// fromDriftFinding adapts one drift.Finding into the unified shape,
// preserving its already-stable ID (prefixed with "drift:" so it can never
// collide with an lldp/health ID) and its fixOps/fixTitle when present —
// Engine.FixOps re-derives those fresh from DriftProvider.FixOps rather than
// caching the adapted copy's private fields, so this adaptation only needs
// to carry Fixable through for display purposes.
func fromDriftFinding(f drift.Finding) Finding {
	out := Finding{
		ID:       "drift:" + f.ID,
		Source:   SourceDrift,
		Check:    f.Check,
		Severity: f.Severity,
		Detail:   f.Detail,
		Nodes:    f.Nodes,
		Refs:     f.Refs,
		Fixable:  f.Fixable,
	}
	if !out.Fixable {
		out.DocsLink = driftDocsLink
	}
	return out
}

// driftFindings adapts every current drift finding, or nil when p is nil
// (drift not wired — e.g. a test Engine that only exercises health checks).
func driftFindings(p DriftProvider) []Finding {
	if p == nil {
		return nil
	}
	src := p.Findings()
	out := make([]Finding, 0, len(src))
	for _, f := range src {
		out = append(out, fromDriftFinding(f))
	}
	return out
}
