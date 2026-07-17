package hostsample

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/store"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("reading testdata/%s: %v", name, err)
	}
	return data
}

func TestParseConntrackTable_Basic(t *testing.T) {
	entries, skipped := ParseConntrackTable(readTestdata(t, "conntrack_basic.txt"))
	if skipped != 0 {
		t.Fatalf("skipped = %d, want 0", skipped)
	}

	want := []ConntrackEntry{
		{Proto: 6, SrcIP: "192.168.1.10", DstIP: "192.168.1.20", SrcPort: 54321, DstPort: 443, Packets: 12, Bytes: 1500},
		{Proto: 17, SrcIP: "192.168.1.11", DstIP: "192.168.1.30", SrcPort: 51000, DstPort: 53, Packets: 1, Bytes: 71},
		{Proto: 1, SrcIP: "192.168.1.50", DstIP: "192.168.1.60", SrcPort: 0, DstPort: 0, Packets: 1, Bytes: 84},
		{Proto: 6, SrcIP: "fd00::1", DstIP: "fd00::2", SrcPort: 1234, DstPort: 22, Packets: 5, Bytes: 700},
	}
	if len(entries) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(entries), len(want), entries)
	}
	for i, e := range entries {
		if e != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, e, want[i])
		}
	}
}

func TestParseConntrackTable_MalformedLinesSkipped(t *testing.T) {
	entries, skipped := ParseConntrackTable(readTestdata(t, "conntrack_malformed.txt"))
	if len(entries) != 2 {
		t.Fatalf("got %d parsed entries, want 2: %+v", len(entries), entries)
	}
	// Skipped: "too few fields" garbage line, "ipv4 2 tcp" (< 4 tokens),
	// the line missing src=, the blank line, and the sport=notaport line —
	// 5 total, mirroring internal/fwlog.ParseAll's Result.Skipped
	// convention (never fails the whole read, just counts what it drops).
	if skipped != 5 {
		t.Fatalf("skipped = %d, want 5", skipped)
	}
}

// fixtureReader serves a fixed sequence of testdata files, one per call —
// simulating successive polls of a live /proc/net/nf_conntrack whose
// counters advance between reads.
type fixtureReader struct {
	files []string
	idx   int
	mu    sync.Mutex
}

func (f *fixtureReader) ReadTable(_ context.Context) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name := f.files[f.idx]
	if f.idx < len(f.files)-1 {
		f.idx++
	}
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func TestConntrackSampler_Sample_FirstPollEmitsAbsoluteCounters(t *testing.T) {
	reader := &fixtureReader{files: []string{"conntrack_basic.txt"}}
	s := NewConntrackSampler(reader, "pve1")
	fixedNow := time.Unix(1700000000, 0)
	s.Now = func() time.Time { return fixedNow }

	records, skipped, err := s.Sample(context.Background())
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if skipped != 0 {
		t.Fatalf("skipped = %d, want 0", skipped)
	}
	if len(records) != 4 {
		t.Fatalf("got %d records, want 4: %+v", len(records), records)
	}
	for _, r := range records {
		if r.Source != flow.SourceConntrack {
			t.Errorf("record %+v: source = %q, want %q", r, r.Source, flow.SourceConntrack)
		}
		if r.Node != "pve1" {
			t.Errorf("record %+v: node = %q, want pve1", r, r.Node)
		}
		if r.At != fixedNow.Unix() {
			t.Errorf("record %+v: at = %d, want %d", r, r.At, fixedNow.Unix())
		}
	}
	// The tcp entry's first-poll record should carry its full current
	// counters (no prior snapshot to diff against).
	found := false
	for _, r := range records {
		if r.SrcIP == "192.168.1.10" && r.DstIP == "192.168.1.20" {
			found = true
			if r.Bytes != 1500 || r.Packets != 12 {
				t.Errorf("first-poll tcp record = %+v, want bytes=1500 packets=12", r)
			}
		}
	}
	if !found {
		t.Fatal("expected a record for the 192.168.1.10->192.168.1.20 connection")
	}
}

