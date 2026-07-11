package ifaces

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// TestGolden_* exercise every op type this package implements against T-102's
// testdata/interfaces corpus, asserting the exact byte-level output
// (task card T-204 AC1) and — for Create ops — that every entry present in
// the original file survives byte-identically as a prefix of the mutated
// file (AC3).
const goldenChangesetID = "TESTCS01"

func TestGolden_BondCreate(t *testing.T) {
	orig, _ := parseCorpus(t, "01-simple-single-bridge.interfaces")
	f, _ := parseCorpus(t, "01-simple-single-bridge.interfaces")
	op := BondCreate{
		Target: ref(inventory.KindBond, "pve1", "bond0"),
		Mode:   "802.3ad", Slaves: []string{"eno1", "eno2"},
		XmitHashPolicy: "layer3+4", MTU: 1500, Comments: "uplink bond", Autostart: true,
	}
	if err := Mutate(f, op, goldenChangesetID); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	requireOriginalEntriesPreserved(t, orig, f)
	checkGolden(t, "bond-create-01.interfaces", f.Render())
}

func TestGolden_BondCreate_OVS(t *testing.T) {
	orig, _ := parseCorpus(t, "04-ovs-bridge.interfaces")
	f, _ := parseCorpus(t, "04-ovs-bridge.interfaces")
	op := BondCreate{
		Target: ref(inventory.KindOVSBond, "pve1", "bond0"),
		Mode:   "802.3ad", Slaves: []string{"eno2", "eno3"},
		Bridge: "vmbr0", Autostart: true,
	}
	if err := Mutate(f, op, goldenChangesetID); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	requireOriginalEntriesPreserved(t, orig, f)
	checkGolden(t, "bond-create-ovs-04.interfaces", f.Render())
}

func TestGolden_BondUpdate(t *testing.T) {
	f, _ := parseCorpus(t, "03-bond-with-vlans.interfaces")
	comment := "reconfigured for active-backup"
	op := BondUpdate{
		Target: ref(inventory.KindBond, "pve1", "bond0"),
		Mode:   "active-backup", MTU: 9000, Comments: &comment,
		RemoveLacpRate: true, RemoveXmitHashPolicy: true,
	}
	if err := Mutate(f, op, goldenChangesetID); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	checkGolden(t, "bond-update-03.interfaces", f.Render())
}

func TestGolden_BondDelete(t *testing.T) {
	f, _ := parseCorpus(t, "03-bond-with-vlans.interfaces")
	op := BondDelete{Target: ref(inventory.KindBond, "pve1", "bond0")}
	if err := Mutate(f, op, goldenChangesetID); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	checkGolden(t, "bond-delete-03.interfaces", f.Render())
}

func TestGolden_BridgeCreate(t *testing.T) {
	orig, _ := parseCorpus(t, "01-simple-single-bridge.interfaces")
	f, _ := parseCorpus(t, "01-simple-single-bridge.interfaces")
	op := BridgeCreate{
		Target: ref(inventory.KindBridge, "pve1", "vmbr1"),
		Ports:  []string{"eno2"}, VlanAware: true,
		Vids: []inventory.VidRange{{Low: 2, High: 4094}}, MTU: 1500, Autostart: true,
	}
	if err := Mutate(f, op, goldenChangesetID); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	requireOriginalEntriesPreserved(t, orig, f)
	checkGolden(t, "bridge-create-01.interfaces", f.Render())
}

func TestGolden_BridgeCreate_OVS(t *testing.T) {
	orig, _ := parseCorpus(t, "01-simple-single-bridge.interfaces")
	f, _ := parseCorpus(t, "01-simple-single-bridge.interfaces")
	op := BridgeCreate{
		Target: ref(inventory.KindOVSBridge, "pve1", "vmbr2"),
		Ports:  []string{"eno3"}, Autostart: true,
	}
	if err := Mutate(f, op, goldenChangesetID); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	requireOriginalEntriesPreserved(t, orig, f)
	checkGolden(t, "bridge-create-ovs-01.interfaces", f.Render())
}

func TestGolden_BridgeUpdate(t *testing.T) {
	f, _ := parseCorpus(t, "02-vlan-aware-bridge.interfaces")
	stp := true
	op := BridgeUpdate{
		Target: ref(inventory.KindBridge, "pve1", "vmbr0"),
		Ports:  []string{"eno1", "eno2"}, STP: &stp, MTU: 1500,
	}
	if err := Mutate(f, op, goldenChangesetID); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	checkGolden(t, "bridge-update-02.interfaces", f.Render())
}

func TestGolden_BridgeDelete(t *testing.T) {
	f, _ := parseCorpus(t, "04-ovs-bridge.interfaces")
	op := BridgeDelete{Target: ref(inventory.KindOVSBridge, "pve1", "vmbr0")}
	if err := Mutate(f, op, goldenChangesetID); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	checkGolden(t, "bridge-delete-04.interfaces", f.Render())
}

