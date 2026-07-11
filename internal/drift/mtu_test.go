package drift_test

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/drift"
)

// TestMTUConsistency_Path: a bridge's own MTU disagrees with its port's
// MTU on the same node — detection only, no computable fix (which side is
// "correct" is ambiguous).
func TestMTUConsistency_Path(t *testing.T) {
	g := newGraphWithNodes("pve1")
	pvePhysNic(g, "pve1", "eno1", 1500)
	pveBridge(g, "pve1", "vmbr0", 9000, false, nil, []string{"eno1"})

	svc := drift.New(drift.Config{Graph: g})
	found := findByCheck(t, svc.Findings(), drift.CheckMTUConsistency)
	if len(found) != 1 {
		t.Fatalf("got %d mtu_consistency findings, want 1: %+v", len(found), found)
	}
	f := found[0]
	if f.Fixable {
		t.Errorf("path mismatch should not be fixable")
	}
	if !strings.Contains(f.Detail, "vmbr0") || !strings.Contains(f.Detail, "eno1") {
		t.Errorf("detail = %q, want mention of vmbr0 and eno1", f.Detail)
	}
}

// TestMTUConsistency_CrossNode: the same-named bridge's MTU disagrees
// across cluster nodes — fixable via "MTU alignment" (docs card's second
// named computable-fix family).
func TestMTUConsistency_CrossNode(t *testing.T) {
	g := newGraphWithNodes("pve1", "pve2", "pve3")
	pveBridge(g, "pve1", "vmbr0", 1500, false, nil, nil)
	pveBridge(g, "pve2", "vmbr0", 9000, false, nil, nil)
	pveBridge(g, "pve3", "vmbr0", 1500, false, nil, nil)

	svc := drift.New(drift.Config{Graph: g})
	found := findByCheck(t, svc.Findings(), drift.CheckMTUConsistency)
	if len(found) != 1 {
		t.Fatalf("got %d mtu_consistency findings, want 1: %+v", len(found), found)
	}
	f := found[0]
	if !f.Fixable {
		t.Fatalf("cross-node MTU divergence should be fixable")
	}
	ops, title, ok := svc.FixOps(f.ID)
	if !ok || len(ops) != 1 {
		t.Fatalf("FixOps(%s) = ok=%v ops=%d, want 1 op", f.ID, ok, len(ops))
	}
	if title == "" {
		t.Error("fix title is empty")
	}
	op := ops[0]
	if op.Type != change.OpBridgeUpdate || op.Target.Node != "pve2" {
		t.Fatalf("op = %+v, want bridge.update targeting pve2", op)
	}
	params, ok := op.Params.(*change.BridgeUpdateParams)
	if !ok || params.MTU == nil || *params.MTU != 1500 {
		t.Errorf("params = %+v, want MTU=1500 (majority)", op.Params)
	}
}

// TestMTUConsistency_Clean: consistent MTU end to end and across nodes
// produces no findings.
func TestMTUConsistency_Clean(t *testing.T) {
	g := newGraphWithNodes("pve1", "pve2")
	for _, n := range []string{"pve1", "pve2"} {
		pvePhysNic(g, n, "eno1", 1500)
		pveBridge(g, n, "vmbr0", 1500, false, nil, []string{"eno1"})
	}
	svc := drift.New(drift.Config{Graph: g})
	if found := findByCheck(t, svc.Findings(), drift.CheckMTUConsistency); len(found) != 0 {
		t.Errorf("got %d mtu_consistency findings on a clean cluster, want 0: %+v", len(found), found)
	}
}
