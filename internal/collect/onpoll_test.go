package collect_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/collect"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// pollObservation is one OnPoll callback invocation, recorded for
// assertions.
type pollObservation struct {
	source, node string
	failed       bool
}

// TestCollector_OnPoll_ReportsPVEAndHostAndLLDP covers T-1903's collector
// poll hook: RefreshNow (the deterministic, non-ticking path this package's
// other tests already use) must report one OnPoll call per source, scoped
// exactly like collect.SourceStatus already scopes Status() — "pve" with an
// empty node (cluster-wide), "host"/"lldp" with the local node — and must
// never report a failure for a successful poll against the mock PVE server.
func TestCollector_OnPoll_ReportsPVEAndHostAndLLDP(t *testing.T) {
	srv := loadFixtureServer(t, fixtureThreeNode)

	var mu sync.Mutex
	var observed []pollObservation
	c, _, _ := newTestCollector(t, srv, func(cfg *collect.Config) {
		cfg.OnPoll = func(source, node string, _ time.Duration, err error) {
			mu.Lock()
			defer mu.Unlock()
			observed = append(observed, pollObservation{source: source, node: node, failed: err != nil})
		}
	})

	ctx := context.Background()
	if _, err := c.RefreshNow(ctx, inventory.Scope{}); err != nil {
		t.Fatalf("RefreshNow: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	bySource := map[string]pollObservation{}
	for _, o := range observed {
		bySource[o.source] = o
	}

	pve, ok := bySource["pve"]
	if !ok {
		t.Fatalf("no OnPoll observation for source=pve; got %+v", observed)
	}
	if pve.node != "" {
		t.Errorf("pve observation node = %q, want \"\" (cluster-wide)", pve.node)
	}
	if pve.failed {
		t.Errorf("pve observation reported a failure against a healthy mock server")
	}

	host, ok := bySource["host"]
	if !ok {
		t.Fatalf("no OnPoll observation for source=host; got %+v", observed)
	}
	if host.node == "" {
		t.Errorf("host observation node = \"\", want the local node name")
	}
	if host.failed {
		t.Errorf("host observation reported a failure against a healthy mock server")
	}

	lldp, ok := bySource["lldp"]
	if !ok {
		t.Fatalf("no OnPoll observation for source=lldp; got %+v", observed)
	}
	if lldp.node == "" {
		t.Errorf("lldp observation node = \"\", want the local node name")
	}
}

// TestCollector_OnPoll_NilHookIsNoOp proves a Collector built without
// Config.OnPoll (every pre-T-1903 caller) behaves exactly as before — the
// nil-safe-optional-hook convention every other Config callback already
// gets.
func TestCollector_OnPoll_NilHookIsNoOp(t *testing.T) {
	srv := loadFixtureServer(t, fixtureThreeNode)
	c, _, _ := newTestCollector(t, srv)
	if _, err := c.RefreshNow(context.Background(), inventory.Scope{}); err != nil {
		t.Fatalf("RefreshNow with no OnPoll configured: %v", err)
	}
}
