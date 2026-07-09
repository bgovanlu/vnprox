package inventory

import (
	"sort"
	"testing"
	"time"
)

// BenchmarkSnapshotAtScale reports the cost of Snapshot() on a graph built at
// the topology.md §4 scale target (acceptance criterion #4).
func BenchmarkSnapshotAtScale(b *testing.B) {
	g := NewGraph()
	n := buildScaleModel().applyAll(g)
	b.ReportMetric(float64(n), "entities")
	b.ResetTimer()
	var sink Snapshot
	for i := 0; i < b.N; i++ {
		sink = g.Snapshot()
	}
	_ = sink
	b.StopTimer()
	// Touch the snapshot so the compiler cannot elide the loop.
	if sink.Len() == 0 {
		b.Fatal("empty snapshot")
	}
}

// BenchmarkSnapshotReadAtScale reports Snapshot() plus a full entity+edge
// traversal, the realistic cost a topology projection pays.
func BenchmarkSnapshotReadAtScale(b *testing.B) {
	g := NewGraph()
	buildScaleModel().applyAll(g)
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

// TestSnapshotP99 asserts acceptance criterion #4 directly: Snapshot() p99 is
// well under 5ms at scale, and reports the measured distribution.
func TestSnapshotP99(t *testing.T) {
	g := NewGraph()
	n := buildScaleModel().applyAll(g)

	const iters = 20000
	samples := make([]time.Duration, iters)
	// Keep a concurrent writer active so we measure Snapshot() under the same
	// contention the acceptance criterion implies.
	stop := make(chan struct{})
	go func() {
		m := buildScaleModel()
		for {
			select {
			case <-stop:
				return
			default:
				g.ApplyPoll(SourceHostNetlink, Scope{Node: m.nodes[0]}, m.netlink[m.nodes[0]])
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
	t.Logf("Snapshot() over %d entities, %d samples: p50=%v p99=%v max=%v", n, iters, p50, p99, pMax)
	if p99 > 5*time.Millisecond {
		t.Errorf("p99 Snapshot latency %v exceeds 5ms target", p99)
	}
}
