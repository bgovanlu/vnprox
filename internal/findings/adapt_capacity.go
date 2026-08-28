// SPDX-License-Identifier: Apache-2.0

package findings

// CapacityProvider is the seam internal/capacity's forecast producer
// satisfies (via cmd/vnproxd's capacityFindingsAdapter, which reads the
// rolled-up capacity_aggregates, runs capacity.Analyze, and converts each
// ForecastFinding into the unified Finding shape — the composition root does
// the conversion so internal/capacity need not import this package, the same
// decoupling ipamFindingsAdapter provides for internal/ipam). Nil
// Config.Capacity means "contribute zero capacity findings" (a daemon with no
// store, or no aggregate history yet), so the seam stays nil-safe.
//
// The seam returns the unified Finding shape directly (Source:
// SourceCapacity), so the adapter constructs findings.Finding values straight
// away rather than exposing a capacity-package-local type Engine would have to
// re-adapt.
type CapacityProvider interface {
	Findings() []Finding
}

// capacityFindings returns p's current forecast findings, or nil when p is
// nil.
func capacityFindings(p CapacityProvider) []Finding {
	if p == nil {
		return nil
	}
	return p.Findings()
}
