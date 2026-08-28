// SPDX-License-Identifier: Apache-2.0

package inventory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// TestIngestNetlink checks the host.LinkState adapter produces runtime-tagged
// entities of the right kinds.
func TestIngestNetlink(t *testing.T) {
	links := []host.LinkState{
		{Kind: "physical", Name: "eno1", Mac: "aa:bb:cc:dd:ee:01", Driver: "ixgbe", SpeedMbps: 10000, Duplex: "full", MTU: 1500, LinkUp: true},
		{Kind: "bond", Name: "bond0", Members: []string{"eno1", "eno2"}, MTU: 1500, Bond: &host.BondDetail{Mode: "802.3ad", ActiveSlave: "eno1", MIIStatus: "up", Slaves: []host.BondSlave{{Name: "eno1", Active: true, MIIStatus: "up"}}}},
		{Kind: "bridge", Name: "vmbr0", Members: []string{"bond0"}, MTU: 1500, Bridge: &host.BridgeDetail{
			VlanAware: true, STP: true, VLANs: []host.VidRange{{Low: 2, High: 4094}},
			STPState: &host.BridgeSTP{
				RootID: "8000.aabbccddeeff", BridgeID: "8000.aabbccddeeff", StpState: 1, IsRoot: true,
				Ports: []host.BridgePortSTP{{Port: "bond0", State: host.PortStateForwarding, Role: host.RoleDesignated, PortNo: 1}},
			},
		}},
		{Kind: "vlan", Name: "vmbr0.100", VlanParent: "vmbr0", VlanID: 100, MTU: 1500},
		{Kind: "veth", Name: "veth99"}, // skipped
	}
	ents := FromNetlinkLinks("pve1", links)
	byKind := map[Kind]int{}
	for _, e := range ents {
		byKind[e.GetRef().Kind]++
	}
	if byKind[KindPhysNic] != 1 || byKind[KindBond] != 1 || byKind[KindBridge] != 1 || byKind[KindVlan] != 1 {
		t.Fatalf("unexpected kind counts: %v", byKind)
	}
	if len(ents) != 4 {
		t.Fatalf("veth should be skipped, got %d entities", len(ents))
	}

	g := NewGraph()
	g.ApplyPoll(SourceHostNetlink, Scope{Node: "pve1"}, ents)
	bond := mustGet[*Bond](t, g.Snapshot(), Ref{Kind: KindBond, Node: "pve1", ID: "bond0"})
	if bond.Mode != "802.3ad" || bond.ActiveSlave != "eno1" || len(bond.SlaveDetail) != 1 {
		t.Errorf("bond runtime detail not mapped: %+v", bond)
	}
	br := mustGet[*Bridge](t, g.Snapshot(), Ref{Kind: KindBridge, Node: "pve1", ID: "vmbr0"})
	if !br.VlanAware || len(br.Vids) != 1 || br.Vids[0] != (VidRange{Low: 2, High: 4094}) {
		t.Errorf("bridge runtime detail not mapped: %+v", br)
	}
	// T-3901: STP now really is read from a live sysfs source and flows
	// through to the resolved Bridge (see BridgeDetail.STPState's doc
	// comment for the pre-T-3901 gap this closes).
	if !br.STP || !br.STPSet {
		t.Errorf("bridge STP/STPSet not mapped: STP=%v STPSet=%v", br.STP, br.STPSet)
	}
	if br.STPState == nil || !br.STPState.IsRoot || br.STPState.RootID != "8000.aabbccddeeff" {
		t.Fatalf("bridge STPState not mapped: %+v", br.STPState)
	}
	if len(br.STPState.Ports) != 1 || br.STPState.Ports[0].Role != "designated" {
		t.Errorf("bridge STPState.Ports not mapped: %+v", br.STPState.Ports)
	}
}

