// SPDX-License-Identifier: Apache-2.0

package change_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// A pre-apply snapshot read failure (here: a changeset targeting a node the
// host agent doesn't know) fails the apply cleanly, before any mutation.
func TestApply_SnapshotReadError(t *testing.T) {
	h := newHarness(t, fixtureSingleNode)
	ctx := context.Background()
	cs := h.mustCreate(t, "root@pam", "ghost", []change.Op{bridgeCreateOp("ghostnode", "vmbrG", nil)})
	if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0); err == nil {
		t.Fatal("expected apply to fail on snapshot read of unknown node")
	}
	if got := h.get(t, cs.ID); got.Status != change.StatusFailed {
		t.Fatalf("status = %s, want failed", got.Status)
	}
}

// Manual rollback when the pre-snapshot is missing (e.g. pruned) cannot
// restore and lands the changeset in failed.
func TestRollback_MissingPreSnapshot(t *testing.T) {
	h := newHarness(t, fixtureSingleNode)
	ctx := context.Background()
	cs := h.mustCreate(t, "root@pam", "x", []change.Op{bridgeCreateOp("pve1", "vmbr1", nil)})
	if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Delete the snapshot rows out from under the rollback (snapshot_files
	// first: it FK-references snapshots, and foreign_keys is on).
	if _, err := h.db.Conn().ExecContext(ctx, "DELETE FROM snapshot_files WHERE snapshot_id IN (SELECT id FROM snapshots WHERE changeset_id = ?)", cs.ID); err != nil {
		t.Fatalf("delete snapshot_files: %v", err)
	}
	if _, err := h.db.Conn().ExecContext(ctx, "DELETE FROM snapshots WHERE changeset_id = ?", cs.ID); err != nil {
		t.Fatalf("delete snapshots: %v", err)
	}
	if _, err := h.svc.Rollback(ctx, cs.ID, "root@pam", nil); err == nil {
		t.Fatal("expected rollback to error with no pre-snapshot")
	}
	if got := h.get(t, cs.ID); got.Status != change.StatusFailed {
		t.Fatalf("status = %s, want failed", got.Status)
	}
}

// Service.UpdateDraft / Discard lifecycle and their guard errors.
func TestService_UpdateDraftAndDiscard(t *testing.T) {
	h := newHarness(t, fixtureSingleNode)
	ctx := context.Background()

	cs := h.mustCreate(t, "root@pam", "t1", []change.Op{bridgeCreateOp("pve1", "vmbr1", nil)})
	title := "renamed"
	updated, err := h.svc.UpdateDraft(ctx, cs.ID, "root@pam", &title, []change.Op{bridgeCreateOp("pve1", "vmbr2", nil)})
	if err != nil {
		t.Fatalf("UpdateDraft: %v", err)
	}
	if updated.Title != "renamed" || len(updated.Ops) != 1 || updated.Ops[0].Target.ID != "vmbr2" {
		t.Fatalf("UpdateDraft result unexpected: %+v", updated)
	}

	if err := h.svc.Discard(ctx, cs.ID, "root@pam"); err != nil {
		t.Fatalf("Discard: %v", err)
	}
	got := h.get(t, cs.ID)
	if got.Status != change.StatusDiscarded {
		t.Fatalf("status = %s, want discarded", got.Status)
	}
	// Update/Discard on a discarded changeset is illegal.
	var illegal *change.ErrIllegalTransition
	if _, err := h.svc.UpdateDraft(ctx, cs.ID, "root@pam", nil, nil); !errors.As(err, &illegal) {
		t.Fatalf("UpdateDraft on discarded err = %v, want *ErrIllegalTransition", err)
	}
	if err := h.svc.Discard(ctx, cs.ID, "root@pam"); !errors.As(err, &illegal) {
		t.Fatalf("Discard on discarded err = %v, want *ErrIllegalTransition", err)
	}
}

// Protected-interface read/write round trip and validation error.
func TestService_ProtectedRoundTrip(t *testing.T) {
	h := newHarness(t, fixtureSingleNode)
	ctx := context.Background()

	// Missing file → empty config, no error.
	got, err := h.svc.GetProtected(ctx)
	if err != nil {
		t.Fatalf("GetProtected (missing): %v", err)
	}
	if len(got.Nodes) != 0 {
		t.Fatalf("expected empty protected config, got %+v", got.Nodes)
	}

	ref := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}
	saved, err := h.svc.SetProtected(ctx, "root@pam", change.ProtectedConfig{
		Nodes: map[string][]string{"pve1": {ref.String()}},
	})
	if err != nil {
		t.Fatalf("SetProtected: %v", err)
	}
	if saved.UpdatedBy != "root@pam" || saved.Version == 0 {
		t.Fatalf("SetProtected did not stamp metadata: %+v", saved)
	}
	reloaded, err := h.svc.GetProtected(ctx)
	if err != nil {
		t.Fatalf("GetProtected: %v", err)
	}
	if len(reloaded.Nodes["pve1"]) != 1 {
		t.Fatalf("reloaded protected = %+v", reloaded.Nodes)
	}

	// Invalid ref → typed error.
	var badRef *change.ErrInvalidProtectedRef
	if _, err := h.svc.SetProtected(ctx, "root@pam", change.ProtectedConfig{
		Nodes: map[string][]string{"pve1": {"not-a-valid-ref-with-no-colons"}},
	}); !errors.As(err, &badRef) {
		t.Fatalf("SetProtected invalid ref err = %v, want *ErrInvalidProtectedRef", err)
	}
}

// Confirm/Rollback path after a manual rollback leaves the changeset terminal
// (no double rollback).
func TestRollback_AlreadyRolledBack(t *testing.T) {
	h := newHarness(t, fixtureSingleNode)
	ctx := context.Background()
	cs := h.mustCreate(t, "root@pam", "x", []change.Op{bridgeCreateOp("pve1", "vmbr1", nil)})
	if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := h.svc.Rollback(ctx, cs.ID, "root@pam", nil); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	var nc *change.ErrNotConfirmable
	if _, err := h.svc.Rollback(ctx, cs.ID, "root@pam", nil); !errors.As(err, &nc) {
		t.Fatalf("second rollback err = %v, want *ErrNotConfirmable", err)
	}
	if _, err := h.svc.Confirm(ctx, cs.ID, "root@pam"); !errors.As(err, &nc) {
		t.Fatalf("confirm after rollback err = %v, want *ErrNotConfirmable", err)
	}
}
