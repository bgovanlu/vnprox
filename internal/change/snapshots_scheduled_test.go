// SPDX-License-Identifier: Apache-2.0

package change_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// countSnapshotsOfKind returns how many snapshots of the given kind exist,
// read back through the service's own list API rather than through SQL — so
// these assertions describe what an operator would actually see.
func countSnapshotsOfKind(t *testing.T, h *applyHarness, kind string) int {
	t.Helper()
	rows, _, err := h.svc.ListSnapshots(context.Background(), "", 100)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	n := 0
	for _, r := range rows {
		if r.Kind == kind {
			n++
		}
	}
	return n
}

// AC1: a capture records every node's file.
func TestScheduledSnapshot_CapturesEveryNodesFile(t *testing.T) {
	h, _ := newSnapshotHarness(t)
	ctx := context.Background()

	summary, created, err := h.svc.CaptureScheduledSnapshot(ctx)
	if err != nil {
		t.Fatalf("CaptureScheduledSnapshot: %v", err)
	}
	if !created {
		t.Fatal("the first capture must create a snapshot; there is nothing to de-duplicate against")
	}
	if summary.Kind != "scheduled" {
		t.Fatalf("kind = %q, want %q", summary.Kind, "scheduled")
	}
	if len(summary.Nodes) != 1 || summary.Nodes[0] != "pve1" {
		t.Fatalf("nodes = %v, want [pve1]", summary.Nodes)
	}

	detail, err := h.svc.GetSnapshot(ctx, summary.ID)
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	if len(detail.Files) != 1 || detail.Files[0].Node != "pve1" {
		t.Fatalf("files = %+v, want one file for pve1", detail.Files)
	}
	if detail.Files[0].SHA256 == "" {
		t.Fatal("the captured file has no content hash; nothing was actually stored")
	}
}

// AC2: an unchanged cluster records NOTHING on the next tick.
//
// Asserted on the snapshot COUNT, not on the returned `created` flag alone — an
// implementation that returned false while still writing a row would pass a
// flag-only assertion and quietly fill the disk, which is precisely the failure
// this de-duplication exists to prevent.
func TestScheduledSnapshot_UnchangedClusterRecordsNothing(t *testing.T) {
	h, _ := newSnapshotHarness(t)
	ctx := context.Background()

	if _, created, err := h.svc.CaptureScheduledSnapshot(ctx); err != nil || !created {
		t.Fatalf("first capture: created=%v err=%v", created, err)
	}
	before := countSnapshotsOfKind(t, h, "scheduled")

	for i := range 3 {
		_, created, err := h.svc.CaptureScheduledSnapshot(ctx)
		if err != nil {
			t.Fatalf("capture %d: %v", i, err)
		}
		if created {
			t.Fatalf("capture %d reported creating a snapshot from unchanged content", i)
		}
	}
	if after := countSnapshotsOfKind(t, h, "scheduled"); after != before {
		t.Fatalf("three ticks over an unchanged cluster grew the snapshot count from %d to %d", before, after)
	}
}

// AC3: an out-of-band edit — the exact case this feature exists for — produces
// exactly one new snapshot, carrying every node's file rather than only the
// changed one.
func TestScheduledSnapshot_OutOfBandEditProducesOneNewSnapshot(t *testing.T) {
	h, _ := newSnapshotHarness(t)
	ctx := context.Background()

	first, created, err := h.svc.CaptureScheduledSnapshot(ctx)
	if err != nil || !created {
		t.Fatalf("first capture: created=%v err=%v", created, err)
	}

	// Someone edits /etc/network/interfaces over ssh and runs `ifreload -a`.
	// vnprox never sees a changeset for this.
	live, err := h.agent.ReadInterfaces(ctx, "pve1")
	if err != nil {
		t.Fatalf("ReadInterfaces: %v", err)
	}
	h.agent.setCommittedOutOfBand("pve1", live+"\nauto vmbr99\niface vmbr99 inet manual\n\tbridge_ports none\n\tbridge_stp off\n\tbridge_fd 0\n")

	second, created, err := h.svc.CaptureScheduledSnapshot(ctx)
	if err != nil {
		t.Fatalf("second capture: %v", err)
	}
	if !created {
		t.Fatal("a changed cluster must produce a new snapshot; this is the whole point of the feature")
	}
	if second.ID == first.ID {
		t.Fatal("the second capture reused the first snapshot's id")
	}
	if n := countSnapshotsOfKind(t, h, "scheduled"); n != 2 {
		t.Fatalf("scheduled snapshot count = %d, want exactly 2", n)
	}
	detail, err := h.svc.GetSnapshot(ctx, second.ID)
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	if len(detail.Files) != 1 {
		t.Fatalf("the new snapshot carries %d files, want every node's file", len(detail.Files))
	}

	// And it is restorable through the ordinary path — a scheduled snapshot is
	// not a second-class one.
	draft, err := h.svc.RestoreSnapshot(ctx, "root@pam", first.ID)
	if err != nil {
		t.Fatalf("RestoreSnapshot from a scheduled snapshot: %v", err)
	}
	if len(draft.Ops) == 0 {
		t.Fatal("restoring the pre-edit snapshot produced no ops; the out-of-band change is not recoverable")
	}
}

