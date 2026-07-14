package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// fakeMgmtStatusService is a minimal ProtectedService stand-in for the
// GET /topology badge-painting test — mirrors fakeDriftService/
// fakeFindingsService's pattern of isolating the painting logic from a real
// change.Service. Options.Protected is statically typed ProtectedService
// (it also backs the /protected-interfaces routes), so this implements the
// whole interface even though only MgmtStatus is exercised here; the other
// methods are unused stubs.
type fakeMgmtStatusService struct {
	err    error
	status change.MgmtStatus
}

func (f fakeMgmtStatusService) MgmtStatus(context.Context) (change.MgmtStatus, error) {
	return f.status, f.err
}

func (f fakeMgmtStatusService) GetProtected(context.Context) (change.ProtectedConfig, error) {
	return change.ProtectedConfig{}, nil
}

func (f fakeMgmtStatusService) SetProtected(context.Context, string, change.ProtectedConfig) (change.ProtectedConfig, error) {
	return change.ProtectedConfig{}, nil
}

func (f fakeMgmtStatusService) SuggestProtected(context.Context) change.ProtectedSet { return nil }

// TestTopologyRoute_PaintsMgmtBadges is T-702's map badge deliverable: GET
// /topology decorates the resolved carrier with its role badge(s) and every
// path member with "mgmt-path", additive to whatever badges the projection
// itself already produced — mirroring TestTopologyRoute_PaintsDriftBadge's
// shape for the drift badge.
func TestTopologyRoute_PaintsMgmtBadges(t *testing.T) {
	nodes := []topology.Node{
		{ID: "bridge:pve1:vmbr0", Kind: "bridge", Layer: topology.LayerL2, Status: topology.StatusOK, Badges: []string{"vlans=10-20"}},
		{ID: "physnic:pve1:eno1", Kind: "physnic", Layer: topology.LayerPhysical, Status: topology.StatusOK, Badges: []string{}},
		{ID: "physnic:pve1:eno2", Kind: "physnic", Layer: topology.LayerPhysical, Status: topology.StatusOK, Badges: []string{}},
	}
	vmbr0 := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}
	eno1 := inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"}
	status := change.MgmtStatus{
		Source: "detected",
		Nodes: map[string][]topology.MgmtPath{
			"pve1": {{
				Ref:       vmbr0,
				Roles:     []topology.MgmtRole{topology.MgmtRoleMgmt},
				Path:      []inventory.Ref{eno1},
				Redundant: false,
			}},
		},
	}

	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:      driftTestAuth(map[string]bool{"netRead": true}),
		Topology:  fakeTopologyService{nodes: nodes},
		Protected: fakeMgmtStatusService{status: status},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/topology", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/topology status = %d, body: %s", rec.Code, rec.Body.String())
	}

	var got topology.Topology
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	byID := map[string]topology.Node{}
	for _, n := range got.Nodes {
		byID[n.ID] = n
	}
	bridge := byID["bridge:pve1:vmbr0"]
	if !containsString(bridge.Badges, "mgmt") {
		t.Errorf("vmbr0 badges = %v, want mgmt", bridge.Badges)
	}
	if !containsString(bridge.Badges, "vlans=10-20") {
		t.Errorf("vmbr0 badges = %v, want its original vlans=10-20 badge preserved", bridge.Badges)
	}
	nic1 := byID["physnic:pve1:eno1"]
	if !containsString(nic1.Badges, "mgmt-path") {
		t.Errorf("eno1 badges = %v, want mgmt-path", nic1.Badges)
	}
	nic2 := byID["physnic:pve1:eno2"]
	if containsString(nic2.Badges, "mgmt-path") || containsString(nic2.Badges, "mgmt") {
		t.Errorf("eno2 badges = %v, want neither (not in vmbr0's resolved path)", nic2.Badges)
	}
}

// TestTopologyRoute_MgmtStatusErrorDegradesQuietly: a MgmtStatus computation
// error must not fail the whole /topology request — it's a display-only
// decoration (paintMgmtStatus's own doc comment).
func TestTopologyRoute_MgmtStatusErrorDegradesQuietly(t *testing.T) {
	nodes := []topology.Node{{ID: "bridge:pve1:vmbr0", Kind: "bridge", Layer: topology.LayerL2, Status: topology.StatusOK, Badges: []string{}}}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:      driftTestAuth(map[string]bool{"netRead": true}),
		Topology:  fakeTopologyService{nodes: nodes},
		Protected: fakeMgmtStatusService{err: context.DeadlineExceeded},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/topology", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/topology status = %d, want 200 even when MgmtStatus errors, body: %s", rec.Code, rec.Body.String())
	}
}