// TestIngestPVENetworkMerge checks the pve.NetworkInterface adapter yields
// declared-tagged entities that merge cleanly with netlink runtime data on
// the same Refs.
func TestIngestPVENetworkMerge(t *testing.T) {
	g := NewGraph()
	node := "pve1"
	g.ApplyPoll(SourceHostNetlink, Scope{Node: node}, FromNetlinkLinks(node, []host.LinkState{
		{Kind: "bridge", Name: "vmbr0", MTU: 1500, Bridge: &host.BridgeDetail{VlanAware: true}},
	}))
	g.ApplyPoll(SourcePVENetwork, Scope{Node: node}, FromPVENetwork(node, []pve.NetworkInterface{
		{Iface: "vmbr0", Type: "bridge", MTU: 9000, BridgeVlanAware: true, Comments: "uplink bridge", Address: "10.0.0.2/24", Gateway: "10.0.0.1"},
	}))
	br := mustGet[*Bridge](t, g.Snapshot(), Ref{Kind: KindBridge, Node: node, ID: "vmbr0"})
	if br.MTU != 1500 {
		t.Errorf("runtime MTU = %d, want 1500", br.MTU)
	}
	if br.MTUDeclared != 9000 {
		t.Errorf("declared MTU = %d, want 9000", br.MTUDeclared)
	}
	if br.Comments != "uplink bridge" || br.Gateway != "10.0.0.1" {
		t.Errorf("declared metadata not merged: %+v", br)
	}
}

// TestIngestGuestConfig checks the guest NIC config parser.
func TestIngestGuestConfig(t *testing.T) {
	resources := []pve.ClusterResource{
		{Type: "qemu", Node: "pve1", VMID: 100, Name: "web", Status: "running"},
	}
	configs := map[int]map[string]string{
		100: {
			"net0":   "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0,tag=20,rate=100,firewall=1",
			"net1":   "e1000=11:22:33:44:55:66,bridge=vnet5,link_down=1",
			"memory": "2048", // ignored
		},
	}
	ents := FromPVEGuests(resources, configs)
	var nics []*GuestNic
	for _, e := range ents {
		if n, ok := e.(*GuestNic); ok {
			nics = append(nics, n)
		}
	}
	if len(nics) != 2 {
		t.Fatalf("want 2 nics, got %d", len(nics))
	}
	byKey := map[string]*GuestNic{}
	for _, n := range nics {
		byKey[n.Key] = n
	}
	net0 := byKey["net0"]
	if net0.Model != "virtio" || net0.Mac != "AA:BB:CC:DD:EE:FF" || net0.TargetName != "vmbr0" || net0.Vid != 20 || net0.RateMbps != 100 || !net0.Firewall {
		t.Errorf("net0 parsed wrong: %+v", net0)
	}
	net1 := byKey["net1"]
	if net1.Model != "e1000" || net1.TargetName != "vnet5" || !net1.LinkDown {
		t.Errorf("net1 parsed wrong: %+v", net1)
	}
}

// TestIngestLLDP checks the LLDP JSON adapter.
func TestIngestLLDP(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	raw := []byte(`[{"local-iface":"eno1","chassis_name":"sw-access-01","chassis_id":"00:11:22:33:44:55","port_id":"Gi0/1","vlan":10,"ttl":120}]`)
	ents, err := FromLLDP("pve1", raw, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		t.Fatalf("want 1 neighbor, got %d", len(ents))
	}
	n := ents[0].(*LldpNeighbor)
	if n.ChassisName != "sw-access-01" || n.LocalIface != "eno1" || n.VLAN != 10 {
		t.Errorf("lldp parsed wrong: %+v", n)
	}
	if n.LastSeen != now.Unix() {
		t.Errorf("LastSeen = %d, want %d", n.LastSeen, now.Unix())
	}

	// End to end: the neighbor links to its local NIC.
	g := NewGraph()
	nicRef := Ref{Kind: KindPhysNic, Node: "pve1", ID: "eno1"}
	g.ApplyPoll(SourceHostNetlink, Scope{Node: "pve1"}, []Entity{&PhysNic{Ref: nicRef, Name: "eno1"}})
	g.ApplyPoll(SourceHostLLDP, Scope{Node: "pve1"}, ents)
	snap := g.Snapshot()
	got := mustGet[*LldpNeighbor](t, snap, n.GetRef())
	if got.LocalNic != nicRef {
		t.Errorf("LocalNic = %v, want %v", got.LocalNic, nicRef)
	}
	if _, ok := findEdge(snap.Edges(), nicRef, n.GetRef(), EdgeLldpAdjacent); !ok {
		t.Errorf("missing lldp-adjacent edge")
	}
}

