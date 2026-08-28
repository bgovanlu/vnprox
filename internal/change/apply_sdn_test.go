// SPDX-License-Identifier: Apache-2.0

package change_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// sdnFakeInventory is a minimal InventorySource modeling the three-node
// fixture's real state: the three cluster nodes plus vmbr0 on each (the
// fixture's own real bridge, matching testdata/clusters/three-node-vlan.yaml)
// — enough for T-402's referential ("node must exist") and sdn
// ("bridge must exist on member node") pre-apply validators to pass for a
// vlan zone assigned to all three nodes using vmbr0.
type sdnFakeInventory struct{ g *inventory.Graph }

func newSDNFakeInventory() *sdnFakeInventory {
	f := &sdnFakeInventory{g: inventory.NewGraph()}
	var ents []inventory.Entity
	for _, node := range []string{"pve1", "pve2", "pve3"} {
		ents = append(ents,
			&inventory.Node{Ref: inventory.Ref{Kind: inventory.KindNode, Node: node, ID: node}, Name: node, Status: "online", Quorate: true},
			&inventory.Bridge{Ref: inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: "vmbr0"}, Name: "vmbr0", Virt: inventory.BridgeLinux},
		)
	}
	f.g.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{}, ents)
	return f
}

func (f *sdnFakeInventory) Snapshot() inventory.Snapshot { return f.g.Snapshot() }

// newSDNHarness builds a harness like newHarness but against the three-node
// fixture with the InventorySource above wired in, so SDN pre-apply
// validation (zone node coverage / bridge existence, T-402) sees real
// cluster/bridge state instead of newHarness' default empty snapshot.
func newSDNHarness(t *testing.T) *applyHarness {
	t.Helper()
	h := newHarness(t, fixtureThreeNode)
	svc := newService(t, change.Config{
		Changesets: h.csRepo, Audit: h.auditRepo, WS: h.ws,
		Nodes: h.agent, Snapshots: h.snapRepo, Blobs: h.blobRepo, Refresher: h.refresher,
		TimerFunc: h.timers.New, Inventory: newSDNFakeInventory(),
	})
	h.svc = svc
	return h
}

// sdnLifecycleOps builds AC1's changeset: a VLAN zone (on vmbr0, all three
// nodes — matching the fixture's real bridge), one VNet, one subnet, and a
// trailing sdn.apply.
func sdnLifecycleOps(zoneID, vnetID, cidr string, tag int) []change.Op {
	return []change.Op{
		{
			Type:   change.OpSdnZoneCreate,
			Target: inventory.Ref{Kind: inventory.KindSDNZone, ID: zoneID},
			Params: &change.SdnZoneCreateParams{Type: "vlan", Bridge: "vmbr0", Nodes: []string{"pve1", "pve2", "pve3"}, MTU: 1500},
		},
		{
			Type:   change.OpSdnVnetCreate,
			Target: inventory.Ref{Kind: inventory.KindSDNVnet, ID: zoneID + "/" + vnetID},
			Params: &change.SdnVnetCreateParams{Zone: zoneID, Alias: "t402", Tag: tag},
		},
		{
			Type:   change.OpSdnSubnetCreate,
			Target: inventory.Ref{Kind: inventory.KindSDNSubnet, ID: cidr},
			Params: &change.SdnSubnetCreateParams{Vnet: zoneID + "/" + vnetID, CIDR: cidr, Gateway: gatewayOf(cidr)},
		},
		sdnApplyOp(),
	}
}

// gatewayOf returns cidr's ".1" host address, for a quick, deterministic
// subnet gateway in test fixtures (e.g. "10.60.0.0/24" -> "10.60.0.1").
func gatewayOf(cidr string) string {
	base := strings.TrimSuffix(cidr, "0/24")
	return base + "1"
}

