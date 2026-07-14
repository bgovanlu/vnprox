package change

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// TestDetectProtectedRoles_BothRoles covers AC1's "+corosync where rings
// match" case at the classification layer: an address that is both the
// node's management IP and its corosync ring address gets both roles on
// the same ref.
func TestDetectProtectedRoles_BothRoles(t *testing.T) {
	node1 := &inventory.Node{Ref: testRef(inventory.KindNode, "pve1", "pve1"), Name: "pve1", IP: "10.10.0.1"}
	mgmtBridge := &inventory.Bridge{
		Ref: testRef(inventory.KindBridge, "pve1", "vmbr0"), Name: "vmbr0",
		Addresses: []string{"10.10.0.1/24"},
	}
	corosyncOnlyVlan := &inventory.VlanIface{
		Ref: testRef(inventory.KindVlan, "pve1", "vmbr0.20"), Name: "vmbr0.20",
		Addresses: []string{"10.10.1.1/24"},
	}
	snap := buildSnapshot(node1, mgmtBridge, corosyncOnlyVlan)

	cor := &host.CorosyncConfig{Nodes: []host.CorosyncNode{
		{Name: "pve1", RingAddrs: []string{"10.10.0.1", "10.10.1.1"}},
	}}

	roles := DetectProtectedRoles(snap, cor)
	refs := roles["pve1"]
	if len(refs) != 2 {
		t.Fatalf("got %d role refs for pve1, want 2: %+v", len(refs), refs)
	}
	byRef := map[inventory.Ref][]topology.MgmtRole{}
	for _, r := range refs {
		byRef[r.Ref] = r.Roles
	}
	mgmtRoles := byRef[mgmtBridge.Ref]
	if len(mgmtRoles) != 2 {
		t.Errorf("vmbr0 roles = %v, want both mgmt and corosync (same address serves both)", mgmtRoles)
	}
	corosyncRoles := byRef[corosyncOnlyVlan.Ref]
	if len(corosyncRoles) != 1 || corosyncRoles[0] != topology.MgmtRoleCorosync {
		t.Errorf("vmbr0.20 roles = %v, want [corosync]", corosyncRoles)
	}
}

// TestDetectProtected_DerivedFromRoles_ByteIdentical is AC5's regression
// guard at this package's own level: DetectProtected's output must be
// unaffected by being re-derived from DetectProtectedRoles. The existing
// TestDetectProtected/TestDetectProtected_NilCorosyncConfig above already
// assert this indirectly (they're unchanged and still pass); this test
// additionally pins down that a ref matching only via role-blind detection
// (regardless of which role(s) it turns out to carry) is still included
// exactly once, never duplicated.
func TestDetectProtected_DerivedFromRoles_ByteIdentical(t *testing.T) {
	node1 := &inventory.Node{Ref: testRef(inventory.KindNode, "pve1", "pve1"), Name: "pve1", IP: "10.10.0.1"}
	bothRolesBridge := &inventory.Bridge{
		Ref: testRef(inventory.KindBridge, "pve1", "vmbr0"), Name: "vmbr0",
		Addresses: []string{"10.10.0.1/24"},
	}
	snap := buildSnapshot(node1, bothRolesBridge)
	cor := &host.CorosyncConfig{Nodes: []host.CorosyncNode{
		{Name: "pve1", RingAddrs: []string{"10.10.0.1"}},
	}}

	set := DetectProtected(snap, cor)
	if len(set["pve1"]) != 1 || set["pve1"][0] != bothRolesBridge.Ref {
		t.Errorf("got %v, want exactly one entry for vmbr0 (not duplicated despite carrying both roles)", set["pve1"])
	}
}

func TestClassifyConfirmedRoles(t *testing.T) {
	node1 := &inventory.Node{Ref: testRef(inventory.KindNode, "pve1", "pve1"), Name: "pve1", IP: "10.10.0.1"}
	mgmtBridge := &inventory.Bridge{
		Ref: testRef(inventory.KindBridge, "pve1", "vmbr0"), Name: "vmbr0",
		Addresses: []string{"10.10.0.1/24"},
	}
	staleRef := testRef(inventory.KindBridge, "pve1", "vmbr9") // confirmed, but no longer exists
	snap := buildSnapshot(node1, mgmtBridge)

	confirmed := ProtectedSet{"pve1": {mgmtBridge.Ref, staleRef}}
	roles := classifyConfirmedRoles(snap, nil, confirmed)
	refs := roles["pve1"]
	if len(refs) != 2 {
		t.Fatalf("got %d entries, want 2 (both confirmed refs, even the stale one): %+v", len(refs), refs)
	}
	var mgmtRoles, staleRoles []topology.MgmtRole
	for _, r := range refs {
		switch r.Ref {
		case mgmtBridge.Ref:
			mgmtRoles = r.Roles
		case staleRef:
			staleRoles = r.Roles
		}
	}
	if len(mgmtRoles) != 1 || mgmtRoles[0] != topology.MgmtRoleMgmt {
		t.Errorf("vmbr0 roles = %v, want [mgmt]", mgmtRoles)
	}
	if len(staleRoles) != 0 {
		t.Errorf("stale ref roles = %v, want none (it no longer exists in the snapshot)", staleRoles)
	}
}

