package change_test

import (
	"context"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// snapshotFakeInventory is a mutable InventorySource: it always knows the
// cluster node (so CreateManualSnapshot has a node list to capture) and the
// test can add bridge entities to model a collector refresh after an apply
// (the fake refresher does nothing, so validation of a later delete op needs
// the test to update inventory the way T-104's collectors would).
type snapshotFakeInventory struct{ g *inventory.Graph }

func newSnapshotFakeInventory() *snapshotFakeInventory {
	f := &snapshotFakeInventory{g: inventory.NewGraph()}
	f.setBridges()
	return f
}

func (f *snapshotFakeInventory) Snapshot() inventory.Snapshot { return f.g.Snapshot() }

// setBridges replaces the fake inventory's contents with the cluster node
// plus the named bridges (modeling what the collectors would report after
// those bridges were applied).
func (f *snapshotFakeInventory) setBridges(names ...string) {
	ents := []inventory.Entity{
		&inventory.Node{Ref: inventory.Ref{Kind: inventory.KindNode, Node: "pve1", ID: "pve1"}, Name: "pve1", Status: "online", Quorate: true, Local: true},
	}
	for _, name := range names {
		ents = append(ents, &inventory.Bridge{
			Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: name}, Name: name, Virt: inventory.BridgeLinux,
		})
	}
	f.g.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{}, ents)
}

// newSnapshotHarness builds a harness like newHarness but with the mutable
// InventorySource above wired in. The returned fake starts with just the
// node.
func newSnapshotHarness(t *testing.T) (*applyHarness, *snapshotFakeInventory) {
	t.Helper()
	h := newHarness(t, fixtureSingleNode)
	inv := newSnapshotFakeInventory()
	svc := newService(t, change.Config{
		Changesets: h.csRepo, Audit: h.auditRepo, WS: h.ws,
		Nodes: h.agent, Snapshots: h.snapRepo, Blobs: h.blobRepo, Refresher: h.refresher,
		TimerFunc: h.timers.New, Inventory: inv,
	})
	h.svc = svc
	return h, inv
}

func TestSnapshots_ApplyThreeChangesets_TimelineAndDiffs(t *testing.T) {
	h, inv := newSnapshotHarness(t)
	_ = inv
	ctx := context.Background()

	for i, name := range []string{"vmbr1", "vmbr2", "vmbr3"} {
		cs := h.mustCreate(t, "root@pam", "add "+name, []change.Op{bridgeCreateOp("pve1", name, nil)})
		if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0); err != nil {
			t.Fatalf("apply %d: %v", i, err)
		}
		if _, err := h.svc.Confirm(ctx, cs.ID, "root@pam"); err != nil {
			t.Fatalf("confirm %d: %v", i, err)
		}
	}

	// Acceptance criterion 1: timeline shows pre/post snapshots for each of
	// the 3 changesets (2 snapshots each = 6), newest first, paginated.
	page1, cursor, err := h.svc.ListSnapshots(ctx, "", 4)
	if err != nil {
		t.Fatalf("ListSnapshots page1: %v", err)
	}
	if len(page1) != 4 {
		t.Fatalf("page1 len = %d, want 4", len(page1))
	}
	if cursor == "" {
		t.Fatal("expected a next-page cursor (6 snapshots, page size 4)")
	}
	page2, cursor2, err := h.svc.ListSnapshots(ctx, cursor, 4)
	if err != nil {
		t.Fatalf("ListSnapshots page2: %v", err)
	}
	if len(page2) != 2 || cursor2 != "" {
		t.Fatalf("page2 = %d entries, cursor %q; want 2 entries, no further page", len(page2), cursor2)
	}

	all := append(page1, page2...)
	if len(all) != 6 {
		t.Fatalf("total snapshots = %d, want 6", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].TakenAt < all[i].TakenAt {
			t.Fatalf("snapshots not newest-first: %+v", all)
		}
	}

	// Diff between the very first "pre" snapshot (empty vmbr1/2/3) and
	// "live" reflects all three bridges having been added.
	first := all[len(all)-1]
	diff, err := h.svc.DiffSnapshots(ctx, first.ID, "live")
	if err != nil {
		t.Fatalf("DiffSnapshots(first, live): %v", err)
	}
	if len(diff.Files) != 1 || !diff.Files[0].Changed {
		t.Fatalf("diff(first,live) = %+v, want one changed file", diff.Files)
	}
	for _, want := range []string{"vmbr1", "vmbr2", "vmbr3"} {
		if !containsLine(diff.Files[0].Unified, want) {
			t.Fatalf("diff(first,live) missing %s:\n%s", want, diff.Files[0].Unified)
		}
	}

	// Diff between the first snapshot and itself is empty (identical).
	selfDiff, err := h.svc.DiffSnapshots(ctx, first.ID, first.ID)
	if err != nil {
		t.Fatalf("DiffSnapshots(first, first): %v", err)
	}
	for _, f := range selfDiff.Files {
		if f.Changed {
			t.Fatalf("diff(first,first) reports a change: %+v", f)
		}
	}
}

