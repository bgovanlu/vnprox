package metrics_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/metrics"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// This test lives in the metrics_test (external) package rather than
// metrics's own internal test package: it exercises the real production WS
// handler (*topology.Hub.ServeWS, the exact handler internal/api mounts at
// /api/ws) end to end alongside a real *metrics.Sampler, which requires
// importing both internal/topology and internal/metrics as a regular
// caller would — nothing here reaches into either package's unexported
// internals.

func testLoadLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestSampler_WSLoad_200SubscribedEntitiesNoBackpressureCollapse covers
// AC4: "WS streams only subscribed refs; 200 subscribed entities sustained
// without backpressure collapse (load test)." 200 real WebSocket clients
// each subscribe to exactly one entity's "metrics:<ref>" topic (never any
// other client's); the sampler is driven through two ingest ticks (seeding,
// then a real rate-producing tick) across all 200 entities, and every
// client must receive exactly its own metrics.sample event, promptly,
// with no broadcaster stall — the hub's non-blocking enqueue (topology/
// hub.go) is what prevents backpressure collapse, this test proves it holds
// up at the documented scale.
func TestSampler_WSLoad_200SubscribedEntitiesNoBackpressureCollapse(t *testing.T) {
	const n = 200

	hub := topology.NewHub(testLoadLogger())
	srv := httptest.NewServer(http.HandlerFunc(hub.ServeWS))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	sampler := metrics.New(metrics.Config{WS: hub, Logger: testLoadLogger()})

	type client struct {
		conn *websocket.Conn
		ref  string
	}
	clients := make([]client, n)

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer dialCancel()
	for i := 0; i < n; i++ {
		node := fmt.Sprintf("pve%d", i)
		ref := inventory.Ref{Kind: inventory.KindPhysNic, Node: node, ID: "eth0"}.String()
		c, _, err := websocket.Dial(dialCtx, wsURL, nil)
		if err != nil {
			t.Fatalf("dial client %d: %v", i, err)
		}
		if err := c.Write(dialCtx, websocket.MessageText, []byte(`{"subscribe":["metrics:`+ref+`"]}`)); err != nil {
			t.Fatalf("subscribe client %d: %v", i, err)
		}
		clients[i] = client{conn: c, ref: ref}
	}
	defer func() {
		for _, c := range clients {
			_ = c.conn.Close(websocket.StatusNormalClosure, "")
		}
	}()

	// Wait for the hub to register every connection and finish parsing its
	// subscribe frame before broadcasting anything.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && hub.ConnCount() < n {
		time.Sleep(10 * time.Millisecond)
	}
	if got := hub.ConnCount(); got < n {
		t.Fatalf("hub.ConnCount() = %d, want >= %d", got, n)
	}
	time.Sleep(100 * time.Millisecond) // let read loops finish parsing subscribes

	ctx := context.Background()
	base := time.Unix(1_700_000_000, 0)

	// Seed tick.
	for i := 0; i < n; i++ {
		node := fmt.Sprintf("pve%d", i)
		links := []host.LinkState{{Kind: "physical", Name: "eth0", SpeedMbps: 1000, LinkUp: true}}
		sampler.Ingest(ctx, node, base, links, map[string]host.IfaceStats{"eth0": {RxBytes: 0}})
	}
	// Rate-producing tick, 5s later: every entity gets a distinct nonzero
	// delta so each client's event is trivially distinguishable from noise.
	for i := 0; i < n; i++ {
		node := fmt.Sprintf("pve%d", i)
		links := []host.LinkState{{Kind: "physical", Name: "eth0", SpeedMbps: 1000, LinkUp: true}}
		sampler.Ingest(ctx, node, base.Add(5*time.Second), links, map[string]host.IfaceStats{
			"eth0": {RxBytes: uint64(1000 + i)},
		})
	}

	// Every client should receive exactly its own event, promptly and
	// concurrently — proving the broadcaster didn't serialize/stall on any
	// one slow-to-drain connection.
	var wg sync.WaitGroup
	errs := make(chan string, n)
	start := time.Now()
	for _, c := range clients {
		wg.Add(1)
		go func(c client) {
			defer wg.Done()
			readCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, data, err := c.conn.Read(readCtx)
			if err != nil {
				errs <- fmt.Sprintf("client for %s: read: %v", c.ref, err)
				return
			}
			var evt struct {
				Event string `json:"event"`
				Ref   string `json:"ref"`
				At    int64  `json:"at"`
				Rates struct {
					RxBps float64 `json:"rxBps"`
				} `json:"rates"`
			}
			if err := json.Unmarshal(data, &evt); err != nil {
				errs <- fmt.Sprintf("client for %s: unmarshal: %v", c.ref, err)
				return
			}
			if evt.Event != "metrics.sample" {
				errs <- fmt.Sprintf("client for %s: event = %q, want metrics.sample", c.ref, evt.Event)
				return
			}
			if evt.Ref != c.ref {
				errs <- fmt.Sprintf("client for %s: received event for %s (subscription-scoping leak)", c.ref, evt.Ref)
				return
			}
			if evt.Rates.RxBps <= 0 {
				errs <- fmt.Sprintf("client for %s: rxBps = %v, want > 0", c.ref, evt.Rates.RxBps)
			}
		}(c)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for all 200 clients to receive their event — backpressure collapse")
	}
	elapsed := time.Since(start)

	close(errs)
	var failures []string
	for e := range errs {
		failures = append(failures, e)
	}
	if len(failures) > 0 {
		t.Fatalf("%d/%d clients failed:\n%s", len(failures), n, strings.Join(failures, "\n"))
	}
	t.Logf("all %d subscribed clients received their scoped event in %v", n, elapsed)
}
