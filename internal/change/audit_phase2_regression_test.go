// SPDX-License-Identifier: Apache-2.0

package change

// Regression tests for the phase-2 audit findings F-01…F-05
// (planning/reports/audit-phase-2.md). Each test reproduces the audit's
// re-execution probe through the real pipeline entry points
// (ValidateWithSafety / referentialValidate) and fails against the
// pre-remediation code.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

// --- F-01: guest-reattach interlock must be net-effect-based ---------------

// f01Snapshot: one running guest attached to vmbr2; vmbr3 exists as a
// plausible reattachment target.
func f01Snapshot() inventory.Snapshot {
	vmbr2 := testRef(inventory.KindBridge, "pve1", "vmbr2")
	return buildSnapshot(
		&inventory.Node{Ref: testRef(inventory.KindNode, "pve1", "pve1"), Name: "pve1"},
		&inventory.PhysNic{Ref: testRef(inventory.KindPhysNic, "pve1", "eno1"), Name: "eno1"},
		&inventory.Bridge{Ref: vmbr2, Name: "vmbr2"},
		&inventory.Bridge{Ref: testRef(inventory.KindBridge, "pve1", "vmbr3"), Name: "vmbr3"},
		&inventory.Guest{Ref: testRef(inventory.KindGuest, "pve1", "100"), Name: "web01", VMID: 100, Status: "running"},
		&inventory.GuestNic{
			Ref:   testRef(inventory.KindGuestNic, "pve1", "100/net0"),
			Guest: testRef(inventory.KindGuest, "pve1", "100"), Key: "net0",
			BridgeOrVnet: vmbr2,
		},
	)
}

// Audit probe A: "reattaching" the NIC to the very bridge being deleted must
// not clear the guest-bearing-bridge error.
func TestSafetyValidate_ReattachToDoomedBridge_StillErrors(t *testing.T) {
	vmbr2 := testRef(inventory.KindBridge, "pve1", "vmbr2")
	nic := testRef(inventory.KindGuestNic, "pve1", "100/net0")

	ops := []Op{
		mkOp(OpGuestNicUpdate, nic, &GuestNicUpdateParams{BridgeOrVnet: strPtr("vmbr2")}),
		mkOp(OpBridgeDelete, vmbr2, &BridgeDeleteParams{}),
	}
	findings := ValidateWithSafety(ops, f01Snapshot(), SafetyOptions{})
	assertFindings(t, findings, []wantFinding{
		{SeverityError, codeGuestBearingBridge, vmbr2.String()},
	})
}

// Audit probe B: reattach to vmbr3, then delete vmbr2 *and* vmbr3 in the
// same changeset — the net effect still strands the running guest, so the
// error must survive (attributed to the delete that dooms the final target).
func TestSafetyValidate_ReattachTargetAlsoDeleted_StillErrors(t *testing.T) {
	vmbr2 := testRef(inventory.KindBridge, "pve1", "vmbr2")
	vmbr3 := testRef(inventory.KindBridge, "pve1", "vmbr3")
	nic := testRef(inventory.KindGuestNic, "pve1", "100/net0")

	ops := []Op{
		mkOp(OpGuestNicUpdate, nic, &GuestNicUpdateParams{BridgeOrVnet: strPtr("vmbr3")}),
		mkOp(OpBridgeDelete, vmbr2, &BridgeDeleteParams{}),
		mkOp(OpBridgeDelete, vmbr3, &BridgeDeleteParams{}),
	}
	findings := ValidateWithSafety(ops, f01Snapshot(), SafetyOptions{})
	assertFindings(t, findings, []wantFinding{
		{SeverityError, codeGuestBearingBridge, vmbr3.String()},
	})
}

// Control: a genuine reattachment to a surviving bridge validates clean
// through the full pipeline (must keep passing after the net-effect fix).
func TestSafetyValidate_ReattachToSurvivingBridge_Clean(t *testing.T) {
	vmbr2 := testRef(inventory.KindBridge, "pve1", "vmbr2")
	nic := testRef(inventory.KindGuestNic, "pve1", "100/net0")

	ops := []Op{
		mkOp(OpGuestNicUpdate, nic, &GuestNicUpdateParams{BridgeOrVnet: strPtr("vmbr3")}),
		mkOp(OpBridgeDelete, vmbr2, &BridgeDeleteParams{}),
	}
	findings := ValidateWithSafety(ops, f01Snapshot(), SafetyOptions{})
	assertFindings(t, findings, nil)
}

