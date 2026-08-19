package sdn

import (
	"context"
	"sort"
	"testing"

	"github.com/bgovanlu/vnprox/internal/pve"
)

// fakeReader is a hand-rolled PVEReader test double: the staged/running
// zone/vnet/subnet trees are fixed in-memory slices, matching
// docs/development.md's table-driven-test convention without needing a
// live pvemock server for this package's own unit tests (the fuller,
// end-to-end fixture coverage — evpn-lab.yaml through the real
// internal/pve client and internal/pvemock server — lives in
// internal/api's golden /sdn test).
type fakeReader struct {
	zoneStatus     map[string][]pve.SDNZoneStatus
	subnets        map[string][]pve.SDNSubnet
	subnetsRunning map[string][]pve.SDNSubnet
	subnetsPending map[string][]pve.SDNPendingEntry
	zones          []pve.SDNZone
	zonesRunning   []pve.SDNZone
	zonesPending   []pve.SDNPendingEntry
	vnets          []pve.SDNVnet
	vnetsRunning   []pve.SDNVnet
	vnetsPending   []pve.SDNPendingEntry
	fabrics        []pve.SDNFabric
	fabricNodes    []pve.SDNFabricNode
	prefixLists    []pve.SDNPrefixList
	routeMaps      []pve.SDNRouteMap
	controllers    []pve.SDNController
	ipams          []pve.IPAM
}

func (f *fakeReader) ListSDNZones(context.Context) ([]pve.SDNZone, error) { return f.zones, nil }
func (f *fakeReader) ListSDNZonesRunning(context.Context) ([]pve.SDNZone, error) {
	return f.zonesRunning, nil
}
func (f *fakeReader) ListSDNZonesPending(context.Context) ([]pve.SDNPendingEntry, error) {
	return f.zonesPending, nil
}
func (f *fakeReader) GetSDNZoneStatus(_ context.Context, zone string) ([]pve.SDNZoneStatus, error) {
	return f.zoneStatus[zone], nil
}
func (f *fakeReader) ListSDNVnets(context.Context) ([]pve.SDNVnet, error) { return f.vnets, nil }
func (f *fakeReader) ListSDNVnetsRunning(context.Context) ([]pve.SDNVnet, error) {
	return f.vnetsRunning, nil
}
func (f *fakeReader) ListSDNVnetsPending(context.Context) ([]pve.SDNPendingEntry, error) {
	return f.vnetsPending, nil
}
func (f *fakeReader) ListSDNSubnets(_ context.Context, vnet string) ([]pve.SDNSubnet, error) {
	return f.subnets[vnet], nil
}
func (f *fakeReader) ListSDNSubnetsRunning(_ context.Context, vnet string) ([]pve.SDNSubnet, error) {
	return f.subnetsRunning[vnet], nil
}
func (f *fakeReader) ListSDNSubnetsPending(_ context.Context, vnet string) ([]pve.SDNPendingEntry, error) {
	return f.subnetsPending[vnet], nil
}
func (f *fakeReader) ListSDNFabrics(context.Context) ([]pve.SDNFabric, error) { return f.fabrics, nil }
func (f *fakeReader) ListSDNFabricNodes(context.Context) ([]pve.SDNFabricNode, error) {
	return f.fabricNodes, nil
}
func (f *fakeReader) ListSDNPrefixLists(context.Context) ([]pve.SDNPrefixList, error) {
	return f.prefixLists, nil
}
func (f *fakeReader) ListSDNRouteMaps(context.Context) ([]pve.SDNRouteMap, error) {
	return f.routeMaps, nil
}
func (f *fakeReader) ListSDNControllers(context.Context) ([]pve.SDNController, error) {
	return f.controllers, nil
}
func (f *fakeReader) ListIPAMs(context.Context) ([]pve.IPAM, error) { return f.ipams, nil }

var _ PVEReader = (*fakeReader)(nil)

