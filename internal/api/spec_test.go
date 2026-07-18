package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

type fakeSpecInventory struct{ g *inventory.Graph }

func (f fakeSpecInventory) Snapshot() inventory.Snapshot { return f.g.Snapshot() }

// specTestInventory builds a graph with one node carrying a single Linux
// bridge (vmbr0) so Export/Import have real managed entities to work with.
func specTestInventory() fakeSpecInventory {
	g := inventory.NewGraph()
	g.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{}, []inventory.Entity{
		&inventory.Node{Ref: inventory.Ref{Kind: inventory.KindNode, Node: "pve1", ID: "pve1"}, Name: "pve1", Status: "online"},
	})
	g.ApplyPoll(inventory.SourcePVENetwork, inventory.Scope{Node: "pve1"}, []inventory.Entity{
		&inventory.Bridge{
			Ref:  inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"},
			Name: "vmbr0", Virt: inventory.BridgeLinux, MTUDeclared: 1500,
			DeclaredPortNames: []string{"eno1"},
		},
	})
	return fakeSpecInventory{g: g}
}

func TestExportSpec_ReturnsVersionedYAML(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: blueprintTestAuth(map[string]bool{"netRead": true}), Spec: specTestInventory(),
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/spec", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var got specExportResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SpecVersion != 1 {
		t.Errorf("specVersion = %d, want 1", got.SpecVersion)
	}
	if !bytes.Contains([]byte(got.Content), []byte("specVersion: 1")) ||
		!bytes.Contains([]byte(got.Content), []byte("vmbr0")) {
		t.Errorf("content missing expected fields:\n%s", got.Content)
	}
}

func TestExportSpec_RequiresNetRead(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: false}, Spec: specTestInventory(),
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/spec", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// AC3 at the API layer: importing a spec that adds a bridge and drops nothing
// creates a DRAFT changeset (never applying/committed) and returns notInSpec.
func TestImportSpec_CreatesDraftAndReportsNotInSpec(t *testing.T) {
	changesets := newChangesetTestService(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:       blueprintTestAuth(map[string]bool{"netRead": true, "netWrite": true}),
		Spec:       specTestInventory(),
		Changesets: changesets,
	})

	// A spec naming a *different* node/bridge than live: the new bridge
	// becomes a create op; live's vmbr0 (absent from this spec) becomes
	// notInSpec, never a delete.
	doc := "specVersion: 1\n" +
		"nodes:\n" +
		"  - name: pve1\n" +
		"    bridges:\n" +
		"      - name: vmbr7\n" +
		"        vlanAware: true\n" +
		"        mtu: 1500\n"
	body, _ := json.Marshal(specImportRequest{Content: doc})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/spec/import", bytes.NewReader(body))
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var got specImportResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "draft" && got.Status != "validated" {
		t.Errorf("changeset status = %q, want draft or validated (never applying/committed)", got.Status)
	}
	if len(got.Ops) != 1 || got.Ops[0].Type != "bridge.create" {
		t.Fatalf("ops = %v, want a single bridge.create", got.Ops)
	}
	wantNotInSpec := "bridge:pve1:vmbr0"
	if len(got.NotInSpec) != 1 || got.NotInSpec[0] != wantNotInSpec {
		t.Errorf("notInSpec = %v, want [%s]", got.NotInSpec, wantNotInSpec)
	}
}

func TestImportSpec_RequiresNetWriteAndCSRF(t *testing.T) {
	changesets := newChangesetTestService(t)
	// netRead only: the write route must 403.
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:       blueprintTestAuth(map[string]bool{"netRead": true}),
		Spec:       specTestInventory(),
		Changesets: changesets,
	})
	body, _ := json.Marshal(specImportRequest{Content: "specVersion: 1\n"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/spec/import", bytes.NewReader(body))
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (missing netWrite)", rec.Code)
	}

	// netWrite but no CSRF header: still 403.
	r2 := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:       blueprintTestAuth(map[string]bool{"netRead": true, "netWrite": true}),
		Spec:       specTestInventory(),
		Changesets: changesets,
	})
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/spec/import", bytes.NewReader(body))
	rec2 := httptest.NewRecorder()
	r2.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (missing CSRF)", rec2.Code)
	}
}

func TestImportSpec_RejectsUnknownVersion(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:       blueprintTestAuth(map[string]bool{"netRead": true, "netWrite": true}),
		Spec:       specTestInventory(),
		Changesets: newChangesetTestService(t),
	})
	body, _ := json.Marshal(specImportRequest{Content: "specVersion: 2\n"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/spec/import", bytes.NewReader(body))
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (unknown specVersion)", rec.Code)
	}
}