// TestProtectedRoutes_MgmtStatus_Detected covers GET
// /protected-interfaces/status against a real change.Service with no
// confirmed protected.json: source "detected", roles/path resolved from
// live inventory + corosync.conf.
func TestProtectedRoutes_MgmtStatus_Detected(t *testing.T) {
	corosync := `
nodelist {
    node {
        name: pve1
        nodeid: 1
        ring0_addr: 10.10.0.1
    }
}
`
	corosyncPath := filepath.Join(t.TempDir(), "corosync.conf")
	if err := os.WriteFile(corosyncPath, []byte(corosync), 0o644); err != nil {
		t.Fatalf("writing corosync fixture: %v", err)
	}

	g := inventory.NewGraph()
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{}, []inventory.Entity{
		&inventory.Node{Ref: inventory.Ref{Kind: inventory.KindNode, Node: "pve1", ID: "pve1"}, Name: "pve1", IP: "10.10.0.1"},
		&inventory.Bridge{
			Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}, Name: "vmbr0",
			Addresses: []string{"10.10.0.1/24"}, PortNames: []string{"eno1"},
		},
		&inventory.PhysNic{Ref: inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"}, Name: "eno1", LinkUp: true, LinkUpSet: true},
	})

	path := filepath.Join(t.TempDir(), "vnprox.db")
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc, err := change.NewService(change.Config{
		Changesets:    store.NewChangesetRepo(db),
		Audit:         store.NewAuditRepo(db),
		Inventory:     g,
		Now:           func() time.Time { return time.Unix(1_700_000_000, 0) },
		ProtectedPath: filepath.Join(t.TempDir(), "protected.json"),
		CorosyncPath:  corosyncPath,
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}

	r := newProtectedTestRouter(svc, fullCapsAuth("alice"))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/protected-interfaces/status", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}

	var got mgmtStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Source != "detected" {
		t.Errorf("source = %q, want detected", got.Source)
	}
	paths := got.Nodes["pve1"]
	if len(paths) != 1 {
		t.Fatalf("nodes[pve1] = %+v, want exactly 1 resolved ref", paths)
	}
	p := paths[0]
	if p.Ref != "bridge:pve1:vmbr0" {
		t.Errorf("ref = %q, want bridge:pve1:vmbr0", p.Ref)
	}
	if len(p.Roles) != 2 || !containsString(p.Roles, "mgmt") || !containsString(p.Roles, "corosync") {
		t.Errorf("roles = %v, want [mgmt corosync] (same address serves both)", p.Roles)
	}
	if len(p.Path) != 1 || p.Path[0] != "physnic:pve1:eno1" {
		t.Errorf("path = %v, want [physnic:pve1:eno1]", p.Path)
	}
	if p.Redundant {
		t.Errorf("redundant = true, want false (single NIC path)")
	}
}

// TestTopologyRoute_MgmtBadges_DetectedSource is AC2's end-to-end case: a
// real change.Service (no confirmed protected.json) drives both GET
// /protected-interfaces/status ("detected") and GET /topology's badge
// painting from the exact same computation — proving badges still render
// with the confirmed set absent, not just when hand-fed a canned status.
func TestTopologyRoute_MgmtBadges_DetectedSource(t *testing.T) {
	g := inventory.NewGraph()
	g.ApplyPoll(inventory.SourceHostNetlink, inventory.Scope{}, []inventory.Entity{
		&inventory.Node{Ref: inventory.Ref{Kind: inventory.KindNode, Node: "pve1", ID: "pve1"}, Name: "pve1", IP: "10.10.0.1"},
		&inventory.Bridge{
			Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}, Name: "vmbr0",
			Addresses: []string{"10.10.0.1/24"}, PortNames: []string{"eno1"},
		},
		&inventory.PhysNic{Ref: inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"}, Name: "eno1", LinkUp: true, LinkUpSet: true},
	})

	path := filepath.Join(t.TempDir(), "vnprox.db")
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc, err := change.NewService(change.Config{
		Changesets:    store.NewChangesetRepo(db),
		Audit:         store.NewAuditRepo(db),
		Inventory:     g,
		Now:           func() time.Time { return time.Unix(1_700_000_000, 0) },
		ProtectedPath: filepath.Join(t.TempDir(), "protected.json"), // never confirmed
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}

	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fullCapsAuth("alice"), Topology: topology.NewService(g, testLogger()), Protected: svc,
	})

	statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/protected-interfaces/status", nil)
	statusRec := httptest.NewRecorder()
	r.ServeHTTP(statusRec, statusReq)
	var status mgmtStatusResponse
	if err := json.Unmarshal(statusRec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decoding status response: %v", err)
	}
	if status.Source != "detected" {
		t.Fatalf("source = %q, want detected", status.Source)
	}

	topoReq := httptest.NewRequest(http.MethodGet, "/api/v1/topology", nil)
	topoRec := httptest.NewRecorder()
	r.ServeHTTP(topoRec, topoReq)
	var topo topology.Topology
	if err := json.Unmarshal(topoRec.Body.Bytes(), &topo); err != nil {
		t.Fatalf("decoding topology response: %v", err)
	}
	byID := map[string]topology.Node{}
	for _, n := range topo.Nodes {
		byID[n.ID] = n
	}
	bridge, ok := byID["bridge:pve1:vmbr0"]
	if !ok || !containsString(bridge.Badges, "mgmt") {
		t.Errorf("vmbr0 badges = %v, want mgmt (badges must still render with protected.json unconfirmed)", bridge.Badges)
	}
	eno1, ok := byID["physnic:pve1:eno1"]
	if !ok || !containsString(eno1.Badges, "mgmt-path") {
		t.Errorf("eno1 badges = %v, want mgmt-path", eno1.Badges)
	}
}

func TestProtectedRoutes_MgmtStatus_Unauthenticated401(t *testing.T) {
	svc := newProtectedTestService(t)
	auth := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: false}, username: "alice"},
		caps:             map[string]bool{capNetRead: true, capNetWrite: true},
	}
	r := newProtectedTestRouter(svc, auth)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/protected-interfaces/status", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