// containsLine reports whether unified has an added ('+') line mentioning
// substr (used to check a diff introduces a given interface stanza).
func containsLine(unified, substr string) bool {
	for _, line := range strings.Split(unified, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") && strings.Contains(line, substr) {
			return true
		}
	}
	return false
}

// Acceptance criterion 2 (golden): apply three changesets, restore snapshot
// N-2 (the pre-apply state of the second changeset — i.e. the world right
// after changeset 1), and the produced draft must exactly reverse the two
// later changesets: its ops delete vmbr2+vmbr3, its rendered diff removes
// exactly those stanzas, and — the byte-level golden property — applying the
// draft leaves the live file with zero differences against the restored
// snapshot (DiffSnapshots(snapshot, live) reports no changed files).
func TestSnapshots_RestoreNMinus2_ExactlyReversesLaterChangesets(t *testing.T) {
	h, inv := newSnapshotHarness(t)
	_ = inv
	ctx := context.Background()

	var changesetIDs []string
	for _, name := range []string{"vmbr1", "vmbr2", "vmbr3"} {
		cs := h.mustCreate(t, "root@pam", "add "+name, []change.Op{bridgeCreateOp("pve1", name, nil)})
		if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		if _, err := h.svc.Confirm(ctx, cs.ID, "root@pam"); err != nil {
			t.Fatalf("confirm %s: %v", name, err)
		}
		changesetIDs = append(changesetIDs, cs.ID)
	}

	// Snapshot N-2: the "pre" snapshot of changeset 2 — vmbr1 exists,
	// vmbr2/vmbr3 do not.
	rows, err := h.snapRepo.List(ctx, changesetIDs[1])
	if err != nil {
		t.Fatalf("listing snapshots for cs2: %v", err)
	}
	preID := ""
	for _, row := range rows {
		if row.Kind == "pre" {
			preID = row.ID
		}
	}
	if preID == "" {
		t.Fatal("no pre-snapshot for changeset 2")
	}

	draft, err := h.svc.RestoreSnapshot(ctx, "root@pam", preID)
	if err != nil {
		t.Fatalf("RestoreSnapshot: %v", err)
	}

	// The draft's ops are exactly the two deletes.
	if len(draft.Ops) != 2 {
		t.Fatalf("restore draft ops = %+v, want exactly 2 deletes", draft.Ops)
	}
	deleted := map[string]bool{}
	for _, op := range draft.Ops {
		if op.Type != change.OpBridgeDelete {
			t.Fatalf("restore draft op = %s, want bridge.delete only", op.Type)
		}
		deleted[op.Target.ID] = true
	}
	if !deleted["vmbr2"] || !deleted["vmbr3"] {
		t.Fatalf("restore draft deletes %v, want vmbr2+vmbr3", deleted)
	}

	// Golden diff check: the rendered draft diff removes the vmbr2/vmbr3
	// stanzas and adds nothing.
	diff, err := h.svc.Diff(ctx, draft.ID)
	if err != nil {
		t.Fatalf("Diff restore draft: %v", err)
	}
	if len(diff.Files) != 1 || !diff.Files[0].Changed {
		t.Fatalf("restore diff files = %+v, want one changed file", diff.Files)
	}
	for _, line := range strings.Split(diff.Files[0].Unified, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") && strings.TrimSpace(line[1:]) != "" {
			t.Fatalf("restore diff adds a line (must only remove):\n%s", diff.Files[0].Unified)
		}
	}
	if !removesLine(diff.Files[0].Unified, "vmbr2") || !removesLine(diff.Files[0].Unified, "vmbr3") {
		t.Fatalf("restore diff does not remove both bridges:\n%s", diff.Files[0].Unified)
	}
	if removesLine(diff.Files[0].Unified, "iface vmbr1") {
		t.Fatalf("restore diff must not touch vmbr1:\n%s", diff.Files[0].Unified)
	}

	// Model the collector refresh that follows the three applies, so the
	// restore draft's delete ops validate against an inventory that has the
	// bridges (the fake refresher does nothing on its own).
	inv.setBridges("vmbr1", "vmbr2", "vmbr3")

	// Byte-level golden property: after applying+confirming the restore
	// draft, the live state is identical to the snapshot (no changed files
	// in a snapshot-vs-live diff).
	if _, applyErr := h.svc.Apply(ctx, draft.ID, "root@pam", nil, 0); applyErr != nil {
		t.Fatalf("apply restore draft: %v", applyErr)
	}
	if _, confirmErr := h.svc.Confirm(ctx, draft.ID, "root@pam"); confirmErr != nil {
		t.Fatalf("confirm restore draft: %v", confirmErr)
	}
	after, err := h.svc.DiffSnapshots(ctx, preID, "live")
	if err != nil {
		t.Fatalf("DiffSnapshots(pre, live) after restore: %v", err)
	}
	for _, f := range after.Files {
		if f.Changed {
			t.Fatalf("live state differs from restored snapshot after applying the draft:\n%s", f.Unified)
		}
	}
}

