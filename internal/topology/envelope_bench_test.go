// SPDX-License-Identifier: Apache-2.0

package topology_test

import (
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/perfbudget"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// envelopePerfSite is this file's own path, as perf/budgets.json spells it.
const envelopePerfSite = "internal/topology/envelope_bench_test.go"

// scaleProfileT607 mirrors testdata/genscale/main.go's topology.md §4
// target (8 nodes x 6 NICs, 4 bridges/node, 40 VNets) closely enough for a
// scaling comparison against BenchmarkProjectAtEnvelope — 304 guests rather
// than exactly 300, since inventory.BuildScaleGraph divides guests evenly
// per node and 300/8 does not, and a same-shape approximation is what this
// comparison needs, not an exact restatement of genscale's own fixture.
var scaleProfileT607 = inventory.ScaleProfileConfig{
	Nodes: 8, NicsPerNode: 6, BridgesPerNode: 4, GuestsPerNode: 38, VNets: 40, Zones: 4,
}

// BenchmarkProjectAtScale is BenchmarkProjectAtEnvelope's topology.md §4-
// scale counterpart, so the two benchmarks' ns/op is a direct linear-vs-
// superlinear scaling comparison (~16x the guests between the two configs)
// rather than a single unanchored number.
func BenchmarkProjectAtScale(b *testing.B) {
	g := inventory.BuildScaleGraph(scaleProfileT607)
	snap := g.Snapshot()
	b.ResetTimer()
	var sink topology.Topology
	for i := 0; i < b.N; i++ {
		sink = topology.Project(snap, topology.Filter{})
	}
	b.StopTimer()
	if len(sink.Nodes) == 0 {
		b.Fatal("empty projection")
	}
	b.ReportMetric(float64(len(sink.Nodes)), "rendered-nodes")
	b.ReportMetric(float64(len(sink.Edges)), "rendered-edges")
}

// BenchmarkProjectAtEnvelope reports topology.Project's cost at T-4107's
// scale envelope (inventory.EnvelopeProfile: 50 nodes, 5,000 guests, 100
// VNets) — the path GET /topology pays on every request (docs/features/
// topology.md §4's "8/300 smooth pan/zoom" target is what
// internal/inventory/bench_test.go and cmd/vnproxd/scale_bench_test.go
// already measure Project at; this is the same call at ~16x the guest
// count).
func BenchmarkProjectAtEnvelope(b *testing.B) {
	g := inventory.BuildScaleGraph(inventory.EnvelopeProfile)
	snap := g.Snapshot()
	b.ResetTimer()
	var sink topology.Topology
	for i := 0; i < b.N; i++ {
		sink = topology.Project(snap, topology.Filter{})
	}
	b.StopTimer()
	if len(sink.Nodes) == 0 {
		b.Fatal("empty projection")
	}
	b.ReportMetric(float64(len(sink.Nodes)), "rendered-nodes")
	b.ReportMetric(float64(len(sink.Edges)), "rendered-edges")
}

// TestPerfBudgets_TopologyEnvelope is T-4107's perfbudget gate for
// topology.Project at the scale envelope, following
// internal/collect/sim_bench_test.go's TestPerfBudgets_Sim pattern.
func TestPerfBudgets_TopologyEnvelope(t *testing.T) {
	file, err := perfbudget.LoadRepo()
	if err != nil {
		t.Fatalf("loading performance budgets: %v", err)
	}
	machine, err := perfbudget.Detect(file)
	if err != nil {
		t.Fatalf("calibrating this machine: %v", err)
	}

	g := inventory.BuildScaleGraph(inventory.EnvelopeProfile)
	snap := g.Snapshot()
	// Discarded warm-up pass, same rationale as sim_bench_test.go's.
	warm := topology.Project(snap, topology.Filter{})
	t.Logf("topology envelope projection: %d rendered nodes, %d rendered edges (from %d entities)",
		len(warm.Nodes), len(warm.Edges), snap.Len())

	budget, err := file.ByID("topology.project_at_envelope_ms")
	if err != nil {
		t.Fatalf("%v", err)
	}
	result, err := perfbudget.Measure(budget, machine, func(int) (float64, error) {
		start := time.Now()
		topo := topology.Project(snap, topology.Filter{})
		el := time.Since(start)
		if len(topo.Nodes) == 0 {
			t.Fatal("empty projection")
		}
		return float64(el.Microseconds()) / 1000, nil
	})
	if err != nil {
		t.Fatalf("measuring %s: %v", budget.ID, err)
	}

	results := []perfbudget.Result{result}
	t.Logf("\n%s", perfbudget.Report(results, machine))

	if err := perfbudget.Missing(file.ForSite(envelopePerfSite), results); err != nil {
		t.Errorf("%v", err)
	}
	if err := perfbudget.Check(results); err != nil {
		t.Errorf("%v", err)
	}
}
