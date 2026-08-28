// SPDX-License-Identifier: Apache-2.0

package fwlog

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/peer"
)

// fakePeerSource is a PeerSource test double: a fixed peer list, each
// peer's own lines served from an in-memory per-node buffer via
// MemorySource, with an optional forced error per node.
type fakePeerSource struct {
	mem     *MemorySource
	failing map[string]error
	peers   []peer.Peer
	mu      sync.Mutex
}

func newFakePeerSource(mem *MemorySource, peers ...peer.Peer) *fakePeerSource {
	return &fakePeerSource{peers: peers, mem: mem, failing: map[string]error{}}
}

func (f *fakePeerSource) Peers(_ context.Context) ([]peer.Peer, error) {
	return f.peers, nil
}

func (f *fakePeerSource) FirewallLog(ctx context.Context, p peer.Peer, node, cursor string, maxLines int) ([]string, string, error) {
	f.mu.Lock()
	err := f.failing[node]
	f.mu.Unlock()
	if err != nil {
		return nil, cursor, err
	}
	lines, next, _, terr := f.mem.Tail(ctx, node, cursor, maxLines)
	return lines, next, terr
}

func (f *fakePeerSource) setFailing(node string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.failing, node)
		return
	}
	f.failing[node] = err
}

// staticNode returns a Config.LocalNode-shaped closure that always
// answers name — the test-only stand-in for cmd/vnproxd's real
// collector.Status().LocalNode-backed closure (see Config.LocalNode's doc
// comment for why production uses a func, not a plain string).
func staticNode(name string) func() string {
	return func() string { return name }
}

type fakeSnapshotSource struct{ snap fw.Snapshot }

func (f fakeSnapshotSource) FirewallSnapshot() fw.Snapshot { return f.snap }

type fakeBroadcaster struct {
	events []capturedEvent
	mu     sync.Mutex
}

type capturedEvent struct {
	Topic   string
	Payload []byte
}

func (b *fakeBroadcaster) Broadcast(topic string, payload []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, capturedEvent{Topic: topic, Payload: append([]byte(nil), payload...)})
}

func (b *fakeBroadcaster) snapshot() []capturedEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]capturedEvent(nil), b.events...)
}

func TestService_TickLocalOnly(t *testing.T) {
	local := NewMemorySource()
	local.Seed("pve1", "100 4 tap100i0-IN 10/Jul/2026:12:00:01 +0000 ACCEPT: SRC=1.1.1.1 DST=2.2.2.2\nnoise noise noise\n")

	svc := New(Config{Local: local, LocalNode: staticNode("pve1"), BufferCapacity: 10})
	res := svc.Tick(context.Background())

	if res.Parsed != 1 || res.GarbageSkipped != 1 {
		t.Fatalf("Parsed/GarbageSkipped = %d/%d, want 1/1", res.Parsed, res.GarbageSkipped)
	}
	if res.Broadcast != 1 {
		t.Fatalf("Broadcast = %d, want 1", res.Broadcast)
	}
	page := svc.TailPage(Filter{}, 10)
	if len(page.Items) != 1 {
		t.Fatalf("TailPage items = %d, want 1", len(page.Items))
	}
}

func TestService_TickWithPeerFanout(t *testing.T) {
	local := NewMemorySource()
	local.Seed("pve1", "100 4 tap100i0-IN 10/Jul/2026:12:00:01 +0000 ACCEPT: SRC=1.1.1.1 DST=2.2.2.2\n")

	peerMem := NewMemorySource()
	peerMem.Seed("pve2", "200 4 tap200i0-IN 10/Jul/2026:12:00:01 +0000 DROP: SRC=3.3.3.3 DST=4.4.4.4\n")
	peers := newFakePeerSource(peerMem, peer.Peer{Node: "pve2", Addr: "10.0.0.2:8007"})

	svc := New(Config{Local: local, LocalNode: staticNode("pve1"), Peers: peers, BufferCapacity: 10})
	res := svc.Tick(context.Background())

	if res.Parsed != 2 {
		t.Fatalf("Parsed = %d, want 2 (one local + one peer)", res.Parsed)
	}
	page := svc.TailPage(Filter{}, 10)
	nodes := map[string]bool{}
	for _, it := range page.Items {
		nodes[it.Entry.Node] = true
	}
	if !nodes["pve1"] || !nodes["pve2"] {
		t.Fatalf("expected entries from both pve1 and pve2, got nodes=%v", nodes)
	}
}

