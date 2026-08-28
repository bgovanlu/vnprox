// SPDX-License-Identifier: Apache-2.0

package change_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/store"
)

// apply_sdn_foreign_test.go pins T-3101-followup-01's fix (debt-sweep
// 2026-08-19, item 2): PUT /cluster/sdn (sdn.apply) commits ALL pending SDN
// state cluster-wide, including edits staged outside vnprox's change
// engine. Before this fix landed, this reproduction demonstrated the
// defect directly — TestApply_SDNForeignPending_RefusedThenAcknowledged's
// first Apply call SUCCEEDED and silently committed the foreign zone; see
// this task's completion report for the pre-fix failure transcript. It now
// asserts the owner's "surface and confirm" decision instead: Apply
// refuses with the foreign entries named, mutates nothing, and proceeds
// only once AcknowledgeSDNForeignPending has recorded exactly that set.

// newSDNForeignHarness is newSDNHarness (apply_sdn_test.go) plus
// SDNPendingAcks wired, so the acknowledgement flow has somewhere to
// record to. A separate constructor rather than a new newSDNHarness
// parameter, matching that function's own precedent as a variant of
// newHarness.
func newSDNForeignHarness(t *testing.T) *applyHarness {
	t.Helper()
	h := newHarness(t, fixtureThreeNode)
	svc := newService(t, change.Config{
		Changesets: h.csRepo, Audit: h.auditRepo, WS: h.ws,
		Nodes: h.agent, Snapshots: h.snapRepo, Blobs: h.blobRepo, Refresher: h.refresher,
		TimerFunc: h.timers.New, Inventory: newSDNFakeInventory(),
		SDNPendingAcks: store.NewChangesetSDNPendingAckRepo(h.db),
	})
	h.svc = svc
	return h
}

// stageForeignSDNZone stages a zone create directly against the mock's PVE
// API — bypassing vnprox's change engine entirely, exactly as an operator
// staging an edit in the PVE GUI would (never validated, never diffed,
// never appearing in any vnprox changeset's ops). It is left unapplied (no
// ApplySDN call), so it is real foreign pending state per this task's
// evidence (planning/reports/evidence/pve-9.2.4-sdn-pending-state.txt).
func stageForeignSDNZone(t *testing.T, client *pve.Client, id string) {
	t.Helper()
	if err := client.CreateSDNZone(context.Background(), pve.SDNZone{
		ID: id, Type: "simple", Nodes: []string{"pve1"},
	}); err != nil {
		t.Fatalf("staging foreign sdn zone %s: %v", id, err)
	}
}

// TestApply_SDNForeignPending_RefusedThenAcknowledged: an operator stages
// an SDN zone directly against PVE (modeling the GUI), never through
// vnprox's change engine. A SEPARATE, unrelated changeset (a legitimate
// zone/vnet/subnet create) is then applied through vnprox. This test pins
// that Apply now REFUSES (ErrSDNForeignPendingUnacknowledged, naming the
// foreign zone) rather than silently sweeping it in; that the refusal
// mutates nothing (changeset stays draft, foreign zone still pending); and
// that acknowledging via AcknowledgeSDNForeignPending lets the SAME Apply
// call proceed — "surface and confirm", never "block" (the owner's
// decision, planning/tasks/debt-sweep-2026-08-19.md): after acknowledging,
// the foreign zone IS committed, exactly as shown to the operator.
func TestApply_SDNForeignPending_RefusedThenAcknowledged(t *testing.T) {
	h := newSDNForeignHarness(t)
	ctx := context.Background()
	gw := &fakePVEGateway{client: h.client, pollNode: "pve1"}

	// The foreign edit: staged directly against PVE, never through vnprox.
	stageForeignSDNZone(t, h.client, "foreignz")

	// The legitimate, vnprox-staged changeset — entirely unrelated to the
	// foreign zone above.
	ops := sdnLifecycleOps("zoneT3101f", "vnetT3101f", "10.61.0.0/24", 61)
	cs := h.mustCreate(t, "root@pam", "unrelated sdn change", ops)

	// --- Apply refuses: foreign pending state exists, unacknowledged.
	_, err := h.svc.Apply(ctx, cs.ID, "root@pam", gw, 0)
	var unacked *change.ErrSDNForeignPendingUnacknowledged
	if !errors.As(err, &unacked) {
		t.Fatalf("Apply err = %v, want *ErrSDNForeignPendingUnacknowledged", err)
	}
	if len(unacked.Entries) != 1 || unacked.Entries[0].Kind != "zone" || unacked.Entries[0].ID != "foreignz" || unacked.Entries[0].State != "new" {
		t.Fatalf("unacked.Entries = %+v, want exactly one {Kind:zone ID:foreignz State:new}", unacked.Entries)
	}

	// No partial mutation: the changeset is still draft (never transitioned
	// to applying), and the foreign zone is still pending (never applied).
	after := h.get(t, cs.ID)
	if after.Status != change.StatusDraft {
		t.Fatalf("changeset status = %s, want draft (refused before any transition/mutation)", after.Status)
	}
	zones, err := h.client.ListSDNZones(ctx)
	if err != nil {
		t.Fatalf("ListSDNZones: %v", err)
	}
	var stillPending bool
	for _, z := range zones {
		if z.ID == "foreignz" && z.Pending == pve.PendingNew {
			stillPending = true
		}
	}
	if !stillPending {
		t.Fatalf("foreign zone was applied despite the refused Apply call — the fix must never mutate PVE before the operator acknowledges")
	}

	// --- The review-screen read surfaces exactly the same entry.
	seen, err := h.svc.SDNForeignPending(ctx, cs.ID, gw)
	if err != nil {
		t.Fatalf("SDNForeignPending: %v", err)
	}
	if len(seen) != 1 || seen[0].ID != "foreignz" {
		t.Fatalf("SDNForeignPending = %+v, want exactly one entry for foreignz", seen)
	}

	// --- Acknowledge, then retry: apply now proceeds, and — because this
	// is "surface and confirm", not "block" — the foreign zone's own
	// pending edit is committed too, exactly as the review screen showed
	// the operator it would be.
	acked, err := h.svc.AcknowledgeSDNForeignPending(ctx, cs.ID, "root@pam", gw)
	if err != nil {
		t.Fatalf("AcknowledgeSDNForeignPending: %v", err)
	}
	if len(acked) != 1 || acked[0].ID != "foreignz" {
		t.Fatalf("AcknowledgeSDNForeignPending returned %+v, want exactly one entry for foreignz", acked)
	}

	got, err := h.svc.Apply(ctx, cs.ID, "root@pam", gw, 0)
	if err != nil {
		t.Fatalf("apply after acknowledgement: %v", err)
	}
	if got.Status != change.StatusAwaitingConfirm {
		t.Fatalf("status = %s, want awaiting_confirm", got.Status)
	}

	zonesAfter, err := h.client.ListSDNZonesRunning(ctx)
	if err != nil {
		t.Fatalf("ListSDNZonesRunning: %v", err)
	}
	var foreignCommitted bool
	for _, z := range zonesAfter {
		if z.ID == "foreignz" {
			foreignCommitted = true
		}
	}
	if !foreignCommitted {
		t.Fatalf("foreign zone was never committed — expected it to be: acknowledgement authorizes exactly this (surface-and-confirm, not block)")
	}
}