// AC4: retention keeps the newest N scheduled snapshots and NEVER touches
// another kind. The interleaved manual snapshot is the load-bearing half of
// this test: a prune that counted rows without filtering on kind would delete
// it and still leave the right number of scheduled ones behind.
func TestScheduledSnapshot_RetentionPrunesOnlyScheduled(t *testing.T) {
	h, _ := newSnapshotHarness(t)
	ctx := context.Background()

	if _, err := h.svc.CreateManualSnapshot(ctx, "root@pam", "before maintenance"); err != nil {
		t.Fatalf("CreateManualSnapshot: %v", err)
	}

	// Five distinct scheduled captures, each forced to differ.
	for i := range 5 {
		live, err := h.agent.ReadInterfaces(ctx, "pve1")
		if err != nil {
			t.Fatalf("ReadInterfaces %d: %v", i, err)
		}
		h.agent.setCommittedOutOfBand("pve1", live+"\n# edit "+string(rune('a'+i))+"\n")
		if _, created, capErr := h.svc.CaptureScheduledSnapshot(ctx); capErr != nil || !created {
			t.Fatalf("capture %d: created=%v err=%v", i, created, capErr)
		}
	}
	if n := countSnapshotsOfKind(t, h, "scheduled"); n != 5 {
		t.Fatalf("scheduled count before prune = %d, want 5", n)
	}

	pruned, err := h.svc.PruneScheduledSnapshots(ctx, 2)
	if err != nil {
		t.Fatalf("PruneScheduledSnapshots: %v", err)
	}
	if pruned != 3 {
		t.Fatalf("pruned %d snapshots, want 3", pruned)
	}
	if n := countSnapshotsOfKind(t, h, "scheduled"); n != 2 {
		t.Fatalf("scheduled count after prune = %d, want 2", n)
	}
	if n := countSnapshotsOfKind(t, h, "manual"); n != 1 {
		t.Fatalf("retention deleted a MANUAL snapshot: manual count = %d, want 1", n)
	}
}

// A keep of 0 (or negative — an unset or misparsed config value) must be a
// no-op, never an instruction to erase the history.
func TestScheduledSnapshot_KeepZeroDeletesNothing(t *testing.T) {
	h, _ := newSnapshotHarness(t)
	ctx := context.Background()

	if _, created, err := h.svc.CaptureScheduledSnapshot(ctx); err != nil || !created {
		t.Fatalf("capture: created=%v err=%v", created, err)
	}
	for _, keep := range []int{0, -1} {
		pruned, err := h.svc.PruneScheduledSnapshots(ctx, keep)
		if err != nil {
			t.Fatalf("PruneScheduledSnapshots(%d): %v", keep, err)
		}
		if pruned != 0 {
			t.Fatalf("PruneScheduledSnapshots(%d) deleted %d snapshots", keep, pruned)
		}
	}
	if n := countSnapshotsOfKind(t, h, "scheduled"); n != 1 {
		t.Fatalf("scheduled count = %d, want 1", n)
	}
}

// AC5: interval 0 means off — RunSnapshotScheduler starts no loop and returns
// immediately. Asserted by the fact that it returns at all against a context
// that is never cancelled: a loop would block here forever and the test would
// time out.
func TestScheduledSnapshot_IntervalZeroRunsNoLoop(t *testing.T) {
	h, _ := newSnapshotHarness(t)
	ctx := context.Background()

	done := make(chan error, 1)
	go func() { done <- h.svc.RunSnapshotScheduler(ctx, 0, 5, quietLogger()) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunSnapshotScheduler(0): %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunSnapshotScheduler(0) did not return; a disabled scheduler must start no loop")
	}
	if n := countSnapshotsOfKind(t, h, "scheduled"); n != 0 {
		t.Fatalf("a disabled scheduler captured %d snapshots", n)
	}
}

// The loop itself: it captures on tick and stops on context cancellation.
func TestScheduledSnapshot_LoopCapturesAndStopsOnCancel(t *testing.T) {
	h, _ := newSnapshotHarness(t)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- h.svc.RunSnapshotScheduler(ctx, 10*time.Millisecond, 5, quietLogger()) }()

	deadline := time.After(5 * time.Second)
	for countSnapshotsOfKind(t, h, "scheduled") == 0 {
		select {
		case <-deadline:
			t.Fatal("the scheduler loop captured nothing within 5s")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunSnapshotScheduler returned %v on cancel", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunSnapshotScheduler did not stop on context cancellation")
	}
}

// A capture with no apply configuration is refused with the typed error, not a
// panic or a silently empty snapshot.
func TestScheduledSnapshot_RefusedWithoutApplyConfiguration(t *testing.T) {
	h, _ := newSnapshotHarness(t)
	// A service with changesets but no node agent / snapshot store: the
	// degraded-startup shape. The guard must refuse, not panic and not record
	// an empty snapshot that would later "restore" a cluster to nothing.
	svc := newService(t, change.Config{Changesets: h.csRepo, Audit: h.auditRepo, WS: h.ws})
	if _, created, err := svc.CaptureScheduledSnapshot(context.Background()); err == nil {
		t.Fatalf("a capture with no node agent should be refused, got created=%v", created)
	}
}
