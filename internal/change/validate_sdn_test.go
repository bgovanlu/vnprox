package change

import (
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// --- bridge existence on member nodes --------------------------------------

func TestSdnValidate_ZoneBridgeExistence(t *testing.T) {
	snap := buildSnapshot(
		&inventory.Node{Ref: testRef(inventory.KindNode, "pve1", "pve1"), Name: "pve1"},
		&inventory.Node{Ref: testRef(inventory.KindNode, "pve2", "pve2"), Name: "pve2"},
		&inventory.Bridge{Ref: testRef(inventory.KindBridge, "pve1", "vmbr9"), Name: "vmbr9"},
	)
	zone := testRef(inventory.KindSDNZone, "", "zone1")

	t.Run("bridge missing on a member node errors", func(t *testing.T) {
		ops := []Op{mkOp(OpSdnZoneCreate, zone, &SdnZoneCreateParams{
			Type: "simple", Bridge: "vmbr9", Nodes: []string{"pve1", "pve2"},
		})}
		findings := sdnValidate(ops, snap)
		assertFindings(t, findings, []wantFinding{{SeverityError, codeSDNBridgeMissing, zone.String()}})
	})

	t.Run("bridge present on every member node is clean", func(t *testing.T) {
		ops := []Op{mkOp(OpSdnZoneCreate, zone, &SdnZoneCreateParams{
			Type: "simple", Bridge: "vmbr9", Nodes: []string{"pve1"},
		})}
		findings := sdnValidate(ops, snap)
		assertFindings(t, findings, nil)
	})

	t.Run("a bridge this same changeset also creates counts as existing", func(t *testing.T) {
		ops := []Op{
			mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve2", "vmbr9"), &BridgeCreateParams{}),
			mkOp(OpSdnZoneCreate, zone, &SdnZoneCreateParams{
				Type: "simple", Bridge: "vmbr9", Nodes: []string{"pve1", "pve2"},
			}),
		}
		findings := sdnValidate(ops, snap)
		assertFindings(t, findings, nil)
	})

	t.Run("vxlan zones are not bridge-checked", func(t *testing.T) {
		ops := []Op{mkOp(OpSdnZoneCreate, zone, &SdnZoneCreateParams{
			Type: "vxlan", Nodes: []string{"pve1", "pve2"},
		})}
		findings := sdnValidate(ops, snap)
		assertFindings(t, findings, nil)
	})

	t.Run("zone.update adding a node without the bridge errors", func(t *testing.T) {
		snapWithZone := buildSnapshot(
			&inventory.Node{Ref: testRef(inventory.KindNode, "pve1", "pve1"), Name: "pve1"},
			&inventory.Node{Ref: testRef(inventory.KindNode, "pve2", "pve2"), Name: "pve2"},
			&inventory.Bridge{Ref: testRef(inventory.KindBridge, "pve1", "vmbr9"), Name: "vmbr9"},
			&inventory.SdnZone{Ref: zone, ID: "zone1", Type: "simple", Bridge: "vmbr9", Nodes: []string{"pve1"}},
		)
		ops := []Op{mkOp(OpSdnZoneUpdate, zone, &SdnZoneUpdateParams{Nodes: strsPtr("pve1", "pve2")})}
		findings := sdnValidate(ops, snapWithZone)
		assertFindings(t, findings, []wantFinding{{SeverityError, codeSDNBridgeMissing, zone.String()}})
	})
}

// --- vnet tag uniqueness ----------------------------------------------------

func TestSdnValidate_VnetTagUniqueness(t *testing.T) {
	zone1 := &inventory.SdnZone{Ref: testRef(inventory.KindSDNZone, "", "zone1"), ID: "zone1", Type: "vlan"}
	existingVnet := &inventory.SdnVnet{Ref: testRef(inventory.KindSDNVnet, "", "zone1/vnet1"), ID: "zone1/vnet1", Zone: "zone1", Tag: 100}

	t.Run("new vnet colliding with an existing sibling's tag errors, naming it", func(t *testing.T) {
		snap := buildSnapshot(zone1, existingVnet)
		target := testRef(inventory.KindSDNVnet, "", "zone1/vnet2")
		ops := []Op{mkOp(OpSdnVnetCreate, target, &SdnVnetCreateParams{Zone: "zone1", Tag: 100})}
		findings := sdnValidate(ops, snap)
		if len(findings) != 1 {
			t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
		}
		f := findings[0]
		if f.Severity != SeverityError || f.Code != codeSDNTagDuplicate || f.Ref != target.String() {
			t.Errorf("finding = %+v", f)
		}
		if !strings.Contains(f.Message, "zone1/vnet1") {
			t.Errorf("message %q does not name the colliding sibling", f.Message)
		}
	})

	t.Run("same tag in a different zone is clean", func(t *testing.T) {
		zone2 := &inventory.SdnZone{Ref: testRef(inventory.KindSDNZone, "", "zone2"), ID: "zone2", Type: "vlan"}
		snap := buildSnapshot(zone1, zone2, existingVnet)
		ops := []Op{mkOp(OpSdnVnetCreate, testRef(inventory.KindSDNVnet, "", "zone2/vnet1"),
			&SdnVnetCreateParams{Zone: "zone2", Tag: 100})}
		findings := sdnValidate(ops, snap)
		assertFindings(t, findings, nil)
	})

	t.Run("two new vnets in the same changeset colliding with each other both error", func(t *testing.T) {
		snap := buildSnapshot(zone1)
		v1 := testRef(inventory.KindSDNVnet, "", "zone1/vnet1")
		v2 := testRef(inventory.KindSDNVnet, "", "zone1/vnet2")
		ops := []Op{
			mkOp(OpSdnVnetCreate, v1, &SdnVnetCreateParams{Zone: "zone1", Tag: 50}),
			mkOp(OpSdnVnetCreate, v2, &SdnVnetCreateParams{Zone: "zone1", Tag: 50}),
		}
		findings := sdnValidate(ops, snap)
		assertFindings(t, findings, []wantFinding{
			{SeverityError, codeSDNTagDuplicate, v1.String()},
			{SeverityError, codeSDNTagDuplicate, v2.String()},
		})
	})

	t.Run("deleting the colliding sibling in the same changeset clears the error", func(t *testing.T) {
		snap := buildSnapshot(zone1, existingVnet)
		target := testRef(inventory.KindSDNVnet, "", "zone1/vnet2")
		ops := []Op{
			mkOp(OpSdnVnetDelete, existingVnet.Ref, &SdnVnetDeleteParams{}),
			mkOp(OpSdnVnetCreate, target, &SdnVnetCreateParams{Zone: "zone1", Tag: 100}),
		}
		findings := sdnValidate(ops, snap)
		assertFindings(t, findings, nil)
	})

	t.Run("untagged (tag 0) vnets never collide", func(t *testing.T) {
		snap := buildSnapshot(zone1)
		ops := []Op{
			mkOp(OpSdnVnetCreate, testRef(inventory.KindSDNVnet, "", "zone1/vnet1"), &SdnVnetCreateParams{Zone: "zone1"}),
			mkOp(OpSdnVnetCreate, testRef(inventory.KindSDNVnet, "", "zone1/vnet2"), &SdnVnetCreateParams{Zone: "zone1"}),
		}
		findings := sdnValidate(ops, snap)
		assertFindings(t, findings, nil)
	})
}

// --- VXLAN MTU advisory (T-402 acceptance criterion 3) ---------------------

func TestAdvisoryValidate_VxlanMTU(t *testing.T) {
	t.Run("underlay 1500 + zone mtu 1500 warns with a fix patch setting 1450", func(t *testing.T) {
		zone := testRef(inventory.KindSDNZone, "", "zone1")
		op := mkOp(OpSdnZoneCreate, zone, &SdnZoneCreateParams{Type: "vxlan", MTU: 1500})

		findings := ValidateWithSafety([]Op{op}, buildSnapshot(), SafetyOptions{})
		var found *Finding
		for i := range findings {
			if findings[i].Code == codeAdvisoryVxlanMTU {
				found = &findings[i]
			}
		}
		if found == nil {
			t.Fatalf("no %s finding in %+v", codeAdvisoryVxlanMTU, findings)
		}
		if found.Severity != SeverityWarning {
			t.Errorf("severity = %s, want warning (never blocks apply)", found.Severity)
		}
		if len(found.Fix) != 1 {
			t.Fatalf("fix = %+v, want exactly one op", found.Fix)
		}
		fixed, ok := found.Fix[0].Params.(*SdnZoneCreateParams)
		if !ok || fixed.MTU != 1450 {
			t.Fatalf("fix params = %+v, want mtu=1450", found.Fix[0].Params)
		}

		// AC3: "fix applies clean" — revalidating with the fix substituted
		// carries no more vxlan-mtu warning.
		after := ValidateWithSafety(found.Fix, buildSnapshot(), SafetyOptions{})
		for _, f := range after {
			if f.Code == codeAdvisoryVxlanMTU {
				t.Errorf("vxlan mtu warning still present after applying its own fix: %+v", after)
			}
		}
	})

	t.Run("mtu already 1450 (the fixed value) does not warn", func(t *testing.T) {
		zone := testRef(inventory.KindSDNZone, "", "zone1")
		op := mkOp(OpSdnZoneCreate, zone, &SdnZoneCreateParams{Type: "vxlan", MTU: 1450})
		findings := ValidateWithSafety([]Op{op}, buildSnapshot(), SafetyOptions{})
		for _, f := range findings {
			if f.Code == codeAdvisoryVxlanMTU {
				t.Errorf("unexpected vxlan mtu warning at the recommended value: %+v", findings)
			}
		}
	})

	t.Run("mtu unset (0) does not warn — PVE applies its own default", func(t *testing.T) {
		zone := testRef(inventory.KindSDNZone, "", "zone1")
		op := mkOp(OpSdnZoneCreate, zone, &SdnZoneCreateParams{Type: "vxlan"})
		findings := ValidateWithSafety([]Op{op}, buildSnapshot(), SafetyOptions{})
		for _, f := range findings {
			if f.Code == codeAdvisoryVxlanMTU {
				t.Errorf("unexpected vxlan mtu warning with no mtu set: %+v", findings)
			}
		}
	})

	t.Run("simple zone type is never checked", func(t *testing.T) {
		zone := testRef(inventory.KindSDNZone, "", "zone1")
		op := mkOp(OpSdnZoneCreate, zone, &SdnZoneCreateParams{Type: "simple", MTU: 1500})
		findings := ValidateWithSafety([]Op{op}, buildSnapshot(), SafetyOptions{})
		for _, f := range findings {
			if f.Code == codeAdvisoryVxlanMTU {
				t.Errorf("unexpected vxlan mtu warning on a simple zone: %+v", findings)
			}
		}
	})

	t.Run("zone.update on an existing vxlan zone is checked against its stored type", func(t *testing.T) {
		zone := testRef(inventory.KindSDNZone, "", "zone1")
		snap := buildSnapshot(&inventory.SdnZone{Ref: zone, ID: "zone1", Type: "vxlan", MTU: 1450})
		op := mkOp(OpSdnZoneUpdate, zone, &SdnZoneUpdateParams{MTU: intPtr(1500)})
		findings := ValidateWithSafety([]Op{op}, snap, SafetyOptions{})
		var found bool
		for _, f := range findings {
			if f.Code == codeAdvisoryVxlanMTU {
				found = true
			}
		}
		if !found {
			t.Errorf("expected a vxlan mtu warning for the update, got %+v", findings)
		}
	})
}

// --- vnet deletion guard (T-402 acceptance criterion 2, mirrors T-203) -----

func vnetGuestBearingSnapshot() inventory.Snapshot {
	zone := &inventory.SdnZone{Ref: testRef(inventory.KindSDNZone, "", "zone1"), ID: "zone1", Type: "vlan"}
	vnet1 := testRef(inventory.KindSDNVnet, "", "zone1/vnet1")
	vnet2 := testRef(inventory.KindSDNVnet, "", "zone1/vnet2")
	return buildSnapshot(
		zone,
		&inventory.SdnVnet{Ref: vnet1, ID: "zone1/vnet1", Zone: "zone1", Tag: 10},
		// vnet2 is a genuine surviving reattachment target (net-effect based,
		// mirroring T-203's own guestBearingSnapshot fixture exactly).
		&inventory.SdnVnet{Ref: vnet2, ID: "zone1/vnet2", Zone: "zone1", Tag: 20},
		&inventory.Guest{Ref: testRef(inventory.KindGuest, "pve1", "100"), Name: "web01", VMID: 100, Status: "running"},
		&inventory.GuestNic{
			Ref:   testRef(inventory.KindGuestNic, "pve1", "100/net0"),
			Guest: testRef(inventory.KindGuest, "pve1", "100"), Key: "net0",
			BridgeOrVnet: vnet1,
		},
		&inventory.Guest{Ref: testRef(inventory.KindGuest, "pve1", "101"), Name: "web02", VMID: 101, Status: "running"},
		&inventory.GuestNic{
			Ref:   testRef(inventory.KindGuestNic, "pve1", "101/net0"),
			Guest: testRef(inventory.KindGuest, "pve1", "101"), Key: "net0",
			BridgeOrVnet: vnet1,
		},
		&inventory.Guest{Ref: testRef(inventory.KindGuest, "pve1", "102"), Name: "stopped01", VMID: 102, Status: "stopped"},
		&inventory.GuestNic{
			Ref:   testRef(inventory.KindGuestNic, "pve1", "102/net0"),
			Guest: testRef(inventory.KindGuest, "pve1", "102"), Key: "net0",
			BridgeOrVnet: vnet1,
		},
	)
}

func TestSafetyValidate_VnetDeletionGuard(t *testing.T) {
	vnet1 := testRef(inventory.KindSDNVnet, "", "zone1/vnet1")
	vnet2 := testRef(inventory.KindSDNVnet, "", "zone1/vnet2")
	deleteOp := mkOp(OpSdnVnetDelete, vnet1, &SdnVnetDeleteParams{})

	t.Run("deleting a vnet with running guests attached errors, listing them, stopped guests don't count", func(t *testing.T) {
		findings := safetyValidate([]Op{deleteOp}, vnetGuestBearingSnapshot(), SafetyOptions{})
		if len(findings) != 1 {
			t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
		}
		f := findings[0]
		if f.Severity != SeverityError || f.Code != codeGuestBearingBridge || f.Ref != vnet1.String() {
			t.Errorf("finding = %+v", f)
		}
		for _, guest := range []string{"web01", "web02"} {
			if !strings.Contains(f.Message, guest) {
				t.Errorf("message %q does not mention running guest %q", f.Message, guest)
			}
		}
		if strings.Contains(f.Message, "stopped01") {
			t.Errorf("message %q must not mention the stopped guest", f.Message)
		}
	})

	t.Run("reattaching every running guest to a surviving vnet in the same changeset clears the error", func(t *testing.T) {
		ops := []Op{
			deleteOp,
			mkOp(OpGuestNicUpdate, testRef(inventory.KindGuestNic, "pve1", "100/net0"),
				&GuestNicUpdateParams{BridgeOrVnet: strPtr(vnet2.ID)}),
			mkOp(OpGuestNicUpdate, testRef(inventory.KindGuestNic, "pve1", "101/net0"),
				&GuestNicUpdateParams{BridgeOrVnet: strPtr(vnet2.ID)}),
		}
		findings := safetyValidate(ops, vnetGuestBearingSnapshot(), SafetyOptions{})
		assertFindings(t, findings, nil)
	})

	t.Run("reattaching only one of two running guests leaves the error naming the other", func(t *testing.T) {
		ops := []Op{
			deleteOp,
			mkOp(OpGuestNicUpdate, testRef(inventory.KindGuestNic, "pve1", "100/net0"),
				&GuestNicUpdateParams{BridgeOrVnet: strPtr(vnet2.ID)}),
		}
		findings := safetyValidate(ops, vnetGuestBearingSnapshot(), SafetyOptions{})
		if len(findings) != 1 {
			t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
		}
		if strings.Contains(findings[0].Message, "web01") {
			t.Errorf("message %q should no longer mention the reattached guest web01", findings[0].Message)
		}
		if !strings.Contains(findings[0].Message, "web02") {
			t.Errorf("message %q must still mention the not-yet-reattached guest web02", findings[0].Message)
		}
	})

	t.Run("allow_dangerous_ops downgrades to a warning", func(t *testing.T) {
		findings := safetyValidate([]Op{deleteOp}, vnetGuestBearingSnapshot(), SafetyOptions{AllowDangerousOps: true})
		if len(findings) != 1 {
			t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
		}
		if findings[0].Severity != SeverityWarning {
			t.Errorf("severity = %s, want warning", findings[0].Severity)
		}
	})

	t.Run("deleting an unrelated vnet has no interlock finding", func(t *testing.T) {
		unrelated := mkOp(OpSdnVnetDelete, vnet2, &SdnVnetDeleteParams{})
		findings := safetyValidate([]Op{unrelated}, vnetGuestBearingSnapshot(), SafetyOptions{})
		assertFindings(t, findings, nil)
	})
}
