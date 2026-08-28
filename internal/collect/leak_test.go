// SPDX-License-Identifier: Apache-2.0

package collect_test

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/bgovanlu/vnprox/internal/collect"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// TestNoGoroutineLeaks is T-104's acceptance criterion 4: repeated
// start/stop cycles of the collectors leave zero leaked goroutines.
//
// The httptest.Server backing the mock is closed explicitly (not via
// t.Cleanup, which only runs after this test function — and any deferred
// goleak check within it — has already returned) before the goleak
// assertion, so its own Accept-loop goroutine doesn't register as a false
// positive.
func TestNoGoroutineLeaks(t *testing.T) {
	srv := loadFixtureServer(t, fixtureThreeNode)
	ts := httptest.NewServer(srv)

	graph := inventory.NewGraph()
	c, err := collect.New(collect.Config{
		PVE:          newTicketClient(t, ts.URL),
		Host:         newFixtureHostReader(srv),
		Graph:        graph,
		PVEInterval:  20 * time.Millisecond,
		HostInterval: 20 * time.Millisecond,
		LLDPInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("collect.New: %v", err)
	}

	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithCancel(context.Background())

		var wg sync.WaitGroup
		wg.Add(3)
		go func() { defer wg.Done(); _ = c.RunPVELoop(ctx) }()
		go func() { defer wg.Done(); _ = c.RunHostLoop(ctx) }()
		go func() { defer wg.Done(); _ = c.RunLLDPLoop(ctx) }()

		// Let at least one poll happen before stopping, so this cycle
		// exercises real work, not just instant start/stop.
		waitFor(t, 2*time.Second, "at least one poll cycle", func() bool {
			return graph.Snapshot().Len() > 0
		})

		cancel()
		wg.Wait()
	}

	ts.Close()
	goleak.VerifyNone(t)
}
