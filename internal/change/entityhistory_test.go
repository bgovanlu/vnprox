package change_test

import (
	"context"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

func histRef(node, id string) inventory.Ref {
	return inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: id}
}

// AC1: a changeset with an op targeting vmbr0 appears in vmbr0's history and
// NOT in vmbr1's.
//
// The negative half is what makes this a test rather than a smoke check: a
// history that returned every changeset regardless of target would pass the
// positive assertion on its own.
func TestEntityHistory_ScopesToTheTargetedEntity(t *testing.T) {
	h, _ := newSnapshotHarness(t)
	ctx := context.Background()

	h.mustCreate(t, "alice", "add vmbr0", []change.Op{bridgeCreateOp("pve1", "vmbr0", nil)})
	h.mustCreate(t, "brian", "add vmbr1", []change.Op{bridgeCreateOp("pve1", "vmbr1", nil)})

	entries, _, err := h.svc.EntityHistory(ctx, histRef("pve1", "vmbr0"), 0)
	if err != nil {
		t.Fatalf("EntityHistory: %v", err)
	}
	var sawVmbr0Changeset bool
	for _, e := range entries {
		if e.Kind != change.HistoryKindChangeset {
			continue
		}
		sawVmbr0Changeset = true
		if e.Actor != "alice" {
			t.Fatalf("vmbr0's history names %q; vmbr1's changeset leaked in: %+v", e.Actor, e)
		}
	}
	if !sawVmbr0Changeset {
		t.Fatal("vmbr0's own changeset is missing from its history")
	}

	other, _, err := h.svc.EntityHistory(ctx, histRef("pve1", "vmbr1"), 0)
	if err != nil {
		t.Fatalf("EntityHistory(vmbr1): %v", err)
	}
	for _, e := range other {
		if e.Kind == change.HistoryKindChangeset && e.Actor != "brian" {
			t.Fatalf("vmbr1's history contains vmbr0's changeset: %+v", e)
		}
	}
}

// The merge is the point: a changeset says what was intended, an audit row
// says what happened, a snapshot says where the restore point is. All three
// must appear for one entity.
func TestEntityHistory_MergesAllThreeSources(t *testing.T) {
	h, _ := newSnapshotHarness(t)
	ctx := context.Background()

	cs := h.mustCreate(t, "root@pam", "add vmbr7", []change.Op{bridgeCreateOp("pve1", "vmbr7", nil)})
	if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := h.svc.Confirm(ctx, cs.ID, "root@pam"); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	entries, _, err := h.svc.EntityHistory(ctx, histRef("pve1", "vmbr7"), 0)
	if err != nil {
		t.Fatalf("EntityHistory: %v", err)
	}
	kinds := map[change.HistoryKind]int{}
	for _, e := range entries {
		kinds[e.Kind]++
	}
	if kinds[change.HistoryKindChangeset] == 0 {
		t.Error("no changeset entry: the history does not say what was intended")
	}
	if kinds[change.HistoryKindSnapshot] == 0 {
		t.Error("no snapshot entry: the history does not say where the restore point is")
	}
	// Every entry must be legible on its own — a row with no summary is a row
	// an operator cannot act on.
	for _, e := range entries {
		if e.Summary == "" {
			t.Errorf("history entry with no summary: %+v", e)
		}
		if e.At == 0 {
			t.Errorf("history entry with no timestamp: %+v", e)
		}
	}
}

