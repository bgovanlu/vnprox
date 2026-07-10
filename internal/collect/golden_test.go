package collect_test

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// threeNodeVlanRefs is the complete expected ref set for the
// three-node-vlan fixture, enumerated in TestGolden_ThreeNodeVLAN's doc
// comment. Returned sorted by Ref string.
func threeNodeVlanRefs() []string {
	var refs []string
	for _, n := range []string{"pve1", "pve2", "pve3"} {
		refs = append(refs,
			"node:"+n+":"+n,
			"physnic:"+n+":eno1",
			"physnic:"+n+":eno2",
			"bond:"+n+":bond0",
			"bridge:"+n+":vmbr0",
			"vlan:"+n+":vmbr0.20",
			"fw-ruleset:"+n+":node",
		)
	}
	refs = append(refs,
		// cluster-scoped SDN + firewall
		"sdn-zone::vlanz",
		"sdn-vnet::vlanz/vnet100",
		"sdn-vnet::vlanz/vnet200",
		"sdn-subnet::10.100.0.0/24",
		"sdn-subnet::10.200.0.0/24",
		"fw-ruleset::cluster",
		// guests (+ their NICs and firewall rulesets)
		"guest:pve1:200",
		"guest-nic:pve1:200/net0",
		"fw-ruleset:pve1:guest/qemu/200",
		"guest:pve2:201",
		"guest-nic:pve2:201/net0",
		"fw-ruleset:pve2:guest/lxc/201",
		// LLDP neighbors, local (pve1) node only
		"lldp-neighbor:pve1:eno1/ac:1f:6b:01:00:01/Te1/0/1",
		"lldp-neighbor:pve1:eno2/ac:1f:6b:02:00:01/Te1/0/1",
	)
	sort.Strings(refs)
	return refs
}

// snapshotRefs returns every ref string in a snapshot, sorted.
func snapshotRefs(snap inventory.Snapshot) []string {
	var refs []string
	for _, e := range snap.All() {
		refs = append(refs, e.GetRef().String())
	}
	sort.Strings(refs)
	return refs
}

