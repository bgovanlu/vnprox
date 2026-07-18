package store

import (
	"context"
	"testing"
)

func sampleLatency(at int64, linkID, fromNode, toNode string, rttMs, lossPct float64) LatencySample {
	return LatencySample{
		LinkID: linkID, Fabric: "corosync", FromNode: fromNode, ToNode: toNode,
		At: at, RttMs: rttMs, LossPct: lossPct,
	}
}

func TestLatencySampleRepo_InsertAndQueryRange(t *testing.T) {
	db := openTestDB(t)
	repo := NewLatencySampleRepo(db)
	ctx := context.Background()

	samples := []LatencySample{
		sampleLatency(100, "corosync:ring0|pve1->pve2", "pve1", "pve2", 10, 0),
		sampleLatency(200, "corosync:ring0|pve1->pve2", "pve1", "pve2", 12, 0),
		sampleLatency(300, "corosync:ring0|pve1->pve3", "pve1", "pve3", 20, 0),
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

	items, err := repo.QueryRange(ctx, "corosync:ring0|pve1->pve2", 0, 1000)
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	// Ascending by at (unlike GET /flows' newest-first convention — see
	// QueryRange's doc comment).
	if items[0].At != 100 || items[1].At != 200 {
		t.Fatalf("unexpected order: %+v", items)
	}
}

func TestLatencySampleRepo_LatestPerLink(t *testing.T) {
	db := openTestDB(t)
	repo := NewLatencySampleRepo(db)
	ctx := context.Background()

	samples := []LatencySample{
		sampleLatency(100, "corosync:ring0|pve1->pve2", "pve1", "pve2", 10, 0),
		sampleLatency(200, "corosync:ring0|pve1->pve2", "pve1", "pve2", 12, 0),
		sampleLatency(150, "corosync:ring0|pve1->pve3", "pve1", "pve3", 20, 0),
	}
	if err := repo.InsertBatch(ctx, samples); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	latest, err := repo.LatestPerLink(ctx)
	if err != nil {
		t.Fatalf("LatestPerLink: %v", err)
	}
	if len(latest) != 2 {
		t.Fatalf("got %d links, want 2", len(latest))
	}
	byLink := map[string]LatencySample{}
	for _, l := range latest {
		byLink[l.LinkID] = l
	}
	if byLink["corosync:ring0|pve1->pve2"].At != 200 || byLink["corosync:ring0|pve1->pve2"].RttMs != 12 {
		t.Fatalf("unexpected latest for pve1->pve2: %+v", byLink["corosync:ring0|pve1->pve2"])
	}
	if byLink["corosync:ring0|pve1->pve3"].At != 150 {
		t.Fatalf("unexpected latest for pve1->pve3: %+v", byLink["corosync:ring0|pve1->pve3"])
	}
}

// TestLatencySampleRepo_PruneOlderThan / TestLatencySampleRepo_PruneToCap:
// AC2's "same assertion shape as T-1002's flow_samples test" — mirrors
// TestFlowSampleRepo_PruneOlderThan/TestFlowSampleRepo_PruneToCap exactly.
func TestLatencySampleRepo_PruneOlderThan(t *testing.T) {
	db := openTestDB(t)
	repo := NewLatencySampleRepo(db)
	ctx := context.Background()

	samples := []LatencySample{
		sampleLatency(100, "corosync:ring0|pve1->pve2", "pve1", "pve2", 10, 0),
		sampleLatency(200, "corosync:ring0|pve1->pve2", "pve1", "pve2", 10, 0),
		sampleLatency(300, "corosync:ring0|pve1->pve2", "pve1", "pve2", 10, 0),
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

func TestLatencySampleRepo_PruneToCap(t *testing.T) {
	db := openTestDB(t)
	repo := NewLatencySampleRepo(db)
	ctx := context.Background()

	var samples []LatencySample
	for i := int64(0); i < 10; i++ {
		samples = append(samples, sampleLatency(1000+i, "corosync:ring0|pve1->pve2", "pve1", "pve2", 10, 0))
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
	remaining, err := repo.QueryRange(ctx, "corosync:ring0|pve1->pve2", 0, 1<<32)
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

func TestLatencySampleRepo_PruneToCap_NonPositiveIsNoop(t *testing.T) {
	db := openTestDB(t)
	repo := NewLatencySampleRepo(db)
	ctx := context.Background()

	if err := repo.InsertBatch(ctx, []LatencySample{sampleLatency(1, "l", "pve1", "pve2", 10, 0)}); err != nil {
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