func TestService_NodeErrorTracking(t *testing.T) {
	local := NewMemorySource()
	local.Seed("pve1", "")
	peerMem := NewMemorySource()
	peers := newFakePeerSource(peerMem, peer.Peer{Node: "pve2"})
	peers.setFailing("pve2", fmt.Errorf("peer unreachable"))

	svc := New(Config{Local: local, LocalNode: staticNode("pve1"), Peers: peers, BufferCapacity: 10})
	svc.Tick(context.Background())

	page := svc.TailPage(Filter{}, 10)
	if len(page.UnavailableNodes) != 1 || page.UnavailableNodes[0] != "pve2" {
		t.Fatalf("UnavailableNodes = %v, want [pve2]", page.UnavailableNodes)
	}

	// Recovery: once the peer succeeds again, it must drop off the
	// unavailable list.
	peerMem.Seed("pve2", "")
	peers.setFailing("pve2", nil)
	svc.Tick(context.Background())
	page = svc.TailPage(Filter{}, 10)
	if len(page.UnavailableNodes) != 0 {
		t.Fatalf("UnavailableNodes = %v, want empty after recovery", page.UnavailableNodes)
	}
}

func TestService_RateCapAndDropIndicator(t *testing.T) {
	local := NewMemorySource()
	local.Seed("pve1", "")

	svc := New(Config{Local: local, LocalNode: staticNode("pve1"), BufferCapacity: 500, MaxBroadcastPerTick: 10})

	var lines []string
	for i := 0; i < 25; i++ {
		lines = append(lines, fmt.Sprintf("100 4 tap100i0-IN 10/Jul/2026:12:00:%02d +0000 ACCEPT: SRC=1.1.1.1 DST=2.2.2.2", i))
	}
	local.Append("pve1", lines...)

	res := svc.Tick(context.Background())
	if res.Parsed != 25 {
		t.Fatalf("Parsed = %d, want 25", res.Parsed)
	}
	if res.Broadcast != 10 {
		t.Fatalf("Broadcast = %d, want 10 (MaxBroadcastPerTick)", res.Broadcast)
	}
	if res.Dropped != 15 {
		t.Fatalf("Dropped = %d, want 15", res.Dropped)
	}
	if svc.DroppedTotal() != 15 {
		t.Fatalf("DroppedTotal() = %d, want 15", svc.DroppedTotal())
	}
}

