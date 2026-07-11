package collect_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/collect"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// TestHostPoll_OnStatsCalledWithLinksAndCounters covers T-601's
// Config.OnStats wiring: a host poll (RefreshNow's local-node path, which
// hostPollOnce/hostPollStateFor drive) must invoke OnStats exactly with the
// polled node's name, its Links() read, and the matching Stats() counters —
// the raw material internal/metrics.Sampler.Ingest needs. Pre-T-601,
// hostPollStateFor read Stats and discarded it; this pins the new behavior.
func TestHostPoll_OnStatsCalledWithLinksAndCounters(t *testing.T) {
	srv := loadFixtureServer(t, fixtureThreeNode)

	var (
		mu       sync.Mutex
		calls    []string
		gotLinks map[string][]host.LinkState
		gotStats map[string]map[string]host.IfaceStats
	)
	gotLinks = map[string][]host.LinkState{}
	gotStats = map[string]map[string]host.IfaceStats{}

	c, _, _ := newTestCollector(t, srv, func(cfg *collect.Config) {
		cfg.OnStats = func(_ context.Context, node string, at time.Time, links []host.LinkState, stats map[string]host.IfaceStats) {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, node)
			gotLinks[node] = links
			gotStats[node] = stats
			if at.IsZero() {
				t.Errorf("OnStats(%s): at is zero", node)
			}
		}
	})

	if _, err := c.RefreshNow(context.Background(), inventory.Scope{}); err != nil {
		t.Fatalf("RefreshNow: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) == 0 {
		t.Fatal("OnStats was never called")
	}
	node := calls[0]
	if len(gotLinks[node]) == 0 {
		t.Errorf("OnStats(%s) links = empty, want the node's netlink link list", node)
	}
	if len(gotStats[node]) == 0 {
		t.Errorf("OnStats(%s) stats = empty, want the node's fixture interface counters", node)
	}
}