func TestGolden_BridgePortAdd(t *testing.T) {
	f, _ := parseCorpus(t, "03-bond-with-vlans.interfaces")
	op := BridgePortAdd{Target: ref(inventory.KindBridge, "pve1", "vmbr1"), Port: "bond0.20"}
	if err := Mutate(f, op, goldenChangesetID); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	checkGolden(t, "bridge-port-add-03.interfaces", f.Render())
}

func TestGolden_BridgePortRemove(t *testing.T) {
	f, _ := parseCorpus(t, "04-ovs-bridge.interfaces")
	op := BridgePortRemove{Target: ref(inventory.KindOVSBridge, "pve1", "vmbr0"), Port: "vlan20"}
	if err := Mutate(f, op, goldenChangesetID); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	checkGolden(t, "bridge-port-remove-04.interfaces", f.Render())
}

func TestGolden_VlanCreate(t *testing.T) {
	orig, _ := parseCorpus(t, "02-vlan-aware-bridge.interfaces")
	f, _ := parseCorpus(t, "02-vlan-aware-bridge.interfaces")
	op := VlanCreate{
		Target: ref(inventory.KindVlan, "pve1", "vmbr0.30"),
		Parent: "vmbr0", VID: 30, Addresses: []string{"10.0.30.5/24"}, MTU: 1500, Autostart: true,
	}
	if err := Mutate(f, op, goldenChangesetID); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	requireOriginalEntriesPreserved(t, orig, f)
	checkGolden(t, "vlan-create-02.interfaces", f.Render())
}

func TestGolden_VlanCreate_OVS(t *testing.T) {
	orig, _ := parseCorpus(t, "04-ovs-bridge.interfaces")
	f, _ := parseCorpus(t, "04-ovs-bridge.interfaces")
	op := VlanCreate{
		Target: ref(inventory.KindVlan, "pve1", "vlan30"),
		Parent: "vmbr0", VID: 30, OVS: true, Autostart: true,
	}
	if err := Mutate(f, op, goldenChangesetID); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	requireOriginalEntriesPreserved(t, orig, f)
	checkGolden(t, "vlan-create-ovs-04.interfaces", f.Render())
}

func TestGolden_VlanCreate_OVS_Trunk(t *testing.T) {
	orig, _ := parseCorpus(t, "04-ovs-bridge.interfaces")
	f, _ := parseCorpus(t, "04-ovs-bridge.interfaces")
	op := VlanCreate{
		Target: ref(inventory.KindVlan, "pve1", "vlan-trunk"),
		Parent: "vmbr0", OVS: true,
		Trunks: []inventory.VidRange{{Low: 10, High: 20}, {Low: 30, High: 30}},
	}
	if err := Mutate(f, op, goldenChangesetID); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	requireOriginalEntriesPreserved(t, orig, f)
	checkGolden(t, "vlan-create-ovs-trunk-04.interfaces", f.Render())
}

func TestGolden_VlanUpdate(t *testing.T) {
	f, _ := parseCorpus(t, "02-vlan-aware-bridge.interfaces")
	op := VlanUpdate{
		Target:    ref(inventory.KindVlan, "pve1", "vmbr0.20"),
		Addresses: []string{"10.0.20.6/24"}, MTU: 1500,
	}
	if err := Mutate(f, op, goldenChangesetID); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	checkGolden(t, "vlan-update-02.interfaces", f.Render())
}

func TestGolden_VlanDelete(t *testing.T) {
	f, _ := parseCorpus(t, "02-vlan-aware-bridge.interfaces")
	op := VlanDelete{Target: ref(inventory.KindVlan, "pve1", "vmbr0.20")}
	if err := Mutate(f, op, goldenChangesetID); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	checkGolden(t, "vlan-delete-02.interfaces", f.Render())
}

func TestGolden_IfaceUpdate(t *testing.T) {
	f, _ := parseCorpus(t, "02-vlan-aware-bridge.interfaces")
	mtu := 1500
	comment := "uplink NIC"
	autostart := true
	op := IfaceUpdate{
		Target: ref(inventory.KindPhysNic, "pve1", "eno1"),
		MTU:    &mtu, Comments: &comment, Autostart: &autostart,
	}
	if err := Mutate(f, op, goldenChangesetID); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	checkGolden(t, "iface-update-02.interfaces", f.Render())
}

func TestGolden_IfaceUpdate_AutostartOff(t *testing.T) {
	f, _ := parseCorpus(t, "01-simple-single-bridge.interfaces")
	autostart := false
	op := IfaceUpdate{Target: ref(inventory.KindPhysNic, "pve1", "eno1"), Autostart: &autostart}
	if err := Mutate(f, op, goldenChangesetID); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	checkGolden(t, "iface-update-autostart-off-01.interfaces", f.Render())
}
