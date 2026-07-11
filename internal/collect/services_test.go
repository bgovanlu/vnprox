package collect_test

// TestOnServices_WiredThroughHostLoop is T-602's collector-plumbing check:
// Config.OnServices fires once per successfully polled node (local +
// peers), fed by host.Reader.Services — the same "hook into the existing
// host-loop cadence" contract OnStats already established for T-601's
// metrics sampler.

import (
	"context"
	"sync"
	"testing"

	"github.com/bgovanlu/vnprox/internal/collect"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

func TestOnServices_WiredThroughHostLoop(t *testing.T) {
	srv := loadFixtureServer(t, fixtureThreeNode)

	var mu sync.Mutex
	seen := map[string]map[string]bool{}
	c, _, _ := newTestCollector(t, srv, func(cfg *collect.Config) {
		cfg.OnServices = func(node string, status map[string]bool) {
			mu.Lock()
			defer mu.Unlock()
			seen[node] = status
		}
	})

	if _, err := c.RefreshNow(context.Background(), inventory.Scope{}); err != nil {
		t.Fatalf("RefreshNow: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 {
		t.Fatal("OnServices was never called")
	}
	for node, status := range seen {
		if !status["dnsmasq"] {
			t.Errorf("node %s: dnsmasq = %v, want true (fixture declares no override, default is healthy)", node, status["dnsmasq"])
		}
		if !status["frr"] {
			t.Errorf("node %s: frr = %v, want true (fixture declares no override, default is healthy)", node, status["frr"])
		}
	}
}