// TestService_Storm_10kLinesPerMinute covers AC3: a storm fixture far
// exceeding 10k lines/min (this test feeds 10,000 lines total, well above
// what a 1s-poll-interval Service would see in a single minute at that
// rate) must engage the rate cap/drop indicator and keep the buffer
// bounded — with no real wall-clock wait: Tick is called directly, back
// to back, simulating many poll intervals' worth of storm traffic in a
// fraction of a second, which is exactly why Tick is factored out of Run's
// ticker loop (see Service.Tick's doc comment).
func TestService_Storm_10kLinesPerMinute(t *testing.T) {
	const totalLines = 10_000
	const linesPerTick = 200 // ~ the volume one 1s poll tick would see at a 10k-lines/min storm rate (10000/60 ≈ 167/s)
	const bufferCap = 300

	local := NewMemorySource()
	local.Seed("pve1", "")
	svc := New(Config{
		Local: local, LocalNode: staticNode("pve1"),
		BufferCapacity: bufferCap, MaxBroadcastPerTick: 50, // deliberately much lower than linesPerTick, so the storm visibly exceeds it
	})

	start := time.Now()
	ctx := context.Background()
	totalParsed, totalDropped := 0, 0
	for sent := 0; sent < totalLines; sent += linesPerTick {
		batch := make([]string, 0, linesPerTick)
		for i := 0; i < linesPerTick; i++ {
			batch = append(batch, fmt.Sprintf("100 4 tap100i0-IN 10/Jul/2026:12:00:00 +0000 ACCEPT: SRC=1.1.1.1 DST=2.2.2.2 n=%d", sent+i))
		}
		local.Append("pve1", batch...)
		res := svc.Tick(ctx)
		totalParsed += res.Parsed
		totalDropped += res.Dropped
	}
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Fatalf("processing a 10k-line storm took %s — too slow, risks a real UI/poll-loop lockup", elapsed)
	}
	if totalParsed != totalLines {
		t.Fatalf("totalParsed = %d, want %d (every line must still be parsed even if not all are broadcast)", totalParsed, totalLines)
	}
	if totalDropped == 0 {
		t.Fatal("totalDropped = 0, want > 0 — the storm must engage the rate cap")
	}
	if svc.DroppedTotal() != int64(totalDropped) {
		t.Fatalf("DroppedTotal() = %d, want %d", svc.DroppedTotal(), totalDropped)
	}
	if svc.buf.Len() > bufferCap {
		t.Fatalf("buffer length = %d, exceeds its own capacity %d — unbounded growth", svc.buf.Len(), bufferCap)
	}
	page := svc.TailPage(Filter{}, 0)
	if len(page.Items) > bufferCap {
		t.Fatalf("TailPage returned %d items, exceeds buffer capacity %d", len(page.Items), bufferCap)
	}
	if page.DroppedTotal == 0 {
		t.Fatal("Page.DroppedTotal = 0, want > 0 so the UI can show the drop indicator")
	}
}

func TestService_TailPage_FilterAndOrder(t *testing.T) {
	local := NewMemorySource()
	local.Seed("pve1", "")
	svc := New(Config{Local: local, LocalNode: staticNode("pve1"), BufferCapacity: 100})

	local.Append("pve1",
		"100 4 tap100i0-IN 10/Jul/2026:12:00:01 +0000 ACCEPT: SRC=1.1.1.1 DST=2.2.2.2",
		"200 4 tap200i0-OUT 10/Jul/2026:12:00:02 +0000 DROP: SRC=3.3.3.3 DST=4.4.4.4",
		"100 4 tap100i0-IN 10/Jul/2026:12:00:03 +0000 DROP: SRC=5.5.5.5 DST=6.6.6.6",
	)
	svc.Tick(context.Background())

	page := svc.TailPage(Filter{VMID: 100}, 10)
	if len(page.Items) != 2 {
		t.Fatalf("filtered by vmid=100: got %d items, want 2", len(page.Items))
	}
	// Oldest-first ordering.
	if page.Items[0].Seq >= page.Items[1].Seq {
		t.Fatalf("items not oldest-first: seqs = %d, %d", page.Items[0].Seq, page.Items[1].Seq)
	}

	page = svc.TailPage(Filter{Action: "drop"}, 10)
	if len(page.Items) != 2 {
		t.Fatalf("filtered by action=drop: got %d items, want 2", len(page.Items))
	}

	page = svc.TailPage(Filter{Direction: "out"}, 10)
	if len(page.Items) != 1 || page.Items[0].Entry.VMID != 200 {
		t.Fatalf("filtered by direction=out: %+v", page.Items)
	}

	limited := svc.TailPage(Filter{}, 1)
	if len(limited.Items) != 1 {
		t.Fatalf("limit=1: got %d items", len(limited.Items))
	}
}