// TestApply_SDNLifecycle_ZoneVnetSubnet is T-402 acceptance criterion 1:
// create a VLAN zone + vnet + subnet in one changeset against pvemock; the
// plan puts sdn.apply last; apply succeeds with per-node/step progress
// recorded; confirming commits the changeset; the pvemock SDN fixture state
// (running/last-applied view) reflects the new zone/vnet/subnet.
func TestApply_SDNLifecycle_ZoneVnetSubnet(t *testing.T) {
	h := newSDNHarness(t)
	ctx := context.Background()
	gw := &fakePVEGateway{client: h.client, pollNode: "pve1"}

	ops := sdnLifecycleOps("zoneT402", "vnetT402", "10.60.0.0/24", 60)
	cs := h.mustCreate(t, "root@pam", "sdn lifecycle", ops)

	// Plan: exactly 4 steps (3 sdn_stage + 1 sdn_apply), sdn.apply last.
	plan := mustPlan(t, ops)
	if len(plan.Steps) != 4 {
		t.Fatalf("plan has %d steps, want 4: %+v", len(plan.Steps), plan.Steps)
	}
	for i := 0; i < 3; i++ {
		if plan.Steps[i].Kind != change.StepSDNStage {
			t.Fatalf("step %d kind = %s, want sdn_stage", i, plan.Steps[i].Kind)
		}
	}
	if plan.Steps[3].Kind != change.StepSDNApply {
		t.Fatalf("last step kind = %s, want sdn_apply", plan.Steps[3].Kind)
	}

	got, err := h.svc.Apply(ctx, cs.ID, "root@pam", gw, 0)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got.Status != change.StatusAwaitingConfirm {
		t.Fatalf("status = %s, want awaiting_confirm", got.Status)
	}

	// Per-node/step progress: every step recorded OK, sdn_apply carries a
	// task UPID (T-402: "per-node progress from the resulting PVE tasks").
	log := h.applyLog(t, cs.ID)
	if len(log.Steps) != 4 {
		t.Fatalf("apply log has %d steps, want 4", len(log.Steps))
	}
	for i, s := range log.Steps {
		if s.Status != change.StepOK {
			t.Fatalf("step %d status = %s, want ok: %+v", i, s.Status, log.Steps)
		}
	}
	if log.Steps[3].TaskUPID == "" {
		t.Errorf("sdn_apply step carries no TaskUPID")
	}

	if _, confirmErr := h.svc.Confirm(ctx, cs.ID, "root@pam"); confirmErr != nil {
		t.Fatalf("confirm: %v", confirmErr)
	}
	committed := h.get(t, cs.ID)
	if committed.Status != change.StatusCommitted {
		t.Fatalf("status after confirm = %s, want committed", committed.Status)
	}

	// Fixture SDN state updated: the running (last-applied) view now has
	// the new zone/vnet/subnet, pending cleared.
	zones, err := h.client.ListSDNZonesRunning(ctx)
	if err != nil {
		t.Fatalf("ListSDNZonesRunning: %v", err)
	}
	var zone *pve.SDNZone
	for i := range zones {
		if zones[i].ID == "zoneT402" {
			zone = &zones[i]
		}
	}
	if zone == nil {
		t.Fatalf("zone zoneT402 not in running view: %+v", zones)
	}
	if zone.Pending != "" {
		t.Errorf("zone pending = %q, want cleared", zone.Pending)
	}
	if zone.Bridge != "vmbr0" || len(zone.Nodes) != 3 {
		t.Errorf("zone = %+v, want bridge vmbr0 on 3 nodes", zone)
	}

	vnets, err := h.client.ListSDNVnetsRunning(ctx)
	if err != nil {
		t.Fatalf("ListSDNVnetsRunning: %v", err)
	}
	var foundVnet bool
	for _, v := range vnets {
		// PVE's own vnet id is bare (no zone prefix — see pve.SDNVnetID's
		// doc comment); Zone is the separate field that recovers the
		// "zone/vnet" internal/change Ref.ID convention.
		if v.ID == "vnetT402" && v.Zone == "zoneT402" && v.Tag == 60 {
			foundVnet = true
		}
	}
	if !foundVnet {
		t.Fatalf("vnet zoneT402/vnetT402 (tag 60) not in running view: %+v", vnets)
	}

	subnets, err := h.client.ListSDNSubnetsRunning(ctx, "vnetT402")
	if err != nil {
		t.Fatalf("ListSDNSubnetsRunning: %v", err)
	}
	var foundSubnet bool
	for _, s := range subnets {
		if s.CIDR == "10.60.0.0/24" {
			foundSubnet = true
		}
	}
	if !foundSubnet {
		t.Fatalf("subnet 10.60.0.0/24 not in running view: %+v", subnets)
	}
}

// TestApply_SDNZone_ExitNodesAndPeers_RoundTrip is T-403: the EVPN zone
// wizard's exit-node/peer selections must actually survive an apply, not
// just be accepted by validation — proving the write-path gap flagged in
// params_sdn.go's SdnZoneCreateParams doc comment (ExitNodes/Peers were
// readable via GET /sdn since T-401 but had no changeset-op way to set them
// until this task) is genuinely closed end to end.
func TestApply_SDNZone_ExitNodesAndPeers_RoundTrip(t *testing.T) {
	h := newSDNHarness(t)
	ctx := context.Background()
	gw := &fakePVEGateway{client: h.client, pollNode: "pve1"}

	ops := []change.Op{
		{
			Type:   change.OpSdnZoneCreate,
			Target: inventory.Ref{Kind: inventory.KindSDNZone, ID: "zoneT403evpn"},
			Params: &change.SdnZoneCreateParams{
				Type:      "evpn",
				Nodes:     []string{"pve1", "pve2", "pve3"},
				ExitNodes: []string{"pve1", "pve2"},
				Peers:     []string{"10.10.0.11", "10.10.0.12", "10.10.0.13"},
			},
		},
		sdnApplyOp(),
	}
	cs := h.mustCreate(t, "root@pam", "sdn exit nodes/peers", ops)

	if _, err := h.svc.Apply(ctx, cs.ID, "root@pam", gw, 0); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := h.svc.Confirm(ctx, cs.ID, "root@pam"); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	zones, err := h.client.ListSDNZonesRunning(ctx)
	if err != nil {
		t.Fatalf("ListSDNZonesRunning: %v", err)
	}
	var zone *pve.SDNZone
	for i := range zones {
		if zones[i].ID == "zoneT403evpn" {
			zone = &zones[i]
		}
	}
	if zone == nil {
		t.Fatalf("zone zoneT403evpn not in running view: %+v", zones)
	}
	wantExit := []string{"pve1", "pve2"}
	if !reflect.DeepEqual(zone.ExitNodes, wantExit) {
		t.Errorf("exitNodes = %v, want %v", zone.ExitNodes, wantExit)
	}
	wantPeers := []string{"10.10.0.11", "10.10.0.12", "10.10.0.13"}
	if !reflect.DeepEqual(zone.Peers, wantPeers) {
		t.Errorf("peers = %v, want %v", zone.Peers, wantPeers)
	}
}

