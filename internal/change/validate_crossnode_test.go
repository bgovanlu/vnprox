// SPDX-License-Identifier: Apache-2.0

package change

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/xnode"
)

// The cross-node consistency validator class (T-801) folds a changeset's
// projected effect across the whole cluster and compares same-named
// bridges/MTU/SDN-realization across nodes, sharing its comparison logic with
// internal/drift through internal/xnode. These white-box tests build snapshots
// shaped like the three-node-vlan and evpn-lab fixtures (a same-named,
// VLAN-aware, VID-trunked bridge present on every node; an SDN zone realized
// by a same-named bridge on every member) so they can exercise crossnodeValidate
// and sdnValidate directly. Companion tests in validate_crossnode_fixture_test.go
// drive the exact same scenarios through the public change.Validate against the
// real pvemock fixtures.

// crossnodeGraph accumulates entities and materializes them into a graph on
// snap(), batching one poll per (source, node) so incremental adds never
// retire one another via removal reconciliation (nodes via pve-cluster,
// bridges via host-netlink per node, zones via pve-sdn) — every field resolves
// through the real merge machinery.
type crossnodeGraph struct {
	nodes    []inventory.Entity
	bridges  map[string][]inventory.Entity
	zones    []inventory.Entity
	nodeList []string
}

func newCrossnodeGraph(nodes ...string) *crossnodeGraph {
	c := &crossnodeGraph{bridges: map[string][]inventory.Entity{}, nodeList: nodes}
	for _, n := range nodes {
		c.nodes = append(c.nodes, &inventory.Node{
			Ref: testRef(inventory.KindNode, n, n), Name: n, Status: "online",
		})
	}
	return c
}

// bridge adds one node's VLAN-aware bridge (runtime MTU + declared
// vlan-awareness/VID set — the fields the cross-node comparison reads).
func (c *crossnodeGraph) bridge(node, name string, mtu int, vlanAware bool, vids []inventory.VidRange) *crossnodeGraph {
	c.bridges[node] = append(c.bridges[node], &inventory.Bridge{
		Ref: testRef(inventory.KindBridge, node, name), Name: name, Virt: inventory.BridgeLinux,
		MTU: mtu, VlanAware: vlanAware, VlanAwareSet: true, Vids: vids,
	})
	return c
}

// zone adds a cluster-scoped SDN zone realized by a same-named per-node bridge.
func (c *crossnodeGraph) zone(id, typ, bridge string, nodes []string) *crossnodeGraph {
	c.zones = append(c.zones, &inventory.SdnZone{
		Ref: testRef(inventory.KindSDNZone, "", id), ID: id, Type: typ, Bridge: bridge, Nodes: nodes,
	})
	return c
}

func (c *crossnodeGraph) snap() inventory.Snapshot {
	g := inventory.NewGraph()
	g.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{}, c.nodes)
	for node, brs := range c.bridges {
		g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{Node: node, Kinds: []inventory.Kind{inventory.KindBridge}}, brs)
	}
	if len(c.zones) > 0 {
		g.ApplyPoll(inventory.SourcePVESDN, inventory.Scope{}, c.zones)
	}
	return g.Snapshot()
}

func vid(n int) inventory.VidRange { return inventory.VidRange{Low: n, High: n} }

func findByCode(findings []Finding, code string) []Finding {
	var out []Finding
	for _, f := range findings {
		if f.Code == code {
			out = append(out, f)
		}
	}
	return out
}

func bridgeUpdate(node, name string, params *BridgeUpdateParams) Op {
	return mkOp(OpBridgeUpdate, testRef(inventory.KindBridge, node, name), params)
}

