// SPDX-License-Identifier: Apache-2.0

package change_test

import (
	"context"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// ipamSubnetRef is the target Ref for three-node-vlan.yaml's 10.100.0.0/24
// subnet (vnet100, zone vlanz) — docs/data-model.md's SdnSubnet.ID is the
// CIDR, matching internal/inventory.FromPVESDN's Ref{Kind: KindSDNSubnet,
// ID: s.CIDR}.
var ipamSubnetRef = inventory.Ref{Kind: inventory.KindSDNSubnet, ID: "10.100.0.0/24"}

// newIpamHarness builds a harness like newHarness (three-node-vlan fixture,
// which already carries a pve-IPAM instance with a gateway + one guest
// allocation per vnet — see that fixture's sdn.ipams comment) with an
// InventorySource carrying the one SdnSubnet entity ipam.alloc ops'
// referential validation and apply-time vnet resolution
// (Service.subnetVnet) need.
func newIpamHarness(t *testing.T) *applyHarness {
	t.Helper()
	h := newHarness(t, fixtureThreeNode)
	g := inventory.NewGraph()
	g.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{}, []inventory.Entity{
		&inventory.SdnSubnet{Ref: ipamSubnetRef, ID: "10.100.0.0/24", Vnet: "vnet100", Gateway: "10.100.0.1"},
	})
	svc := newService(t, change.Config{
		Changesets: h.csRepo, Audit: h.auditRepo, WS: h.ws,
		Nodes: h.agent, Snapshots: h.snapRepo, Blobs: h.blobRepo, Refresher: h.refresher,
		TimerFunc: h.timers.New, Inventory: inventorySource{g},
	})
	h.svc = svc
	return h
}

// Acceptance criterion 3: reserve -> changeset -> apply (pvemock) -> grid
// updates (verified here via GetIPAMStatus, the same read internal/ipam's
// grid renders from); release likewise.
func TestApply_IpamAlloc_ReserveThenRelease(t *testing.T) {
	h := newIpamHarness(t)
	ctx := context.Background()
	gw := &fakePVEGateway{client: h.client, pollNode: "pve1"}

	hasIP := func(t *testing.T, ip string) bool {
		t.Helper()
		entries, err := h.client.GetIPAMStatus(ctx, "pve")
		if err != nil {
			t.Fatalf("GetIPAMStatus: %v", err)
		}
		for _, e := range entries {
			if e.IP == ip {
				return true
			}
		}
		return false
	}

	if hasIP(t, "10.100.0.77") {
		t.Fatal("precondition: 10.100.0.77 already allocated")
	}

	// --- reserve ---
	cs := h.mustCreate(t, "root@pam", "reserve 10.100.0.77", []change.Op{
		{
			Type:   change.OpIpamAllocCreate,
			Target: ipamSubnetRef,
			Params: &change.IpamAllocCreateParams{CIDR: "10.100.0.77/32", Hostname: "test1", MAC: "aa:bb:cc:dd:ee:01"},
		},
	})
	applied, err := h.svc.Apply(ctx, cs.ID, "root@pam", gw, 0)
	if err != nil {
		t.Fatalf("Apply (create): %v", err)
	}
	if applied.Status != change.StatusAwaitingConfirm {
		t.Fatalf("status after apply = %s, want awaiting_confirm", applied.Status)
	}

	plan := h.plan(t, cs.ID)
	if len(plan.Steps) != 1 || plan.Steps[0].Kind != change.StepIpamAlloc {
		t.Fatalf("unexpected plan for ipam-only changeset: %+v", plan.Steps)
	}
	log := h.applyLog(t, cs.ID)
	if len(log.Steps) != 1 || log.Steps[0].Status != change.StepOK {
		t.Fatalf("unexpected apply log: %+v", log.Steps)
	}

	if !hasIP(t, "10.100.0.77") {
		t.Fatal("10.100.0.77 not allocated after apply")
	}

	if _, confirmErr := h.svc.Confirm(ctx, cs.ID, "root@pam"); confirmErr != nil {
		t.Fatalf("Confirm (create): %v", confirmErr)
	}

	// --- release ---
	cs2 := h.mustCreate(t, "root@pam", "release 10.100.0.77", []change.Op{
		{
			Type:   change.OpIpamAllocDelete,
			Target: ipamSubnetRef,
			Params: &change.IpamAllocDeleteParams{CIDR: "10.100.0.77/32"},
		},
	})
	applied2, err := h.svc.Apply(ctx, cs2.ID, "root@pam", gw, 0)
	if err != nil {
		t.Fatalf("Apply (delete): %v", err)
	}
	if applied2.Status != change.StatusAwaitingConfirm {
		t.Fatalf("status after apply = %s, want awaiting_confirm", applied2.Status)
	}
	if hasIP(t, "10.100.0.77") {
		t.Fatal("10.100.0.77 still allocated after release apply")
	}
	if _, err := h.svc.Confirm(ctx, cs2.ID, "root@pam"); err != nil {
		t.Fatalf("Confirm (delete): %v", err)
	}
}

// A step failure after a successful ipam.alloc.create rolls the allocation
// back (rollbackIpamSteps) alongside the usual node-file rollback — this
// changeset mixes an ipam.alloc.create with a bridge.create on a node whose
// reload is injected to fail, so the ipam step (StepIpamAlloc always
// precedes the node-file steps) succeeds before the later reload failure
// triggers rollback.
func TestApply_IpamAlloc_RollbackOnLaterStepFailure(t *testing.T) {
	h := newIpamHarness(t)
	ctx := context.Background()
	gw := &fakePVEGateway{client: h.client, pollNode: "pve1"}
	h.setReloadFail(t, "pve1", true)

	cs := h.mustCreate(t, "root@pam", "reserve + bad bridge", []change.Op{
		{
			Type:   change.OpIpamAllocCreate,
			Target: ipamSubnetRef,
			Params: &change.IpamAllocCreateParams{CIDR: "10.100.0.88/32", Hostname: "rollback-me"},
		},
		bridgeCreateOp("pve1", "vmbr9", nil),
	})
	_, err := h.svc.Apply(ctx, cs.ID, "root@pam", gw, 0)
	if err == nil {
		t.Fatal("Apply: want error from injected reload failure")
	}

	final := h.get(t, cs.ID)
	if final.Status != change.StatusFailed {
		t.Fatalf("status after failed apply = %s, want failed", final.Status)
	}

	entries, err := h.client.GetIPAMStatus(ctx, "pve")
	if err != nil {
		t.Fatalf("GetIPAMStatus: %v", err)
	}
	for _, e := range entries {
		if e.IP == "10.100.0.88" {
			t.Fatal("10.100.0.88 still allocated after rollback")
		}
	}

	log := h.applyLog(t, cs.ID)
	foundRollback := false
	for _, rb := range log.Rollback {
		if rb.Status == change.StepOK {
			foundRollback = true
		}
	}
	if !foundRollback {
		t.Fatalf("expected a successful ipam rollback entry in apply log: %+v", log.Rollback)
	}
}