// TestGolden_ThreeNodeVLAN is T-104's acceptance criterion 1: against
// internal/pvemock's three-node-vlan fixture, the inventory graph converges
// to the fixture's full expected entity set within two poll cycles.
//
// Expected entity inventory, enumerated directly from
// testdata/clusters/three-node-vlan.yaml:
//
//   - 3 Node entities (pve1, pve2, pve3).
//   - Per node, from PVE's declared network view (FromPVENetwork skips the
//     fixture's "lo" loopback stanza, which is not a modeled entity kind):
//     eno1, eno2 (PhysNic), bond0 (Bond), vmbr0 (Bridge), vmbr0.20 (Vlan) —
//     5 entities x 3 nodes = 15.
//   - SDN: zone "vlanz", vnets "vlanz/vnet100" + "vlanz/vnet200", subnets
//     "10.100.0.0/24" + "10.200.0.0/24" — 5 entities, cluster-scoped.
//   - Guests: qemu 200 "app01" on pve1 (Guest + GuestNic net0), lxc 201
//     "cache01" on pve2 (Guest + GuestNic net0) — 4 entities; pve3 has none.
//   - Firewall rulesets: 1 cluster + 3 node (one per node) + 2 guest (one
//     per guest, 200 and 201) — 6 entities.
//   - LLDP neighbors: eno1 + eno2, but only for pve1 — the fixture marks
//     pve1 "local" (pvemock.handleClusterStatus: first cluster node is
//     always local), and the host/LLDP pollers are local-node-only by
//     design (host.Reader's Real implementation "only ever serves its own
//     node" — reading a peer's host/LLDP state is a future peer-client
//     task, not T-104's). 2 entities.
//
// Total: 3 + 15 + 5 + 4 + 6 + 2 = 35 entities.
func TestGolden_ThreeNodeVLAN(t *testing.T) {
	srv := loadFixtureServer(t, fixtureThreeNode)
	c, graph, _ := newTestCollector(t, srv)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.RunPVELoop(ctx) }()
	go func() { _ = c.RunHostLoop(ctx) }()
	go func() { _ = c.RunLLDPLoop(ctx) }()

	wantRefs := threeNodeVlanRefs()
	// Two poll cycles at the configured (50ms) intervals is 100ms; allow a
	// generous margin for CI scheduling jitter while still bounding the
	// wait well below what would indicate the collectors are stuck.
	waitFor(t, 3*time.Second, "graph to converge to the full three-node-vlan fixture", func() bool {
		return graph.Snapshot().Len() == len(wantRefs)
	})

	// Full sorted ref-set equality, not just a count: an extra entity plus
	// a missing one must not cancel out.
	snap := graph.Snapshot()
	if got := snapshotRefs(snap); !reflect.DeepEqual(got, wantRefs) {
		t.Fatalf("converged ref set mismatch:\n got %v\nwant %v", got, wantRefs)
	}

	// --- spot-check representative entities from each layer -----------

	nodeRef := inventory.Ref{Kind: inventory.KindNode, Node: "pve1", ID: "pve1"}
	nodeEnt, ok := snap.Get(nodeRef)
	if !ok {
		t.Fatalf("missing node entity %s", nodeRef)
	}
	node, ok := nodeEnt.(*inventory.Node)
	if !ok {
		t.Fatalf("node entity has wrong type %T", nodeEnt)
	}
	if node.Status != "online" {
		t.Errorf("pve1 node status = %q, want online", node.Status)
	}
	if node.IP != "10.10.0.11" {
		t.Errorf("pve1 node IP = %q, want 10.10.0.11", node.IP)
	}

	bondRef := inventory.Ref{Kind: inventory.KindBond, Node: "pve1", ID: "bond0"}
	bondEnt, ok := snap.Get(bondRef)
	if !ok {
		t.Fatalf("missing bond entity %s", bondRef)
	}
	bond, ok := bondEnt.(*inventory.Bond)
	if !ok {
		t.Fatalf("bond entity has wrong type %T", bondEnt)
	}
	if len(bond.Slaves) != 2 {
		t.Errorf("pve1 bond0 slaves = %v, want 2 members", bond.Slaves)
	}
	// SourceHostNetlink (local node only) should have enriched bond0's
	// live MII status.
	if bond.MIIStatus == "" {
		t.Errorf("pve1 bond0 MIIStatus not populated by the host/netlink poller")
	}

	bridgeRef := inventory.Ref{Kind: inventory.KindBridge, Node: "pve2", ID: "vmbr0"}
	bridgeEnt, ok := snap.Get(bridgeRef)
	if !ok {
		t.Fatalf("missing bridge entity %s", bridgeRef)
	}
	bridge, ok := bridgeEnt.(*inventory.Bridge)
	if !ok {
		t.Fatalf("bridge entity has wrong type %T", bridgeEnt)
	}
	if !bridge.VlanAware {
		t.Errorf("pve2 vmbr0 VlanAware = false, want true")
	}

	vlanRef := inventory.Ref{Kind: inventory.KindVlan, Node: "pve3", ID: "vmbr0.20"}
	if _, vlanOK := snap.Get(vlanRef); !vlanOK {
		t.Fatalf("missing vlan entity %s", vlanRef)
	}

	zoneRef := inventory.Ref{Kind: inventory.KindSDNZone, ID: "vlanz"}
	if _, zoneOK := snap.Get(zoneRef); !zoneOK {
		t.Fatalf("missing SDN zone entity %s", zoneRef)
	}

	vnetRef := inventory.Ref{Kind: inventory.KindSDNVnet, ID: "vlanz/vnet100"}
	vnetEnt, ok := snap.Get(vnetRef)
	if !ok {
		t.Fatalf("missing SDN vnet entity %s", vnetRef)
	}
	vnet, ok := vnetEnt.(*inventory.SdnVnet)
	if !ok {
		t.Fatalf("vnet entity has wrong type %T", vnetEnt)
	}
	if vnet.Tag != 100 {
		t.Errorf("vlanz/vnet100 Tag = %d, want 100", vnet.Tag)
	}

	guestRef := inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "200"}
	guestEnt, ok := snap.Get(guestRef)
	if !ok {
		t.Fatalf("missing guest entity %s", guestRef)
	}
	guest, ok := guestEnt.(*inventory.Guest)
	if !ok {
		t.Fatalf("guest entity has wrong type %T", guestEnt)
	}
	if guest.Name != "app01" {
		t.Errorf("guest 200 name = %q, want app01", guest.Name)
	}

	nicRef := inventory.Ref{Kind: inventory.KindGuestNic, Node: "pve1", ID: "200/net0"}
	nicEnt, ok := snap.Get(nicRef)
	if !ok {
		t.Fatalf("missing guest nic entity %s", nicRef)
	}
	nic, ok := nicEnt.(*inventory.GuestNic)
	if !ok {
		t.Fatalf("guest nic entity has wrong type %T", nicEnt)
	}
	if nic.Vid != 100 || nic.TargetName != "vmbr0" {
		t.Errorf("guest 200 net0 = %+v, want vid 100 on vmbr0", nic)
	}

	clusterFWRef := inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"}
	fwEnt, ok := snap.Get(clusterFWRef)
	if !ok {
		t.Fatalf("missing cluster firewall ruleset %s", clusterFWRef)
	}
	fw, ok := fwEnt.(*inventory.FwRuleset)
	if !ok {
		t.Fatalf("firewall entity has wrong type %T", fwEnt)
	}
	if len(fw.Rules) != 1 {
		t.Errorf("cluster firewall rules = %d, want 1", len(fw.Rules))
	}

	lldpRef := inventory.Ref{Kind: inventory.KindLldpNeighbor, Node: "pve1", ID: "eno1/ac:1f:6b:01:00:01/Te1/0/1"}
	lldpEnt, ok := snap.Get(lldpRef)
	if !ok {
		t.Fatalf("missing lldp neighbor entity %s", lldpRef)
	}
	lldp, ok := lldpEnt.(*inventory.LldpNeighbor)
	if !ok {
		t.Fatalf("lldp entity has wrong type %T", lldpEnt)
	}
	if lldp.ChassisName != "sw-core-01" {
		t.Errorf("pve1 eno1 lldp chassis name = %q, want sw-core-01", lldp.ChassisName)
	}

	// pve2/pve3 must NOT have LLDP neighbors: the host/LLDP pollers only
	// ever cover this daemon's own (local) node.
	for _, e := range snap.All() {
		if e.GetRef().Kind == inventory.KindLldpNeighbor && e.GetRef().Node != "pve1" {
			t.Errorf("unexpected lldp neighbor on non-local node: %s", e.GetRef())
		}
	}
}