func TestTree_Structure(t *testing.T) {
	reader := &fakeReader{
		zones: []pve.SDNZone{
			{ID: "vlanz", Type: "vlan", Bridge: "vmbr0", Nodes: []string{"pve1", "pve2"}},
		},
		zonesRunning: []pve.SDNZone{
			{ID: "vlanz", Type: "vlan", Bridge: "vmbr0", Nodes: []string{"pve1", "pve2"}},
		},
		zoneStatus: map[string][]pve.SDNZoneStatus{
			"vlanz": {{Node: "pve1", Status: "ok"}, {Node: "pve2", Status: "ok"}},
		},
		vnets: []pve.SDNVnet{
			{ID: "vnet1", Zone: "vlanz", Tag: 100},
		},
		vnetsRunning: []pve.SDNVnet{
			{ID: "vnet1", Zone: "vlanz", Tag: 100},
		},
		subnets: map[string][]pve.SDNSubnet{
			"vnet1": {{ID: "10.0.0.0/24", Vnet: "vnet1", CIDR: "10.0.0.0/24", Gateway: "10.0.0.1"}},
		},
		subnetsRunning: map[string][]pve.SDNSubnet{
			"vnet1": {{ID: "10.0.0.0/24", Vnet: "vnet1", CIDR: "10.0.0.0/24", Gateway: "10.0.0.1"}},
		},
	}

	svc := NewService(reader)
	tree, err := svc.Tree(context.Background())
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if len(tree.Zones) != 1 {
		t.Fatalf("zones = %d, want 1", len(tree.Zones))
	}
	zone := tree.Zones[0]
	if zone.ID != "vlanz" || zone.Type != "vlan" {
		t.Fatalf("zone = %+v", zone)
	}
	if zone.Diff != nil {
		t.Fatalf("in-sync zone should have a nil Diff, got %+v", zone.Diff)
	}
	if len(zone.NodeStatus) != 2 || zone.NodeStatus[0].Node != "pve1" || zone.NodeStatus[1].Node != "pve2" {
		t.Fatalf("nodeStatus = %+v", zone.NodeStatus)
	}
	if len(zone.Vnets) != 1 || zone.Vnets[0].ID != "vnet1" {
		t.Fatalf("vnets = %+v", zone.Vnets)
	}
	vnet := zone.Vnets[0]
	if len(vnet.Subnets) != 1 || vnet.Subnets[0].ID != "10.0.0.0/24" {
		t.Fatalf("subnets = %+v", vnet.Subnets)
	}
}

// TestTree_Fabrics (T-3101) proves fabrics arrive as a sibling top-level
// Tree collection (not nested under a zone), with per-node membership
// grouped from the fabrics/node collection rather than inferred from any
// zone.
func TestTree_Fabrics(t *testing.T) {
	reader := &fakeReader{
		fabrics: []pve.SDNFabric{
			{ID: "fab1", Protocol: "ospf", Area: "0.0.0.0", Redistribute: []string{"connected"}},
			{ID: "fab2", Protocol: "wireguard", PersistentKeepalive: 25},
		},
		fabricNodes: []pve.SDNFabricNode{
			{Fabric: "fab1", Node: "pve2", IP: "10.255.0.2"},
			{Fabric: "fab1", Node: "pve1", IP: "10.255.0.1"},
		},
		prefixLists: []pve.SDNPrefixList{{ID: "pl1"}},
		routeMaps:   []pve.SDNRouteMap{{ID: "rm1"}},
	}

	svc := NewService(reader)
	tree, err := svc.Tree(context.Background())
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}

	if len(tree.Fabrics) != 2 {
		t.Fatalf("fabrics = %d, want 2 (%+v)", len(tree.Fabrics), tree.Fabrics)
	}
	fab1 := tree.Fabrics[0]
	if fab1.ID != "fab1" || fab1.Protocol != "ospf" || fab1.Area != "0.0.0.0" {
		t.Fatalf("fab1 = %+v", fab1)
	}
	if len(fab1.NodeStatus) != 2 {
		t.Fatalf("fab1.NodeStatus = %+v, want 2 entries", fab1.NodeStatus)
	}
	// Sorted by node — pve1 before pve2 despite fixture declaration order.
	if fab1.NodeStatus[0].Node != "pve1" || fab1.NodeStatus[1].Node != "pve2" {
		t.Fatalf("fab1.NodeStatus not sorted by node: %+v", fab1.NodeStatus)
	}
	if fab1.NodeStatus[0].Status != "ok" || fab1.NodeStatus[0].Detail != "10.255.0.1" {
		t.Fatalf("fab1.NodeStatus[0] = %+v", fab1.NodeStatus[0])
	}

	fab2 := tree.Fabrics[1]
	if fab2.ID != "fab2" || fab2.Protocol != "wireguard" || fab2.PersistentKeepalive != 25 {
		t.Fatalf("fab2 = %+v", fab2)
	}
	// fab2 has no fabrics/node rows — NodeStatus must be an empty slice,
	// never nil (Tree's own nil-to-empty-slice discipline for every list
	// field it renders, mirroring Zone.Vnets/Vnet.Subnets).
	if fab2.NodeStatus == nil || len(fab2.NodeStatus) != 0 {
		t.Fatalf("fab2.NodeStatus = %+v, want a non-nil empty slice", fab2.NodeStatus)
	}

	if len(tree.PrefixLists) != 1 || tree.PrefixLists[0].ID != "pl1" {
		t.Fatalf("prefixLists = %+v", tree.PrefixLists)
	}
	if len(tree.RouteMaps) != 1 || tree.RouteMaps[0].ID != "rm1" {
		t.Fatalf("routeMaps = %+v", tree.RouteMaps)
	}
}