func TestConntrackSampler_Sample_DiffsAcrossPolls(t *testing.T) {
	reader := &fixtureReader{files: []string{"conntrack_basic.txt", "conntrack_basic_poll2.txt"}}
	s := NewConntrackSampler(reader, "pve1")

	if _, _, err := s.Sample(context.Background()); err != nil {
		t.Fatalf("first Sample: %v", err)
	}

	records, _, err := s.Sample(context.Background())
	if err != nil {
		t.Fatalf("second Sample: %v", err)
	}

	// Expect exactly two records: the advancing tcp connection (delta) and
	// the brand-new tcp connection (first-seen this poll). The udp
	// connection's counters didn't move (no record); the icmp/ipv6
	// connections vanished between polls (no record, and no longer
	// tracked).
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2: %+v", len(records), records)
	}

	var delta, fresh *flow.Record
	for i := range records {
		r := &records[i]
		switch {
		case r.SrcIP == "192.168.1.10" && r.DstIP == "192.168.1.20":
			delta = r
		case r.SrcIP == "192.168.1.99" && r.DstIP == "192.168.1.100":
			fresh = r
		}
	}
	if delta == nil {
		t.Fatal("expected a delta record for the advancing tcp connection")
	}
	if delta.Packets != 8 || delta.Bytes != 1100 {
		t.Errorf("delta record = %+v, want packets=8 bytes=1100 (20-12, 2600-1500)", *delta)
	}
	if fresh == nil {
		t.Fatal("expected a first-seen record for the new tcp connection")
	}
	if fresh.Packets != 3 || fresh.Bytes != 400 {
		t.Errorf("fresh record = %+v, want packets=3 bytes=400", *fresh)
	}

	// A third poll with unchanged counters (same file again) should yield
	// no records at all — proving per-connection state survived the
	// second poll's prune of vanished connections rather than being reset.
	reader.mu.Lock()
	reader.files = append(reader.files, "conntrack_basic_poll2.txt")
	reader.idx = len(reader.files) - 1
	reader.mu.Unlock()
	stableRecords, _, err := s.Sample(context.Background())
	if err != nil {
		t.Fatalf("third Sample: %v", err)
	}
	if len(stableRecords) != 0 {
		t.Fatalf("third (unchanged) poll produced %d records, want 0: %+v", len(stableRecords), stableRecords)
	}
}

func TestConntrackSampler_Run_TicksAndIngests(t *testing.T) {
	reader := &fixtureReader{files: []string{"conntrack_basic.txt", "conntrack_basic_poll2.txt"}}
	s := NewConntrackSampler(reader, "pve1")

	var mu sync.Mutex
	var batches [][]flow.Record
	ingest := func(_ context.Context, records []flow.Record) {
		mu.Lock()
		defer mu.Unlock()
		batches = append(batches, records)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx, 10*time.Millisecond, ingest) }()

	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := len(batches)
		mu.Unlock()
		if n >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Run did not deliver at least two ingest batches in time")
		case <-time.After(5 * time.Millisecond):
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil after ctx cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancellation")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(batches[0]) != 4 {
		t.Errorf("first ingest batch had %d records, want 4", len(batches[0]))
	}
}

// fakeFlowStore is an in-memory flow.FlowStore double (mirrors internal/
// flow's own fakeStore in service_test.go) — used below to prove
// ConntrackSampler feeds the *same* flow.Service/store path T-1002's
// UDP listeners do, not a second storage mechanism (AC2).
type fakeFlowStore struct {
	samples []store.FlowSample
	mu      sync.Mutex
}

func (f *fakeFlowStore) InsertBatch(_ context.Context, samples []store.FlowSample) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.samples = append(f.samples, samples...)
	return nil
}

func (f *fakeFlowStore) Query(_ context.Context, filter store.FlowFilter, _ string, limit int) ([]store.FlowSample, string, error) {
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

func (f *fakeFlowStore) PruneOlderThan(_ context.Context, _ int64) (int64, error) { return 0, nil }
func (f *fakeFlowStore) PruneToCap(_ context.Context, _ int64) (int64, error)     { return 0, nil }

// TestConntrackSampler_FeedsSharedFlowServiceRing is AC2: enabling
// conntrack sampling and feeding its output through flow.Service.Ingest —
// exactly the ingest closure cmd/vnproxd's setupFlows/setupHostSample wire
// every source through — must make the records visible via
// flow.Service.Query, the same read path GET /flows and GET /api/peer/flows
// both use. No second storage path.
func TestConntrackSampler_FeedsSharedFlowServiceRing(t *testing.T) {
	fs := &fakeFlowStore{}
	svc := flow.New(flow.Config{Store: fs})

	reader := &fixtureReader{files: []string{"conntrack_basic.txt"}}
	sampler := NewConntrackSampler(reader, "pve1")

	records, _, err := sampler.Sample(context.Background())
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	svc.Ingest(context.Background(), records)

	got, _, err := svc.Query(context.Background(), flow.Filter{}, "", 100)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != len(records) {
		t.Fatalf("Query returned %d records, want %d", len(got), len(records))
	}
	for _, r := range got {
		if r.Source != flow.SourceConntrack {
			t.Errorf("queried record %+v: source = %q, want %q", r, r.Source, flow.SourceConntrack)
		}
	}
}
