package store

import (
	"context"
	"testing"
)

func sampleWan(at int64, linkID, fromNode, toNode string, rttMs, lossPct float64) LatencySample {
	return LatencySample{
		LinkID: linkID, FromNode: fromNode, ToNode: toNode,
		At: at, RttMs: rttMs, LossPct: lossPct,
	}
}

func TestWanProbeSampleRepo_InsertAndQueryRange(t *testing.T) {
	db := openTestDB(t)
	repo := NewWanProbeSampleRepo(db)
	ctx := context.Background()

	samples := []LatencySample{
		sampleWan(100, "wan:vmbr0|pve1->1.1.1.1", "pve1", "1.1.1.1", 10, 0),
		sampleWan(200, "wan:vmbr0|pve1->1.1.1.1", "pve1", "1.1.1.1", 12, 0),
		sampleWan(300, "wan:vmbr0|pve1->8.8.8.8", "pve1", "8.8.8.8", 20, 0),
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

	items, err := repo.QueryRange(ctx, "wan:vmbr0|pve1->1.1.1.1", 0, 1000)
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].At != 100 || items[1].At != 200 {
		t.Fatalf("unexpected order: %+v", items)
	}
	for _, it := range items {
		if it.Fabric != WanFabric {
			t.Errorf("Fabric = %q, want %q", it.Fabric, WanFabric)
		}
	}
}

func TestWanProbeSampleRepo_LatestPerLink(t *testing.T) {
	db := openTestDB(t)
	repo := NewWanProbeSampleRepo(db)
	ctx := context.Background()

	samples := []LatencySample{
		sampleWan(100, "wan:vmbr0|pve1->1.1.1.1", "pve1", "1.1.1.1", 10, 0),
		sampleWan(200, "wan:vmbr0|pve1->1.1.1.1", "pve1", "1.1.1.1", 12, 5),
		sampleWan(150, "wan:vmbr1|pve1->8.8.8.8", "pve1", "8.8.8.8", 20, 0),
	}
	if err := repo.InsertBatch(ctx, samples); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	latest, err := repo.LatestPerLink(ctx)
	if err != nil {
		t.Fatalf("LatestPerLink: %v", err)
	}
	if len(latest) != 2 {
		t.Fatalf("got %d links, want 2: %+v", len(latest), latest)
	}
	byLink := map[string]LatencySample{}
	for _, l := range latest {
		byLink[l.LinkID] = l
	}
	if got := byLink["wan:vmbr0|pve1->1.1.1.1"]; got.At != 200 || got.LossPct != 5 {
		t.Errorf("latest for vmbr0 link = %+v, want at=200 loss=5", got)
	}
}