// Control: reattaching to a bridge created earlier in the same changeset
// (and surviving) validates clean.
func TestSafetyValidate_ReattachToNewBridge_Clean(t *testing.T) {
	vmbr2 := testRef(inventory.KindBridge, "pve1", "vmbr2")
	vmbr9 := testRef(inventory.KindBridge, "pve1", "vmbr9")
	nic := testRef(inventory.KindGuestNic, "pve1", "100/net0")

	ops := []Op{
		mkOp(OpBridgeCreate, vmbr9, &BridgeCreateParams{Comments: "new home", Ports: []string{"eno1"}}),
		mkOp(OpGuestNicUpdate, nic, &GuestNicUpdateParams{BridgeOrVnet: strPtr("vmbr9")}),
		mkOp(OpBridgeDelete, vmbr2, &BridgeDeleteParams{}),
	}
	findings := ValidateWithSafety(ops, f01Snapshot(), SafetyOptions{})
	assertFindings(t, findings, nil)
}

// --- F-02: protected IP parked on a path-less bridge -----------------------

// Audit probe C: delete vmbr0 (protected, mgmt IP); create vmbr9 carrying
// the same IP but with no ports. The address "survives", but on a bridge
// with no physical path — must be a hard safety error, not clean.
func TestSafetyValidate_MgmtIPOnPortlessNewBridge_Errors(t *testing.T) {
	vmbr0 := testRef(inventory.KindBridge, "pve1", "vmbr0")
	vmbr9 := testRef(inventory.KindBridge, "pve1", "vmbr9")

	ops := []Op{
		mkOp(OpBridgeDelete, vmbr0, &BridgeDeleteParams{}),
		mkOp(OpBridgeCreate, vmbr9, &BridgeCreateParams{
			Comments:  "usurper",
			Addresses: []string{"10.10.0.1/24"},
		}),
	}
	findings := ValidateWithSafety(ops, baseMgmtSnapshot(), SafetyOptions{Protected: mgmtProtected()})
	assertFindings(t, findings, []wantFinding{
		{SeverityError, codeProtectedInterface, vmbr0.String()},
	})
}

// Control: the same migration with the physical port moved along validates
// clean (this is the AC2 chain, delete-first order).
func TestSafetyValidate_MgmtIPOnPortedNewBridge_Clean(t *testing.T) {
	vmbr0 := testRef(inventory.KindBridge, "pve1", "vmbr0")
	vmbr9 := testRef(inventory.KindBridge, "pve1", "vmbr9")

	ops := []Op{
		mkOp(OpBridgeDelete, vmbr0, &BridgeDeleteParams{}),
		mkOp(OpBridgeCreate, vmbr9, &BridgeCreateParams{
			Comments:  "new mgmt bridge",
			Ports:     []string{"eno1"},
			Addresses: []string{"10.10.0.1/24"},
		}),
	}
	findings := ValidateWithSafety(ops, baseMgmtSnapshot(), SafetyOptions{Protected: mgmtProtected()})
	assertFindings(t, findings, nil)
}

// --- F-03: AC2's mgmt-IP migration must validate clean through the FULL ---
// pipeline in both op orders.

func TestValidateWithSafety_MgmtIPMigration_BothOrders_Clean(t *testing.T) {
	vmbr0 := testRef(inventory.KindBridge, "pve1", "vmbr0")
	vmbr2 := testRef(inventory.KindBridge, "pve1", "vmbr2")

	createOp := mkOp(OpBridgeCreate, vmbr2, &BridgeCreateParams{
		Comments:  "replacement mgmt bridge",
		Ports:     []string{"eno1"},
		Addresses: []string{"10.10.0.1/24"},
	})
	deleteOp := mkOp(OpBridgeDelete, vmbr0, &BridgeDeleteParams{})

	orders := map[string][]Op{
		"create-first (natural order)": {createOp, deleteOp},
		"delete-first":                 {deleteOp, createOp},
	}
	for name, ops := range orders {
		t.Run(name, func(t *testing.T) {
			findings := ValidateWithSafety(ops, baseMgmtSnapshot(), SafetyOptions{Protected: mgmtProtected()})
			assertFindings(t, findings, nil)
		})
	}
}