func TestSnapshots_ManualCreate_DedupAndDetail(t *testing.T) {
	h, inv := newSnapshotHarness(t)
	_ = inv
	ctx := context.Background()

	summary, err := h.svc.CreateManualSnapshot(ctx, "root@pam", "before maintenance")
	if err != nil {
		t.Fatalf("CreateManualSnapshot: %v", err)
	}
	if summary.Kind != "manual" || summary.Note != "before maintenance" {
		t.Fatalf("summary = %+v", summary)
	}
	if len(summary.Nodes) != 1 || summary.Nodes[0] != "pve1" {
		t.Fatalf("summary.Nodes = %+v, want [pve1]", summary.Nodes)
	}

	detail, err := h.svc.GetSnapshot(ctx, summary.ID)
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	if len(detail.Files) != 1 || detail.Files[0].Node != "pve1" {
		t.Fatalf("detail.Files = %+v", detail.Files)
	}
	firstHash := detail.Files[0].SHA256

	// A second manual snapshot with unchanged content dedups to the same blob.
	summary2, err := h.svc.CreateManualSnapshot(ctx, "root@pam", "")
	if err != nil {
		t.Fatalf("CreateManualSnapshot 2: %v", err)
	}
	detail2, err := h.svc.GetSnapshot(ctx, summary2.ID)
	if err != nil {
		t.Fatalf("GetSnapshot 2: %v", err)
	}
	if detail2.Files[0].SHA256 != firstHash {
		t.Fatalf("dedup: hash changed for identical content (%s vs %s)", detail2.Files[0].SHA256, firstHash)
	}

	var blobCount int
	if err := h.db.Conn().QueryRowContext(ctx, "SELECT count(*) FROM blobs WHERE sha256 = ?", firstHash).Scan(&blobCount); err != nil {
		t.Fatalf("counting blob rows: %v", err)
	}
	if blobCount != 1 {
		t.Fatalf("blob row count = %d, want 1 (dedup)", blobCount)
	}
}

func TestSnapshots_Restore_ProducesReviewableDraft(t *testing.T) {
	h, inv := newSnapshotHarness(t)
	_ = inv
	ctx := context.Background()

	baseline, err := h.svc.CreateManualSnapshot(ctx, "root@pam", "baseline")
	if err != nil {
		t.Fatalf("CreateManualSnapshot: %v", err)
	}

	cs := h.mustCreate(t, "root@pam", "add vmbr9", []change.Op{bridgeCreateOp("pve1", "vmbr9", nil)})
	if _, applyErr := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0); applyErr != nil {
		t.Fatalf("apply: %v", applyErr)
	}
	if _, confirmErr := h.svc.Confirm(ctx, cs.ID, "root@pam"); confirmErr != nil {
		t.Fatalf("confirm: %v", confirmErr)
	}

	draft, err := h.svc.RestoreSnapshot(ctx, "root@pam", baseline.ID)
	if err != nil {
		t.Fatalf("RestoreSnapshot: %v", err)
	}
	if draft.Status != change.StatusDraft {
		t.Fatalf("restore draft status = %s, want draft", draft.Status)
	}
	if len(draft.Ops) != 1 || draft.Ops[0].Type != change.OpBridgeDelete || draft.Ops[0].Target.ID != "vmbr9" {
		t.Fatalf("restore draft ops = %+v, want a single bridge.delete vmbr9", draft.Ops)
	}

	// Restoring a snapshot that already matches live yields a valid,
	// empty-ops draft (not an error) — "nothing to restore" is a legitimate
	// outcome the review screen can show plainly.
	current, err := h.svc.CreateManualSnapshot(ctx, "root@pam", "current")
	if err != nil {
		t.Fatalf("CreateManualSnapshot current: %v", err)
	}
	noop, err := h.svc.RestoreSnapshot(ctx, "root@pam", current.ID)
	if err != nil {
		t.Fatalf("RestoreSnapshot (no-op case): %v", err)
	}
	if len(noop.Ops) != 0 {
		t.Fatalf("no-op restore ops = %+v, want none", noop.Ops)
	}
}
