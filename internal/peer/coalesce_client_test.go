package peer

import (
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/host"
)

// TestClient_NeighborsCoalescesConcurrentCallers is T-3712's core proof:
// internal/neighbor.Service and internal/ipam.Service (via
// cmd/vnproxd/rogue.go's rogueScanAdapter too, in production — see
// cmd/vnproxd/server.go, all three share one neighbor.Service instance)
// both end up calling Client.Neighbors for the same peer/node. Before this
// task, each such call produced its own signed HTTP request; now N
// concurrent callers must produce exactly one.
func TestClient_NeighborsCoalescesConcurrentCallers(t *testing.T) {
	h := newTwoDaemonHarness(t)
	h.readerA.neighbors["pve1"] = []host.Neighbor{{IP: "10.0.0.5", MAC: "aa:bb:cc:dd:ee:ff"}}

	const n = 25
	var wg sync.WaitGroup
	errs := make([]error, n)
	results := make([][]host.Neighbor, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = h.client.Neighbors(t.Context(), h.nodeA, "pve1")
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: unexpected error: %v", i, err)
		}
		if len(results[i]) != 1 || results[i][0].IP != "10.0.0.5" {
			t.Errorf("caller %d: got %+v, want the fixture neighbor", i, results[i])
		}
	}
	if h.readerA.neighborsCalls != 1 {
		t.Fatalf("server-side reader.Neighbors called %d times, want exactly 1 (N=%d concurrent client callers should coalesce into one upstream request)", h.readerA.neighborsCalls, n)
	}
}

// TestClient_NeighborsCoalescesSequentialWithinTTL reproduces the exact
// shape T-3712's evidence showed on pvecube — not concurrent goroutines,
// but one caller finishing and a second, independent caller (a different
// consumer of the same neighbor.Service) starting a few milliseconds
// later, both well inside the replay window.
func TestClient_NeighborsCoalescesSequentialWithinTTL(t *testing.T) {
	h := newTwoDaemonHarness(t)
	h.readerA.neighbors["pve1"] = []host.Neighbor{{IP: "10.0.0.6", MAC: "11:22:33:44:55:66"}}

	if _, err := h.client.Neighbors(t.Context(), h.nodeA, "pve1"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	h.now = h.now.Add(24 * time.Millisecond) // the exact gap the evidence file recorded
	if _, err := h.client.Neighbors(t.Context(), h.nodeA, "pve1"); err != nil {
		t.Fatalf("second call: %v", err)
	}

	if h.readerA.neighborsCalls != 1 {
		t.Fatalf("server-side reader.Neighbors called %d times, want exactly 1", h.readerA.neighborsCalls)
	}
}

// TestClient_NeighborsDoesNotCoalesceAcrossPollCycles proves the dedupe is
// bounded: once PeerReadCoalesceTTL has elapsed (a later poll cycle), a
// caller must get a fresh read, not year-old cached data.
func TestClient_NeighborsDoesNotCoalesceAcrossPollCycles(t *testing.T) {
	h := newTwoDaemonHarness(t)
	h.readerA.neighbors["pve1"] = []host.Neighbor{{IP: "10.0.0.7"}}

	if _, err := h.client.Neighbors(t.Context(), h.nodeA, "pve1"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	h.now = h.now.Add(PeerReadCoalesceTTL + time.Second)
	if _, err := h.client.Neighbors(t.Context(), h.nodeA, "pve1"); err != nil {
		t.Fatalf("second call: %v", err)
	}

	if h.readerA.neighborsCalls != 2 {
		t.Fatalf("server-side reader.Neighbors called %d times, want 2 (TTL should have expired between calls)", h.readerA.neighborsCalls)
	}
}

// TestClient_NeighborsDoesNotCoalesceAcrossNodes proves the cache key is
// scoped per target node: reading two different nodes through the same
// peer must never share a result.
func TestClient_NeighborsDoesNotCoalesceAcrossNodes(t *testing.T) {
	h := newTwoDaemonHarness(t)
	h.readerA.neighbors["pve1"] = []host.Neighbor{{IP: "10.0.0.8"}}
	h.readerA.neighbors["other"] = []host.Neighbor{{IP: "10.0.0.9"}}

	got1, err := h.client.Neighbors(t.Context(), h.nodeA, "pve1")
	if err != nil {
		t.Fatalf("node pve1: %v", err)
	}
	got2, err := h.client.Neighbors(t.Context(), h.nodeA, "other")
	if err != nil {
		t.Fatalf("node other: %v", err)
	}

	if h.readerA.neighborsCalls != 2 {
		t.Fatalf("server-side reader.Neighbors called %d times, want 2 (distinct nodes must not coalesce)", h.readerA.neighborsCalls)
	}
	if got1[0].IP != "10.0.0.8" || got2[0].IP != "10.0.0.9" {
		t.Fatalf("got1=%+v got2=%+v, want each node's own fixture data", got1, got2)
	}
}

// TestClient_DHCPLeasesCoalescesConcurrentCallers is Neighbors' sibling for
// internal/dhcp's PeerSource seam (T-3712's card: "Check whether
// internal/dhcp's PeerSource ... has the same problem").
func TestClient_DHCPLeasesCoalescesConcurrentCallers(t *testing.T) {
	h := newTwoDaemonHarness(t)
	h.readerA.dhcpLeases["pve1"] = []byte("lease content")

	const n = 25
	var wg sync.WaitGroup
	errs := make([]error, n)
	results := make([][]byte, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = h.client.DHCPLeases(t.Context(), h.nodeA, "pve1")
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: unexpected error: %v", i, err)
		}
		if string(results[i]) != "lease content" {
			t.Errorf("caller %d: got %q, want %q", i, results[i], "lease content")
		}
	}
	if h.readerA.dhcpLeasesCalls != 1 {
		t.Fatalf("server-side reader.DHCPLeases called %d times, want exactly 1 (N=%d concurrent client callers should coalesce into one upstream request)", h.readerA.dhcpLeasesCalls, n)
	}
}
