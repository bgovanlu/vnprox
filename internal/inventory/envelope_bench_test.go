// SPDX-License-Identifier: Apache-2.0

package inventory

import (
	"fmt"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/perfbudget"
)

// envelopePerfSite is this file's own path, as perf/budgets.json spells it.
const envelopePerfSite = "internal/inventory/envelope_bench_test.go"

// BenchmarkSnapshotAtEnvelope is BenchmarkSnapshotAtScale's T-4107
// counterpart, at the 50-node/5,000-guest scale envelope
// (inventory.EnvelopeProfile) rather than the topology.md §4 target.
func BenchmarkSnapshotAtEnvelope(b *testing.B) {
	g := BuildScaleGraph(EnvelopeProfile)
	n := g.Snapshot().Len()
	b.ReportMetric(float64(n), "entities")
	b.ResetTimer()
	var sink Snapshot
	for i := 0; i < b.N; i++ {
		sink = g.Snapshot()
	}
	b.StopTimer()
	if sink.Len() == 0 {
		b.Fatal("empty snapshot")
	}
}

// BenchmarkSnapshotReadAtEnvelope is BenchmarkSnapshotReadAtScale's
// envelope counterpart: Snapshot() plus a full entity+edge traversal, the
// realistic cost a topology projection pays.
func BenchmarkSnapshotReadAtEnvelope(b *testing.B) {
	g := BuildScaleGraph(EnvelopeProfile)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s := g.Snapshot()
		var acc int
		for _, e := range s.All() {
			acc += len(e.fieldMap())
		}
		acc += len(s.Edges())
		if acc == 0 {
			b.Fatal("no work")
		}
	}
}

// TestPerfBudgets_InventoryEnvelope is T-4107's Go-side perfbudget gate for
// the inventory graph at the scale envelope, following
// internal/collect/sim_bench_test.go's TestPerfBudgets_Sim pattern exactly:
// load perf/budgets.json, calibrate this machine, measure Budget.Samples
// times, gate.
//
// It measures Graph.Snapshot() itself (the O(1)-by-design copy-on-write
// read T-103 documents) rather than ApplyPoll — ApplyPoll's cost is paid
// once per poll cycle server-side and is not on any request's critical
// path, whereas Snapshot() is called by every topology/simulate/validate
// request.
func TestPerfBudgets_InventoryEnvelope(t *testing.T) {
	file, err := perfbudget.LoadRepo()
	if err != nil {
		t.Fatalf("loading performance budgets: %v", err)
	}
	machine, err := perfbudget.Detect(file)
	if err != nil {
		t.Fatalf("calibrating this machine: %v", err)
	}

	g := BuildScaleGraph(EnvelopeProfile)
	n := g.Snapshot().Len()
	t.Logf("inventory envelope graph: %d entities", n)

	budget, err := file.ByID("inventory.snapshot_at_envelope_us")
	if err != nil {
		t.Fatalf("%v", err)
	}
	// One discarded read: the first Snapshot() after ApplyPoll pays for a
	// cold-ish heap; a first-sample-inflated median would report a
	// regression that is not there (same rationale as sim_bench_test.go's
	// discarded warm-up pass).
	_ = g.Snapshot()

	result, err := perfbudget.Measure(budget, machine, func(int) (float64, error) {
		const perSample = 50
		start := time.Now()
		for i := 0; i < perSample; i++ {
			s := g.Snapshot()
			if s.Len() == 0 {
				t.Fatal("empty snapshot")
			}
		}
		return float64(time.Since(start).Microseconds()) / perSample, nil
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

// TestSnapshotP99AtEnvelope is TestSnapshotP99's counterpart at the T-4107
// scale envelope: report-only (no gate — that is perfbudget's job above),
// but useful evidence in its own right that Snapshot()'s O(1)-by-design
// contract actually holds an order of magnitude past the topology.md §4
// target.
func TestSnapshotP99AtEnvelope(t *testing.T) {
	g := BuildScaleGraph(EnvelopeProfile)
	n := g.Snapshot().Len()

	const iters = 5000
	samples := make([]time.Duration, iters)
	stop := make(chan struct{})
	go func() {
		// Re-applying the same real batch every cycle (rather than an empty
		// one) keeps entity cardinality stable across the run while still
		// forcing ApplyPoll's full write path — lock, merge, publish a
		// fresh immutable state — on every iteration, the same contention
		// TestSnapshotP99 exercises at the topology.md §4 target.
		var writeBatch []Entity
		for j := 1; j <= EnvelopeProfile.NicsPerNode; j++ {
			name := "eno" + strconv.Itoa(j)
			writeBatch = append(writeBatch, &PhysNic{
				Ref: Ref{Kind: KindPhysNic, Node: "pve1", ID: name}, Name: name,
				Mac: fmt.Sprintf("aa:bb:cc:%02d:%02d:01", 1, j), Driver: "ixgbe",
				SpeedMbps: 10000, Duplex: "full", MTU: 1500, LinkUp: true, LinkUpSet: true, OperState: "up",
			})
		}
		for {
			select {
			case <-stop:
				return
			default:
				g.ApplyPoll(SourceHostNetlink, Scope{Node: "pve1", Kinds: []Kind{KindPhysNic}}, writeBatch)
			}
		}
	}()
	for i := 0; i < iters; i++ {
		start := time.Now()
		s := g.Snapshot()
		_ = s.Len()
		samples[i] = time.Since(start)
	}
	close(stop)

	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	p50 := samples[iters*50/100]
	p99 := samples[iters*99/100]
	pMax := samples[iters-1]
	t.Logf("Snapshot() over %d entities (envelope: 50 nodes/5000 guests), %d samples: p50=%v p99=%v max=%v", n, iters, p50, p99, pMax)
}