func TestService_Run_NilLocalIsNoOp(t *testing.T) {
	svc := New(Config{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := svc.Run(ctx); err != nil {
		t.Fatalf("Run with nil Local returned %v, want nil", err)
	}
}

func TestService_Run_StopsOnContextCancel(t *testing.T) {
	local := NewMemorySource()
	local.Seed("pve1", "")
	svc := New(Config{Local: local, LocalNode: staticNode("pve1"), PollInterval: 5 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop within 2s of context cancellation")
	}
}

func TestService_Broadcast_EventShape(t *testing.T) {
	local := NewMemorySource()
	local.Seed("pve1", "100 4 tap100i0-IN 10/Jul/2026:12:00:01 +0000 ACCEPT: SRC=1.1.1.1 DST=2.2.2.2\n")
	bc := &fakeBroadcaster{}
	svc := New(Config{Local: local, LocalNode: staticNode("pve1"), WS: bc, BufferCapacity: 10})
	svc.Tick(context.Background())

	events := bc.snapshot()
	if len(events) != 1 {
		t.Fatalf("broadcast events = %d, want 1", len(events))
	}
	if events[0].Topic != TopicFirewallLog {
		t.Fatalf("topic = %q, want %q", events[0].Topic, TopicFirewallLog)
	}
	var decoded batchEvent
	if err := json.Unmarshal(events[0].Payload, &decoded); err != nil {
		t.Fatalf("decoding payload: %v", err)
	}
	if decoded.Event != "firewall.log.batch" {
		t.Fatalf("event name = %q, want firewall.log.batch", decoded.Event)
	}
	if len(decoded.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(decoded.Entries))
	}
	if decoded.Entries[0].GuestRef != "guest:pve1:100" {
		t.Fatalf("guestRef = %q, want guest:pve1:100", decoded.Entries[0].GuestRef)
	}
}

func TestService_CorrelateWithSnapshot(t *testing.T) {
	guest := guestRef100()
	snap := fw.Snapshot{
		Guests: map[inventory.Ref]*inventory.FwRuleset{
			guest: {
				Ref: inventory.Ref{Kind: inventory.KindFwRuleset, Node: "pve1", ID: "guest/qemu/100"}, Scope: inventory.FwScopeGuest, Enabled: true,
				Rules: []inventory.FwRule{
					{Pos: 0, Enabled: true, Direction: "in", Action: "DROP"},
				},
			},
		},
	}

	local := NewMemorySource()
	local.Seed("pve1", "100 4 tap100i0-IN 10/Jul/2026:12:00:01 +0000 DROP: SRC=1.1.1.1 DST=2.2.2.2\n")
	svc := New(Config{Local: local, LocalNode: staticNode("pve1"), Snapshot: fakeSnapshotSource{snap: snap}, BufferCapacity: 10})
	svc.Tick(context.Background())

	page := svc.TailPage(Filter{}, 10)
	if len(page.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(page.Items))
	}
	corr := page.Items[0].Correlation
	if corr.Status != StatusRule {
		t.Fatalf("Correlation.Status = %s, want %s (reason: %s)", corr.Status, StatusRule, corr.Reason)
	}
	if corr.Rule == nil || corr.Rule.Pos != 0 || corr.Rule.GuestRef != guest.String() {
		t.Fatalf("Correlation.Rule = %+v, want pos=0 guestRef=%s", corr.Rule, guest.String())
	}
}

func TestService_CorrelateNoGuestDataAtAll(t *testing.T) {
	local := NewMemorySource()
	local.Seed("pve1", "999 4 tap999i0-IN 10/Jul/2026:12:00:01 +0000 DROP: SRC=1.1.1.1 DST=2.2.2.2\n")
	svc := New(Config{Local: local, LocalNode: staticNode("pve1"), BufferCapacity: 10}) // no Snapshot configured at all
	svc.Tick(context.Background())

	page := svc.TailPage(Filter{}, 10)
	if len(page.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(page.Items))
	}
	if page.Items[0].Correlation.Status != StatusNoGuestData {
		t.Fatalf("Correlation.Status = %s, want %s", page.Items[0].Correlation.Status, StatusNoGuestData)
	}
}