// TestWanProbeSampleRepo_PruneOlderThan/PruneToCap: T-1405 AC4 — "the
// history ring enforces its stated size/age cap" — the same shape
// TestLatencySampleRepo_PruneOlderThan/PruneToCap already assert for
// T-1303's own ring.
func TestWanProbeSampleRepo_PruneOlderThan(t *testing.T) {
	db := openTestDB(t)
	repo := NewWanProbeSampleRepo(db)
	ctx := context.Background()

	samples := []LatencySample{
		sampleWan(100, "wan:vmbr0|pve1->1.1.1.1", "pve1", "1.1.1.1", 10, 0),
		sampleWan(200, "wan:vmbr0|pve1->1.1.1.1", "pve1", "1.1.1.1", 10, 0),
		sampleWan(300, "wan:vmbr0|pve1->1.1.1.1", "pve1", "1.1.1.1", 10, 0),
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

func TestWanProbeSampleRepo_PruneToCap(t *testing.T) {
	db := openTestDB(t)
	repo := NewWanProbeSampleRepo(db)
	ctx := context.Background()

	var samples []LatencySample
	for i := int64(0); i < 10; i++ {
		samples = append(samples, sampleWan(1000+i, "wan:vmbr0|pve1->1.1.1.1", "pve1", "1.1.1.1", 10, 0))
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
	remaining, err := repo.QueryRange(ctx, "wan:vmbr0|pve1->1.1.1.1", 0, 1<<32)
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if len(remaining) != 4 {
		t.Fatalf("remaining = %d, want 4", len(remaining))
	}
	for _, r := range remaining {
		if r.At < 1006 {
			t.Fatalf("unexpected surviving row at=%d, expected only at>=1006", r.At)
		}
	}
}

// TestWanProbeSampleRepo_QueryAll: T-1405 AC4's "exposes an export path" —
// export returns the expected bounded set (newest first, capped at limit),
// reflecting exactly what survived the prune above, never more.
func TestWanProbeSampleRepo_QueryAll(t *testing.T) {
	db := openTestDB(t)
	repo := NewWanProbeSampleRepo(db)
	ctx := context.Background()

	var samples []LatencySample
	for i := int64(0); i < 5; i++ {
		samples = append(samples, sampleWan(1000+i, "wan:vmbr0|pve1->1.1.1.1", "pve1", "1.1.1.1", 10, 0))
	}
	if err := repo.InsertBatch(ctx, samples); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	all, err := repo.QueryAll(ctx, 100)
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("got %d rows, want 5", len(all))
	}
	if all[0].At != 1004 {
		t.Errorf("QueryAll[0].At = %d, want 1004 (newest first)", all[0].At)
	}
	if all[0].Uplink != "vmbr0" {
		t.Errorf("QueryAll[0].Uplink = %q, want vmbr0", all[0].Uplink)
	}

	capped, err := repo.QueryAll(ctx, 2)
	if err != nil {
		t.Fatalf("QueryAll with limit: %v", err)
	}
	if len(capped) != 2 {
		t.Fatalf("got %d rows with limit=2, want 2", len(capped))
	}

	// A non-positive limit defaults rather than returning everything
	// unbounded (AC4: "never an unbounded dump").
	defaulted, err := repo.QueryAll(ctx, 0)
	if err != nil {
		t.Fatalf("QueryAll with limit=0: %v", err)
	}
	if len(defaulted) != 5 {
		t.Fatalf("got %d rows with limit=0 (defaulted), want 5", len(defaulted))
	}
}

func TestWanTargetRepo_ReplaceForNode_FullSetReplace(t *testing.T) {
	db := openTestDB(t)
	repo := NewWanTargetRepo(db)
	ctx := context.Background()

	initial := []WanTarget{
		{Uplink: "vmbr0", Host: "1.1.1.1"},
		{Uplink: "vmbr0", Host: "8.8.8.8"},
	}
	if err := repo.ReplaceForNode(ctx, "pve1", initial, 1000); err != nil {
		t.Fatalf("ReplaceForNode: %v", err)
	}

	got, err := repo.ListByNode(ctx, "pve1")
	if err != nil {
		t.Fatalf("ListByNode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d targets, want 2: %+v", len(got), got)
	}

	// Replacing again with a smaller set drops the removed one — a full
	// replace, never a merge/patch.
	replacement := []WanTarget{{Uplink: "vmbr0", Host: "1.1.1.1"}}
	if err = repo.ReplaceForNode(ctx, "pve1", replacement, 2000); err != nil {
		t.Fatalf("ReplaceForNode (2nd): %v", err)
	}
	got, err = repo.ListByNode(ctx, "pve1")
	if err != nil {
		t.Fatalf("ListByNode (2nd): %v", err)
	}
	if len(got) != 1 || got[0].Host != "1.1.1.1" {
		t.Fatalf("got %+v, want exactly [1.1.1.1]", got)
	}

	// A different node's targets are untouched by pve1's replace.
	other := []WanTarget{{Uplink: "eth0", Host: "9.9.9.9"}}
	if err = repo.ReplaceForNode(ctx, "pve2", other, 3000); err != nil {
		t.Fatalf("ReplaceForNode (pve2): %v", err)
	}
	gotOther, err := repo.ListByNode(ctx, "pve2")
	if err != nil {
		t.Fatalf("ListByNode (pve2): %v", err)
	}
	if len(gotOther) != 1 || gotOther[0].Host != "9.9.9.9" {
		t.Fatalf("pve2 targets = %+v, want exactly [9.9.9.9]", gotOther)
	}
	gotPve1Again, err := repo.ListByNode(ctx, "pve1")
	if err != nil {
		t.Fatalf("ListByNode (pve1 again): %v", err)
	}
	if len(gotPve1Again) != 1 {
		t.Fatalf("pve1 targets changed after replacing pve2's: %+v", gotPve1Again)
	}
}