// TestApply_SDNForeignPending_StaleAckIsNotEnough pins the "honest diff"
// requirement (CLAUDE.md, this card's own text): an acknowledgement covers
// exactly the foreign-pending set the operator was shown, not "foreign
// pending state in general". A new foreign edit appearing after the
// operator acknowledged must force a fresh acknowledgement, not silently
// ride along under the old one.
func TestApply_SDNForeignPending_StaleAckIsNotEnough(t *testing.T) {
	h := newSDNForeignHarness(t)
	ctx := context.Background()
	gw := &fakePVEGateway{client: h.client, pollNode: "pve1"}

	stageForeignSDNZone(t, h.client, "foreignz1")

	ops := sdnLifecycleOps("zoneT3101g", "vnetT3101g", "10.62.0.0/24", 62)
	cs := h.mustCreate(t, "root@pam", "unrelated sdn change", ops)

	if _, err := h.svc.AcknowledgeSDNForeignPending(ctx, cs.ID, "root@pam", gw); err != nil {
		t.Fatalf("AcknowledgeSDNForeignPending: %v", err)
	}

	// A SECOND foreign edit appears after the first was acknowledged.
	stageForeignSDNZone(t, h.client, "foreignz2")

	_, err := h.svc.Apply(ctx, cs.ID, "root@pam", gw, 0)
	var unacked *change.ErrSDNForeignPendingUnacknowledged
	if !errors.As(err, &unacked) {
		t.Fatalf("Apply err = %v, want *ErrSDNForeignPendingUnacknowledged (a stale ack must not cover a new foreign edit)", err)
	}
	if len(unacked.Entries) != 2 {
		t.Fatalf("unacked.Entries = %+v, want both foreignz1 and foreignz2 (the CURRENT full foreign set, not just what's new)", unacked.Entries)
	}
}

// TestApply_SDNForeignPending_NoSDNOps_NeverChecksPVE: a changeset with no
// SDN ops at all must not pay a live PVE round trip for this gate, and
// must never be refused by it — plan.hasSDN() gates the whole check.
func TestApply_SDNForeignPending_NoSDNOps_NeverChecksPVE(t *testing.T) {
	h := newHarness(t, fixtureThreeNode)
	ctx := context.Background()

	ops := []change.Op{bridgeCreateOp("pve1", "vmbr9", nil)}
	cs := h.mustCreate(t, "root@pam", "non-sdn change", ops)

	// pveGW is nil: if the gate incorrectly ran for a non-SDN changeset it
	// would nil-pointer-dereference or error, not silently succeed.
	got, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got.Status != change.StatusAwaitingConfirm {
		t.Fatalf("status = %s, want awaiting_confirm", got.Status)
	}
}
