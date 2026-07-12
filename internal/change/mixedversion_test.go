package change_test

// T-606: "mixed-version refusal behavior verified in the multi-daemon
// harness" (planning/tasks/phase-6.md#T-606). docs/architecture.md §5
// promises "a daemon refuses to coordinate changes involving a peer with an
// incompatible schema version (upgrade prompt in UI)", and docs/api.md's
// stable error-code list already reserves `peer_incompatible` for exactly
// this — but until this task nothing in internal/change actually checked
// peer.Client.CheckCompatible before dispatching apply steps to a peer.
// This file exercises the gate added to beginApply (apply.go): it reuses
// T-304's real three-daemon harness (distributed_test.go) — coordinator
// pve1 talking to real peer.Server/peer.Client instances over loopback
// HTTP for pve2/pve3 — and swaps pve3's peer server for a stub reporting an
// incompatible protocol version (see makePeerIncompatible's doc comment).

import (
	"context"
	"errors"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/store"
)

// TestApply_RefusesWhenPeerProtocolIncompatible is the core mixed-version
// refusal case: a changeset touching all three nodes is refused outright
// when pve3 advertises an incompatible peer.ProtocolVersion, before any
// snapshot is captured or any node is touched — pve1/pve2's files (and
// pve3's) must be byte-identical to their pre-apply state, and the
// changeset must stay in draft/validated (never applying), so a fixed
// retry needs no cleanup.
func TestApply_RefusesWhenPeerProtocolIncompatible(t *testing.T) {
	h := newThreeDaemonHarness(t)
	ctx := context.Background()
	nodes := []string{"pve1", "pve2", "pve3"}

	before := map[string]string{}
	for _, node := range nodes {
		before[node] = mustReadNode(t, h, node)
	}

	h.makePeerIncompatible(t, "pve3")

	cs := h.mustCreate(t, threeNodeOps())
	_, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0)
	if err == nil {
		t.Fatal("Apply with an incompatible peer in the plan: want an error, got nil")
	}
	var incompatible *change.ErrIncompatiblePeer
	if !errors.As(err, &incompatible) {
		t.Fatalf("Apply err = %v (%T), want it to unwrap to *change.ErrIncompatiblePeer", err, err)
	}
	if incompatible.Node != "pve3" {
		t.Errorf("ErrIncompatiblePeer.Node = %q, want pve3", incompatible.Node)
	}
	if !errors.Is(err, peer.ErrPeerIncompatible) {
		t.Errorf("Apply err = %v, want it to also wrap peer.ErrPeerIncompatible", err)
	}

	// Refused before any mutation: the changeset never left its pre-apply
	// status, and every node's file is untouched.
	got := h.get(t, cs.ID)
	if got.Status != change.StatusValidated && got.Status != change.StatusDraft {
		t.Errorf("changeset status after refused apply = %s, want draft or validated (never applying)", got.Status)
	}
	for _, node := range nodes {
		if after := mustReadNode(t, h, node); after != before[node] {
			t.Errorf("node %s file changed despite the apply being refused: before=%q after=%q", node, before[node], after)
		}
	}

	// The refusal is itself audited (operators need to see *why* an apply
	// was refused, same as validation_failed).
	audits, err := store.NewAuditRepo(h.coordDB).List(ctx, cs.ID, 50)
	if err != nil {
		t.Fatalf("AuditRepo.List: %v", err)
	}
	found := false
	for _, a := range audits {
		if a.Result == "peer_incompatible" {
			found = true
		}
	}
	if !found {
		t.Error("no audit entry with result=peer_incompatible for the refused changeset")
	}
}

// TestApply_SucceedsWhenAllPeersCompatible is the control case: the same
// three-node changeset, same harness, no incompatible peer swapped in —
// proves the new compatibility gate doesn't false-positive on the ordinary
// all-peers-compatible path T-304's other tests already exercise.
func TestApply_SucceedsWhenAllPeersCompatible(t *testing.T) {
	h := newThreeDaemonHarness(t)
	ctx := context.Background()

	cs := h.mustCreate(t, threeNodeOps())
	got, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0)
	if err != nil {
		t.Fatalf("Apply with every peer compatible: %v", err)
	}
	if got.Status != change.StatusAwaitingConfirm {
		t.Errorf("status after apply = %s, want awaiting_confirm", got.Status)
	}
}