// TestCrossnode_BridgeVIDDivergence_FixRestores is T-801 acceptance
// criterion 1: an op that removes a VID from one node's trunk while the same
// VID stays tagged elsewhere in the projected cluster is a
// crossnode.bridge_divergence error whose fix restores the VID; applying the
// fix revalidates clean.
func TestCrossnode_BridgeVIDDivergence_FixRestores(t *testing.T) {
	// three-node-vlan-shaped: vmbr0 VLAN-aware carrying VIDs {10,20,30} on
	// every node. (The real fixture leaves vmbr0's VID set implicit/"all", so
	// the explicit set here is what makes "remove a VID" a meaningful op.)
	base := newCrossnodeGraph("pve1", "pve2", "pve3")
	for _, n := range []string{"pve1", "pve2", "pve3"} {
		base.bridge(n, "vmbr0", 1500, true, []inventory.VidRange{vid(10), vid(20), vid(30)})
	}
	snap := base.snap()

	// Remove VID 20 from pve1's trunk only.
	drop20 := bridgeUpdate("pve1", "vmbr0", &BridgeUpdateParams{Vids: &[]VidRange{{Low: 10, High: 10}, {Low: 30, High: 30}}})

	findings := Validate([]Op{drop20}, snap)
	bd := findByCode(findings, codeCrossnodeBridge)
	if len(bd) != 1 {
		t.Fatalf("crossnode.bridge_divergence findings = %d, want 1: %+v", len(bd), findings)
	}
	f := bd[0]
	if f.Severity != SeverityError {
		t.Errorf("severity = %s, want error", f.Severity)
	}
	if len(f.Fix) != 1 {
		t.Fatalf("fix ops = %d, want 1 (restore the dropped VID on pve1): %+v", len(f.Fix), f.Fix)
	}
	fix := f.Fix[0]
	if fix.Type != OpBridgeUpdate || fix.Target.Node != "pve1" {
		t.Fatalf("fix op = %+v, want bridge.update targeting pve1", fix)
	}
	p, ok := fix.Params.(*BridgeUpdateParams)
	if !ok || p.Vids == nil {
		t.Fatalf("fix params = %+v, want non-nil Vids", fix.Params)
	}
	// Majority (pve2+pve3) is {10,20,30}; the fix restores that set on pve1.
	if len(*p.Vids) != 3 {
		t.Errorf("fix Vids = %+v, want the 3-VID majority set {10,20,30}", *p.Vids)
	}

	// Applying the fix (it shares pve1's vmbr0 target, so it supersedes the
	// offending op) revalidates with no cross-node error.
	after := Validate(f.Fix, snap)
	if len(findByCode(after, codeCrossnodeBridge)) != 0 {
		t.Errorf("after applying fix, still have bridge divergence: %+v", after)
	}
	if hasError(after) {
		t.Errorf("after applying fix, unexpected error findings: %+v", after)
	}
}

// TestCrossnode_MTUDivergence_MajorityFix is T-801 acceptance criterion 2: an
// op setting one node's bridge MTU divergent from the cluster's same-named
// bridge is a crossnode.mtu_consistency error whose fix aligns the outlier to
// the majority MTU; applying the fix revalidates clean.
func TestCrossnode_MTUDivergence_MajorityFix(t *testing.T) {
	base := newCrossnodeGraph("pve1", "pve2", "pve3")
	for _, n := range []string{"pve1", "pve2", "pve3"} {
		base.bridge(n, "vmbr0", 1500, true, nil)
	}
	snap := base.snap()

	bump := bridgeUpdate("pve1", "vmbr0", &BridgeUpdateParams{MTU: intPtr(9000)})

	findings := Validate([]Op{bump}, snap)
	mtu := findByCode(findings, codeCrossnodeMTU)
	if len(mtu) != 1 {
		t.Fatalf("crossnode.mtu_consistency findings = %d, want 1: %+v", len(mtu), findings)
	}
	f := mtu[0]
	if f.Severity != SeverityError {
		t.Errorf("severity = %s, want error", f.Severity)
	}
	if len(f.Fix) != 1 {
		t.Fatalf("fix ops = %d, want 1: %+v", len(f.Fix), f.Fix)
	}
	fix := f.Fix[0]
	if fix.Target.Node != "pve1" {
		t.Errorf("fix targets %s, want pve1 (the outlier)", fix.Target.Node)
	}
	p, ok := fix.Params.(*BridgeUpdateParams)
	if !ok || p.MTU == nil || *p.MTU != 1500 {
		t.Errorf("fix params = %+v, want MTU=1500 (majority)", fix.Params)
	}

	after := Validate(f.Fix, snap)
	if len(findByCode(after, codeCrossnodeMTU)) != 0 {
		t.Errorf("after applying fix, still have MTU divergence: %+v", after)
	}
	if hasError(after) {
		t.Errorf("after applying fix, unexpected error findings: %+v", after)
	}
}