// --- FromInterfaces (F-12) -------------------------------------------------

// parseCorpus parses one of the shared interfaces(5) corpus files.
func parseCorpus(t *testing.T, name string) (*host.File, string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("../../testdata/interfaces", name))
	if err != nil {
		t.Fatalf("reading corpus %s: %v", name, err)
	}
	f, err := host.ParseInterfaces(raw)
	if err != nil {
		t.Fatalf("parsing corpus %s: %v", name, err)
	}
	return f, string(raw)
}

func entityByRef(t *testing.T, ents []Entity, ref Ref) Entity {
	t.Helper()
	for _, e := range ents {
		if e.GetRef() == ref {
			return e
		}
	}
	t.Fatalf("no entity %s in %d adapter outputs", ref, len(ents))
	return nil
}

// TestFromInterfaces_VlanAwareBridge maps the vlan-aware-bridge corpus file:
// physical NIC MTU, a fully-optioned bridge (ports, vlan-aware, vids, stp,
// address/gateway), and a VLAN sub-interface named parent.VID.
func TestFromInterfaces_VlanAwareBridge(t *testing.T) {
	f, src := parseCorpus(t, "02-vlan-aware-bridge.interfaces")
	ents := FromInterfaces("pve1", f)

	// lo is loopback and contributes nothing; eno1, vmbr0, vmbr0.20 remain.
	if len(ents) != 3 {
		t.Fatalf("entity count = %d, want 3 (%v)", len(ents), ents)
	}

	nic := entityByRef(t, ents, Ref{Kind: KindPhysNic, Node: "pve1", ID: "eno1"}).(*PhysNic)
	if nic.MTUDeclared != 9000 {
		t.Errorf("eno1 MTUDeclared = %d, want 9000", nic.MTUDeclared)
	}
	if nic.LinkUpSet {
		t.Errorf("interfaces file must not report runtime linkUp")
	}

	br := entityByRef(t, ents, Ref{Kind: KindBridge, Node: "pve1", ID: "vmbr0"}).(*Bridge)
	if got := br.DeclaredPortNames; len(got) != 1 || got[0] != "eno1" {
		t.Errorf("vmbr0 DeclaredPortNames = %v, want [eno1]", got)
	}
	if !br.VlanAware || !br.VlanAwareSet {
		t.Errorf("vmbr0 vlanAware = (%v, set=%v), want (true, set)", br.VlanAware, br.VlanAwareSet)
	}
	if br.STP || !br.STPSet {
		t.Errorf("vmbr0 stp = (%v, set=%v), want (false, set): bridge-stp off is a genuine report", br.STP, br.STPSet)
	}
	if len(br.Vids) != 1 || br.Vids[0] != (VidRange{Low: 2, High: 4094}) {
		t.Errorf("vmbr0 Vids = %v, want [2-4094]", br.Vids)
	}
	if len(br.Addresses) != 1 || br.Addresses[0] != "10.0.0.5/24" {
		t.Errorf("vmbr0 Addresses = %v, want [10.0.0.5/24]", br.Addresses)
	}
	if br.Gateway != "10.0.0.1" || br.MTUDeclared != 9000 {
		t.Errorf("vmbr0 gateway/mtu = %q/%d, want 10.0.0.1/9000", br.Gateway, br.MTUDeclared)
	}
	if br.MTU != 0 || len(br.PortNames) != 0 {
		t.Errorf("interfaces file must not set runtime fields: %+v", br)
	}

	vl := entityByRef(t, ents, Ref{Kind: KindVlan, Node: "pve1", ID: "vmbr0.20"}).(*VlanIface)
	if vl.ParentName != "vmbr0" || vl.Vid != 20 {
		t.Errorf("vmbr0.20 parent/vid = %q/%d, want vmbr0/20", vl.ParentName, vl.Vid)
	}
	if len(vl.Addresses) != 1 || vl.Addresses[0] != "10.0.20.5/24" {
		t.Errorf("vmbr0.20 Addresses = %v, want [10.0.20.5/24]", vl.Addresses)
	}

	// Raw source: the bridge's stanza text is lossless — the exact bytes of
	// the file's "iface vmbr0 ..." stanza, verbatim.
	raw := br.rawSource()
	if !strings.HasPrefix(raw, "iface vmbr0 inet static\n") {
		t.Errorf("vmbr0 raw source does not start with its stanza header: %q", raw)
	}
	if !strings.Contains(src, raw) {
		t.Errorf("vmbr0 raw source is not a verbatim slice of the file:\n%s", raw)
	}
	if !strings.Contains(raw, "\tbridge-vlan-aware yes\n") {
		t.Errorf("vmbr0 raw source lost an option line:\n%s", raw)
	}
}

