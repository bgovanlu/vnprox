package xnode_test

// T-801 acceptance criterion 4: the three crossnode.* comparison families and
// internal/drift's CheckBridgeDivergence/CheckMTUConsistency/CheckSDNRealization
// share ONE implementation — not merely equivalent-looking code. This test
// lives in a neutral package that imports internal/xnode directly and asserts
// that a drift finding and a change (cross-node validator) finding for the
// same input both carry byte-identical detail/message text produced by the
// same xnode function — which they can only do if both route through it.

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/drift"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/xnode"
)

func ref(kind inventory.Kind, node, id string) inventory.Ref {
	return inventory.Ref{Kind: kind, Node: node, ID: id}
}

// buildDivergentGraph builds a 3-node cluster whose same-named bridge vmbr0
// has an MTU that diverges on pve1 (9000 vs. the 1500 majority) — a genuine
// cross-node MTU divergence every one of the three consumers can observe.
func buildDivergentGraph() *inventory.Graph {
	g := inventory.NewGraph()
	var nodes []inventory.Entity
	for _, n := range []string{"pve1", "pve2", "pve3"} {
		nodes = append(nodes, &inventory.Node{Ref: ref(inventory.KindNode, n, n), Name: n, Status: "online"})
	}
	g.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{}, nodes)
	mtus := map[string]int{"pve1": 9000, "pve2": 1500, "pve3": 1500}
	for _, n := range []string{"pve1", "pve2", "pve3"} {
		br := &inventory.Bridge{
			Ref: ref(inventory.KindBridge, n, "vmbr0"), Name: "vmbr0", Virt: inventory.BridgeLinux,
			MTU: mtus[n],
		}
		g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: n, Kinds: []inventory.Kind{inventory.KindBridge}}, []inventory.Entity{br})
	}
	return g
}

func TestSharedImplementation_MTUDivergence(t *testing.T) {
	g := buildDivergentGraph()
	snap := g.Snapshot()

	// (1) The shared comparison, called directly.
	divs := xnode.CrossNodeMTU(snap)
	if len(divs) != 1 {
		t.Fatalf("xnode.CrossNodeMTU = %d divergences, want 1: %+v", len(divs), divs)
	}
	want := divs[0].Detail
	if want == "" {
		t.Fatal("xnode divergence has empty detail")
	}

	// (2) internal/drift's finding for the same input.
	var driftDetail string
	for _, f := range drift.New(drift.Config{Graph: g}).Findings() {
		if f.Check == drift.CheckMTUConsistency && f.Fixable {
			driftDetail = f.Detail
		}
	}
	if driftDetail != want {
		t.Errorf("drift detail =\n  %q\nxnode detail =\n  %q\nnot the same implementation", driftDetail, want)
	}

	// (3) internal/change's cross-node validator finding for the same input.
	// A bridge.update that merely touches vmbr0 (no MTU change) leaves the
	// projected divergence intact, so the class reports it.
	touch := change.Op{
		Type:   change.OpBridgeUpdate,
		Target: ref(inventory.KindBridge, "pve1", "vmbr0"),
		Params: &change.BridgeUpdateParams{Comments: strptr("touch")},
	}
	var changeMsg string
	var changeFix []change.Op
	for _, f := range change.Validate([]change.Op{touch}, snap) {
		if f.Code == "crossnode.mtu_consistency" {
			changeMsg = f.Message
			changeFix = f.Fix
		}
	}
	if changeMsg != want {
		t.Errorf("change message =\n  %q\nxnode detail =\n  %q\nnot the same implementation", changeMsg, want)
	}

	// The fix op construction is also shared (change.CrossNodeFixOps, called
	// by both drift and change): both align pve1 to the 1500 majority.
	if len(changeFix) != 1 {
		t.Fatalf("change fix = %d ops, want 1: %+v", len(changeFix), changeFix)
	}
	p, ok := changeFix[0].Params.(*change.BridgeUpdateParams)
	if !ok || p.MTU == nil || *p.MTU != 1500 || changeFix[0].Target.Node != "pve1" {
		t.Errorf("change fix = %+v, want bridge.update pve1 MTU=1500", changeFix[0])
	}
}

func strptr(s string) *string { return &s }
