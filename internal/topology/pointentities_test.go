package topology_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// PointEntities exists so an in-memory comparison (T-2605's live snapshot vs.
// its post-apply projection) goes through the SAME canonical field renderer the
// captured-file comparison uses. These tests pin the two properties that make
// that true: it flattens every kind, not just the interfaces(5) ones, and its
// output diffs against EntitiesFromInterfaces' output without a translation
// step.
func TestPointEntities_FlattensEveryKindAndOrdersByRef(t *testing.T) {
	ents := []inventory.Entity{
		&inventory.SdnVnet{Ref: inventory.Ref{Kind: inventory.KindSDNVnet, ID: "vnet1"}, ID: "vnet1", Zone: "zone1", Tag: 10},
		&inventory.Bridge{
			Ref:  inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"},
			Name: "vmbr0", Virt: inventory.BridgeLinux, MTUDeclared: 1500,
		},
	}

	points, err := topology.PointEntities(ents)
	if err != nil {
		t.Fatalf("PointEntities: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("flattened %d entities, want 2", len(points))
	}
	// Deterministic order: sorted by Ref string, so "bridge:…" precedes
	// "sdn-vnet:…" regardless of input order.
	if points[0].Ref.Kind != inventory.KindBridge {
		t.Errorf("first entity = %s, want the bridge (output must be ref-ordered)", points[0].Ref)
	}
	if points[0].Fields["Name"] != "vmbr0" || points[0].Name != "vmbr0" {
		t.Errorf("bridge fields = %+v, want Name vmbr0", points[0].Fields)
	}
	// An SDN kind has no interfaces(5) representation at all; it must still be
	// flattened, or an SDN-only changeset would preview as changing nothing.
	if points[1].Fields["Zone"] != "zone1" || points[1].Fields["Tag"] != "10" {
		t.Errorf("vnet fields = %+v, want Zone zone1 and Tag 10", points[1].Fields)
	}
	// Identity fields are carried by Ref, never repeated as field rows.
	for _, forbidden := range []string{"Kind", "Node", "ID"} {
		if _, present := points[0].Fields[forbidden]; present {
			t.Errorf("field map carries the identity field %q", forbidden)
		}
	}
}

func TestPointEntities_DiffsAgainstAnEmptySetAsAdded(t *testing.T) {
	points, err := topology.PointEntities([]inventory.Entity{
		&inventory.Bridge{
			Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}, Name: "vmbr0",
		},
	})
	if err != nil {
		t.Fatalf("PointEntities: %v", err)
	}

	diffs := topology.DiffPoints(nil, points)
	if len(diffs) != 1 || diffs[0].Change != topology.DiffAdded {
		t.Fatalf("diffs = %+v, want one added row", diffs)
	}
	if diffs[0].Ref != "bridge:pve1:vmbr0" {
		t.Errorf("ref = %q, want bridge:pve1:vmbr0", diffs[0].Ref)
	}
}

// A nil entry is skipped rather than panicking: the projection builds its
// entity list from a map, and a defensive skip costs nothing.
func TestPointEntities_SkipsNilEntries(t *testing.T) {
	points, err := topology.PointEntities([]inventory.Entity{nil})
	if err != nil {
		t.Fatalf("PointEntities: %v", err)
	}
	if len(points) != 0 {
		t.Errorf("flattened %d entities, want 0", len(points))
	}
}
