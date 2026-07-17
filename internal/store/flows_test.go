package store

import (
	"context"
	"testing"
)

func sampleFlow(at int64, node, srcIP, dstIP string, srcPort, dstPort int) FlowSample {
	return FlowSample{
		At: at, Node: node, SrcIP: srcIP, DstIP: dstIP,
		SrcPort: srcPort, DstPort: dstPort, Proto: 6, Bytes: 100, Packets: 1,
		Source: "netflow5",
	}
}

func TestFlowSampleRepo_InsertAndQuery(t *testing.T) {
	db := openTestDB(t)
	repo := NewFlowSampleRepo(db)
	ctx := context.Background()

	samples := []FlowSample{
		sampleFlow(100, "pve1", "10.0.0.1", "10.0.0.2", 1000, 80),
		sampleFlow(200, "pve1", "10.0.0.3", "10.0.0.4", 2000, 443),
		sampleFlow(300, "pve2", "10.0.1.1", "10.0.1.2", 3000, 53),
	}
	if err := repo.InsertBatch(ctx, samples); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	n, err := repo.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 3 {
		t.Fatalf("Count = %d, want 3", n)
	}

	items, next, err := repo.Query(ctx, FlowFilter{}, "", 10)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if next != "" {
		t.Fatalf("expected no next cursor, got %q", next)
	}
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	// Newest first.
	if items[0].At != 300 || items[1].At != 200 || items[2].At != 100 {
		t.Fatalf("unexpected order: %+v", items)
	}
}

func TestFlowSampleRepo_Query_Pagination(t *testing.T) {
	db := openTestDB(t)
	repo := NewFlowSampleRepo(db)
	ctx := context.Background()

	var samples []FlowSample
	for i := int64(0); i < 25; i++ {
		samples = append(samples, sampleFlow(1000+i, "pve1", "10.0.0.1", "10.0.0.2", 100, 200))
	}
	if err := repo.InsertBatch(ctx, samples); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	var all []FlowSample
	cursor := ""
	for {
		page, next, err := repo.Query(ctx, FlowFilter{}, cursor, 10)
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		all = append(all, page...)
		if next == "" {
			break
		}
		cursor = next
		if len(all) > 100 {
			t.Fatal("pagination did not terminate")
		}
	}
	if len(all) != 25 {
		t.Fatalf("got %d total items across pages, want 25", len(all))
	}
	// No duplicates, strictly descending by At.
	seen := map[int64]bool{}
	for i, s := range all {
		if seen[s.At] {
			t.Fatalf("duplicate at=%d in paginated results", s.At)
		}
		seen[s.At] = true
		if i > 0 && all[i-1].At <= s.At {
			t.Fatalf("results not strictly descending at index %d", i)
		}
	}
}