// TestTree_PendingDiff_States also proves Tree sources its Diff/Pending
// state from ListSDNZonesPending — NOT from the staged zone's own Pending
// field, which the debt-sweep fix (2026-08-19, "internal/pve.SDNZone.Pending
// assumes a marker real PVE does not emit") stopped reading. Every fixture
// below leaves the staged pve.SDNZone.Pending field at its zero value
// (pve.PendingNone) on purpose — the state comes only from zonesPending — so
// this test would fail if Tree ever regressed to reading the stale field
// again.
func TestTree_PendingDiff_States(t *testing.T) {
	tests := []struct {
		name        string
		pending     pve.PendingState
		wantState   string
		running     []pve.SDNZone
		wantChanged []string
		staged      pve.SDNZone
		wantHasRun  bool
	}{
		{
			name:      "in sync has no diff",
			pending:   pve.PendingNone,
			staged:    pve.SDNZone{ID: "z1", Type: "vlan", MTU: 1500},
			running:   []pve.SDNZone{{ID: "z1", Type: "vlan", MTU: 1500}},
			wantState: "",
		},
		{
			name:      "new has no running counterpart",
			pending:   pve.PendingNew,
			staged:    pve.SDNZone{ID: "z1", Type: "vlan"},
			running:   nil,
			wantState: "new",
		},
		{
			name:        "changed renders exactly the changed fields",
			pending:     pve.PendingChanged,
			staged:      pve.SDNZone{ID: "z1", Type: "vlan", MTU: 1600},
			running:     []pve.SDNZone{{ID: "z1", Type: "vlan", MTU: 1500}},
			wantState:   "changed",
			wantChanged: []string{"mtu"},
			wantHasRun:  true,
		},
		{
			name:      "deleted still has a running counterpart (not yet applied)",
			pending:   pve.PendingDeleted,
			staged:    pve.SDNZone{ID: "z1", Type: "vlan"},
			running:   []pve.SDNZone{{ID: "z1", Type: "vlan"}},
			wantState: "deleted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			z := tt.staged
			var zonesPending []pve.SDNPendingEntry
			if tt.pending != pve.PendingNone {
				zonesPending = []pve.SDNPendingEntry{{Kind: "zone", ID: z.ID, State: tt.pending}}
			}
			reader := &fakeReader{
				zones:        []pve.SDNZone{z},
				zonesRunning: tt.running,
				zonesPending: zonesPending,
				zoneStatus:   map[string][]pve.SDNZoneStatus{},
			}
			svc := NewService(reader)
			tree, err := svc.Tree(context.Background())
			if err != nil {
				t.Fatalf("Tree: %v", err)
			}
			got := tree.Zones[0].Diff
			if tt.wantState == "" {
				if got != nil {
					t.Fatalf("expected nil Diff for in-sync zone, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected non-nil Diff")
			}
			if got.State != tt.wantState {
				t.Errorf("State = %q, want %q", got.State, tt.wantState)
			}
			sort.Strings(got.ChangedFields)
			if !equalStrings(got.ChangedFields, tt.wantChanged) {
				t.Errorf("ChangedFields = %v, want %v", got.ChangedFields, tt.wantChanged)
			}
			if tt.wantHasRun && got.Running == nil {
				t.Errorf("expected Running to be populated")
			}
			if got.Staged == nil {
				t.Errorf("expected Staged to be populated")
			}
			// The diff must never leak the "pending" marker itself as a
			// pseudo-field — it's already PendingDiff.State.
			if _, ok := got.Staged["pending"]; ok {
				t.Errorf("Staged map should not carry a \"pending\" key")
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestTree_ZoneStatusFailureDoesNotFailWholeRequest(t *testing.T) {
	reader := &fakeReader{
		zones:        []pve.SDNZone{{ID: "z1", Type: "vlan"}},
		zonesRunning: []pve.SDNZone{{ID: "z1", Type: "vlan"}},
		// zoneStatus deliberately has no entry for "z1" — GetSDNZoneStatus
		// returns an empty slice, not an error, but this exercises the
		// "no status yet" path the same way a real failure would.
		zoneStatus: map[string][]pve.SDNZoneStatus{},
	}
	svc := NewService(reader)
	tree, err := svc.Tree(context.Background())
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if len(tree.Zones) != 1 {
		t.Fatalf("zones = %d, want 1", len(tree.Zones))
	}
	if tree.Zones[0].NodeStatus == nil {
		t.Fatalf("NodeStatus should be an empty slice, not nil (docs/features/topology.md-style array field contract)")
	}
}
