package collect_test

// T-407 acceptance criterion 1: against internal/pvemock's ovs-lab fixture
// (testdata/clusters/ovs-lab.yaml), the inventory graph converges to the
// fixture's full expected entity set — a plain Linux management bridge
// alongside a full OVS stack (OVSBridge + OVSBond + a tagged OVSIntPort) —
// with every OVS-specific field (Kind, Virt, Ports, Trunks) correct. This
// is the "inventory... correct" half of AC1; map/inspector correctness
// (the same resolved entities, rendered) is exercised by web/src/topology's
// own fixture-driven tests (T-107), which read the same Snapshot shape.

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

const fixtureOVSLab = "../../testdata/clusters/ovs-lab.yaml"

// ovsLabRefs is the complete expected ref set for the ovs-lab fixture,
// enumerated directly from testdata/clusters/ovs-lab.yaml.
func ovsLabRefs() []string {
	refs := []string{
		"node:pve1:pve1",
		"physnic:pve1:eno1",
		"physnic:pve1:eno2",
		"physnic:pve1:eno3",
		"physnic:pve1:eno4",
		"bridge:pve1:vmbr0",
		"ovs-bond:pve1:bond0",
		"ovs-bridge:pve1:vmbr1",
		"vlan:pve1:vlan30",
		"fw-ruleset:pve1:node",
		"fw-ruleset::cluster",
	}
	sort.Strings(refs)
	return refs
}

func TestGolden_OVSLab(t *testing.T) {
	srv := loadFixtureServer(t, fixtureOVSLab)
	c, graph, _ := newTestCollector(t, srv)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.RunPVELoop(ctx) }()
	go func() { _ = c.RunHostLoop(ctx) }()
	go func() { _ = c.RunLLDPLoop(ctx) }()

	wantRefs := ovsLabRefs()
	waitFor(t, 3*time.Second, "graph to converge to the full ovs-lab fixture", func() bool {
		return graph.Snapshot().Len() == len(wantRefs)
	})

	snap := graph.Snapshot()
	if got := snapshotRefs(snap); !reflect.DeepEqual(got, wantRefs) {
		t.Fatalf("converged ref set mismatch:\n got %v\nwant %v", got, wantRefs)
	}

	// --- Linux management bridge: unaffected by the OVS stack alongside it

	linuxBridgeRef := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}
	linuxBridgeEnt, ok := snap.Get(linuxBridgeRef)
	if !ok {
		t.Fatalf("missing linux bridge entity %s", linuxBridgeRef)
	}
	linuxBridge, ok := linuxBridgeEnt.(*inventory.Bridge)
	if !ok {
		t.Fatalf("linux bridge entity has wrong type %T", linuxBridgeEnt)
	}
	if linuxBridge.Virt != inventory.BridgeLinux {
		t.Errorf("vmbr0 Virt = %q, want %q", linuxBridge.Virt, inventory.BridgeLinux)
	}

	// --- OVS bridge: kind, virt, and ports (bond + int port) -----------

	ovsBridgeRef := inventory.Ref{Kind: inventory.KindOVSBridge, Node: "pve1", ID: "vmbr1"}
	ovsBridgeEnt, ok := snap.Get(ovsBridgeRef)
	if !ok {
		t.Fatalf("missing ovs bridge entity %s", ovsBridgeRef)
	}
	ovsBridge, ok := ovsBridgeEnt.(*inventory.Bridge)
	if !ok {
		t.Fatalf("ovs bridge entity has wrong type %T", ovsBridgeEnt)
	}
	if ovsBridge.Virt != inventory.BridgeOVS {
		t.Errorf("vmbr1 Virt = %q, want %q", ovsBridge.Virt, inventory.BridgeOVS)
	}
	wantPortNames := []string{"bond0", "vlan30"}
	gotPortNames := append([]string(nil), ovsBridge.DeclaredPortNames...)
	sort.Strings(gotPortNames)
	if !reflect.DeepEqual(gotPortNames, wantPortNames) {
		t.Errorf("vmbr1 DeclaredPortNames = %v, want %v", gotPortNames, wantPortNames)
	}

	// --- OVS bond: kind, mode, declared slaves --------------------------

	ovsBondRef := inventory.Ref{Kind: inventory.KindOVSBond, Node: "pve1", ID: "bond0"}
	ovsBondEnt, ok := snap.Get(ovsBondRef)
	if !ok {
		t.Fatalf("missing ovs bond entity %s", ovsBondRef)
	}
	ovsBond, ok := ovsBondEnt.(*inventory.Bond)
	if !ok {
		t.Fatalf("ovs bond entity has wrong type %T", ovsBondEnt)
	}
	if ovsBond.Mode != "active-backup" {
		t.Errorf("bond0 Mode = %q, want active-backup", ovsBond.Mode)
	}
	wantSlaves := []string{"eno3", "eno4"}
	gotSlaves := append([]string(nil), ovsBond.DeclaredSlaves...)
	sort.Strings(gotSlaves)
	if !reflect.DeepEqual(gotSlaves, wantSlaves) {
		t.Errorf("bond0 DeclaredSlaves = %v, want %v", gotSlaves, wantSlaves)
	}

	// --- OVS Int Port: Virt marker, tag, parent -------------------------

	intPortRef := inventory.Ref{Kind: inventory.KindVlan, Node: "pve1", ID: "vlan30"}
	intPortEnt, ok := snap.Get(intPortRef)
	if !ok {
		t.Fatalf("missing ovs int port entity %s", intPortRef)
	}
	intPort, ok := intPortEnt.(*inventory.VlanIface)
	if !ok {
		t.Fatalf("ovs int port entity has wrong type %T", intPortEnt)
	}
	if intPort.Virt != "ovs" {
		t.Errorf("vlan30 Virt = %q, want \"ovs\"", intPort.Virt)
	}
	if intPort.Vid != 30 {
		t.Errorf("vlan30 Vid = %d, want 30", intPort.Vid)
	}
	if intPort.ParentName != "vmbr1" {
		t.Errorf("vlan30 ParentName = %q, want vmbr1", intPort.ParentName)
	}
	if len(intPort.Addresses) != 1 || intPort.Addresses[0] != "10.20.30.5/24" {
		t.Errorf("vlan30 Addresses = %v, want [10.20.30.5/24]", intPort.Addresses)
	}
}