// TestFromInterfaces_BondWithVlans maps the bond corpus file: bond option
// vocabulary (slaves, mode, lacp rate normalization, xmit hash policy) and
// VLAN-on-bond naming.
func TestFromInterfaces_BondWithVlans(t *testing.T) {
	f, _ := parseCorpus(t, "03-bond-with-vlans.interfaces")
	ents := FromInterfaces("pve1", f)

	bond := entityByRef(t, ents, Ref{Kind: KindBond, Node: "pve1", ID: "bond0"}).(*Bond)
	if got := sortedJoin(bond.DeclaredSlaves); got != "eno1,eno2" {
		t.Errorf("bond0 DeclaredSlaves = %v, want eno1+eno2", bond.DeclaredSlaves)
	}
	if bond.Mode != "802.3ad" {
		t.Errorf("bond0 Mode = %q, want 802.3ad", bond.Mode)
	}
	if bond.LACPRate != "fast" {
		t.Errorf("bond0 LACPRate = %q, want fast (normalized from bond-lacp-rate 1)", bond.LACPRate)
	}
	if bond.XmitHashPolicy != "layer3+4" {
		t.Errorf("bond0 XmitHashPolicy = %q, want layer3+4", bond.XmitHashPolicy)
	}
	if len(bond.Slaves) != 0 {
		t.Errorf("interfaces file must not set runtime Slaves: %v", bond.Slaves)
	}

	v10 := entityByRef(t, ents, Ref{Kind: KindVlan, Node: "pve1", ID: "bond0.10"}).(*VlanIface)
	if v10.ParentName != "bond0" || v10.Vid != 10 {
		t.Errorf("bond0.10 parent/vid = %q/%d, want bond0/10", v10.ParentName, v10.Vid)
	}

	br := entityByRef(t, ents, Ref{Kind: KindBridge, Node: "pve1", ID: "vmbr1"}).(*Bridge)
	if got := sortedJoin(br.DeclaredPortNames); got != "bond0.10" {
		t.Errorf("vmbr1 DeclaredPortNames = %v, want [bond0.10]", br.DeclaredPortNames)
	}
	if br.VlanAwareSet {
		t.Errorf("vmbr1 has no bridge-vlan-aware option; must be not-reported, got set")
	}
}