// The net-effect suppression must not weaken genuine overlap/enslavement
// errors: without the delete of vmbr0, the same create is still rejected.
func TestValidateWithSafety_OverlapWithoutDelete_StillErrors(t *testing.T) {
	vmbr2 := testRef(inventory.KindBridge, "pve1", "vmbr2")
	ops := []Op{mkOp(OpBridgeCreate, vmbr2, &BridgeCreateParams{
		Comments:  "usurper",
		Ports:     []string{"eno1"},
		Addresses: []string{"10.10.0.1/24"},
	})}
	findings := ValidateWithSafety(ops, baseMgmtSnapshot(), SafetyOptions{Protected: mgmtProtected()})
	assertFindings(t, findings, []wantFinding{
		{SeverityError, codeAddressOverlap, vmbr2.String()},
		{SeverityError, codeDuplicateEnslavement, vmbr2.String()},
	})
}

// Delete-then-recreate of the doomed owner later in the changeset restores
// the conflict: the suppression only applies while the owner stays deleted.
func TestValidateWithSafety_DoomedOwnerRecreated_ConflictRestored(t *testing.T) {
	vmbr0 := testRef(inventory.KindBridge, "pve1", "vmbr0")
	vmbr2 := testRef(inventory.KindBridge, "pve1", "vmbr2")

	ops := []Op{
		mkOp(OpBridgeCreate, vmbr2, &BridgeCreateParams{
			Comments:  "new mgmt bridge",
			Ports:     []string{"eno1"},
			Addresses: []string{"10.10.0.1/24"},
		}),
		mkOp(OpBridgeDelete, vmbr0, &BridgeDeleteParams{}),
		mkOp(OpBridgeCreate, vmbr0, &BridgeCreateParams{
			Comments:  "vmbr0 rises again",
			Ports:     []string{"eno1"},
			Addresses: []string{"10.10.0.1/24"},
		}),
	}
	findings := Validate(ops, baseMgmtSnapshot())
	assertFindings(t, findings, []wantFinding{
		{SeverityError, codeAddressOverlap, vmbr0.String()},
		{SeverityError, codeDuplicateEnslavement, vmbr0.String()},
	})
}

// --- F-04: duplicate enslavement vs. pre-existing (snapshot) bonds ---------

// Audit probe E: snapshot bond0 owns eno1; bond.create bond1 {slaves:[eno1]}
// must be rejected with referential.duplicate_enslavement.
func TestValidate_DuplicateEnslavement_SnapshotBondSlave(t *testing.T) {
	snap := buildSnapshot(
		&inventory.Node{Ref: testRef(inventory.KindNode, "pve1", "pve1"), Name: "pve1"},
		&inventory.PhysNic{Ref: testRef(inventory.KindPhysNic, "pve1", "eno1"), Name: "eno1"},
		&inventory.Bond{
			Ref: testRef(inventory.KindBond, "pve1", "bond0"), Name: "bond0",
			Mode: "active-backup", Slaves: []string{"eno1"},
		},
	)
	bond1 := testRef(inventory.KindBond, "pve1", "bond1")
	ops := []Op{mkOp(OpBondCreate, bond1, &BondCreateParams{Mode: "active-backup", Slaves: []string{"eno1"}})}
	findings := Validate(ops, snap)
	// The pipeline short-circuits on the referential error, so no advisory
	// (single-slave bond) finding accompanies it.
	assertFindings(t, findings, []wantFinding{
		{SeverityError, codeDuplicateEnslavement, bond1.String()},
	})
}

