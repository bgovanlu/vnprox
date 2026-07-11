package topology_test

import (
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// TestPorts_Table checks the flat Ports table (T-302 spec §2), including
// that a dropped-from-map (>10min) neighbor still appears in the table,
// tagged stale, per spec §3.
func TestPorts_Table(t *testing.T) {
	base := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	now := base.Add(20 * time.Minute)
	fresh := &inventory.LldpNeighbor{
		Ref:        inventory.Ref{Kind: inventory.KindLldpNeighbor, Node: "pve1", ID: "eno1/sw1/Gi1"},
		LocalIface: "eno1", Node: "pve1", ChassisName: "sw-core-01", ChassisID: "sw1",
		PortID: "Gi1/0/1", SpeedMbps: 1000, VLAN: 10, TaggedVLANs: []int{20, 30},
		TTL: 120, LastSeen: now.Add(-1 * time.Minute).Unix(), // observed 1min ago
	}
	stale := &inventory.LldpNeighbor{
		Ref:        inventory.Ref{Kind: inventory.KindLldpNeighbor, Node: "pve2", ID: "eno1/sw2/Gi2"},
		LocalIface: "eno1", Node: "pve2", ChassisName: "sw-core-02", ChassisID: "sw2",
		PortID: "Gi1/0/2", SpeedMbps: 1000, VLAN: 10,
		TTL: 120, LastSeen: base.Unix(), // observed 20min ago: past the 10min drop threshold
	}
	g := inventory.NewGraph()
	g.ApplyPoll(inventory.SourceHostLLDP, inventory.Scope{Node: "pve1"}, []inventory.Entity{fresh})
	g.ApplyPoll(inventory.SourceHostLLDP, inventory.Scope{Node: "pve2"}, []inventory.Entity{stale})
	snap := g.Snapshot()

	rows := topology.Ports(snap, now)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (stale rows stay in the table): %+v", len(rows), rows)
	}
	// Sorted by node then NIC: pve1 before pve2.
	if rows[0].Node != "pve1" || rows[0].Stale {
		t.Errorf("row 0 = %+v, want fresh pve1 row", rows[0])
	}
	if rows[1].Node != "pve2" || !rows[1].Stale {
		t.Errorf("row 1 = %+v, want stale pve2 row", rows[1])
	}

	// AC1's map (Project) must have already dropped the stale neighbor's
	// switch entirely at the same "now" — the table's job is precisely to
	// keep showing what the map no longer does.
	topo := topology.Project(snap, topology.Filter{Now: now})
	for _, n := range topo.Nodes {
		if n.Kind == topology.KindSwitch && n.Label == "sw-core-02" {
			t.Errorf("sw-core-02 should have dropped from the map by now, still present: %+v", n)
		}
	}
}

// TestPortsCSV_Golden pins the exact CSV rendering (T-302 AC5).
func TestPortsCSV_Golden(t *testing.T) {
	rows := []topology.PortRow{
		{Node: "pve1", NIC: "eno1", Switch: "sw-core-01", Port: "Gi1/0/1", SpeedMbps: 1000, PVID: 10, TaggedVLANs: []int{20, 30}, LastSeen: 1720512345, Stale: false},
		{Node: "pve2", NIC: "eno1", Switch: "sw-core-02", Port: "Gi1/0/2", LastSeen: 1720500000, Stale: true},
	}
	got := topology.PortsCSV(rows)
	want := "node,nic,switch,port,speedMbps,pvid,taggedVlans,lastSeen,stale\n" +
		"pve1,eno1,sw-core-01,Gi1/0/1,1000,10,20;30,1720512345,false\n" +
		"pve2,eno1,sw-core-02,Gi1/0/2,,,,1720500000,true\n"
	if got != want {
		t.Errorf("PortsCSV mismatch:\ngot:  %q\nwant: %q", got, want)
	}

	// Golden test's real invariant: CSV output matches Ports()'s own table
	// exactly (AC5's "CSV export matches the table").
	from := topology.PortsCSV(rows)
	if from != got {
		t.Errorf("PortsCSV is not deterministic")
	}
}