// TestFromInterfaces_OVS maps the two OVS corpus files: OVSBridge with
// ovs_ports, OVSBond with ovs_bonds + ovs_options bond_mode, OVSIntPort as
// a VLAN with an ovs_options tag, and OVSPort stanzas contributing nothing.
func TestFromInterfaces_OVS(t *testing.T) {
	f, _ := parseCorpus(t, "04-ovs-bridge.interfaces")
	ents := FromInterfaces("pve1", f)
	if len(ents) != 2 {
		t.Fatalf("ovs-bridge corpus entity count = %d, want 2 (OVSPort eno1 skipped)", len(ents))
	}
	br := entityByRef(t, ents, Ref{Kind: KindOVSBridge, Node: "pve1", ID: "vmbr0"}).(*Bridge)
	if br.Virt != BridgeOVS {
		t.Errorf("vmbr0 Virt = %q, want ovs", br.Virt)
	}
	if got := sortedJoin(br.DeclaredPortNames); got != "eno1,vlan20" {
		t.Errorf("vmbr0 DeclaredPortNames = %v, want eno1+vlan20", br.DeclaredPortNames)
	}
	intPort := entityByRef(t, ents, Ref{Kind: KindVlan, Node: "pve1", ID: "vlan20"}).(*VlanIface)
	if intPort.ParentName != "vmbr0" || intPort.Vid != 20 {
		t.Errorf("vlan20 parent/vid = %q/%d, want vmbr0/20", intPort.ParentName, intPort.Vid)
	}
	if len(intPort.Addresses) != 1 || intPort.Addresses[0] != "192.168.20.5/24" {
		t.Errorf("vlan20 Addresses = %v, want [192.168.20.5/24]", intPort.Addresses)
	}

	f, _ = parseCorpus(t, "05-ovs-bond.interfaces")
	ents = FromInterfaces("pve1", f)
	bond := entityByRef(t, ents, Ref{Kind: KindOVSBond, Node: "pve1", ID: "bond0"}).(*Bond)
	if got := sortedJoin(bond.DeclaredSlaves); got != "eno1,eno2" {
		t.Errorf("ovs bond0 DeclaredSlaves = %v, want eno1+eno2", bond.DeclaredSlaves)
	}
	if bond.Mode != "balance-slb" {
		t.Errorf("ovs bond0 Mode = %q, want balance-slb (from ovs_options)", bond.Mode)
	}
}

// TestFromInterfaces_MultiStanzaAndNetmask covers interfaces(5) shapes the
// corpus renders differently from PVE: several stanzas per interface
// (addresses accumulate) and legacy address+netmask lines (canonicalized to
// the CIDR form pve-network reports, so equal declared config does not
// register a conflict).
func TestFromInterfaces_MultiStanzaAndNetmask(t *testing.T) {
	f, _ := parseCorpus(t, "09-dual-stack-multi-stanza.interfaces")
	ents := FromInterfaces("pve1", f)
	br := entityByRef(t, ents, Ref{Kind: KindBridge, Node: "pve1", ID: "vmbr0"}).(*Bridge)
	if got := sortedJoin(br.Addresses); got != "192.168.1.10/24,fd00:1::10/64" {
		t.Errorf("dual-stack vmbr0 Addresses = %v, want both families", br.Addresses)
	}
	if br.Gateway != "192.168.1.1" {
		t.Errorf("vmbr0 Gateway = %q, want the inet stanza's 192.168.1.1", br.Gateway)
	}
	raw := br.rawSource()
	if !strings.Contains(raw, "iface vmbr0 inet static\n") || !strings.Contains(raw, "iface vmbr0 inet6 static\n") {
		t.Errorf("multi-stanza raw source must contain both stanzas:\n%s", raw)
	}

	// Legacy netmask spelling (what pvemock's fixture renderer emits).
	legacy := "auto vmbr0\niface vmbr0 inet static\n\taddress 10.10.0.11\n\tnetmask 255.255.255.0\n\tbridge-ports bond0\n\t#mgmt bridge\n"
	lf, err := host.ParseInterfaces([]byte(legacy))
	if err != nil {
		t.Fatalf("parsing legacy stanza: %v", err)
	}
	lents := FromInterfaces("pve1", lf)
	lbr := entityByRef(t, lents, Ref{Kind: KindBridge, Node: "pve1", ID: "vmbr0"}).(*Bridge)
	if len(lbr.Addresses) != 1 || lbr.Addresses[0] != "10.10.0.11/24" {
		t.Errorf("legacy Addresses = %v, want [10.10.0.11/24]", lbr.Addresses)
	}
	if lbr.Comments != "mgmt bridge" {
		t.Errorf("Comments = %q, want %q (matching pve-network's form)", lbr.Comments, "mgmt bridge")
	}
}

