package topology_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// TestDetail covers GET /inventory/{ref}'s underlying projection: resolved
// fields, provenance, and related (edge-linked) entities.
func TestDetail(t *testing.T) {
	graph, _, _ := buildGraph(t, fixtureSingleNode)
	snap := graph.Snapshot()

	t.Run("not found", func(t *testing.T) {
		_, ok := topology.Detail(snap, inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "does-not-exist"})
		if ok {
			t.Error("expected ok=false for an unknown ref")
		}
	})

	t.Run("physnic", func(t *testing.T) {
		ref := inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"}
		d, ok := topology.Detail(snap, ref)
		if !ok {
			t.Fatal("expected ok=true for eno1")
		}
		if d.Ref != ref.String() || d.Kind != "physnic" || d.Node != "pve1" {
			t.Errorf("detail identity = %+v, want ref/kind/node for %s", d, ref)
		}
		if d.Label != "eno1" {
			t.Errorf("label = %q, want eno1", d.Label)
		}
		if d.Fields["Mac"] != "bc:24:11:00:00:01" {
			t.Errorf("Fields[Mac] = %v, want bc:24:11:00:00:01 (got fields: %+v)", d.Fields["Mac"], d.Fields)
		}
		// Provenance: linkUp is host-netlink-only and single-source here
		// (only pve1 is polled), so it should be owned by host-netlink with
		// no conflicts.
		fp, ok := d.Provenance["linkUp"]
		if !ok {
			t.Fatalf("expected a linkUp provenance entry; got %+v", d.Provenance)
		}
		if fp.Owner != "host-netlink" {
			t.Errorf("linkUp owner = %q, want host-netlink", fp.Owner)
		}
		// Related: eno1 is a bridge port and an LLDP-adjacent NIC.
		var sawPortOf, sawLldp bool
		for _, rel := range d.Related {
			switch rel.EdgeKind {
			case "port-of":
				sawPortOf = true
				if rel.Direction != "to" {
					t.Errorf("port-of direction = %q, want to (eno1 is the From side)", rel.Direction)
				}
			case "lldp-adjacent":
				sawLldp = true
			}
		}
		if !sawPortOf {
			t.Errorf("expected a port-of related entry; got %+v", d.Related)
		}
		if !sawLldp {
			t.Errorf("expected an lldp-adjacent related entry; got %+v", d.Related)
		}
		if d.GeneratedAt <= 0 {
			t.Errorf("GeneratedAt = %d, want > 0", d.GeneratedAt)
		}
	})
}
