// SPDX-License-Identifier: Apache-2.0

package change_test

import (
	"context"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// TestApply_SDN_DHCPRange_AddThenChange is T-406 acceptance criterion 1:
// "Range add/change round-trips through a changeset -> pvemock SDN state
// (golden test)." A first changeset creates a zone/vnet/subnet carrying a
// DHCP range (the "add" half); a second changeset issues sdn.subnet.update
// with a different range (the "change" half) against the same subnet.
// Both are applied through the real change engine against pvemock, and
// the running (last-applied) SDN view is asserted to carry the range PVE
// actually has on file after each step — the range editor T-402 already
// built into SdnSubnetEditor and the DHCPRanges field already threaded
// through params_sdn.go/apply_sdn.go/pvemock round-trips end to end, not
// just at the params-shape level.
func TestApply_SDN_DHCPRange_AddThenChange(t *testing.T) {
	h := newHarness(t, fixtureThreeNode)
	fakeInv := newSDNFakeInventory()
	svc := newService(t, change.Config{
		Changesets: h.csRepo, Audit: h.auditRepo, WS: h.ws,
		Nodes: h.agent, Snapshots: h.snapRepo, Blobs: h.blobRepo, Refresher: h.refresher,
		TimerFunc: h.timers.New, Inventory: fakeInv,
	})
	h.svc = svc
	ctx := context.Background()
	gw := &fakePVEGateway{client: h.client, pollNode: "pve1"}

	const zoneID, vnetID, cidr = "zoneT406", "vnetT406", "10.61.0.0/24"
	const rangeA = "10.61.0.100-10.61.0.150"

	createOps := []change.Op{
		{
			Type:   change.OpSdnZoneCreate,
			Target: inventory.Ref{Kind: inventory.KindSDNZone, ID: zoneID},
			Params: &change.SdnZoneCreateParams{Type: "vlan", Bridge: "vmbr0", Nodes: []string{"pve1", "pve2", "pve3"}, MTU: 1500},
		},
		{
			Type:   change.OpSdnVnetCreate,
			Target: inventory.Ref{Kind: inventory.KindSDNVnet, ID: zoneID + "/" + vnetID},
			Params: &change.SdnVnetCreateParams{Zone: zoneID, Alias: "t406", Tag: 61},
		},
		{
			Type:   change.OpSdnSubnetCreate,
			Target: inventory.Ref{Kind: inventory.KindSDNSubnet, ID: cidr},
			Params: &change.SdnSubnetCreateParams{
				Vnet: zoneID + "/" + vnetID, CIDR: cidr, Gateway: gatewayOf(cidr),
				DHCPRanges: []string{rangeA},
			},
		},
		sdnApplyOp(),
	}

	cs1 := h.mustCreate(t, "root@pam", "sdn dhcp range add", createOps)
	if _, err := h.svc.Apply(ctx, cs1.ID, "root@pam", gw, 0); err != nil {
		t.Fatalf("apply (create): %v", err)
	}
	if _, err := h.svc.Confirm(ctx, cs1.ID, "root@pam"); err != nil {
		t.Fatalf("confirm (create): %v", err)
	}

	assertSDNSubnetDHCPRange(t, ctx, h.client, vnetID, cidr, "10.61.0.100", "10.61.0.150")

	// Simulate the poll cycle catching up with cs1's just-applied SDN
	// state: an sdn.subnet.update op's referential check (T-202) needs its
	// target subnet to already exist in the inventory snapshot (see
	// resolveSubnetVnet's doc comment in apply_sdn.go on this same
	// realistic-but-not-instantaneous gap), which this fake InventorySource
	// otherwise never gets since it isn't wired to a live collector.
	fakeInv.addSDNSubnet(zoneID, vnetID, cidr)

	// Change: a second changeset updates the same subnet's DHCP range to a
	// different start/end pair.
	const rangeB = "10.61.0.160-10.61.0.200"
	updateOps := []change.Op{
		{
			Type:   change.OpSdnSubnetUpdate,
			Target: inventory.Ref{Kind: inventory.KindSDNSubnet, ID: cidr},
			Params: &change.SdnSubnetUpdateParams{DHCPRanges: &[]string{rangeB}},
		},
		sdnApplyOp(),
	}
	cs2 := h.mustCreate(t, "root@pam", "sdn dhcp range change", updateOps)
	if _, err := h.svc.Apply(ctx, cs2.ID, "root@pam", gw, 0); err != nil {
		t.Fatalf("apply (update): %v", err)
	}
	if _, err := h.svc.Confirm(ctx, cs2.ID, "root@pam"); err != nil {
		t.Fatalf("confirm (update): %v", err)
	}

	assertSDNSubnetDHCPRange(t, ctx, h.client, vnetID, cidr, "10.61.0.160", "10.61.0.200")
}

// addSDNSubnet injects a zone/vnet/subnet triple into f's inventory graph,
// modeling a poll cycle that has caught up with a just-applied SDN
// changeset (see this file's TestApply_SDN_DHCPRange_AddThenChange doc
// comment on why the "change" half of AC1 needs this).
func (f *sdnFakeInventory) addSDNSubnet(zoneID, vnetID, cidr string) {
	f.g.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{}, []inventory.Entity{
		&inventory.SdnZone{Ref: inventory.Ref{Kind: inventory.KindSDNZone, ID: zoneID}, ID: zoneID, Type: "vlan", Bridge: "vmbr0", Nodes: []string{"pve1", "pve2", "pve3"}},
		&inventory.SdnVnet{Ref: inventory.Ref{Kind: inventory.KindSDNVnet, ID: zoneID + "/" + vnetID}, ID: vnetID, Zone: zoneID},
		&inventory.SdnSubnet{Ref: inventory.Ref{Kind: inventory.KindSDNSubnet, ID: cidr}, ID: cidr, Vnet: zoneID + "/" + vnetID},
	})
}

// assertSDNSubnetDHCPRange fetches vnet's subnets from pvemock's running
// (last-applied) view and asserts cidr's dhcp range matches wantStart/
// wantEnd exactly.
func assertSDNSubnetDHCPRange(t *testing.T, ctx context.Context, client *pve.Client, vnet, cidr, wantStart, wantEnd string) {
	t.Helper()
	subnets, err := client.ListSDNSubnetsRunning(ctx, vnet)
	if err != nil {
		t.Fatalf("ListSDNSubnetsRunning: %v", err)
	}
	for _, s := range subnets {
		if s.CIDR != cidr {
			continue
		}
		if s.DHCPRangeStart != wantStart || s.DHCPRangeEnd != wantEnd {
			t.Fatalf("subnet %s dhcp range = %s-%s, want %s-%s", cidr, s.DHCPRangeStart, s.DHCPRangeEnd, wantStart, wantEnd)
		}
		return
	}
	t.Fatalf("subnet %s not found in running view: %+v", cidr, subnets)
}
