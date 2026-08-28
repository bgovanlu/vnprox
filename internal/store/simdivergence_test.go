// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSimDivergenceRepo_UpsertAndList(t *testing.T) {
	db := openTestDB(t)
	repo := NewSimDivergenceRepo(db)
	ctx := context.Background()

	f := SimDivergenceFinding{
		ID:     "probe:sim_divergence|guest-nic:pve1:300/net0|ip:10.20.0.5|tcp|22",
		SrcRef: "guest-nic:pve1:300/net0", DstKind: "ip", DstIP: "10.20.0.5",
		Proto: "tcp", Port: 22, SimulatedVerdict: "allow", ObservedOutcome: "unreachable",
		Detail: "tcp connection refused", CreatedAt: 100, UpdatedAt: 100,
	}
	if err := repo.Upsert(ctx, f); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0] != f {
		t.Fatalf("List() = %+v, want [%+v]", list, f)
	}
}

func TestSimDivergenceRepo_Upsert_OverwritesSameID(t *testing.T) {
	db := openTestDB(t)
	repo := NewSimDivergenceRepo(db)
	ctx := context.Background()

	base := SimDivergenceFinding{
		ID:     "probe:sim_divergence|guest-nic:pve1:300/net0|ip:10.20.0.5|tcp|22",
		SrcRef: "guest-nic:pve1:300/net0", DstKind: "ip", DstIP: "10.20.0.5",
		Proto: "tcp", Port: 22, SimulatedVerdict: "allow", ObservedOutcome: "unreachable",
		Detail: "first", CreatedAt: 100, UpdatedAt: 100,
	}
	if err := repo.Upsert(ctx, base); err != nil {
		t.Fatalf("Upsert (first): %v", err)
	}

	updated := base
	updated.ObservedOutcome = "timeout"
	updated.Detail = "second"
	updated.UpdatedAt = 200
	if err := repo.Upsert(ctx, updated); err != nil {
		t.Fatalf("Upsert (second): %v", err)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// Re-verifying the identical tuple upserts the same row — never
	// accumulates duplicates (docs/features/firewall.md §5/§6 honesty
	// contract: the finding always reflects the most recent live check).
	if len(list) != 1 {
		t.Fatalf("List() len = %d, want 1 (upsert, not insert)", len(list))
	}
	if list[0].ObservedOutcome != "timeout" || list[0].Detail != "second" || list[0].UpdatedAt != 200 {
		t.Errorf("List()[0] = %+v, want the updated row", list[0])
	}
}

func TestSimDivergenceRepo_Clear(t *testing.T) {
	db := openTestDB(t)
	repo := NewSimDivergenceRepo(db)
	ctx := context.Background()

	f := SimDivergenceFinding{
		ID:     "probe:sim_divergence|guest-nic:pve1:300/net0|ip:10.20.0.5|tcp|22",
		SrcRef: "guest-nic:pve1:300/net0", DstKind: "ip", DstIP: "10.20.0.5",
		Proto: "tcp", Port: 22, SimulatedVerdict: "allow", ObservedOutcome: "unreachable",
		CreatedAt: 100, UpdatedAt: 100,
	}
	if err := repo.Upsert(ctx, f); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := repo.Clear(ctx, f.ID); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("List() after Clear = %+v, want empty", list)
	}
	// Clearing an already-absent id is not an error.
	if err := repo.Clear(ctx, f.ID); err != nil {
		t.Fatalf("Clear (already absent): %v", err)
	}
}

// TestSimDivergenceRepo_SurvivesRestart is AC4's persistence half: a
// divergence finding written before a (simulated) daemon restart is still
// there after — the whole reason this producer gets a table instead of
// living only in the findings.Engine's in-memory recomputation like every
// other producer.
func TestSimDivergenceRepo_SurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vnprox.db")
	ctx := context.Background()

	db1, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	f := SimDivergenceFinding{
		ID:     "probe:sim_divergence|guest-nic:pve1:300/net0|ip:10.20.0.5|tcp|22",
		SrcRef: "guest-nic:pve1:300/net0", DstKind: "ip", DstIP: "10.20.0.5",
		Proto: "tcp", Port: 22, SimulatedVerdict: "deny", ObservedOutcome: "reachable",
		Detail: "connected", CreatedAt: 100, UpdatedAt: 100,
	}
	if upsertErr := NewSimDivergenceRepo(db1).Upsert(ctx, f); upsertErr != nil {
		t.Fatalf("Upsert: %v", upsertErr)
	}
	if closeErr := db1.Close(); closeErr != nil {
		t.Fatalf("Close: %v", closeErr)
	}

	db2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })

	list, err := NewSimDivergenceRepo(db2).List(ctx)
	if err != nil {
		t.Fatalf("List after restart: %v", err)
	}
	if len(list) != 1 || list[0] != f {
		t.Fatalf("List() after restart = %+v, want [%+v]", list, f)
	}
}