// TestCrossnode_SDNRealization_BareBridgeDelete is T-801 acceptance
// criterion 3: a changeset containing only a bridge.delete (no sdn.* ops) that
// removes a zone-realizing bridge on one member node is a
// crossnode.sdn_realization error with no fix — and the companion assertion
// that sdnValidate alone (T-402's class, unmodified) catches nothing of the
// sort, proving this is the gap T-801 closes, not a redundant check.
func TestCrossnode_SDNRealization_BareBridgeDelete(t *testing.T) {
	// evpn-lab-shaped simple zone "simplez" realized by vmbr1 on every node.
	base := newCrossnodeGraph("pve1", "pve2", "pve3")
	for _, n := range []string{"pve1", "pve2", "pve3"} {
		base.bridge(n, "vmbr1", 1500, false, nil)
	}
	base.zone("simplez", "simple", "vmbr1", []string{"pve1", "pve2", "pve3"})
	snap := base.snap()

	del := mkOp(OpBridgeDelete, testRef(inventory.KindBridge, "pve2", "vmbr1"), &BridgeDeleteParams{})

	// The cross-node class catches it.
	cn := crossnodeValidate([]Op{del}, snap)
	sdn := findByCode(cn, codeCrossnodeSDN)
	if len(sdn) != 1 {
		t.Fatalf("crossnode.sdn_realization findings = %d, want 1: %+v", len(sdn), cn)
	}
	if sdn[0].Severity != SeverityError {
		t.Errorf("severity = %s, want error", sdn[0].Severity)
	}
	if len(sdn[0].Fix) != 0 {
		t.Errorf("sdn realization finding must have no fix, got %+v", sdn[0].Fix)
	}

	// Companion negative: T-402's SDN class, run over the identical op, finds
	// nothing — it only inspects zones the changeset's own sdn.* ops name, and
	// there are none here.
	if got := sdnValidate([]Op{del}, snap); len(got) != 0 {
		t.Fatalf("sdnValidate should not catch a bare bridge.delete's realization break, got %+v", got)
	}
}

// TestCrossnode_Lockstep_NoFindings is the hand-built half of T-801 acceptance
// criterion 5: a changeset that changes every node's same-named bridge in
// lockstep introduces no divergence, so the class emits zero findings.
func TestCrossnode_Lockstep_NoFindings(t *testing.T) {
	base := newCrossnodeGraph("pve1", "pve2", "pve3")
	for _, n := range []string{"pve1", "pve2", "pve3"} {
		base.bridge(n, "vmbr0", 1500, true, []inventory.VidRange{vid(10), vid(20)})
	}
	base.zone("vlanz", "vlan", "vmbr0", []string{"pve1", "pve2", "pve3"})
	snap := base.snap()

	var ops []Op
	for _, n := range []string{"pve1", "pve2", "pve3"} {
		ops = append(ops, bridgeUpdate(n, "vmbr0", &BridgeUpdateParams{MTU: intPtr(9000)}))
	}

	if got := crossnodeValidate(ops, snap); len(got) != 0 {
		t.Fatalf("lockstep change produced %d cross-node findings, want 0: %+v", len(got), got)
	}
}

// TestCrossnode_UntouchedBridgeNotReported proves the scoping rule: a
// pre-existing divergence on a bridge the changeset does not touch is not this
// changeset's responsibility (that's the async drift checker's job), so it is
// not reported even though the projected cluster is genuinely inconsistent.
func TestCrossnode_UntouchedBridgeNotReported(t *testing.T) {
	base := newCrossnodeGraph("pve1", "pve2", "pve3")
	// vmbr9 diverges in MTU across the cluster already — but the changeset
	// only touches vmbr0.
	base.bridge("pve1", "vmbr9", 1500, false, nil)
	base.bridge("pve2", "vmbr9", 9000, false, nil)
	base.bridge("pve3", "vmbr9", 1500, false, nil)
	for _, n := range []string{"pve1", "pve2", "pve3"} {
		base.bridge(n, "vmbr0", 1500, false, nil)
	}
	snap := base.snap()

	op := bridgeUpdate("pve1", "vmbr0", &BridgeUpdateParams{Comments: strPtr("touch, no change")})
	if got := crossnodeValidate([]Op{op}, snap); len(got) != 0 {
		t.Fatalf("untouched vmbr9 divergence leaked into findings: %+v", got)
	}

	// The drift checker (which reports every divergence, touched or not) does
	// see vmbr9 — sanity that the divergence is real, just out of this class's
	// scope. Verified via the shared xnode comparison directly in
	// internal/xnode's shared-implementation test.
}