// TestFromInterfaces_WinsDeclaredFields is the merge-level check: with all
// three sources contributing, SourceHostInterfaces owns every declared
// field it reports, host-netlink keeps the runtime fields, and a genuine
// declared disagreement is conflict-tagged against the interfaces value.
func TestFromInterfaces_WinsDeclaredFields(t *testing.T) {
	f, _ := parseCorpus(t, "02-vlan-aware-bridge.interfaces")
	g := NewGraph()
	node := "pve1"
	g.ApplyPoll(SourceHostNetlink, Scope{Node: node}, FromNetlinkLinks(node, []host.LinkState{
		{Kind: "bridge", Name: "vmbr0", Members: []string{"eno1"}, MTU: 9000,
			Bridge: &host.BridgeDetail{VlanAware: true, VLANs: []host.VidRange{{Low: 2, High: 4094}}}},
	}))
	g.ApplyPoll(SourceHostInterfaces, Scope{Node: node}, FromInterfaces(node, f))
	// PVE disagrees on the declared MTU (drifted cluster view).
	g.ApplyPoll(SourcePVENetwork, Scope{Node: node}, FromPVENetwork(node, []pve.NetworkInterface{
		{Iface: "vmbr0", Type: "bridge", MTU: 1500, BridgePorts: "eno1", BridgeVlanAware: true, Address: "10.0.0.5/24", Gateway: "10.0.0.1"},
	}))

	ref := Ref{Kind: KindBridge, Node: node, ID: "vmbr0"}
	snap := g.Snapshot()
	br := mustGet[*Bridge](t, snap, ref)
	if br.MTUDeclared != 9000 {
		t.Errorf("MTUDeclared = %d, want 9000 (interfaces file outranks pve-network)", br.MTUDeclared)
	}
	if br.MTU != 9000 {
		t.Errorf("runtime MTU = %d, want 9000 (netlink)", br.MTU)
	}
	prov, ok := snap.Provenance(ref)
	if !ok {
		t.Fatal("no provenance for vmbr0")
	}
	for _, field := range []string{"mtuDeclared", "declaredPortNames", "addresses", "gateway"} {
		if owner := prov.Fields[field].Owner; owner != SourceHostInterfaces {
			t.Errorf("%s owner = %s, want host-interfaces", field, owner)
		}
	}
	fp := prov.Fields["mtuDeclared"]
	if len(fp.Conflicts) != 1 || fp.Conflicts[0].Source != SourcePVENetwork || fp.Conflicts[0].Value != "1500" {
		t.Errorf("mtuDeclared conflicts = %+v, want pve-network=1500", fp.Conflicts)
	}
	// vlanAware and stp are runtime-first: netlink reported both (its
	// bridge detail is present), so it owns them; everyone agrees on
	// vlanAware, so no conflict.
	if owner := prov.Fields["stp"].Owner; owner != SourceHostNetlink {
		t.Errorf("stp owner = %s, want host-netlink (runtime-first)", owner)
	}
	if owner := prov.Fields["vlanAware"].Owner; owner != SourceHostNetlink {
		t.Errorf("vlanAware owner = %s, want host-netlink", owner)
	}
	if len(prov.Fields["vlanAware"].Conflicts) != 0 {
		t.Errorf("vlanAware conflicts = %+v, want none", prov.Fields["vlanAware"].Conflicts)
	}
}

// --- raw source retention (F-08) -------------------------------------------

