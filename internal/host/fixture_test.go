// SPDX-License-Identifier: Apache-2.0

package host

import (
	"context"
	"errors"
	"testing"

	"github.com/bgovanlu/vnprox/internal/pvemock"
)

func loadFixtureReader(t *testing.T, name string) *FixtureReader {
	t.Helper()
	f, err := pvemock.LoadFixture("../../testdata/clusters/" + name)
	if err != nil {
		t.Fatalf("LoadFixture(%s): %v", name, err)
	}
	srv := pvemock.NewServer(f)
	return NewFixtureReader(pvemock.NewFixtureHostReader(srv))
}

// TestFixtureReader_Links exercises the adapter against T-004's
// three-node-vlan fixture (T-102 AC3: netlink-shaped readers producing
// documented data-model fields for fixture states), checking the bond,
// VLAN-aware bridge, and VLAN sub-interface enrichment this package's own
// interfaces(5) parser recovers on top of pvemock's minimal LinkState.
func TestFixtureReader_Links(t *testing.T) {
	r := loadFixtureReader(t, "three-node-vlan.yaml")
	ctx := context.Background()

	links, err := r.Links(ctx, "pve1")
	if err != nil {
		t.Fatalf("Links: %v", err)
	}

	byName := make(map[string]LinkState, len(links))
	for _, l := range links {
		byName[l.Name] = l
	}

	bond, ok := byName["bond0"]
	if !ok {
		t.Fatalf("bond0 not found in %+v", byName)
	}
	if bond.Kind != "bond" {
		t.Errorf("bond0.Kind = %q, want bond", bond.Kind)
	}
	if bond.Bond == nil {
		t.Fatalf("bond0.Bond is nil")
	}
	if bond.Bond.Mode != "802.3ad" {
		t.Errorf("bond0.Bond.Mode = %q, want 802.3ad", bond.Bond.Mode)
	}
	if len(bond.Bond.Slaves) != 2 {
		t.Errorf("bond0.Bond.Slaves = %+v, want 2 entries", bond.Bond.Slaves)
	}
	if len(bond.Members) != 2 {
		t.Errorf("bond0.Members = %v, want 2 entries (eno1, eno2)", bond.Members)
	}

	vmbr0, ok := byName["vmbr0"]
	if !ok {
		t.Fatalf("vmbr0 not found")
	}
	if vmbr0.Kind != "bridge" {
		t.Errorf("vmbr0.Kind = %q, want bridge", vmbr0.Kind)
	}
	if vmbr0.Bridge == nil || !vmbr0.Bridge.VlanAware {
		t.Errorf("vmbr0.Bridge = %+v, want VlanAware=true", vmbr0.Bridge)
	}
	if len(vmbr0.Members) != 1 || vmbr0.Members[0] != "bond0" {
		t.Errorf("vmbr0.Members = %v, want [bond0]", vmbr0.Members)
	}

	vlan, ok := byName["vmbr0.20"]
	if !ok {
		t.Fatalf("vmbr0.20 not found")
	}
	if vlan.Kind != "vlan" {
		t.Errorf("vmbr0.20.Kind = %q, want vlan", vlan.Kind)
	}
	if vlan.VlanID != 20 {
		t.Errorf("vmbr0.20.VlanID = %d, want 20", vlan.VlanID)
	}
	if vlan.VlanParent != "vmbr0" {
		t.Errorf("vmbr0.20.VlanParent = %q, want vmbr0", vlan.VlanParent)
	}
}

// TestFixtureReader_InterfacesFile verifies the adapter passes through the
// literal rendered interfaces(5) text unchanged, and that it parses
// cleanly with this package's own parser (proving the fixture's rendered
// output and the real parser agree on what a valid file looks like).
func TestFixtureReader_InterfacesFile(t *testing.T) {
	r := loadFixtureReader(t, "single-node.yaml")
	ctx := context.Background()

	raw, err := r.InterfacesFile(ctx, "pve1", false)
	if err != nil {
		t.Fatalf("InterfacesFile: %v", err)
	}
	pf, err := ParseInterfaces([]byte(raw))
	if err != nil {
		t.Fatalf("ParseInterfaces(fixture rendered output): %v", err)
	}
	if _, ok := pf.Iface("vmbr0"); !ok {
		t.Errorf("parsed fixture interfaces file has no vmbr0 stanza:\n%s", raw)
	}
	if got := pf.Render(); got != raw {
		t.Errorf("fixture-rendered interfaces file did not round-trip through the parser")
	}
}