// TestCrossnode_VlanAwareDivergence_Fix covers the VLAN-awareness divergence
// family and its fix (the CrossNodeFixOps VlanAware branch): flipping one
// node's bridge to non-VLAN-aware against a VLAN-aware majority is a
// crossnode.bridge_divergence error whose fix restores VLAN-awareness.
func TestCrossnode_VlanAwareDivergence_Fix(t *testing.T) {
	base := newCrossnodeGraph("pve1", "pve2", "pve3")
	for _, n := range []string{"pve1", "pve2", "pve3"} {
		base.bridge(n, "vmbr0", 1500, true, nil)
	}
	snap := base.snap()

	flip := bridgeUpdate("pve1", "vmbr0", &BridgeUpdateParams{VlanAware: boolPtr(false)})
	findings := Validate([]Op{flip}, snap)
	bd := findByCode(findings, codeCrossnodeBridge)
	if len(bd) != 1 {
		t.Fatalf("crossnode.bridge_divergence = %d, want 1: %+v", len(bd), findings)
	}
	if len(bd[0].Fix) != 1 {
		t.Fatalf("fix ops = %d, want 1: %+v", len(bd[0].Fix), bd[0].Fix)
	}
	p, ok := bd[0].Fix[0].Params.(*BridgeUpdateParams)
	if !ok || p.VlanAware == nil || !*p.VlanAware || bd[0].Fix[0].Target.Node != "pve1" {
		t.Errorf("fix = %+v, want bridge.update pve1 VlanAware=true", bd[0].Fix[0])
	}
}

// TestCrossnode_BridgeCreate_MTUDivergence covers the projection's
// bridge.create fold: creating vmbr0 on a third node with a divergent MTU
// makes the same-named group inconsistent.
func TestCrossnode_BridgeCreate_MTUDivergence(t *testing.T) {
	base := newCrossnodeGraph("pve1", "pve2", "pve3")
	base.bridge("pve1", "vmbr7", 1500, false, nil)
	base.bridge("pve2", "vmbr7", 1500, false, nil)
	snap := base.snap()

	create := mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve3", "vmbr7"),
		&BridgeCreateParams{Comments: "x", MTU: 9000})
	findings := Validate([]Op{create}, snap)
	if len(findByCode(findings, codeCrossnodeMTU)) != 1 {
		t.Fatalf("crossnode.mtu_consistency = %d, want 1: %+v", len(findByCode(findings, codeCrossnodeMTU)), findings)
	}
}

// TestCrossnode_SDNZoneFold covers the projection's sdn.zone.create/update/
// delete folds (and the zone() accessor): an sdn.zone.update that extends a
// zone onto a node lacking its bridge, folded alongside a touching bridge op,
// surfaces the new realization gap; a zone this changeset also deletes never
// does.
func TestCrossnode_SDNZoneFold(t *testing.T) {
	base := newCrossnodeGraph("pve1", "pve2", "pve3")
	// vmbr1 realizes on pve1/pve2 only.
	base.bridge("pve1", "vmbr1", 1500, false, nil)
	base.bridge("pve2", "vmbr1", 1500, false, nil)
	base.zone("simplez", "simple", "vmbr1", []string{"pve1", "pve2"})
	base.zone("doomedz", "simple", "vmbr1", []string{"pve1", "pve2", "pve3"})
	snap := base.snap()

	ops := []Op{
		// A touching bridge op so the class runs at all.
		bridgeUpdate("pve1", "vmbr1", &BridgeUpdateParams{Comments: strPtr("touch")}),
		// Extend simplez onto pve3, which lacks vmbr1 -> new realization gap.
		mkOp(OpSdnZoneUpdate, testRef(inventory.KindSDNZone, "", "simplez"),
			&SdnZoneUpdateParams{Nodes: strsPtr("pve1", "pve2", "pve3")}),
		// Create a fresh zone on the same (partially-realized) bridge.
		mkOp(OpSdnZoneCreate, testRef(inventory.KindSDNZone, "", "newz"),
			&SdnZoneCreateParams{Type: "simple", Bridge: "vmbr1", Nodes: []string{"pve1", "pve2", "pve3"}}),
		// Delete doomedz entirely -> it must not produce a realization gap.
		mkOp(OpSdnZoneDelete, testRef(inventory.KindSDNZone, "", "doomedz"), &SdnZoneDeleteParams{}),
	}

	cn := crossnodeValidate(ops, snap)
	sdn := findByCode(cn, codeCrossnodeSDN)
	zones := map[string]bool{}
	for _, f := range sdn {
		zones[f.Ref] = true
	}
	if !zones[testRef(inventory.KindSDNZone, "", "simplez").String()] {
		t.Errorf("expected a realization gap for simplez, got %+v", sdn)
	}
	if !zones[testRef(inventory.KindSDNZone, "", "newz").String()] {
		t.Errorf("expected a realization gap for newz, got %+v", sdn)
	}
	if zones[testRef(inventory.KindSDNZone, "", "doomedz").String()] {
		t.Errorf("deleted zone doomedz must not produce a realization gap: %+v", sdn)
	}
}

