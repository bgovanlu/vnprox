package flow

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/store"
)

// fakeStore is an in-memory FlowStore double for tests, avoiding a real
// SQLite file — the same pattern internal/metrics' tests use for
// MetricStore.
type fakeStore struct {
	samples []store.FlowSample
	mu      sync.Mutex
}

func (f *fakeStore) InsertBatch(_ context.Context, samples []store.FlowSample) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.samples = append(f.samples, samples...)
	return nil
}

func (f *fakeStore) Query(_ context.Context, filter store.FlowFilter, _ string, limit int) ([]store.FlowSample, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.FlowSample
	for _, s := range f.samples {
		if filter.Guest != "" && s.SrcRef != filter.Guest && s.DstRef != filter.Guest {
			continue
		}
		out = append(out, s)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, "", nil
}

func (f *fakeStore) PruneOlderThan(_ context.Context, cutoff int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var kept []store.FlowSample
	var removed int64
	for _, s := range f.samples {
		if s.At < cutoff {
			removed++
			continue
		}
		kept = append(kept, s)
	}
	f.samples = kept
	return removed, nil
}

func (f *fakeStore) PruneToCap(_ context.Context, maxRows int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if maxRows <= 0 || int64(len(f.samples)) <= maxRows {
		return 0, nil
	}
	removed := int64(len(f.samples)) - maxRows
	f.samples = f.samples[removed:]
	return removed, nil
}

func (f *fakeStore) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.samples)
}

// fakeBroadcaster captures every Broadcast call for assertions.
type fakeBroadcaster struct {
	calls []struct {
		topic   string
		payload []byte
	}
	mu sync.Mutex
}

func (b *fakeBroadcaster) Broadcast(topic string, payload []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, struct {
		topic   string
		payload []byte
	}{topic, payload})
}

func (b *fakeBroadcaster) last() (string, []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.calls) == 0 {
		return "", nil
	}
	c := b.calls[len(b.calls)-1]
	return c.topic, c.payload
}

func (b *fakeBroadcaster) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.calls)
}

func TestService_Ingest_PersistsAndBroadcasts(t *testing.T) {
	fs := &fakeStore{}
	fb := &fakeBroadcaster{}
	svc := New(Config{Store: fs, WS: fb})

	records := []Record{
		{At: 1, Node: "pve1", SrcIP: "10.0.0.1", DstIP: "10.0.0.2", Source: SourceNetFlow5},
		{At: 2, Node: "pve1", SrcIP: "10.0.0.3", DstIP: "10.0.0.4", Source: SourceSFlow},
	}
	svc.Ingest(context.Background(), records)

	if fs.count() != 2 {
		t.Fatalf("store has %d samples, want 2", fs.count())
	}
	topic, payload := fb.last()
	if topic != TopicFlows {
		t.Fatalf("broadcast topic = %q, want %q", topic, TopicFlows)
	}
	var evt batchEvent
	if err := json.Unmarshal(payload, &evt); err != nil {
		t.Fatalf("unmarshaling broadcast payload: %v", err)
	}
	if evt.Event != "flow.batch" {
		t.Fatalf("event = %q, want flow.batch", evt.Event)
	}
	if len(evt.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(evt.Entries))
	}
	if evt.DroppedTotal != 0 {
		t.Fatalf("droppedTotal = %d, want 0", evt.DroppedTotal)
	}
}

func TestService_Ingest_NilResolverLeavesRefsEmpty(t *testing.T) {
	fs := &fakeStore{}
	svc := New(Config{Store: fs})
	svc.Ingest(context.Background(), []Record{{At: 1, SrcIP: "10.0.0.1", DstIP: "10.0.0.2"}})
	if fs.samples[0].SrcRef != "" || fs.samples[0].DstRef != "" {
		t.Fatalf("expected empty refs with no resolver wired, got src=%q dst=%q", fs.samples[0].SrcRef, fs.samples[0].DstRef)
	}
}