// Same flavor from the bridge side: a bridge.create must not steal a NIC a
// snapshot bond already enslaves.
func TestValidate_DuplicateEnslavement_SnapshotBondPort(t *testing.T) {
	snap := buildSnapshot(
		&inventory.Node{Ref: testRef(inventory.KindNode, "pve1", "pve1"), Name: "pve1"},
		&inventory.PhysNic{Ref: testRef(inventory.KindPhysNic, "pve1", "eno1"), Name: "eno1"},
		&inventory.PhysNic{Ref: testRef(inventory.KindPhysNic, "pve1", "eno2"), Name: "eno2"},
		&inventory.Bond{
			Ref: testRef(inventory.KindBond, "pve1", "bond0"), Name: "bond0",
			Mode: "active-backup", Slaves: []string{"eno1", "eno2"},
		},
	)
	vmbr9 := testRef(inventory.KindBridge, "pve1", "vmbr9")
	ops := []Op{mkOp(OpBridgeCreate, vmbr9, &BridgeCreateParams{Comments: "x", Ports: []string{"eno2"}})}
	findings := Validate(ops, snap)
	assertFindings(t, findings, []wantFinding{
		{SeverityError, codeDuplicateEnslavement, vmbr9.String()},
	})
}

// --- F-05: delete-then-recreate must not trip stale projection indexes -----

// Audit probe F: [vlan.delete vmbr0.20, vlan.create vmbr0.20 (vid 20)] is a
// legitimate recreate draft and must validate clean (no vid_overlap).
func TestValidate_VlanDeleteThenRecreate_Clean(t *testing.T) {
	snap := buildSnapshot(
		&inventory.Node{Ref: testRef(inventory.KindNode, "pve1", "pve1"), Name: "pve1"},
		&inventory.Bridge{Ref: testRef(inventory.KindBridge, "pve1", "vmbr0"), Name: "vmbr0"},
		&inventory.VlanIface{Ref: testRef(inventory.KindVlan, "pve1", "vmbr0.20"), Name: "vmbr0.20", ParentName: "vmbr0", Vid: 20},
	)
	vlan := testRef(inventory.KindVlan, "pve1", "vmbr0.20")
	ops := []Op{
		mkOp(OpVlanDelete, vlan, &VlanDeleteParams{}),
		mkOp(OpVlanCreate, vlan, &VlanCreateParams{Parent: "vmbr0", Vid: 20}),
	}
	findings := Validate(ops, snap)
	assertFindings(t, findings, nil)
}

// Subnet flavor: delete 10.0.0.0/24 then create the overlapping 10.0.0.0/25
// under the same vnet — the deleted sibling must not cause a false
// address_overlap.
func TestValidate_SubnetDeleteThenRecreate_Clean(t *testing.T) {
	snap := buildSnapshot(
		&inventory.SdnZone{Ref: testRef(inventory.KindSDNZone, "", "zone1"), ID: "zone1", Type: "vxlan"},
		&inventory.SdnVnet{Ref: testRef(inventory.KindSDNVnet, "", "zone1/vnet1"), ID: "zone1/vnet1", Zone: "zone1"},
		&inventory.SdnSubnet{Ref: testRef(inventory.KindSDNSubnet, "", "10.0.0.0/24"), ID: "10.0.0.0/24", Vnet: "zone1/vnet1"},
	)
	ops := []Op{
		mkOp(OpSdnSubnetDelete, testRef(inventory.KindSDNSubnet, "", "10.0.0.0/24"), &SdnSubnetDeleteParams{}),
		mkOp(OpSdnSubnetCreate, testRef(inventory.KindSDNSubnet, "", "10.0.0.0/25"),
			&SdnSubnetCreateParams{Vnet: "zone1/vnet1", CIDR: "10.0.0.0/25"}),
	}
	findings := Validate(ops, snap)
	assertFindings(t, findings, nil)
}

// Control: without the delete, the overlapping sibling still errors.
func TestValidate_SubnetOverlapWithoutDelete_StillErrors(t *testing.T) {
	snap := buildSnapshot(
		&inventory.SdnZone{Ref: testRef(inventory.KindSDNZone, "", "zone1"), ID: "zone1", Type: "vxlan"},
		&inventory.SdnVnet{Ref: testRef(inventory.KindSDNVnet, "", "zone1/vnet1"), ID: "zone1/vnet1", Zone: "zone1"},
		&inventory.SdnSubnet{Ref: testRef(inventory.KindSDNSubnet, "", "10.0.0.0/24"), ID: "10.0.0.0/24", Vnet: "zone1/vnet1"},
	)
	target := testRef(inventory.KindSDNSubnet, "", "10.0.0.0/25")
	ops := []Op{mkOp(OpSdnSubnetCreate, target, &SdnSubnetCreateParams{Vnet: "zone1/vnet1", CIDR: "10.0.0.0/25"})}
	findings := Validate(ops, snap)
	assertFindings(t, findings, []wantFinding{
		{SeverityError, codeAddressOverlap, target.String()},
	})
}

