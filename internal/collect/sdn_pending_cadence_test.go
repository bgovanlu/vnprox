// SPDX-License-Identifier: Apache-2.0

package collect_test

// Debt-sweep 2026-08-19 follow-up ("inventory.FromPVESDN ... read .Pending
// the same wrong way, to paint topology badges. Migrate them to the correct
// mechanism ... be careful about poll cost"): pollSDN's badge-painting
// pending reads (zones/vnets/subnets/controllers "?pending=1") must not run
// on every single 10s poll tick — this test pins the cheaper cadence
// (collect.sdnPendingEvery, 3 pollSDN cycles) rather than letting it
// silently regress back to "every tick" (tripling this poll step's SDN call
// count) or drift to some other unintended interval.

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// pendingRequestCounter counts GET /cluster/sdn/zones?pending=1 requests —
// one specific, deterministic endpoint pollSDN's pending refresh always
// hits exactly once per refresh (unlike the per-vnet subnets-pending calls,
// whose count depends on fixture vnet count) — while passing every other
// request through untouched.
type pendingRequestCounter struct {
	inner http.Handler
	n     atomic.Int64
}

func (c *pendingRequestCounter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == "/api2/json/cluster/sdn/zones" && r.URL.Query().Get("pending") == "1" {
		c.n.Add(1)
	}
	c.inner.ServeHTTP(w, r)
}

// TestSDNPendingBadgeRefresh_SlowerThanEveryPoll proves pollSDN's
// "?pending=1" badge refresh (feeding inventory.FromPVESDN's
// zonePending/vnetPending/subnetPending maps) does NOT fire on every
// RefreshNow/pollSDN cycle — only every collect.sdnPendingEvery-th one — by
// driving six full poll cycles (via RefreshNow, so each is independently
// awaited rather than racing a background ticker) and counting exactly how
// many issued a zones "?pending=1" request.
func TestSDNPendingBadgeRefresh_SlowerThanEveryPoll(t *testing.T) {
	srv := loadFixtureServer(t, fixtureThreeNode)
	counter := &pendingRequestCounter{inner: srv}
	c, _, _ := newTestCollectorHandler(t, srv, counter)

	ctx := context.Background()
	const cycles = 6
	for i := 0; i < cycles; i++ {
		if _, err := c.RefreshNow(ctx, inventory.Scope{}); err != nil {
			t.Fatalf("RefreshNow #%d: %v", i+1, err)
		}
	}

	// sdnPendingEvery == 3: refreshes happen on tick 1 (always, so the very
	// first poll has real badge data rather than an empty cache) and every
	// 3rd tick after — ticks 1, 3, 6 of 6, i.e. exactly 3 refreshes, not 6
	// (every tick) and not 2 (a plain "every 3rd, no first-tick" ceil).
	const want = 3
	if got := counter.n.Load(); got != want {
		t.Fatalf("zones ?pending=1 request count = %d over %d RefreshNow cycles, want %d (collect.sdnPendingEvery's cadence)", got, cycles, want)
	}
}
