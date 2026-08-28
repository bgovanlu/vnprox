// SPDX-License-Identifier: Apache-2.0

package findings

// ProbeProvider is the seam T-806's persisted sim_divergence findings
// producer satisfies (via cmd/vnproxd's probeFindingsAdapter, which reads
// *store.SimDivergenceRepo and converts each row into the unified Finding
// shape — the composition root does the conversion so internal/findings
// need not import internal/store, the same decoupling ipamFindingsAdapter
// provides for internal/ipam). Nil Config.Probe means "contribute zero
// probe findings" (e.g. no PVE client wired, so /simulate/verify itself
// was never mounted), the same nil-safe degradation every other optional
// producer seam in this package already has.
//
// Unlike every other producer here, this one's findings are read from
// persisted storage rather than recomputed fresh from live/polled state —
// see docs/data-model.md §5 and the 0005_sim_divergence_findings.sql
// migration's doc comment for why a user-triggered diagnostic probe result
// needs a table instead of an in-memory recomputation.
type ProbeProvider interface {
	Findings() []Finding
}

// probeFindings returns p's current findings, or nil when p is nil.
func probeFindings(p ProbeProvider) []Finding {
	if p == nil {
		return nil
	}
	return p.Findings()
}
