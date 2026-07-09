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
