package api

import (
	"bytes"
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
)

// newProtectedTestService builds a real *change.Service (which satisfies
// ProtectedService directly) backed by a temp SQLite file and a temp
// protected.json path, mirroring newChangesetTestService's pattern in
// changesets_test.go.
func newProtectedTestService(t *testing.T) *change.Service {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vnprox.db")
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("db.Close: %v", closeErr)
		}
	})
	svc, err := change.NewService(change.Config{
		Changesets:    store.NewChangesetRepo(db),
		Audit:         store.NewAuditRepo(db),
		Now:           func() time.Time { return time.Unix(1_700_000_000, 0) },
		ProtectedPath: filepath.Join(t.TempDir(), "protected.json"),
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}
	return svc
}

func newProtectedTestRouter(svc *change.Service, auth fakeAuthWithCaps) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, Topology: fakeTopologyService{}, Protected: svc,
	})
}

func TestProtectedRoutes_NotMountedWithoutUsernameLookup(t *testing.T) {
	svc := newProtectedTestService(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{}, Protected: svc,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/protected-interfaces", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route should not be mounted)", rec.Code)
	}
}

func TestProtectedRoutes_GetEmpty(t *testing.T) {
	svc := newProtectedTestService(t)
	r := newProtectedTestRouter(svc, fullCapsAuth("alice"))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/protected-interfaces", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	var got protectedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.Nodes) != 0 {
		t.Errorf("Nodes = %v, want empty", got.Nodes)
	}
}

func TestProtectedRoutes_GetRequiresNetRead(t *testing.T) {
	svc := newProtectedTestService(t)
	auth := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"},
		caps:             map[string]bool{capNetRead: false, capNetWrite: true},
	}
	r := newProtectedTestRouter(svc, auth)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/protected-interfaces", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestProtectedRoutes_PutRequiresNetWrite(t *testing.T) {
	svc := newProtectedTestService(t)
	auth := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"},
		caps:             map[string]bool{capNetRead: true, capNetWrite: false},
	}
	r := newProtectedTestRouter(svc, auth)

	body := bytes.NewBufferString(`{"nodes":{"pve1":["bridge:pve1:vmbr0"]}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/protected-interfaces", body)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403, body: %s", rec.Code, rec.Body.String())
	}
}

func TestProtectedRoutes_PutAndGetRoundTrip(t *testing.T) {
	svc := newProtectedTestService(t)
	auth := fullCapsAuth("alice")
	r := newProtectedTestRouter(svc, auth)

	putBody := bytes.NewBufferString(`{"nodes":{"pve1":["bridge:pve1:vmbr0"]}}`)
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/protected-interfaces", putBody)
	putReq.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	putRec := httptest.NewRecorder()
	r.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200, body: %s", putRec.Code, putRec.Body.String())
	}
	var putResp protectedResponse
	if err := json.Unmarshal(putRec.Body.Bytes(), &putResp); err != nil {
		t.Fatalf("decoding PUT response: %v", err)
	}
	if putResp.UpdatedBy != "alice" {
		t.Errorf("UpdatedBy = %q, want alice", putResp.UpdatedBy)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/protected-interfaces", nil)
	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", getRec.Code)
	}
	var getResp protectedResponse
	if err := json.Unmarshal(getRec.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("decoding GET response: %v", err)
	}
	if len(getResp.Nodes["pve1"]) != 1 || getResp.Nodes["pve1"][0] != "bridge:pve1:vmbr0" {
		t.Errorf("Nodes = %v, want the just-saved config", getResp.Nodes)
	}
}

func TestProtectedRoutes_Put_InvalidRef(t *testing.T) {
	svc := newProtectedTestService(t)
	auth := fullCapsAuth("alice")
	r := newProtectedTestRouter(svc, auth)

	body := bytes.NewBufferString(`{"nodes":{"pve1":["not-a-ref"]}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/protected-interfaces", body)
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
}

func TestProtectedRoutes_Put_RequiresCSRF(t *testing.T) {
	svc := newProtectedTestService(t)
	auth := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"},
		caps:             map[string]bool{capNetRead: true, capNetWrite: true},
		csrf:             true,
	}
	r := newProtectedTestRouter(svc, auth)

	body := bytes.NewBufferString(`{"nodes":{"pve1":["bridge:pve1:vmbr0"]}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/protected-interfaces", body)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (missing CSRF header), body: %s", rec.Code, rec.Body.String())
	}
}

func TestProtectedRoutes_Unauthenticated401(t *testing.T) {
	svc := newProtectedTestService(t)
	auth := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: false}, username: "alice"},
		caps:             map[string]bool{capNetRead: true, capNetWrite: true},
	}
	r := newProtectedTestRouter(svc, auth)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/protected-interfaces", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// TestProtectedRoutes_Suggest covers GET /protected-interfaces/suggest
// (audit-phase-2 F-14): the detection-suggested set, served through the
// mounted router from a real change.Service wired with an inventory
// snapshot and a corosync.conf fixture.
func TestProtectedRoutes_Suggest(t *testing.T) {
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
		&inventory.Bridge{Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}, Name: "vmbr0", Addresses: []string{"10.10.0.1/24"}},
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
	req := httptest.NewRequest(http.MethodGet, "/api/v1/protected-interfaces/suggest", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Nodes map[string][]string `json:"nodes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	want := []string{"bridge:pve1:vmbr0"}
	if len(got.Nodes) != 1 || len(got.Nodes["pve1"]) != 1 || got.Nodes["pve1"][0] != want[0] {
		t.Errorf("nodes = %v, want {pve1: %v}", got.Nodes, want)
	}
}