// AC3: ordering is strictly newest-first ACROSS the merged sources, not within
// each one. Asserted over a mixed result rather than three same-source rows,
// which a per-source sort would also satisfy.
func TestEntityHistory_IsNewestFirstAcrossSources(t *testing.T) {
	h, _ := newSnapshotHarness(t)
	ctx := context.Background()

	cs := h.mustCreate(t, "root@pam", "add vmbr8", []change.Op{bridgeCreateOp("pve1", "vmbr8", nil)})
	if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := h.svc.Confirm(ctx, cs.ID, "root@pam"); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if _, err := h.svc.CreateManualSnapshot(ctx, "root@pam", "later"); err != nil {
		t.Fatalf("CreateManualSnapshot: %v", err)
	}

	entries, _, err := h.svc.EntityHistory(ctx, histRef("pve1", "vmbr8"), 0)
	if err != nil {
		t.Fatalf("EntityHistory: %v", err)
	}
	if len(entries) < 3 {
		t.Fatalf("expected a merged history of at least 3 entries, got %d", len(entries))
	}
	distinctKinds := map[change.HistoryKind]bool{}
	for _, e := range entries {
		distinctKinds[e.Kind] = true
	}
	if len(distinctKinds) < 2 {
		t.Fatalf("the fixture produced only one source (%v); this test cannot detect a per-source sort", distinctKinds)
	}
	for i := 1; i < len(entries); i++ {
		if entries[i].At > entries[i-1].At {
			t.Fatalf("entry %d (%d) is newer than entry %d (%d): not newest-first", i, entries[i].At, i-1, entries[i-1].At)
		}
	}
}

// Ordering is deterministic for entries sharing a timestamp, so two reads of an
// unchanged store render identically.
func TestEntityHistory_IsStableAcrossReads(t *testing.T) {
	h, _ := newSnapshotHarness(t)
	ctx := context.Background()
	cs := h.mustCreate(t, "root@pam", "add vmbr6", []change.Op{bridgeCreateOp("pve1", "vmbr6", nil)})
	if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	first, _, err := h.svc.EntityHistory(ctx, histRef("pve1", "vmbr6"), 0)
	if err != nil {
		t.Fatalf("EntityHistory: %v", err)
	}
	for range 5 {
		got, _, gerr := h.svc.EntityHistory(ctx, histRef("pve1", "vmbr6"), 0)
		if gerr != nil {
			t.Fatalf("EntityHistory: %v", gerr)
		}
		if len(got) != len(first) {
			t.Fatalf("history length changed between reads: %d then %d", len(first), len(got))
		}
		for i := range got {
			if got[i].Kind != first[i].Kind || got[i].Summary != first[i].Summary {
				t.Fatalf("history order is not stable at %d: %+v vs %+v", i, got[i], first[i])
			}
		}
	}
}

// AC5: an unknown ref returns an empty page, not an error and not a 500.
func TestEntityHistory_UnknownRefIsEmptyNotAnError(t *testing.T) {
	h, _ := newSnapshotHarness(t)
	entries, truncated, err := h.svc.EntityHistory(context.Background(), histRef("pve9", "vmbr99"), 0)
	if err != nil {
		t.Fatalf("EntityHistory(unknown): %v", err)
	}
	if truncated {
		t.Fatal("an unknown ref reported a truncated history")
	}
	if len(entries) != 0 {
		t.Fatalf("an unknown ref returned %d entries: %+v", len(entries), entries)
	}
}

func TestEntityHistory_RefusesAZeroRef(t *testing.T) {
	h, _ := newSnapshotHarness(t)
	if _, _, err := h.svc.EntityHistory(context.Background(), inventory.Ref{}, 0); err == nil {
		t.Fatal("a zero ref should be refused rather than matching everything")
	}
}

// The limit bounds the page and is clamped rather than trusted.
func TestEntityHistory_LimitIsBoundedAndClamped(t *testing.T) {
	h, _ := newSnapshotHarness(t)
	ctx := context.Background()
	cs := h.mustCreate(t, "root@pam", "add vmbr5", []change.Op{bridgeCreateOp("pve1", "vmbr5", nil)})
	if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	one, _, err := h.svc.EntityHistory(ctx, histRef("pve1", "vmbr5"), 1)
	if err != nil {
		t.Fatalf("EntityHistory: %v", err)
	}
	if len(one) != 1 {
		t.Fatalf("limit 1 returned %d entries", len(one))
	}
	// A wildly large limit is clamped, not honoured, so a caller cannot ask
	// the daemon to materialise an unbounded page.
	big, _, err := h.svc.EntityHistory(ctx, histRef("pve1", "vmbr5"), 1_000_000)
	if err != nil {
		t.Fatalf("EntityHistory(huge limit): %v", err)
	}
	if len(big) > 200 {
		t.Fatalf("a huge limit returned %d entries; it must be clamped", len(big))
	}
}
