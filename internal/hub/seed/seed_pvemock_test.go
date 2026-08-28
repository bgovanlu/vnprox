// SPDX-License-Identifier: Apache-2.0

// seed_pvemock_test.go is T-2104 AC3's literal proof for the flagship
// three-node Ceph seed: blueprint.Instantiate's produced ops, pushed through
// the exact same pve.Client calls cmd/vnproxd's own PVEGateway.SDNStageOp
// issues (mirrored here — that type is unexported in package main and so
// cannot be imported), against a *running* internal/pvemock server, then
// read back through the real client to confirm the documented topology
// (zone/vnet/subnet with this seed's exact parameters) actually landed.
//
// This intentionally does not go through internal/change.Service (the
// change-engine state machine — changeset rows, snapshots, commit-confirm,
// rollback): that machinery is internal/change's own, already covered by
// internal/change's tests, and CLAUDE.md's "never apply network changes
// outside the change engine" governs vnprox's *runtime* code paths, not a
// content test verifying that a blueprint's produced ops, translated by
// production's own SDNStageOp mapping, land correctly against a PVE-shaped
// server. What this proves is narrower and exactly what AC3 asks: the seed's
// ops are well-formed and PVE (mocked) accepts and realizes them as
// documented — not that the change engine's own apply/rollback machinery
// works, which is not this card's concern.
package seed_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/hub/seed"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
)

// applySDNOp mirrors cmd/vnproxd/changeagent.go's pveGateway.SDNStageOp
// switch, narrowed to the three create-op cases blueprint.Instantiate can
// ever produce for a bare sdn-zone/sdn-vnet/sdn-subnet fixture (Instantiate
// never emits an update/delete op against an empty snapshot).
func applySDNOp(ctx context.Context, client *pve.Client, op change.Op) error {
	switch p := op.Params.(type) {
	case *change.SdnZoneCreateParams:
		return client.CreateSDNZone(ctx, pve.SDNZone{
			ID: op.Target.ID, Type: p.Type, Nodes: p.Nodes, VrfVxlan: p.VrfVxlan,
		})
	case *change.SdnVnetCreateParams:
		return client.CreateSDNVnet(ctx, pve.SDNVnet{
			ID: pve.SDNVnetID(op.Target.ID), Zone: p.Zone, Alias: p.Alias, Tag: p.Tag, VlanAware: p.VlanAware,
		})
	case *change.SdnSubnetCreateParams:
		return client.CreateSDNSubnet(ctx, pve.SDNVnetID(p.Vnet), pve.SDNSubnet{
			ID: pve.SDNSubnetID(op.Target.ID), Vnet: pve.SDNVnetID(p.Vnet), CIDR: p.CIDR, Gateway: p.Gateway, SNAT: p.SNAT,
		})
	default:
		panic("seed_pvemock_test: applySDNOp: unhandled op type " + op.Type)
	}
}

// TestSeedCeph3NodeStorage_AppliesAgainstPVEMock is T-2104 AC3, literally:
// the Ceph seed's Instantiate output, applied through the real pve.Client
// against a running internal/pvemock three-node cluster, produces exactly
// the documented zone/vnet/subnet topology when read back.
func TestSeedCeph3NodeStorage_AppliesAgainstPVEMock(t *testing.T) {
	fx, err := pvemock.LoadFixture("../../../testdata/clusters/three-node-vlan.yaml")
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	srv := httptest.NewServer(pvemock.NewServer(fx))
	t.Cleanup(srv.Close)

	client, err := pve.New(pve.Config{APIURL: srv.URL, Auth: pve.AuthTicket, Username: "root@pam", Password: "vnprox-mock"})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}
	ctx := context.Background()

	bp, ok := seed.ByID(seed.SeedCeph3NodeStorage)
	if !ok {
		t.Fatal("no such seed")
	}
	nodes := []string{"pve1", "pve2", "pve3"}
	// Instantiate against a bare graph: this fixture's own pre-existing SDN
	// zone (see internal/pvemock's README: three-node-vlan.yaml ships a
	// "vlan" zone already) has a different id than this seed's, so it never
	// collides with — or is required by — the seed's own bare/conforming
	// diff, which only ever looks at the seed's own target refs.
	ops, err := blueprint.Instantiate(bp, blueprint.InstantiateRequest{Nodes: nodes}, newGraphWithNodes(nodes...).Snapshot())
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	if len(ops) != 3 {
		t.Fatalf("Instantiate produced %d ops, want 3 (zone, vnet, subnet): %+v", len(ops), ops)
	}

	for _, op := range ops {
		if applyErr := applySDNOp(ctx, client, op); applyErr != nil {
			t.Fatalf("applying %s: %v", op.Type, applyErr)
		}
	}

	upid, err := client.ApplySDN(ctx)
	if err != nil {
		t.Fatalf("ApplySDN: %v", err)
	}
	if _, waitErr := client.WaitTask(ctx, "pve1", upid, pve.WaitOptions{Timeout: 30 * time.Second}); waitErr != nil {
		t.Fatalf("WaitTask: %v", waitErr)
	}

	// Read the *running* (post-apply) state back — the documented topology,
	// confirmed from pvemock's own resulting state, not from anything this
	// test remembered constructing.
	zones, err := client.ListSDNZonesRunning(ctx)
	if err != nil {
		t.Fatalf("ListSDNZonesRunning: %v", err)
	}
	zone := findZone(zones, "cephstorage")
	if zone == nil {
		t.Fatalf("zone %q not found in running zones: %+v", "cephstorage", zones)
	}
	if zone.Type != "vxlan" {
		t.Errorf("zone.Type = %q, want vxlan", zone.Type)
	}
	if zone.VrfVxlan != 10500 {
		t.Errorf("zone.VrfVxlan = %d, want 10500", zone.VrfVxlan)
	}
	if len(zone.Nodes) != 3 {
		t.Errorf("zone.Nodes = %v, want all 3 target nodes", zone.Nodes)
	}

	vnets, err := client.ListSDNVnetsRunning(ctx)
	if err != nil {
		t.Fatalf("ListSDNVnetsRunning: %v", err)
	}
	vnet := findVnet(vnets, "cephnet")
	if vnet == nil {
		t.Fatalf("vnet %q not found in running vnets: %+v", "cephnet", vnets)
	}
	if vnet.Zone != "cephstorage" {
		t.Errorf("vnet.Zone = %q, want cephstorage", vnet.Zone)
	}

	subnets, err := client.ListSDNSubnetsRunning(ctx, "cephnet")
	if err != nil {
		t.Fatalf("ListSDNSubnetsRunning: %v", err)
	}
	subnet := findSubnet(subnets, "10.50.0.0/24")
	if subnet == nil {
		t.Fatalf("subnet %q not found in running subnets: %+v", "10.50.0.0/24", subnets)
	}
	if subnet.Gateway != "10.50.0.1" {
		t.Errorf("subnet.Gateway = %q, want 10.50.0.1", subnet.Gateway)
	}
}

func findZone(zones []pve.SDNZone, id string) *pve.SDNZone {
	for i := range zones {
		if zones[i].ID == id {
			return &zones[i]
		}
	}
	return nil
}

func findVnet(vnets []pve.SDNVnet, id string) *pve.SDNVnet {
	for i := range vnets {
		if vnets[i].ID == id {
			return &vnets[i]
		}
	}
	return nil
}

func findSubnet(subnets []pve.SDNSubnet, cidr string) *pve.SDNSubnet {
	for i := range subnets {
		if subnets[i].CIDR == cidr {
			return &subnets[i]
		}
	}
	return nil
}