// --- F-13: config seam for the protected.json path -------------------------

// internal/config duplicates DefaultProtectedPath (it cannot import this
// package without inverting the dependency); pin the two strings equal so
// they can't drift.
func TestDefaultProtectedPath_MatchesConfigPackage(t *testing.T) {
	if config.DefaultProtectedPath != DefaultProtectedPath {
		t.Errorf("config.DefaultProtectedPath = %q, change.DefaultProtectedPath = %q — keep them in sync",
			config.DefaultProtectedPath, DefaultProtectedPath)
	}
}

// --- F-14: protected-interface detection wired into the daemon -------------

// TestService_SuggestProtected_ComposesCorosyncAndInventory is the
// integration test the audit asked for: a realistic corosync.conf fixture
// parsed by host.ReadCorosyncConf and composed with a live-shaped inventory
// snapshot through Service.SuggestProtected (the method behind
// GET /protected-interfaces/suggest).
func TestService_SuggestProtected_ComposesCorosyncAndInventory(t *testing.T) {
	corosync := `
totem {
    version: 2
    cluster_name: testcluster
    transport: knet
}

nodelist {
    node {
        name: pve1
        nodeid: 1
        quorum_votes: 1
        ring0_addr: 10.10.0.1
        ring1_addr: 10.10.1.1
    }
    node {
        name: pve2
        nodeid: 2
        quorum_votes: 1
        ring0_addr: 10.10.0.2
    }
}

quorum {
    provider: corosync_votequorum
}
`
	corosyncPath := filepath.Join(t.TempDir(), "corosync.conf")
	if err := os.WriteFile(corosyncPath, []byte(corosync), 0o644); err != nil {
		t.Fatalf("writing corosync fixture: %v", err)
	}

	snap := buildSnapshot(
		&inventory.Node{Ref: testRef(inventory.KindNode, "pve1", "pve1"), Name: "pve1", IP: "10.10.0.1"},
		&inventory.Node{Ref: testRef(inventory.KindNode, "pve2", "pve2"), Name: "pve2", IP: "10.10.0.2"},
		// pve1: vmbr0 carries the mgmt IP / ring0, vmbr0.20 carries ring1.
		&inventory.Bridge{Ref: testRef(inventory.KindBridge, "pve1", "vmbr0"), Name: "vmbr0", Addresses: []string{"10.10.0.1/24"}},
		&inventory.VlanIface{Ref: testRef(inventory.KindVlan, "pve1", "vmbr0.20"), Name: "vmbr0.20", ParentName: "vmbr0", Vid: 20, Addresses: []string{"10.10.1.1/24"}},
		// pve1: an unrelated bridge that must NOT be suggested.
		&inventory.Bridge{Ref: testRef(inventory.KindBridge, "pve1", "vmbr7"), Name: "vmbr7", Addresses: []string{"192.168.7.1/24"}},
		// pve2: vmbr0 carries its mgmt IP / ring0.
		&inventory.Bridge{Ref: testRef(inventory.KindBridge, "pve2", "vmbr0"), Name: "vmbr0", Addresses: []string{"10.10.0.2/24"}},
	)

	db := openTestDB(t)
	svc, err := NewService(Config{
		Changesets:   store.NewChangesetRepo(db),
		Audit:        store.NewAuditRepo(db),
		Inventory:    fakeInventorySource{snap: snap},
		CorosyncPath: corosyncPath,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	got := svc.SuggestProtected(context.Background())
	want := ProtectedSet{
		"pve1": {testRef(inventory.KindBridge, "pve1", "vmbr0"), testRef(inventory.KindVlan, "pve1", "vmbr0.20")},
		"pve2": {testRef(inventory.KindBridge, "pve2", "vmbr0")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SuggestProtected = %v, want %v", got, want)
	}
}

// A missing corosync.conf must degrade to management-IP-only detection,
// never fail (a not-yet-clustered node has no corosync.conf at all).
func TestService_SuggestProtected_MissingCorosyncFallsBack(t *testing.T) {
	snap := buildSnapshot(
		&inventory.Node{Ref: testRef(inventory.KindNode, "pve1", "pve1"), Name: "pve1", IP: "10.10.0.1"},
		&inventory.Bridge{Ref: testRef(inventory.KindBridge, "pve1", "vmbr0"), Name: "vmbr0", Addresses: []string{"10.10.0.1/24"}},
	)
	db := openTestDB(t)
	svc, err := NewService(Config{
		Changesets:   store.NewChangesetRepo(db),
		Audit:        store.NewAuditRepo(db),
		Inventory:    fakeInventorySource{snap: snap},
		CorosyncPath: filepath.Join(t.TempDir(), "nope", "corosync.conf"),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	got := svc.SuggestProtected(context.Background())
	want := ProtectedSet{"pve1": {testRef(inventory.KindBridge, "pve1", "vmbr0")}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SuggestProtected = %v, want %v", got, want)
	}
}

// --- F-15: SetProtected must reject node-key/ref mismatches ----------------

func TestService_SetProtected_RejectsNodeKeyRefMismatch(t *testing.T) {
	svc, _ := newSafetyTestService(t, buildSnapshot(), filepath.Join(t.TempDir(), "protected.json"), false)

	_, err := svc.SetProtected(context.Background(), "alice", ProtectedConfig{
		Nodes: map[string][]string{"pve1": {"bridge:pve2:vmbr0"}},
	})
	var invalidRef *ErrInvalidProtectedRef
	if !errors.As(err, &invalidRef) {
		t.Fatalf("SetProtected error = %v, want *ErrInvalidProtectedRef (ref filed under the wrong node key)", err)
	}
	if len(invalidRef.Refs) != 1 || invalidRef.Refs[0] != "bridge:pve2:vmbr0" {
		t.Errorf("invalid refs = %v, want [bridge:pve2:vmbr0]", invalidRef.Refs)
	}
}

// --- F-16(b): auto-validation on draft mutation, positive case -------------

// The pre-existing auto-validation tests only asserted hasError == false,
// which would also pass if Create/UpdateDraft never validated at all. This
// asserts the positive half: a known-bad op yields non-empty error findings
// immediately on Create, UpdateDraft recomputes them for the new ops, and
// replacing the bad op clears them.
func TestService_AutoValidation_ComputesFindingsOnMutation(t *testing.T) {
	svc := newTestService(t, nil)
	ctx := context.Background()

	badSchema := mkOp(OpBondCreate, testRef(inventory.KindBond, "pve1", "bond0"),
		&BondCreateParams{Slaves: []string{"eno1"}}) // missing required mode
	badReferential := mkOp(OpBridgeUpdate, testRef(inventory.KindBridge, "pve1", "vmbr9"),
		&BridgeUpdateParams{Comments: strPtr("x")}) // vmbr9 doesn't exist

	c, err := svc.Create(ctx, "alice", "bad draft", []Op{badSchema})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	assertFindings(t, c.Findings, []wantFinding{
		{SeverityError, codeRequiredFieldMissing, "bond:pve1:bond0"},
	})

	c, err = svc.UpdateDraft(ctx, c.ID, "alice", nil, []Op{badReferential})
	if err != nil {
		t.Fatalf("UpdateDraft: %v", err)
	}
	assertFindings(t, c.Findings, []wantFinding{
		{SeverityError, codeTargetNotFound, "bridge:pve1:vmbr9"},
	})

	c, err = svc.UpdateDraft(ctx, c.ID, "alice", nil, []Op{
		mkOp(OpBridgeCreate, testRef(inventory.KindBridge, "pve1", "vmbr9"),
			&BridgeCreateParams{Comments: "described"}),
	})
	if err != nil {
		t.Fatalf("UpdateDraft: %v", err)
	}
	assertFindings(t, c.Findings, nil)
}

// --- F-12: apply-time allow_dangerous_ops use must be audited ---------------

// stubNodeAgent is a minimal in-memory NodeAgent for driving Apply through
// beginApply's revalidation without a real host or pvemock fixture.
type stubNodeAgent struct {
	files  map[string]string
	staged map[string]string
}

func (a *stubNodeAgent) ReadInterfaces(_ context.Context, node string) (string, error) {
	return a.files[node], nil
}
func (a *stubNodeAgent) StageInterfaces(_ context.Context, node, content string) error {
	a.staged[node] = content
	return nil
}
func (a *stubNodeAgent) ReloadInterfaces(_ context.Context, node string) error {
	a.files[node] = a.staged[node]
	delete(a.staged, node)
	return nil
}
func (a *stubNodeAgent) DiscardStaged(_ context.Context, node string) error {
	delete(a.staged, node)
	return nil
}

type stubStopper struct{}

func (stubStopper) Stop() bool { return true }

// TestApply_AllowDangerousOps_AuditsOverrideAtApplyTime: a changeset that is
// clean at create time but trips a (downgraded) safety interlock at
// apply-time revalidation must leave an apply-time changeset.safety_override
// audit entry (T-203's card: "its use audited"; audit-phase-2 F-12 — only
// create/validate-time entries existed).
func TestApply_AllowDangerousOps_AuditsOverrideAtApplyTime(t *testing.T) {
	protectedPath := filepath.Join(t.TempDir(), "protected.json")
	vmbr0 := testRef(inventory.KindBridge, "pve1", "vmbr0")
	snap := buildSnapshot(
		&inventory.Node{Ref: testRef(inventory.KindNode, "pve1", "pve1"), Name: "pve1", IP: "10.10.0.1"},
		&inventory.Bridge{Ref: vmbr0, Name: "vmbr0", Addresses: []string{"10.10.0.1/24"}},
	)

	agent := &stubNodeAgent{
		files:  map[string]string{"pve1": "auto vmbr0\niface vmbr0 inet static\n\taddress 10.10.0.1/24\n\tbridge-ports none\n\tbridge-stp off\n\tbridge-fd 0\n"},
		staged: map[string]string{},
	}
	db := openTestDB(t)
	audit := store.NewAuditRepo(db)
	svc, err := NewService(Config{
		Changesets:        store.NewChangesetRepo(db),
		Audit:             audit,
		Inventory:         fakeInventorySource{snap: snap},
		Nodes:             agent,
		Snapshots:         store.NewSnapshotRepo(db),
		Blobs:             store.NewBlobRepo(db),
		ProtectedPath:     protectedPath,
		AllowDangerousOps: true,
		TimerFunc:         func(time.Duration, func()) Stopper { return stubStopper{} },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	ctx := context.Background()

	// Created while nothing is protected: clean, no override entry yet.
	c, err := svc.Create(ctx, "alice@pam", "delete mgmt bridge", []Op{
		mkOp(OpBridgeDelete, vmbr0, &BridgeDeleteParams{}),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// The protected set lands between create and apply — only the apply-time
	// revalidation can trip (and therefore must audit) the override.
	if saveErr := SaveProtectedConfig(protectedPath, ProtectedConfig{Nodes: map[string][]string{"pve1": {vmbr0.String()}}}); saveErr != nil {
		t.Fatalf("SaveProtectedConfig: %v", saveErr)
	}

	if _, applyErr := svc.Apply(ctx, c.ID, "alice@pam", nil, 0); applyErr != nil {
		t.Fatalf("Apply: %v", applyErr)
	}

	entries, err := audit.List(ctx, c.ID, 0)
	if err != nil {
		t.Fatalf("audit List: %v", err)
	}
	var overrides int
	for _, e := range entries {
		if e.Action == "changeset.safety_override" {
			overrides++
			if e.Username != "alice@pam" {
				t.Errorf("override entry = %+v, want username=alice@pam", e)
			}
		}
	}
	if overrides != 1 {
		t.Errorf("changeset.safety_override entries = %d, want exactly 1 (the apply-time one); all entries: %+v", overrides, entries)
	}
}