func TestFlowSampleRepo_Query_Filters(t *testing.T) {
	db := openTestDB(t)
	repo := NewFlowSampleRepo(db)
	ctx := context.Background()

	samples := []FlowSample{
		{At: 100, Node: "pve1", SrcIP: "10.0.0.5", DstIP: "10.0.0.6", SrcPort: 1000, DstPort: 80, Proto: 6, VLAN: 10, SrcRef: "bridge:pve1:vmbr0", Source: "netflow5"},
		{At: 200, Node: "pve1", SrcIP: "192.168.1.5", DstIP: "192.168.1.6", SrcPort: 2000, DstPort: 53, Proto: 17, VLAN: 20, DstRef: "bridge:pve1:vmbr1", Source: "sflow"},
		{At: 300, Node: "pve2", SrcIP: "172.16.0.5", DstIP: "8.8.8.8", SrcPort: 3000, DstPort: 443, Proto: 6, VLAN: 0, Source: "ipfix"},
	}
	if err := repo.InsertBatch(ctx, samples); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	tests := []struct {
		name   string
		wantAt []int64
		filter FlowFilter
	}{
		{name: "guest matches src ref", filter: FlowFilter{Guest: "bridge:pve1:vmbr0"}, wantAt: []int64{100}},
		{name: "guest matches dst ref", filter: FlowFilter{Guest: "bridge:pve1:vmbr1"}, wantAt: []int64{200}},
		{name: "vlan filter", filter: FlowFilter{VLAN: 20}, wantAt: []int64{200}},
		{name: "subnet filter matches src", filter: FlowFilter{Subnet: "10.0.0.0/24"}, wantAt: []int64{100}},
		{name: "subnet filter matches dst", filter: FlowFilter{Subnet: "192.168.1.0/24"}, wantAt: []int64{200}},
		{name: "subnet filter no match", filter: FlowFilter{Subnet: "203.0.113.0/24"}, wantAt: nil},
		{name: "malformed subnet matches nothing, not an error", filter: FlowFilter{Subnet: "not-a-cidr"}, wantAt: nil},
		{name: "port filter matches src port", filter: FlowFilter{Port: 1000}, wantAt: []int64{100}},
		{name: "port filter matches dst port", filter: FlowFilter{Port: 443}, wantAt: []int64{300}},
		{name: "protocol filter", filter: FlowFilter{Proto: 17}, wantAt: []int64{200}},
		{name: "combined filters AND", filter: FlowFilter{Proto: 6, VLAN: 10}, wantAt: []int64{100}},
		{name: "combined filters no match", filter: FlowFilter{Proto: 17, VLAN: 10}, wantAt: nil},
		{name: "unrecognized guest ref matches nothing", filter: FlowFilter{Guest: "bridge:pve1:doesnotexist"}, wantAt: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, _, err := repo.Query(ctx, tt.filter, "", 100)
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			var gotAt []int64
			for _, it := range items {
				gotAt = append(gotAt, it.At)
			}
			if len(gotAt) != len(tt.wantAt) {
				t.Fatalf("got %v, want %v", gotAt, tt.wantAt)
			}
			for i := range gotAt {
				if gotAt[i] != tt.wantAt[i] {
					t.Fatalf("got %v, want %v", gotAt, tt.wantAt)
				}
			}
		})
	}
}

func TestFlowSampleRepo_PruneOlderThan(t *testing.T) {
	db := openTestDB(t)
	repo := NewFlowSampleRepo(db)
	ctx := context.Background()

	samples := []FlowSample{
		sampleFlow(100, "pve1", "10.0.0.1", "10.0.0.2", 1, 2),
		sampleFlow(200, "pve1", "10.0.0.1", "10.0.0.2", 1, 2),
		sampleFlow(300, "pve1", "10.0.0.1", "10.0.0.2", 1, 2),
	}
	if err := repo.InsertBatch(ctx, samples); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	n, err := repo.PruneOlderThan(ctx, 250)
	if err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if n != 2 {
		t.Fatalf("pruned %d rows, want 2", n)
	}
	remaining, err := repo.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("remaining = %d, want 1", remaining)
	}
}

func TestFlowSampleRepo_PruneToCap(t *testing.T) {
	db := openTestDB(t)
	repo := NewFlowSampleRepo(db)
	ctx := context.Background()

	var samples []FlowSample
	for i := int64(0); i < 10; i++ {
		samples = append(samples, sampleFlow(1000+i, "pve1", "10.0.0.1", "10.0.0.2", 1, 2))
	}
	if err := repo.InsertBatch(ctx, samples); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	n, err := repo.PruneToCap(ctx, 4)
	if err != nil {
		t.Fatalf("PruneToCap: %v", err)
	}
	if n != 6 {
		t.Fatalf("pruned %d rows, want 6", n)
	}
	remaining, _, err := repo.Query(ctx, FlowFilter{}, "", 100)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(remaining) != 4 {
		t.Fatalf("remaining = %d, want 4", len(remaining))
	}
	// The newest 4 (at 1006..1009) must survive.
	for _, r := range remaining {
		if r.At < 1006 {
			t.Fatalf("unexpected surviving row at=%d, expected only at>=1006", r.At)
		}
	}
}

func TestFlowSampleRepo_PruneToCap_NonPositiveIsNoop(t *testing.T) {
	db := openTestDB(t)
	repo := NewFlowSampleRepo(db)
	ctx := context.Background()

	if err := repo.InsertBatch(ctx, []FlowSample{sampleFlow(1, "pve1", "10.0.0.1", "10.0.0.2", 1, 2)}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	n, err := repo.PruneToCap(ctx, 0)
	if err != nil {
		t.Fatalf("PruneToCap: %v", err)
	}
	if n != 0 {
		t.Fatalf("pruned %d rows, want 0", n)
	}
}
