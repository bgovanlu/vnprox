// SPDX-License-Identifier: Apache-2.0

package drift_test

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/drift"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

func findByCheck(t *testing.T, findings []drift.Finding, check string) []drift.Finding {
	t.Helper()
	var out []drift.Finding
	for _, f := range findings {
		if f.Check == check {
			out = append(out, f)
		}
	}
	return out
}

// TestBridgeDivergence_Presence: same-named bridge on 2 of 3 nodes.
func TestBridgeDivergence_Presence(t *testing.T) {
	g := newGraphWithNodes("pve1", "pve2", "pve3")
	pveBridge(g, "pve1", "vmbr99", 1500, false, nil, nil)
	pveBridge(g, "pve2", "vmbr99", 1500, false, nil, nil)

	svc := drift.New(drift.Config{Graph: g})
	found := findByCheck(t, svc.Findings(), drift.CheckBridgeDivergence)
	if len(found) != 1 {
		t.Fatalf("got %d bridge_divergence findings, want 1: %+v", len(found), found)
	}
	f := found[0]
	if f.Fixable {
		t.Errorf("presence divergence should not be fixable, got Fixable=true")
	}
	if !strings.Contains(f.Detail, "vmbr99") || !strings.Contains(f.Detail, "pve3") {
		t.Errorf("detail = %q, want mention of vmbr99 and pve3", f.Detail)
	}
}

// TestBridgeDivergence_VlanAware: same-named bridge disagrees on
// VLAN-awareness; the fix aligns the minority to the majority.
func TestBridgeDivergence_VlanAware(t *testing.T) {
	g := newGraphWithNodes("pve1", "pve2", "pve3")
	pveBridge(g, "pve1", "vmbr0", 1500, true, nil, nil)
	pveBridge(g, "pve2", "vmbr0", 1500, true, nil, nil)
	pveBridge(g, "pve3", "vmbr0", 1500, false, nil, nil)

	svc := drift.New(drift.Config{Graph: g})
	findings := svc.Findings()
	found := findByCheck(t, findings, drift.CheckBridgeDivergence)
	if len(found) != 1 {
		t.Fatalf("got %d bridge_divergence findings, want 1: %+v", len(found), found)
	}
	f := found[0]
	if !f.Fixable {
		t.Fatalf("vlanAware divergence should be fixable")
	}

	ops, title, ok := svc.FixOps(f.ID)
	if !ok {
		t.Fatalf("FixOps(%s): not found", f.ID)
	}
	if title == "" {
		t.Error("fix title is empty")
	}
	if len(ops) != 1 {
		t.Fatalf("got %d fix ops, want 1: %+v", len(ops), ops)
	}
	op := ops[0]
	if op.Type != change.OpBridgeUpdate {
		t.Errorf("op type = %s, want %s", op.Type, change.OpBridgeUpdate)
	}
	if op.Target != (inventory.Ref{Kind: inventory.KindBridge, Node: "pve3", ID: "vmbr0"}) {
		t.Errorf("op target = %v, want pve3's vmbr0", op.Target)
	}
	params, ok := op.Params.(*change.BridgeUpdateParams)
	if !ok || params.VlanAware == nil || !*params.VlanAware {
		t.Errorf("op params = %+v, want VlanAware=true", op.Params)
	}
}

// TestBridgeDivergence_VidSet: same-named VLAN-aware bridge disagrees on
// its VID set; the fix aligns the minority to the majority set.
func TestBridgeDivergence_VidSet(t *testing.T) {
	g := newGraphWithNodes("pve1", "pve2", "pve3")
	pveBridge(g, "pve1", "vmbr1", 1500, true, []inventory.VidRange{{Low: 10, High: 30}}, nil)
	pveBridge(g, "pve2", "vmbr1", 1500, true, []inventory.VidRange{{Low: 10, High: 30}}, nil)
	pveBridge(g, "pve3", "vmbr1", 1500, true, []inventory.VidRange{{Low: 10, High: 20}}, nil)

	svc := drift.New(drift.Config{Graph: g})
	found := findByCheck(t, svc.Findings(), drift.CheckBridgeDivergence)
	if len(found) != 1 {
		t.Fatalf("got %d bridge_divergence findings, want 1: %+v", len(found), found)
	}
	f := found[0]
	if !f.Fixable {
		t.Fatalf("vid-set divergence should be fixable")
	}
	ops, _, ok := svc.FixOps(f.ID)
	if !ok || len(ops) != 1 {
		t.Fatalf("FixOps(%s) = %v, %v ops, want 1 op", f.ID, ok, len(ops))
	}
	if ops[0].Target.Node != "pve3" {
		t.Errorf("fix targets node %s, want pve3", ops[0].Target.Node)
	}
	params, ok := ops[0].Params.(*change.BridgeUpdateParams)
	if !ok || params.Vids == nil {
		t.Fatalf("params = %+v, want non-nil Vids", ops[0].Params)
	}
	if len(*params.Vids) != 1 || (*params.Vids)[0] != (change.VidRange{Low: 10, High: 30}) {
		t.Errorf("fix vids = %+v, want [{10 30}]", *params.Vids)
	}
}

// TestBridgeDivergence_Clean: identical same-named bridges across three
// nodes produce no findings (T-305 acceptance criterion 2's "clean-cluster
// no-findings case").
func TestBridgeDivergence_Clean(t *testing.T) {
	g := newGraphWithNodes("pve1", "pve2", "pve3")
	for _, n := range []string{"pve1", "pve2", "pve3"} {
		pveBridge(g, n, "vmbr0", 1500, true, []inventory.VidRange{{Low: 10, High: 30}}, nil)
	}
	svc := drift.New(drift.Config{Graph: g})
	if found := findByCheck(t, svc.Findings(), drift.CheckBridgeDivergence); len(found) != 0 {
		t.Errorf("got %d bridge_divergence findings on a clean cluster, want 0: %+v", len(found), found)
	}
}

// TestBridgeDivergence_SingleNodeBridgeIgnored: a bridge that exists on
// only one node is not a divergence (nothing to compare it against).
func TestBridgeDivergence_SingleNodeBridgeIgnored(t *testing.T) {
	g := newGraphWithNodes("pve1", "pve2")
	pveBridge(g, "pve1", "vmbr-local", 1500, false, nil, nil)
	svc := drift.New(drift.Config{Graph: g})
	if found := findByCheck(t, svc.Findings(), drift.CheckBridgeDivergence); len(found) != 0 {
		t.Errorf("got %d findings for a single-node-only bridge, want 0: %+v", len(found), found)
	}
}