// TestFixtureReader_LLDPAndStats exercises the remaining two Reader
// methods end to end against a fixture.
func TestFixtureReader_LLDPAndStats(t *testing.T) {
	r := loadFixtureReader(t, "single-node.yaml")
	ctx := context.Background()

	lldp, err := r.LLDP(ctx, "pve1")
	if err != nil {
		t.Fatalf("LLDP: %v", err)
	}
	if len(lldp) == 0 {
		t.Errorf("LLDP returned no data")
	}

	stats, err := r.Stats(ctx, "pve1")
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	eno1, ok := stats["eno1"]
	if !ok {
		t.Fatalf("stats missing eno1: %+v", stats)
	}
	if eno1.RxBytes != 1048576000 {
		t.Errorf("eno1.RxBytes = %d, want 1048576000", eno1.RxBytes)
	}
}

// TestFixtureReader_Conntrack exercises the pvemock.ConntrackEntry ->
// host.ConntrackEntry conversion (T-1305), including NAT pointer fields.
func TestFixtureReader_Conntrack(t *testing.T) {
	r := loadFixtureReader(t, "three-node-vlan.yaml")
	ctx := context.Background()

	entries, err := r.Conntrack(ctx, "pve1")
	if err != nil {
		t.Fatalf("Conntrack: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("got %d entries, want 4: %+v", len(entries), entries)
	}

	var sawSNAT, sawDNAT, sawPlain bool
	for _, e := range entries {
		switch {
		case e.NatSrc != nil:
			sawSNAT = true
			if e.NatSrc.IP != "203.0.113.10" {
				t.Errorf("NatSrc = %+v, unexpected", e.NatSrc)
			}
		case e.NatDst != nil:
			sawDNAT = true
			if e.NatDst.IP != "10.10.0.11" {
				t.Errorf("NatDst = %+v, unexpected", e.NatDst)
			}
		case e.NatSrc == nil && e.NatDst == nil && e.Proto == 6:
			sawPlain = true
		}
	}
	if !sawSNAT || !sawDNAT || !sawPlain {
		t.Errorf("missing expected entry kinds: SNAT=%v DNAT=%v plain=%v", sawSNAT, sawDNAT, sawPlain)
	}
}

// TestFixtureReader_UnknownNode mirrors pvemock's own
// TestFixtureHostReader_UnknownNode, confirming errors propagate through
// the adapter rather than being swallowed.
func TestFixtureReader_UnknownNode(t *testing.T) {
	r := loadFixtureReader(t, "single-node.yaml")
	_, err := r.Links(context.Background(), "nope")
	if err == nil {
		t.Fatalf("expected error for unknown node")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Links(nope) error = %v, want errors.Is(err, host.ErrNotFound)", err)
	}
}

// TestFixtureReader_MediaPort round-trips T-3503's fixture-declared media
// port through the whole fixture chain this test file's other cases
// exercise piecemeal: pvemock.LinkInfo.MediaPort (a fixture's declared
// stand-in for the SIOCETHTOOL ioctl's Port field) -> FixtureHostReader.
// Links() -> this package's convertFixtureLink -> LinkState.MediaPort. Built
// from a minimal in-memory *pvemock.Fixture (not the shared golden YAML
// clusters other tests in this package/internal/topology assert exact node
// counts against) specifically so a down fibre link can be declared without
// touching fixtures those other tests depend on.
func TestFixtureReader_MediaPort(t *testing.T) {
	f := &pvemock.Fixture{
		Nodes: map[string]*pvemock.NodeSpec{
			"n1": {
				Network: []pvemock.NetIface{
					{Iface: "sfp0", Type: "eth", Method: "manual"},
					{Iface: "eno1", Type: "eth", Method: "manual"},
				},
				Links: map[string]pvemock.LinkInfo{
					// The evidence transcript's exact case: a fibre/DA port
					// with no carrier still reports its media type.
					"sfp0": {Mac: "bc:24:11:00:00:03", MediaPort: "fibre", LinkUp: false},
					"eno1": {Mac: "bc:24:11:00:00:01", MediaPort: "tp", LinkUp: true},
				},
			},
		},
	}
	srv := pvemock.NewServer(f)
	r := NewFixtureReader(pvemock.NewFixtureHostReader(srv))

	links, err := r.Links(context.Background(), "n1")
	if err != nil {
		t.Fatalf("Links: %v", err)
	}
	byName := make(map[string]LinkState, len(links))
	for _, l := range links {
		byName[l.Name] = l
	}

	if got, want := byName["sfp0"].MediaPort, "fibre"; got != want {
		t.Errorf("sfp0.MediaPort = %q, want %q", got, want)
	}
	if byName["sfp0"].LinkUp {
		t.Errorf("sfp0.LinkUp = true, want false (down fibre link)")
	}
	if got, want := byName["eno1"].MediaPort, "tp"; got != want {
		t.Errorf("eno1.MediaPort = %q, want %q", got, want)
	}
}

// TestFixtureReader_MulticastMDB exercises T-3902's fixture-declared bridge
// multicast state end to end: BridgeDetail's Snooping/Querier/RouterMode
// fields (recovered via Links(), the FDB-shaped "fixture-declared directly"
// exception fixtureBridgeDetail's doc comment describes) and MDB() (raw
// `bridge -d -j mdb show` bytes, parsed with the real ParseMDB). Two
// bridges: one with declared entries and multicast config, one with
// snooping/querier/router left at their zero value and no MDB rows at
// all — the "empty MDB table" case the evidence transcript documents as the
// common real-world state (planning/reports/evidence/
// pve-9.2.4-bridge-mdb-2026-08-27.txt), which real users will hit most.
func TestFixtureReader_MulticastMDB(t *testing.T) {
	f := &pvemock.Fixture{
		Nodes: map[string]*pvemock.NodeSpec{
			"n1": {
				Network: []pvemock.NetIface{
					{Iface: "vmbr0", Type: "bridge", Method: "manual"},
					{Iface: "vmbr1", Type: "bridge", Method: "manual"},
				},
				Links: map[string]pvemock.LinkInfo{
					"vmbr0": {
						Mac: "bc:24:11:00:00:01", LinkUp: true,
						MulticastSnooping: true, MulticastQuerier: false, MulticastRouter: 1,
						MDB: []pvemock.MDBEntrySpec{
							{Group: "ff02::fb", Port: "eno1", State: "temp", Protocol: "kernel"},
						},
					},
					// vmbr1: declared but with no multicast config and no MDB
					// rows at all — the observed-on-pvecube common case.
					"vmbr1": {Mac: "bc:24:11:00:00:02", LinkUp: true},
				},
			},
		},
	}
	srv := pvemock.NewServer(f)
	r := NewFixtureReader(pvemock.NewFixtureHostReader(srv))
	ctx := context.Background()

	links, err := r.Links(ctx, "n1")
	if err != nil {
		t.Fatalf("Links: %v", err)
	}
	byName := make(map[string]LinkState, len(links))
	for _, l := range links {
		byName[l.Name] = l
	}

	vmbr0 := byName["vmbr0"]
	if vmbr0.Bridge == nil {
		t.Fatalf("vmbr0.Bridge is nil")
	}
	if !vmbr0.Bridge.MulticastSnooping {
		t.Errorf("vmbr0.Bridge.MulticastSnooping = false, want true")
	}
	if vmbr0.Bridge.MulticastQuerier {
		t.Errorf("vmbr0.Bridge.MulticastQuerier = true, want false")
	}
	if vmbr0.Bridge.MulticastRouterMode != 1 {
		t.Errorf("vmbr0.Bridge.MulticastRouterMode = %d, want 1", vmbr0.Bridge.MulticastRouterMode)
	}

	vmbr1 := byName["vmbr1"]
	if vmbr1.Bridge == nil {
		t.Fatalf("vmbr1.Bridge is nil")
	}
	if vmbr1.Bridge.MulticastSnooping || vmbr1.Bridge.MulticastQuerier || vmbr1.Bridge.MulticastRouterMode != 0 {
		t.Errorf("vmbr1.Bridge multicast state = %+v, want all zero (not declared)", *vmbr1.Bridge)
	}

	raw, err := r.MDB(ctx, "n1")
	if err != nil {
		t.Fatalf("MDB: %v", err)
	}
	rows, err := ParseMDB(raw)
	if err != nil {
		t.Fatalf("ParseMDB(fixture-rendered MDB output): %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ParseMDB rows = %+v, want exactly 1 (vmbr1 declares none)", rows)
	}
	want := MDBRow{Bridge: "vmbr0", Group: "ff02::fb", Port: "eno1", State: "temp", Protocol: "kernel"}
	if rows[0] != want {
		t.Errorf("row = %+v, want %+v", rows[0], want)
	}
}

// TestFixtureReader_STP exercises T-3901's fixture-declared STP path
// end-to-end (LinkInfo.STP -> pvemock.LinkState.STP -> host.BridgeSTP),
// mirroring TestFixtureReader_MulticastMDB's pattern. Two bridges: vmbr0 is
// a standalone, STP-off root (pvecube's actual observed shape — see
// planning/reports/evidence/pve-9.2.4-bridge-stp-2026-08-27.txt) and vmbr1
// is a constructed non-root bridge with a blocking port, to prove role
// derivation (root/designated/blocking) flows correctly through the
// fixture path exactly like it does through Real's sysfs read (both call
// the same deriveBridgePortRole).
func TestFixtureReader_STP(t *testing.T) {
	f := &pvemock.Fixture{
		Nodes: map[string]*pvemock.NodeSpec{
			"n1": {
				Network: []pvemock.NetIface{
					{Iface: "vmbr0", Type: "bridge", Method: "manual"},
					{Iface: "vmbr1", Type: "bridge", Method: "manual"},
				},
				Links: map[string]pvemock.LinkInfo{
					"vmbr0": {
						Mac: "a8:b8:e0:00:0e:e8", LinkUp: true,
						STP: &pvemock.BridgeSTPSpec{
							RootID: "8000.a8b8e0000ee8", BridgeID: "8000.a8b8e0000ee8",
							StpState: 0, RootPort: 0,
							Ports: []pvemock.BridgePortSTPSpec{
								{Port: "enp1s0", State: "forwarding", PortNo: 1},
							},
						},
					},
					"vmbr1": {
						Mac: "bc:24:11:00:00:02", LinkUp: true,
						STP: &pvemock.BridgeSTPSpec{
							RootID: "8000.aaaaaaaaaaaa", BridgeID: "8000.bbbbbbbbbbbb",
							StpState: 1, RootPort: 1,
							Ports: []pvemock.BridgePortSTPSpec{
								{Port: "eth0", State: "forwarding", PortNo: 1},
								{Port: "eth1", State: "blocking", PortNo: 2},
							},
						},
					},
				},
			},
		},
	}
	srv := pvemock.NewServer(f)
	r := NewFixtureReader(pvemock.NewFixtureHostReader(srv))
	ctx := context.Background()

	links, err := r.Links(ctx, "n1")
	if err != nil {
		t.Fatalf("Links: %v", err)
	}
	byName := make(map[string]LinkState, len(links))
	for _, l := range links {
		byName[l.Name] = l
	}

	vmbr0 := byName["vmbr0"]
	if vmbr0.Bridge == nil || vmbr0.Bridge.STPState == nil {
		t.Fatalf("vmbr0.Bridge.STPState is nil")
	}
	if vmbr0.Bridge.STP {
		t.Errorf("vmbr0.Bridge.STP = true, want false (StpState 0)")
	}
	if !vmbr0.Bridge.STPState.IsRoot {
		t.Errorf("vmbr0.Bridge.STPState.IsRoot = false, want true")
	}
	if len(vmbr0.Bridge.STPState.Ports) != 1 || vmbr0.Bridge.STPState.Ports[0].Role != RoleDesignated {
		t.Errorf("vmbr0 port roles = %+v, want [designated]", vmbr0.Bridge.STPState.Ports)
	}

	vmbr1 := byName["vmbr1"]
	if vmbr1.Bridge == nil || vmbr1.Bridge.STPState == nil {
		t.Fatalf("vmbr1.Bridge.STPState is nil")
	}
	if !vmbr1.Bridge.STP {
		t.Errorf("vmbr1.Bridge.STP = false, want true (StpState 1)")
	}
	if vmbr1.Bridge.STPState.IsRoot {
		t.Errorf("vmbr1.Bridge.STPState.IsRoot = true, want false")
	}
	roles := map[string]BridgePortRole{}
	for _, p := range vmbr1.Bridge.STPState.Ports {
		roles[p.Port] = p.Role
	}
	if roles["eth0"] != RoleRoot {
		t.Errorf("eth0 role = %q, want root", roles["eth0"])
	}
	if roles["eth1"] != RoleBlocking {
		t.Errorf("eth1 role = %q, want blocking", roles["eth1"])
	}
}
