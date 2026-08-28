// SPDX-License-Identifier: Apache-2.0

package topology_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func dialWS(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("websocket.Dial: %v", err)
	}
	return c
}

func readEvent(t *testing.T, c *websocket.Conn, timeout time.Duration) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("ws Read: %v", err)
	}
	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal ws message %s: %v", data, err)
	}
	return msg
}

// TestHub_ServeWS_SubscribeAndBroadcast drives real WS clients against
// Hub.ServeWS end to end: a client that subscribes to "topology" receives a
// topology.delta event shaped exactly per docs/api.md's `{added, updated,
// removed: [Ref]}`; a client that never subscribes gets nothing.
func TestHub_ServeWS_SubscribeAndBroadcast(t *testing.T) {
	hub := topology.NewHub(testLogger())
	srv := httptest.NewServer(http.HandlerFunc(hub.ServeWS))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	subscribed := dialWS(t, wsURL)
	defer func() { _ = subscribed.Close(websocket.StatusNormalClosure, "") }()
	unsubscribed := dialWS(t, wsURL)
	defer func() { _ = unsubscribed.Close(websocket.StatusNormalClosure, "") }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := subscribed.Write(ctx, websocket.MessageText, []byte(`{"subscribe":["topology"]}`)); err != nil {
		t.Fatalf("subscribe write: %v", err)
	}

	// Give the server a moment to process the subscribe frame before
	// broadcasting (there is no ack in the protocol).
	waitForConns(t, hub, 2)

	ref := inventory.Ref{Kind: inventory.KindBridge, Node: "n1", ID: "vmbr0"}
	hub.BroadcastDelta(inventory.Delta{Added: []inventory.Ref{ref}})

	msg := readEvent(t, subscribed, 2*time.Second)
	if msg["event"] != "topology.delta" {
		t.Fatalf("event = %v, want topology.delta", msg["event"])
	}
	added, ok := msg["added"].([]any)
	if !ok || len(added) != 1 || added[0] != ref.String() {
		t.Fatalf("added = %v, want [%q]", msg["added"], ref.String())
	}
	if _, ok := msg["updated"]; !ok {
		t.Errorf("expected an 'updated' key even when empty; got %v", msg)
	}
	if _, ok := msg["removed"]; !ok {
		t.Errorf("expected a 'removed' key even when empty; got %v", msg)
	}

	// The never-subscribed connection must not receive it. Read with a
	// short timeout and expect it to time out (context deadline).
	roCtx, roCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer roCancel()
	if _, _, err := unsubscribed.Read(roCtx); err == nil {
		t.Error("expected the unsubscribed connection to receive nothing, but it read a message")
	}
}

// TestHub_SlowClientDropsInsteadOfBlocking is T-106's "a slow WS client
// must not block or crash the server" requirement: a client that never
// reads must not stop other clients (or the broadcaster) from making
// progress.
func TestHub_SlowClientDropsInsteadOfBlocking(t *testing.T) {
	hub := topology.NewHub(testLogger())
	srv := httptest.NewServer(http.HandlerFunc(hub.ServeWS))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	slow := dialWS(t, wsURL)
	defer func() { _ = slow.Close(websocket.StatusNormalClosure, "") }()
	fast := dialWS(t, wsURL)
	defer func() { _ = fast.Close(websocket.StatusNormalClosure, "") }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sub := []byte(`{"subscribe":["topology"]}`)
	if err := slow.Write(ctx, websocket.MessageText, sub); err != nil {
		t.Fatalf("slow subscribe: %v", err)
	}
	if err := fast.Write(ctx, websocket.MessageText, sub); err != nil {
		t.Fatalf("fast subscribe: %v", err)
	}
	waitForConns(t, hub, 2)

	// The "slow" client just never reads again from here on. Flood far
	// more broadcasts than the outbound queue can hold; none of this may
	// block (the test itself would hang if BroadcastDelta ever blocked).
	done := make(chan struct{})
	go func() {
		for i := 0; i < 500; i++ {
			hub.BroadcastDelta(inventory.Delta{Added: []inventory.Ref{{Kind: inventory.KindBridge, Node: "n1", ID: "x"}}})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("BroadcastDelta blocked with a non-reading client connected — slow-client drop is not working")
	}

	// The fast client (which DOES keep reading, draining as fast as
	// broadcasts arrive) must still receive events without the slow
	// client's non-reading having stalled it.
	_ = readEvent(t, fast, 2*time.Second)
}

// waitForConns polls until the hub reports at least n live connections
// (readiness signal before relying on a subscribe frame having been
// processed).
func waitForConns(t *testing.T, hub *topology.Hub, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if hub.ConnCount() >= n {
			// Give the read loop a beat to actually parse the subscribe
			// frame before the caller broadcasts.
			time.Sleep(50 * time.Millisecond)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d ws connections to register with the hub", n)
}