// newMgmtStatusTestService builds a real *change.Service backed by a temp
// SQLite file, wired with the given inventory graph and (optional)
// corosync.conf path, mirroring internal/api/protected_test.go's
// newProtectedTestService/TestProtectedRoutes_Suggest pattern.
func newMgmtStatusTestService(t *testing.T, g *inventory.Graph, corosyncPath string) *Service {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vnprox.db")
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc, err := NewService(Config{
		Changesets:    store.NewChangesetRepo(db),
		Audit:         store.NewAuditRepo(db),
		Inventory:     g,
		Now:           func() time.Time { return time.Unix(1_700_000_000, 0) },
		ProtectedPath: filepath.Join(t.TempDir(), "protected.json"),
		CorosyncPath:  corosyncPath,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func TestMgmtStatus_DetectedWhenUnconfirmed(t *testing.T) {
	g := inventory.NewGraph()
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{}, []inventory.Entity{
		&inventory.Node{Ref: testRef(inventory.KindNode, "pve1", "pve1"), Name: "pve1", IP: "10.10.0.1"},
		&inventory.Bridge{
			Ref: testRef(inventory.KindBridge, "pve1", "vmbr0"), Name: "vmbr0",
			Addresses: []string{"10.10.0.1/24"}, PortNames: []string{"eno1"},
		},
		&inventory.PhysNic{Ref: testRef(inventory.KindPhysNic, "pve1", "eno1"), Name: "eno1", LinkUp: true, LinkUpSet: true},
	})

	svc := newMgmtStatusTestService(t, g, "")
	status, err := svc.MgmtStatus(context.Background())
	if err != nil {
		t.Fatalf("MgmtStatus: %v", err)
	}
	if status.Source != "detected" {
		t.Errorf("Source = %q, want detected (protected.json never confirmed)", status.Source)
	}
	paths := status.Nodes["pve1"]
	if len(paths) != 1 {
		t.Fatalf("Nodes[pve1] = %+v, want exactly 1 resolved ref", paths)
	}
	p := paths[0]
	if p.Ref != testRef(inventory.KindBridge, "pve1", "vmbr0") {
		t.Errorf("Ref = %v, want vmbr0", p.Ref)
	}
	if len(p.Roles) != 1 || p.Roles[0] != topology.MgmtRoleMgmt {
		t.Errorf("Roles = %v, want [mgmt]", p.Roles)
	}
	if len(p.Path) != 1 || p.Path[0] != testRef(inventory.KindPhysNic, "pve1", "eno1") {
		t.Errorf("Path = %v, want [eno1]", p.Path)
	}
	if p.Redundant {
		t.Errorf("Redundant = true, want false (single NIC path)")
	}
}

func TestMgmtStatus_ConfirmedWhenProtectedJSONSet(t *testing.T) {
	g := inventory.NewGraph()
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{}, []inventory.Entity{
		&inventory.Node{Ref: testRef(inventory.KindNode, "pve1", "pve1"), Name: "pve1", IP: "10.10.0.1"},
		&inventory.Bridge{
			Ref: testRef(inventory.KindBridge, "pve1", "vmbr0"), Name: "vmbr0",
			Addresses: []string{"10.10.0.1/24"}, PortNames: []string{"eno1"},
		},
		&inventory.PhysNic{Ref: testRef(inventory.KindPhysNic, "pve1", "eno1"), Name: "eno1", LinkUp: true, LinkUpSet: true},
	})

	svc := newMgmtStatusTestService(t, g, "")
	_, err := svc.SetProtected(context.Background(), "root@pam", ProtectedConfig{
		Nodes: map[string][]string{"pve1": {"bridge:pve1:vmbr0"}},
	})
	if err != nil {
		t.Fatalf("SetProtected: %v", err)
	}

	status, err := svc.MgmtStatus(context.Background())
	if err != nil {
		t.Fatalf("MgmtStatus: %v", err)
	}
	if status.Source != "confirmed" {
		t.Errorf("Source = %q, want confirmed", status.Source)
	}
	if len(status.Nodes["pve1"]) != 1 {
		t.Fatalf("Nodes[pve1] = %+v, want exactly 1 resolved ref", status.Nodes["pve1"])
	}
}
