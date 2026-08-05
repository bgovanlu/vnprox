package change

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// TestOpsTouchSDNConfig proves the family gate: only sdn.zone/vnet/subnet
// ops trigger the config-diff extension; sdn.apply and node-file ops don't.
func TestOpsTouchSDNConfig(t *testing.T) {
	cases := []struct {
		name string
		ops  []Op
		want bool
	}{
		{"empty", nil, false},
		{"bridge only", []Op{mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve1", "vmbr1"), &BridgeCreateParams{})}, false},
		{"sdn apply only", []Op{mkOp(OpSdnApply, inventory.Ref{}, &SdnApplyParams{})}, false},
		{"zone create", []Op{mkOp(OpSdnZoneCreate, testRef(inventory.KindSDNZone, "", "z1"), &SdnZoneCreateParams{Type: "simple"})}, true},
		{"vnet update", []Op{mkOp(OpSdnVnetUpdate, testRef(inventory.KindSDNVnet, "", "z1/v1"), &SdnVnetUpdateParams{})}, true},
		{"subnet delete", []Op{mkOp(OpSdnSubnetDelete, testRef(inventory.KindSDNSubnet, "", "10.0.0.0/24"), &SdnSubnetDeleteParams{})}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := opsTouchSDNConfig(c.ops); got != c.want {
				t.Errorf("opsTouchSDNConfig(%s) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

// TestProjectSDNZoneConfigs_MatchesCreateParamsFieldForField is T-2003
// acceptance criterion 4's core claim for zones: the "after" projection a
// sdn.zone.create op produces must carry exactly the fields that op's params
// would write via internal/pve's real create call (params_sdn.go's
// SdnZoneCreateParams is a 1:1 field mirror of SDNZoneConfig).
func TestProjectSDNZoneConfigs_MatchesCreateParamsFieldForField(t *testing.T) {
	ops := []Op{
		mkOp(OpSdnZoneCreate, testRef(inventory.KindSDNZone, "", "z1"), &SdnZoneCreateParams{
			Type: "vxlan", Bridge: "vmbr0", Controller: "ctl1", Nodes: []string{"pve1", "pve2"},
			ExitNodes: []string{"pve1"}, Peers: []string{"10.0.0.1", "10.0.0.2"}, VrfVxlan: 42, MTU: 1450,
		}),
	}
	before, after := projectSDNZoneConfigs(ops, inventory.NewGraph().Snapshot())
	if len(before) != 0 {
		t.Fatalf("before = %+v, want empty (no pre-existing zones)", before)
	}
	if len(after) != 1 {
		t.Fatalf("after = %+v, want exactly one zone", after)
	}
	want := SDNZoneConfig{
		ID: "z1", Type: "vxlan", Bridge: "vmbr0", Controller: "ctl1", Nodes: []string{"pve1", "pve2"},
		ExitNodes: []string{"pve1"}, Peers: []string{"10.0.0.1", "10.0.0.2"}, VrfVxlan: 42, MTU: 1450,
	}
	if got := after[0]; !zoneConfigEqual(got, want) {
		t.Errorf("after[0] = %+v, want %+v", got, want)
	}
}

func zoneConfigEqual(a, b SDNZoneConfig) bool {
	if a.ID != b.ID || a.Type != b.Type || a.Bridge != b.Bridge || a.Controller != b.Controller ||
		a.VrfVxlan != b.VrfVxlan || a.MTU != b.MTU {
		return false
	}
	return strsEqual(a.Nodes, b.Nodes) && strsEqual(a.ExitNodes, b.ExitNodes) && strsEqual(a.Peers, b.Peers)
}

func strsEqual(a, b []string) bool {
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

// TestProjectSDNZoneConfigs_UpdateFoldsOntoExisting proves an update op
// folds onto a pre-existing (inventory-snapshot) zone: fields the update
// leaves nil (e.g. MTU) survive from "before" into "after" unchanged, and
// fields it sets replace the old value — matching a real PVE PUT's
// partial-update semantics.
func TestProjectSDNZoneConfigs_UpdateFoldsOntoExisting(t *testing.T) {
	existing := &inventory.SdnZone{Ref: testRef(inventory.KindSDNZone, "", "z1"), ID: "z1", Type: "simple", Bridge: "vmbr0", MTU: 1500, Nodes: []string{"pve1"}}
	snap := buildSnapshot(existing)
	ops := []Op{
		mkOp(OpSdnZoneUpdate, testRef(inventory.KindSDNZone, "", "z1"), &SdnZoneUpdateParams{Bridge: strPtr("vmbr1")}),
	}
	before, after := projectSDNZoneConfigs(ops, snap)
	if len(before) != 1 || before[0].Bridge != "vmbr0" || before[0].MTU != 1500 {
		t.Fatalf("before = %+v, want the pre-existing zone unchanged", before)
	}
	if len(after) != 1 {
		t.Fatalf("after = %+v, want one zone", after)
	}
	if after[0].Bridge != "vmbr1" {
		t.Errorf("after[0].Bridge = %q, want vmbr1 (updated)", after[0].Bridge)
	}
	if after[0].MTU != 1500 {
		t.Errorf("after[0].MTU = %d, want 1500 (untouched field carried forward)", after[0].MTU)
	}
}

// TestProjectSDNVnetConfigs_DeleteRemovesFromAfter proves a delete op drops
// the entity from "after" while "before" still shows it — the diff's whole
// point.
func TestProjectSDNVnetConfigs_DeleteRemovesFromAfter(t *testing.T) {
	existing := &inventory.SdnVnet{Ref: testRef(inventory.KindSDNVnet, "", "z1/v1"), ID: "z1/v1", Zone: "z1", Tag: 100}
	snap := buildSnapshot(existing)
	ops := []Op{mkOp(OpSdnVnetDelete, testRef(inventory.KindSDNVnet, "", "z1/v1"), &SdnVnetDeleteParams{})}
	before, after := projectSDNVnetConfigs(ops, snap)
	if len(before) != 1 {
		t.Fatalf("before = %+v, want one vnet", before)
	}
	if len(after) != 0 {
		t.Fatalf("after = %+v, want empty (deleted)", after)
	}
}

// TestProjectSDNSubnetConfigs_CreateThenUpdateInSameChangeset proves
// net-effect folding within one changeset: a subnet created and then updated
// by two ops in the same batch ends up with the update's values in "after".
func TestProjectSDNSubnetConfigs_CreateThenUpdateInSameChangeset(t *testing.T) {
	ops := []Op{
		mkOp(OpSdnSubnetCreate, testRef(inventory.KindSDNSubnet, "", "10.0.0.0/24"), &SdnSubnetCreateParams{
			Vnet: "z1/v1", CIDR: "10.0.0.0/24", Gateway: "10.0.0.1",
		}),
		mkOp(OpSdnSubnetUpdate, testRef(inventory.KindSDNSubnet, "", "10.0.0.0/24"), &SdnSubnetUpdateParams{
			SNAT: boolPtr(true),
		}),
	}
	_, after := projectSDNSubnetConfigs(ops, inventory.NewGraph().Snapshot())
	if len(after) != 1 {
		t.Fatalf("after = %+v, want one subnet", after)
	}
	if after[0].Gateway != "10.0.0.1" || !after[0].SNAT {
		t.Errorf("after[0] = %+v, want gateway 10.0.0.1 and snat true", after[0])
	}
}

// TestSdnConfigDiffFiles_NoSDNOps_ReturnsNil proves the extension is a
// complete no-op for a changeset that never touches SDN — the ordinary
// node-file-only case (the overwhelming majority of changesets) gets zero
// extra FileDiff entries.
func TestSdnConfigDiffFiles_NoSDNOps_ReturnsNil(t *testing.T) {
	ops := []Op{mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve1", "vmbr1"), &BridgeCreateParams{})}
	if got := sdnConfigDiffFiles(ops, inventory.NewGraph().Snapshot()); got != nil {
		t.Errorf("sdnConfigDiffFiles = %+v, want nil for a non-SDN changeset", got)
	}
}

// TestSdnConfigDiffFiles_RendersThreeFilesWithUnifiedDiffs is T-2003
// acceptance criterion 4's end-to-end shape check: a changeset creating a
// zone, vnet, and subnet renders exactly the three synthetic
// /etc/pve/sdn/*.cfg paths, each Changed and each unified diff mentioning
// the new entity's id.
func TestSdnConfigDiffFiles_RendersThreeFilesWithUnifiedDiffs(t *testing.T) {
	ops := []Op{
		mkOp(OpSdnZoneCreate, testRef(inventory.KindSDNZone, "", "z1"), &SdnZoneCreateParams{Type: "simple", Bridge: "vmbr0"}),
		mkOp(OpSdnVnetCreate, testRef(inventory.KindSDNVnet, "", "z1/v1"), &SdnVnetCreateParams{Zone: "z1", Tag: 10}),
		mkOp(OpSdnSubnetCreate, testRef(inventory.KindSDNSubnet, "", "10.60.0.0/24"), &SdnSubnetCreateParams{Vnet: "z1/v1", CIDR: "10.60.0.0/24", Gateway: "10.60.0.1"}),
	}
	files := sdnConfigDiffFiles(ops, inventory.NewGraph().Snapshot())
	if len(files) != 3 {
		t.Fatalf("len(files) = %d, want 3 (zones/vnets/subnets)", len(files))
	}
	wantPaths := map[string]string{
		sdnZonesSnapshotPath:   "z1",
		sdnVnetsSnapshotPath:   "v1",
		sdnSubnetsSnapshotPath: "10.60.0.0/24",
	}
	seen := map[string]bool{}
	for _, f := range files {
		seen[f.Path] = true
		want, ok := wantPaths[f.Path]
		if !ok {
			t.Errorf("unexpected file path %q", f.Path)
			continue
		}
		if !f.Changed {
			t.Errorf("file %s: Changed = false, want true", f.Path)
		}
		if !strings.Contains(f.Unified, want) {
			t.Errorf("file %s: unified diff = %q, want it to mention %q", f.Path, f.Unified, want)
		}
	}
	for path := range wantPaths {
		if !seen[path] {
			t.Errorf("missing expected file %s", path)
		}
	}
}
