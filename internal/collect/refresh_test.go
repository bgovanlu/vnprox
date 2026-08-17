package collect_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/collect"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// TestRefreshNow is T-104's acceptance criterion 3: a full targeted refresh
// against pvemock completes in under 2s and triggers exactly one delta
// batch (not one per sub-poll-step).
func TestRefreshNow(t *testing.T) {
	srv := loadFixtureServer(t, fixtureThreeNode)

	var deltaBatches int64
	c, graph, _ := newTestCollector(t, srv, func(cfg *collect.Config) {
		cfg.OnDelta = func(inventory.Delta) { atomic.AddInt64(&deltaBatches, 1) }
	})
	// Note: this test never starts RunPVELoop/RunHostLoop/RunLLDPLoop, so
	// only RefreshNow calls touch the graph/OnDelta below — no background
	// ticking to race against.

	ctx := context.Background()

	// --- full (cluster-wide) refresh ------------------------------------
	start := time.Now()
	delta, err := c.RefreshNow(ctx, inventory.Scope{})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("RefreshNow (full): %v", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("RefreshNow (full) took %v, want < 2s", elapsed)
	}
	if delta.Empty() {
		t.Fatalf("RefreshNow (full): expected a non-empty delta on first run")
	}
	if got := atomic.LoadInt64(&deltaBatches); got != 1 {
		t.Fatalf("delta batches after full refresh = %d, want exactly 1", got)
	}
	// T-3103: +2 for the two vnet-scope firewall rulesets (vnet100/vnet200)
	// pollFirewall now polls alongside the cluster ruleset.
	if got := graph.Snapshot().Len(); got != 38 {
		t.Fatalf("entity count after full refresh = %d, want 38", got)
	}

	// --- targeted single-node refresh (no-op change, so its own delta is
	// empty and must NOT invoke OnDelta again — but must still complete
	// fast and hit the API exactly once as one logical batch). ---------
	start = time.Now()
	delta2, err := c.RefreshNow(ctx, inventory.Scope{Node: "pve2"})
	elapsed = time.Since(start)
	if err != nil {
		t.Fatalf("RefreshNow (node pve2): %v", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("RefreshNow (node pve2) took %v, want < 2s", elapsed)
	}
	if !delta2.Empty() {
		t.Logf("RefreshNow (node pve2) unexpectedly produced a delta (fixture state may have changed): %+v", delta2)
	}
	// Since nothing changed on pve2 between the two refreshes, OnDelta
	// must not have fired again.
	if got := atomic.LoadInt64(&deltaBatches); got != 1 {
		t.Fatalf("delta batches after the second (no-op) refresh = %d, want still 1", got)
	}
}