// TestApply_SDN_InjectedNodeFailure_RollsBack is T-402 acceptance criterion
// 4: a node's SDN apply status is injected to fail post-task (pvemock's
// per-node sdn-status-fail control, modeling a node whose apply task itself
// reported success but which nonetheless failed to realize the config).
// The PUT /cluster/sdn task itself succeeds, but T-402's post-apply health
// verification catches the unhealthy node, the changeset ends up failed,
// the sdn config is rolled back (the newly-created zone/vnet/subnet no
// longer exist, staged or running), and the apply log carries a task-log
// deep link (node + UPID) for the failing step.
func TestApply_SDN_InjectedNodeFailure_RollsBack(t *testing.T) {
	h := newSDNHarness(t)
	ctx := context.Background()
	gw := &fakePVEGateway{client: h.client, pollNode: "pve1"}

	h.setSDNZoneStatusFail(t, "pve3", true)
	t.Cleanup(func() { h.setSDNZoneStatusFail(t, "pve3", false) })

	ops := sdnLifecycleOps("zoneFail", "vnetFail", "10.61.0.0/24", 61)
	cs := h.mustCreate(t, "root@pam", "sdn failure", ops)

	_, err := h.svc.Apply(ctx, cs.ID, "root@pam", gw, 0)
	if err == nil {
		t.Fatal("apply should have failed post-apply health verification")
	}
	var unhealthy *change.ErrSDNZoneUnhealthy
	if !errors.As(err, &unhealthy) {
		t.Fatalf("apply err = %v, want *ErrSDNZoneUnhealthy", err)
	}
	if unhealthy.Zone != "zoneFail" || unhealthy.Node != "pve3" || unhealthy.Status != "error" {
		t.Errorf("unhealthy = %+v, want zone=zoneFail node=pve3 status=error", unhealthy)
	}
	if unhealthy.UPID == "" || unhealthy.TaskNode == "" {
		t.Errorf("unhealthy = %+v, missing task-log deep link (UPID/TaskNode)", unhealthy)
	}

	got := h.get(t, cs.ID)
	if got.Status != change.StatusFailed {
		t.Fatalf("status = %s, want failed", got.Status)
	}

	// Apply log: the sdn_apply step is the failed one and carries the same
	// task-log deep link (TaskUPID/Node) even though it "failed" only at
	// health verification, not the task itself.
	log := h.applyLog(t, cs.ID)
	if log.FailedStep == nil {
		t.Fatal("apply log has no FailedStep")
	}
	failedStep := log.Steps[*log.FailedStep]
	if failedStep.Kind != change.StepSDNApply {
		t.Fatalf("failed step kind = %s, want sdn_apply", failedStep.Kind)
	}
	if failedStep.TaskUPID == "" {
		t.Errorf("failed sdn_apply step carries no TaskUPID (no task-log deep link)")
	}

	// Rollback restored sdn/*.cfg: the newly-created zone/vnet/subnet are
	// gone from both the staged and running views.
	var sawSDNRollback bool
	for _, rb := range log.Rollback {
		if strings.Contains(rb.Summary, "sdn") {
			sawSDNRollback = true
			if rb.Status != change.StepOK {
				t.Errorf("sdn rollback log entry = %+v, want ok", rb)
			}
		}
	}
	if !sawSDNRollback {
		t.Fatalf("apply log carries no sdn rollback entry: %+v", log.Rollback)
	}

	zones, err := h.client.ListSDNZones(ctx)
	if err != nil {
		t.Fatalf("ListSDNZones: %v", err)
	}
	for _, z := range zones {
		if z.ID == "zoneFail" {
			t.Fatalf("zone zoneFail still present after rollback: %+v", z)
		}
	}
	vnets, err := h.client.ListSDNVnets(ctx)
	if err != nil {
		t.Fatalf("ListSDNVnets: %v", err)
	}
	for _, v := range vnets {
		if v.ID == "vnetFail" && v.Zone == "zoneFail" {
			t.Fatalf("vnet zoneFail/vnetFail still present after rollback: %+v", v)
		}
	}
}
