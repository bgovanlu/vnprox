package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bgovanlu/vnprox/internal/store"
)

func TestStoreCapacityAdapter_ReportsSizeAndLocalNode(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "vnprox.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	adapter := storeCapacityAdapter{db: db, localNode: func() string { return "pve1" }}

	rep, err := adapter.StoreCapacity()
	if err != nil {
		t.Fatalf("StoreCapacity: %v", err)
	}
	if rep.Node != "pve1" {
		t.Errorf("Node = %q, want pve1", rep.Node)
	}
	if rep.SizeBytes <= 0 {
		t.Errorf("SizeBytes = %d, want > 0 for a migrated store", rep.SizeBytes)
	}

	direct, err := db.SizeBytes()
	if err != nil {
		t.Fatalf("db.SizeBytes: %v", err)
	}
	if rep.SizeBytes != direct {
		t.Errorf("adapter reported %d, want it to equal db.SizeBytes() directly (%d) — no second measurement", rep.SizeBytes, direct)
	}
}
