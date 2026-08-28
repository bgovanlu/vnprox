// SPDX-License-Identifier: Apache-2.0

package topology_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/bgovanlu/vnprox/internal/collect"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// TestIntegration_WSDeltaFromRealMutation is T-106 acceptance criterion 4's
// first half: connect a real WS client, subscribe to "topology", mutate the
// pvemock fixture's live state via a genuine PVE API call (not a
// hand-constructed fake delta), and confirm the client receives a
// correctly-shaped topology.delta within one poll interval.
func TestIntegration_WSDeltaFromRealMutation(t *testing.T) {
	srv := loadFixtureServer(t, fixtureSingleNode)
	pveSrv := httptest.NewServer(srv)
	defer pveSrv.Close()

	graph := inventory.NewGraph()
	topoSvc := topology.NewService(graph, testLogger())

	const pollInterval = 50 * time.Millisecond
	cfg := collect.Config{
		PVE:          newTicketClient(t, pveSrv.URL),
		Host:         host.NewFixtureReader(pvemock.NewFixtureHostReader(srv)),
		Graph:        graph,
		PVEInterval:  pollInterval,
		HostInterval: pollInterval,
		LLDPInterval: pollInterval,
		OnDelta:      topoSvc.OnDelta,
		Logger:       testLogger(),
	}
	collector, err := collect.New(cfg)
	if err != nil {
		t.Fatalf("collect.New: %v", err)
	}

	// Seed the graph once before opening the WS connection, exactly like
	// vnproxd's startup would (a fresh subscriber shouldn't have to wait
	// through a whole poll cycle just to see the daemon's already-known
	// state) — this poll's own delta (everything "added") happens before
	// any client is subscribed, so it's not what the assertion below
	// checks for.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if _, refreshErr := collector.RefreshNow(ctx, inventory.Scope{}); refreshErr != nil {
		t.Fatalf("initial RefreshNow: %v", refreshErr)
	}
	cancel()

	loopCtx, stopLoops := context.WithCancel(context.Background())
	defer stopLoops()
	go func() { _ = collector.RunPVELoop(loopCtx) }()
	go func() { _ = collector.RunHostLoop(loopCtx) }()
	go func() { _ = collector.RunLLDPLoop(loopCtx) }()

	wsSrv := httptest.NewServer(http.HandlerFunc(topoSvc.ServeWS))
	defer wsSrv.Close()
	wsURL := "ws" + strings.TrimPrefix(wsSrv.URL, "http")
	conn := dialWS(t, wsURL)
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	subCtx, subCancel := context.WithTimeout(context.Background(), 2*time.Second)
	if writeErr := conn.Write(subCtx, websocket.MessageText, []byte(`{"subscribe":["topology"]}`)); writeErr != nil {
		t.Fatalf("subscribe write: %v", writeErr)
	}
	subCancel()

	// Wait for the hub to register the connection before mutating, so the
	// broadcast the mutation triggers isn't racing the subscribe frame.
	deadline := time.Now().Add(2 * time.Second)
	for topoSvc.ConnCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the ws connection to register")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // let the read loop parse the subscribe frame

	// The real mutation: change web01's (vmid 100) net0 to ride VLAN 55,
	// via a genuine PUT /nodes/pve1/qemu/100/config call against the mock
	// — the same call a real changeset apply would make. The next poll
	// cycle (<=50ms away) must observe this, produce an inventory.Delta
	// naming guest-nic:pve1:100/net0 as Updated, and the collector's
	// OnDelta callback must fan it out as a topology.delta WS event.
	mutClient := newTicketClient(t, pveSrv.URL)
	mutCtx, mutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer mutCancel()
	err = mutClient.UpdateGuestConfig(mutCtx, "pve1", pve.GuestQemu, 100, pve.GuestConfigUpdate{
		Set: map[string]string{"net0": "virtio=BC:24:11:AA:00:64,bridge=vmbr0,tag=55,firewall=1"},
	})
	if err != nil {
		t.Fatalf("UpdateGuestConfig: %v", err)
	}

	wantRef := "guest-nic:pve1:100/net0"
	deadlineMsg := time.Now().Add(3 * time.Second)
	var lastMsg map[string]any
	for time.Now().Before(deadlineMsg) {
		msg := readEvent(t, conn, 3*time.Second)
		lastMsg = msg
		if msg["event"] != "topology.delta" {
			t.Fatalf("event = %v, want topology.delta", msg["event"])
		}
		if containsString(msg["updated"], wantRef) {
			return // success
		}
		// Some earlier in-flight poll cycle's delta (e.g. stats-only
		// noise) may have raced in first; keep reading until the specific
		// mutation's delta arrives or the deadline passes.
	}
	t.Fatalf("never observed %q in an 'updated' delta before the deadline; last message: %+v", wantRef, lastMsg)
}

func containsString(v any, want string) bool {
	arr, ok := v.([]any)
	if !ok {
		return false
	}
	for _, e := range arr {
		if e == want {
			return true
		}
	}
	return false
}

// TestIntegration_LoadFiveHundredClients is T-106 acceptance criterion 4's
// second half: 500 concurrent WS clients all subscribed, one of them never
// reading, sustained without the server crashing, blocking, or growing
// memory unboundedly, and every reading client still gets the broadcast.
func TestIntegration_LoadFiveHundredClients(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 500-connection load test in -short mode")
	}
	hub := topology.NewHub(testLogger())
	srv := httptest.NewServer(http.HandlerFunc(hub.ServeWS))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	const clientCount = 500
	conns := make([]*websocket.Conn, clientCount)
	for i := range conns {
		conns[i] = dialWS(t, wsURL)
	}
	defer func() {
		for _, c := range conns {
			_ = c.Close(websocket.StatusNormalClosure, "")
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i, c := range conns {
		if err := c.Write(ctx, websocket.MessageText, []byte(`{"subscribe":["topology"]}`)); err != nil {
			t.Fatalf("client %d subscribe: %v", i, err)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for hub.ConnCount() < clientCount {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for all %d connections to register; got %d", clientCount, hub.ConnCount())
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond) // let every read loop parse its subscribe frame

	// Client 0 is the deliberately slow/non-reading one from here on.
	var wg sync.WaitGroup
	var receivedCount int64
	for i := 1; i < clientCount; i++ {
		wg.Add(1)
		go func(c *websocket.Conn) {
			defer wg.Done()
			rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer rcancel()
			if _, _, err := c.Read(rctx); err == nil {
				atomic.AddInt64(&receivedCount, 1)
			}
		}(conns[i])
	}

	ref := inventory.Ref{Kind: inventory.KindBridge, Node: "n1", ID: "vmbr0"}
	broadcastDone := make(chan struct{})
	go func() {
		hub.BroadcastDelta(inventory.Delta{Added: []inventory.Ref{ref}})
		close(broadcastDone)
	}()
	select {
	case <-broadcastDone:
	case <-time.After(5 * time.Second):
		t.Fatal("BroadcastDelta blocked with 500 clients connected (one non-reading) — did not sustain the load")
	}

	wg.Wait()
	got := atomic.LoadInt64(&receivedCount)
	if got != int64(clientCount-1) {
		t.Errorf("clients that received the broadcast = %d, want %d (the slow client is expected to miss it, not the rest)", got, clientCount-1)
	}
	t.Logf("500-client load test: %d/%d reading clients received the broadcast; server sustained the load without blocking", got, clientCount-1)
}