func TestService_Ingest_ResolvesRefs(t *testing.T) {
	fs := &fakeStore{}
	// A minimal stub Resolver is enough to prove Service wires resolution
	// through on every ingested record; GraphResolver's own resolution
	// logic is covered by resolve_test.go.
	svc := New(Config{Store: fs, Resolver: stubResolver{"10.0.0.1": "bridge:pve1:vmbr0"}})
	svc.Ingest(context.Background(), []Record{{At: 1, SrcIP: "10.0.0.1", DstIP: "10.0.0.9"}})
	if fs.samples[0].SrcRef != "bridge:pve1:vmbr0" {
		t.Fatalf("SrcRef = %q, want bridge:pve1:vmbr0", fs.samples[0].SrcRef)
	}
	if fs.samples[0].DstRef != "" {
		t.Fatalf("DstRef = %q, want empty (unresolved, never guessed)", fs.samples[0].DstRef)
	}
}

type stubResolver map[string]string

func (s stubResolver) Resolve(ip string) (string, bool) {
	ref, ok := s[ip]
	return ref, ok
}

func TestService_Broadcast_RateCapped(t *testing.T) {
	fb := &fakeBroadcaster{}
	svc := New(Config{WS: fb, MaxBroadcastPerBatch: 3})

	var records []Record
	for i := 0; i < 10; i++ {
		records = append(records, Record{At: int64(i), SrcIP: "10.0.0.1", DstIP: "10.0.0.2"})
	}
	svc.Ingest(context.Background(), records)

	_, payload := fb.last()
	var evt batchEvent
	if err := json.Unmarshal(payload, &evt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(evt.Entries) != 3 {
		t.Fatalf("entries = %d, want 3 (rate-capped)", len(evt.Entries))
	}
	if evt.DroppedTotal != 7 {
		t.Fatalf("droppedTotal = %d, want 7", evt.DroppedTotal)
	}
	if svc.DroppedTotal() != 7 {
		t.Fatalf("Service.DroppedTotal() = %d, want 7", svc.DroppedTotal())
	}

	// A second storm accumulates droppedTotal rather than resetting it.
	svc.Ingest(context.Background(), records)
	if svc.DroppedTotal() != 14 {
		t.Fatalf("Service.DroppedTotal() after second storm = %d, want 14", svc.DroppedTotal())
	}
}

func TestService_Ingest_EmptyIsNoop(t *testing.T) {
	fs := &fakeStore{}
	fb := &fakeBroadcaster{}
	svc := New(Config{Store: fs, WS: fb})
	svc.Ingest(context.Background(), nil)
	if fs.count() != 0 || fb.count() != 0 {
		t.Fatal("expected no store writes or broadcasts for an empty batch")
	}
}

func TestService_Query_NilStoreReturnsEmpty(t *testing.T) {
	svc := New(Config{})
	items, next, err := svc.Query(context.Background(), Filter{}, "", 10)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if items != nil || next != "" {
		t.Fatalf("expected empty result with no store wired, got items=%v next=%q", items, next)
	}
}

func TestService_RunPruneLoop_EnforcesBound(t *testing.T) {
	now := time.Now().Unix()
	fs := &fakeStore{}
	fs.samples = []store.FlowSample{
		{ID: 1, At: now - 3}, {ID: 2, At: now - 2}, {ID: 3, At: now - 1},
	}
	// RetentionMinutes generous (60m default) so none of the fresh samples
	// above are pruned by age — only the hard row cap (2) should apply.
	svc := New(Config{Store: fs, MaxRows: 2})

	ctx, cancel := context.WithCancel(context.Background())
	// prune() is called synchronously once by RunPruneLoop before it would
	// block on the ticker — cancel immediately so Run returns after that
	// single priming call, keeping this test deterministic without a real
	// wall-clock wait.
	cancel()
	if err := svc.RunPruneLoop(ctx, 0); err != nil {
		t.Fatalf("RunPruneLoop: %v", err)
	}
	if fs.count() != 2 {
		t.Fatalf("expected the hard row cap (2) to be enforced, got %d rows", fs.count())
	}
}
