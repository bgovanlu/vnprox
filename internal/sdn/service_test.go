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
	zones          []pve.SDNZone
	zonesRunning   []pve.SDNZone
	vnets          []pve.SDNVnet
	vnetsRunning   []pve.SDNVnet
}

func (f *fakeReader) ListSDNZones(context.Context) ([]pve.SDNZone, error) { return f.zones, nil }
func (f *fakeReader) ListSDNZonesRunning(context.Context) ([]pve.SDNZone, error) {
	return f.zonesRunning, nil
}
func (f *fakeReader) GetSDNZoneStatus(_ context.Context, zone string) ([]pve.SDNZoneStatus, error) {
	return f.zoneStatus[zone], nil
}
func (f *fakeReader) ListSDNVnets(context.Context) ([]pve.SDNVnet, error) { return f.vnets, nil }
func (f *fakeReader) ListSDNVnetsRunning(context.Context) ([]pve.SDNVnet, error) {
	return f.vnetsRunning, nil
}
func (f *fakeReader) ListSDNSubnets(_ context.Context, vnet string) ([]pve.SDNSubnet, error) {
	return f.subnets[vnet], nil
}
func (f *fakeReader) ListSDNSubnetsRunning(_ context.Context, vnet string) ([]pve.SDNSubnet, error) {
	return f.subnetsRunning[vnet], nil
}

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
			staged:    pve.SDNZone{ID: "z1", Type: "vlan", Pending: pve.PendingNew},
			running:   nil,
			wantState: "new",
		},
		{
			name:        "changed renders exactly the changed fields",
			pending:     pve.PendingChanged,
			staged:      pve.SDNZone{ID: "z1", Type: "vlan", MTU: 1600, Pending: pve.PendingChanged},
			running:     []pve.SDNZone{{ID: "z1", Type: "vlan", MTU: 1500}},
			wantState:   "changed",
			wantChanged: []string{"mtu"},
			wantHasRun:  true,
		},
		{
			name:      "deleted still has a running counterpart (not yet applied)",
			pending:   pve.PendingDeleted,
			staged:    pve.SDNZone{ID: "z1", Type: "vlan", Pending: pve.PendingDeleted},
			running:   []pve.SDNZone{{ID: "z1", Type: "vlan"}},
			wantState: "deleted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			z := tt.staged
			z.Pending = tt.pending
			reader := &fakeReader{
				zones:        []pve.SDNZone{z},
				zonesRunning: tt.running,
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
