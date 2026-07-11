package topology_test

import (
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// TestLLDPStaleness_GreyThenDrop is T-302 AC3: an entry greys (renders
// StatusUnknown) once its age exceeds 2xTTL, and drops from the map
// entirely once its age exceeds the fixed 10-minute threshold
// (docs/features/lldp-discovery.md §3), driven by an injected clock
// (topology.Filter.Now) rather than wall time.
func TestLLDPStaleness_GreyThenDrop(t *testing.T) {
	seen := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	const ttl = 120 // seconds; grey threshold = 2*ttl = 240s = 4min
	neighbor := &inventory.LldpNeighbor{
		Ref:        inventory.Ref{Kind: inventory.KindLldpNeighbor, Node: "pve1", ID: "eno1/sw1/p1"},
		LocalIface: "eno1", Node: "pve1",
		ChassisID: "aa:bb:cc:dd:ee:ff", ChassisName: "sw1", PortID: "Gi0/1",
		TTL: ttl, LastSeen: seen.Unix(),
	}
	snap := buildLLDPGraph(t, []*inventory.LldpNeighbor{neighbor})

	cases := []struct {
		now        time.Time
		name       string
		wantSwitch bool
		wantOKEdge bool
	}{
		{name: "fresh", now: seen.Add(1 * time.Minute), wantSwitch: true, wantOKEdge: true},
		{name: "just under grey threshold", now: seen.Add(3*time.Minute + 59*time.Second), wantSwitch: true, wantOKEdge: true},
		{name: "greyed past 2xTTL", now: seen.Add(5 * time.Minute), wantSwitch: true, wantOKEdge: false},
		{name: "still present just under 10min", now: seen.Add(9*time.Minute + 59*time.Second), wantSwitch: true, wantOKEdge: false},
		{name: "dropped past 10min", now: seen.Add(11 * time.Minute), wantSwitch: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			topo := topology.Project(snap, topology.Filter{Now: tc.now})
			switches := switchNodes(topo.Nodes)
			if tc.wantSwitch {
				if len(switches) != 1 {
					t.Fatalf("got %d switch nodes, want 1: %+v", len(switches), switches)
				}
				var portEdge *topology.Edge
				for i, e := range topo.Edges {
					if e.Kind == topology.EdgeLLDPPort {
						portEdge = &topo.Edges[i]
					}
				}
				if portEdge == nil {
					t.Fatal("missing lldp-port edge")
				}
				wantStatus := topology.StatusUnknown
				if tc.wantOKEdge {
					wantStatus = topology.StatusOK
				}
				if portEdge.Status != wantStatus {
					t.Errorf("lldp-port edge status = %q, want %q", portEdge.Status, wantStatus)
				}
				if switches[0].Status != wantStatus {
					t.Errorf("switch node status = %q, want %q", switches[0].Status, wantStatus)
				}
			} else if len(switches) != 0 {
				t.Errorf("got %d switch nodes, want 0 (past 10min drop threshold): %+v", len(switches), switches)
			}
		})
	}
}