// TestCrossNodeFixOps_Branches covers the shared op builder directly across
// all three fix kinds and the empty input.
func TestCrossNodeFixOps_Branches(t *testing.T) {
	if CrossNodeFixOps(nil) != nil {
		t.Error("CrossNodeFixOps(nil) should be nil")
	}
	va := true
	mtu := 1500
	vids := []inventory.VidRange{{Low: 10, High: 20}}
	ref := testRef(inventory.KindBridge, "pve1", "vmbr0")
	ops := CrossNodeFixOps([]xnode.BridgeFix{
		{Target: ref, VlanAware: &va},
		{Target: ref, MTU: &mtu},
		{Target: ref, Vids: &vids},
	})
	if len(ops) != 3 {
		t.Fatalf("ops = %d, want 3: %+v", len(ops), ops)
	}
	if p := ops[0].Params.(*BridgeUpdateParams); p.VlanAware == nil || !*p.VlanAware {
		t.Errorf("op[0] = %+v, want VlanAware=true", ops[0].Params)
	}
	if p := ops[1].Params.(*BridgeUpdateParams); p.MTU == nil || *p.MTU != 1500 {
		t.Errorf("op[1] = %+v, want MTU=1500", ops[1].Params)
	}
	if p := ops[2].Params.(*BridgeUpdateParams); p.Vids == nil || len(*p.Vids) != 1 {
		t.Errorf("op[2] = %+v, want one Vid range", ops[2].Params)
	}
}

// TestProjectedGraph_Guards exercises the defensive nil/wrong-kind branches of
// the projection accessors and fold (paths the real pipeline can't reach,
// since the referential class short-circuits before this class on a missing
// target — guarded anyway).
func TestProjectedGraph_Guards(t *testing.T) {
	snap := newCrossnodeGraph("pve1").snap()
	g := projectCrossnode(nil, snap)
	nodeRef := testRef(inventory.KindNode, "pve1", "pve1")

	if g.bridge(testRef(inventory.KindBridge, "pve1", "absent")) != nil {
		t.Error("bridge() on an absent ref should be nil")
	}
	if g.bridge(nodeRef) != nil {
		t.Error("bridge() on a non-bridge ref should be nil")
	}
	if g.zone(testRef(inventory.KindSDNZone, "", "absent")) != nil {
		t.Error("zone() on an absent ref should be nil")
	}
	if g.zone(nodeRef) != nil {
		t.Error("zone() on a non-zone ref should be nil")
	}
	// fold on nonexistent targets must be a silent no-op, not a panic.
	g.fold(bridgeUpdate("pve1", "absent", &BridgeUpdateParams{MTU: intPtr(9000)}))
	g.fold(mkOp(OpSdnZoneUpdate, testRef(inventory.KindSDNZone, "", "absent"),
		&SdnZoneUpdateParams{Bridge: strPtr("x")}))
}

// TestCrossnode_NoBridgeOps_Skipped confirms a changeset with no bridge op is
// left entirely to T-402's sdnValidate (this class returns nil early).
func TestCrossnode_NoBridgeOps_Skipped(t *testing.T) {
	base := newCrossnodeGraph("pve1", "pve2", "pve3")
	for _, n := range []string{"pve1", "pve2", "pve3"} {
		base.bridge(n, "vmbr1", 1500, false, nil)
	}
	base.zone("simplez", "simple", "vmbr1", []string{"pve1", "pve2", "pve3"})
	snap := base.snap()

	// A subnet op that touches no bridge — crossnode has nothing to say.
	op := mkOp(OpSdnZoneUpdate, testRef(inventory.KindSDNZone, "", "simplez"),
		&SdnZoneUpdateParams{Nodes: strsPtr("pve1", "pve2", "pve3")})
	if got := crossnodeValidate([]Op{op}, snap); got != nil {
		t.Fatalf("crossnodeValidate with no bridge ops = %+v, want nil", got)
	}
}