// TestRawSourceRoundTrip checks Snapshot.RawSource for a bridge seen by both
// the interfaces file and PVE: the interfaces text round-trips byte-exact,
// and the PVE JSON parses back to the object the entity came from.
func TestRawSourceRoundTrip(t *testing.T) {
	f, src := parseCorpus(t, "02-vlan-aware-bridge.interfaces")
	g := NewGraph()
	node := "pve1"
	pveIface := pve.NetworkInterface{
		Iface: "vmbr0", Type: "bridge", MTU: 9000, BridgePorts: "eno1",
		BridgeVlanAware: true, Address: "10.0.0.5/24", Gateway: "10.0.0.1",
	}
	g.ApplyPoll(SourceHostInterfaces, Scope{Node: node}, FromInterfaces(node, f))
	g.ApplyPoll(SourcePVENetwork, Scope{Node: node}, FromPVENetwork(node, []pve.NetworkInterface{pveIface}))

	ref := Ref{Kind: KindBridge, Node: node, ID: "vmbr0"}
	snap := g.Snapshot()
	raw := snap.RawSource(ref)
	if raw == nil {
		t.Fatal("RawSource returned nil for a bridge with two contributions")
	}
	if len(raw) != 2 {
		t.Fatalf("RawSource sources = %v, want host-interfaces + pve-network", raw)
	}

	stanza := raw[SourceHostInterfaces]
	if !strings.Contains(src, stanza) || !strings.HasPrefix(stanza, "iface vmbr0 inet static\n") {
		t.Errorf("interfaces raw source is not the verbatim stanza:\n%q", stanza)
	}

	var back pve.NetworkInterface
	if err := json.Unmarshal([]byte(raw[SourcePVENetwork]), &back); err != nil {
		t.Fatalf("pve raw source is not valid JSON: %v", err)
	}
	if back != pveIface {
		t.Errorf("pve raw source round-trip = %+v, want %+v", back, pveIface)
	}

	// A ref nobody attached raw text to returns nil, and mutating the
	// returned map must not affect the snapshot (it is a copy).
	if got := snap.RawSource(Ref{Kind: KindBridge, Node: node, ID: "nope"}); got != nil {
		t.Errorf("RawSource for unknown ref = %v, want nil", got)
	}
	raw[SourcePVENetwork] = "mutated"
	if snap.RawSource(ref)[SourcePVENetwork] == "mutated" {
		t.Errorf("RawSource must return a copy, not the snapshot's own map")
	}
}

// TestFromPVESDN_PendingSourcedFromPendingMaps proves FromPVESDN sources
// SdnZone/SdnVnet/SdnSubnet.Pending from its zonePending/vnetPending/
// subnetPending map parameters — NOT from the staged pve.SDNZone/SDNVnet/
// SDNSubnet's own .Pending field, which the debt-sweep 2026-08-19 follow-up
// ("inventory.FromPVESDN ... read .Pending the same wrong way [as the
// pre-fix internal/sdn.Service.Tree], to paint topology badges") stopped
// reading — that field decodes a "pending" key the DEFAULT SDN list view
// never actually carries against real PVE (internal/pve.SDNZone.Pending's
// doc comment). Every staged fixture object below leaves its own .Pending
// field at its zero value (pve.PendingNone) on purpose — the state comes
// only from the maps — so this test fails if FromPVESDN ever regresses to
// reading the stale fields again.
func TestFromPVESDN_PendingSourcedFromPendingMaps(t *testing.T) {
	zones := []pve.SDNZone{{ID: "z1", Type: "vlan"}, {ID: "z2", Type: "simple"}}
	vnets := []pve.SDNVnet{{ID: "v1", Zone: "z1"}}
	subnets := map[string][]pve.SDNSubnet{
		"v1": {{ID: "sub1", Vnet: "v1", CIDR: "10.0.0.0/24"}},
	}
	zonePending := map[string]pve.PendingState{"z1": pve.PendingChanged}
	vnetPending := map[string]pve.PendingState{"v1": pve.PendingNew}
	subnetPending := map[string]pve.PendingState{"sub1": pve.PendingDeleted}

	entities := FromPVESDN(zones, vnets, subnets, nil, zonePending, vnetPending, subnetPending)

	var gotZ1, gotZ2, gotV1, gotSub1 string
	var sawZ1, sawZ2, sawV1, sawSub1 bool
	for _, e := range entities {
		switch v := e.(type) {
		case *SdnZone:
			if v.ID == "z1" {
				gotZ1, sawZ1 = v.Pending, true
			}
			if v.ID == "z2" {
				gotZ2, sawZ2 = v.Pending, true
			}
		case *SdnVnet:
			if v.ID == "v1" {
				gotV1, sawV1 = v.Pending, true
			}
		case *SdnSubnet:
			if v.ID == "10.0.0.0/24" {
				gotSub1, sawSub1 = v.Pending, true
			}
		}
	}
	if !sawZ1 || gotZ1 != "changed" {
		t.Errorf("z1.Pending = %q (found=%v), want %q", gotZ1, sawZ1, "changed")
	}
	// z2 has no entry in zonePending at all — real PVE's "?pending=1" view
	// still lists an in-sync object with no state key (evidence file §3/§6);
	// a genuinely absent map key must render the same PendingNone.
	if !sawZ2 || gotZ2 != "" {
		t.Errorf("z2.Pending = %q (found=%v), want \"\" (in sync)", gotZ2, sawZ2)
	}
	if !sawV1 || gotV1 != "new" {
		t.Errorf("v1.Pending = %q (found=%v), want %q", gotV1, sawV1, "new")
	}
	if !sawSub1 || gotSub1 != "deleted" {
		t.Errorf("sub1.Pending = %q (found=%v), want %q", gotSub1, sawSub1, "deleted")
	}
}

// TestFromPVESDNControllers_PendingSourcedFromPendingMap is
// TestFromPVESDN_PendingSourcedFromPendingMaps's FromPVESDNControllers
// counterpart.
func TestFromPVESDNControllers_PendingSourcedFromPendingMap(t *testing.T) {
	controllers := []pve.SDNController{{ID: "ctl1", Type: "bgp"}, {ID: "ctl2", Type: "bgp"}}
	pending := map[string]pve.PendingState{"ctl1": pve.PendingNew}

	entities := FromPVESDNControllers(controllers, pending)

	var got1, got2 string
	var saw1, saw2 bool
	for _, e := range entities {
		c, ok := e.(*SdnController)
		if !ok {
			continue
		}
		if c.ID == "ctl1" {
			got1, saw1 = c.Pending, true
		}
		if c.ID == "ctl2" {
			got2, saw2 = c.Pending, true
		}
	}
	if !saw1 || got1 != "new" {
		t.Errorf("ctl1.Pending = %q (found=%v), want %q", got1, saw1, "new")
	}
	if !saw2 || got2 != "" {
		t.Errorf("ctl2.Pending = %q (found=%v), want \"\" (in sync)", got2, saw2)
	}
}

// TestFromPVESDNIpams_PendingAlwaysEmpty proves FromPVESDNIpams never
// propagates pve.IPAM.Pending: unlike zones/vnets/subnets/controllers,
// there is no "?pending=1" view for ipam instances to source a real value
// from (internal/pve/ipam.go's IPAM.Pending doc comment, confirmed against
// pvecube's own perl source that Ipams.pm accepts no `pending` parameter at
// all) — so surfacing i.Pending here would be surfacing a stale
// default-view artifact with no correct alternative reading behind it,
// exactly the trap this debt-sweep follow-up otherwise fixes for the other
// three families. A populated i.Pending on the input (as a mock's
// deliberately-unchanged default-view leak, or a hand-built fixture, might
// supply) must not leak through.
func TestFromPVESDNIpams_PendingAlwaysEmpty(t *testing.T) {
	ipams := []pve.IPAM{{ID: "pve", Type: "pve", Pending: pve.PendingChanged}}

	entities := FromPVESDNIpams(ipams)

	if len(entities) != 1 {
		t.Fatalf("entities = %+v, want 1", entities)
	}
	ip, ok := entities[0].(*SdnIpam)
	if !ok {
		t.Fatalf("entities[0] = %T, want *SdnIpam", entities[0])
	}
	if ip.Pending != "" {
		t.Errorf("SdnIpam.Pending = %q, want \"\" (no ?pending=1 view exists for ipams)", ip.Pending)
	}
}
