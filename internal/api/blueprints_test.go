package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

type fakeBlueprintInventory struct{ g *inventory.Graph }

func (f fakeBlueprintInventory) Snapshot() inventory.Snapshot { return f.g.Snapshot() }

func newBlueprintTestService(t *testing.T, nodes ...string) *blueprint.Service {
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

	g := inventory.NewGraph()
	var entities []inventory.Entity
	for _, n := range nodes {
		entities = append(entities, &inventory.Node{Ref: inventory.Ref{Kind: inventory.KindNode, Node: n, ID: n}, Name: n, Status: "online"})
	}
	g.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{}, entities)

	return blueprint.New(blueprint.Config{Repo: store.NewBlueprintRepo(db), Inventory: fakeBlueprintInventory{g: g}})
}

func blueprintTestAuth(caps map[string]bool) fakeAuthWithCaps {
	return fakeAuthWithCaps{
		caps: caps, csrf: true,
		fakeAuthWithUser: fakeAuthWithUser{username: "root@pam", fakeAuth: fakeAuth{authenticated: true}},
	}
}

func TestBlueprintsRoute_Unauthenticated401(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: false}, Blueprints: newBlueprintTestService(t),
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/blueprints", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestListBlueprints_IncludesStarters(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: blueprintTestAuth(map[string]bool{"netRead": true}), Blueprints: newBlueprintTestService(t, "pve1"),
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/blueprints", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var got blueprintsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.Items) < 5 {
		t.Fatalf("got %d items, want at least the 5 bundled starters", len(got.Items))
	}
}

func TestGetBlueprint_Starter(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: blueprintTestAuth(map[string]bool{"netRead": true}), Blueprints: newBlueprintTestService(t, "pve1"),
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/blueprints/"+blueprint.StarterSingleNICHomelab, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var got blueprint.Blueprint
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.ID != blueprint.StarterSingleNICHomelab || !got.ReadOnly {
		t.Fatalf("got %+v", got)
	}
}

func TestGetBlueprint_NotFound(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: blueprintTestAuth(map[string]bool{"netRead": true}), Blueprints: newBlueprintTestService(t, "pve1"),
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/blueprints/no-such-id", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body: %s", rec.Code, rec.Body.String())
	}
}

func TestSaveBlueprint_RequiresNetWrite(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: blueprintTestAuth(map[string]bool{"netRead": true}), Blueprints: newBlueprintTestService(t, "pve1"),
	})
	body := `{"name":"x","entities":[{"kind":"bridge","idTemplate":"vmbr0","fields":{}}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/blueprints", bytes.NewBufferString(body))
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (missing netWrite)", rec.Code)
	}
}

func TestSaveBlueprint_RequiresCSRF(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: blueprintTestAuth(map[string]bool{"netRead": true, "netWrite": true}), Blueprints: newBlueprintTestService(t, "pve1"),
	})
	body := `{"name":"x","entities":[{"kind":"bridge","idTemplate":"vmbr0","fields":{}}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/blueprints", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (missing CSRF)", rec.Code)
	}
}

func TestSaveAndDeleteBlueprint(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: blueprintTestAuth(map[string]bool{"netRead": true, "netWrite": true}), Blueprints: newBlueprintTestService(t, "pve1"),
	})

	body := `{"name":"my bp","entities":[{"kind":"bridge","idTemplate":"vmbr9","fields":{}}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/blueprints", bytes.NewBufferString(body))
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("save status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var saved blueprint.Blueprint
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decoding save response: %v", err)
	}
	if saved.ID == "" {
		t.Fatal("save did not assign an id")
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/blueprints/"+saved.ID, nil)
	delReq.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	delRec := httptest.NewRecorder()
	r.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body: %s", delRec.Code, delRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/blueprints/"+saved.ID, nil)
	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("get-after-delete status = %d, want 404", getRec.Code)
	}
}

func TestDeleteBlueprint_StarterRejected(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: blueprintTestAuth(map[string]bool{"netRead": true, "netWrite": true}), Blueprints: newBlueprintTestService(t, "pve1"),
	})
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/blueprints/"+blueprint.StarterSingleNICHomelab, nil)
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body: %s", rec.Code, rec.Body.String())
	}
}

func TestInstantiateBlueprint_CreatesChangeset(t *testing.T) {
	changesets := newChangesetTestService(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:       blueprintTestAuth(map[string]bool{"netRead": true, "netWrite": true}),
		Blueprints: newBlueprintTestService(t, "pve1"),
		Changesets: changesets,
	})

	body := `{"nodes":["pve1"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/blueprints/"+blueprint.StarterSingleNICHomelab+"/instantiate", bytes.NewBufferString(body))
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	ops, ok := got["ops"].([]any)
	if !ok || len(ops) != 1 {
		t.Fatalf("got ops = %v, want a single op", got["ops"])
	}
}

func TestCaptureBlueprint(t *testing.T) {
	svc := newBlueprintTestService(t, "pve1")
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: blueprintTestAuth(map[string]bool{"netRead": true, "netWrite": true}), Blueprints: svc,
	})
	// pve1 has no captured entities yet — Capture should 4xx via
	// writeBlueprintError's ErrNotFound branch.
	body := `{"node":"pve1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/blueprints/capture", bytes.NewBufferString(body))
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (nothing to capture), body: %s", rec.Code, rec.Body.String())
	}
}

func TestSuggestAddress_Route(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: blueprintTestAuth(map[string]bool{"netRead": true}), Blueprints: newBlueprintTestService(t, "pve1"),
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/blueprints/"+blueprint.StarterSingleNICHomelab+"/suggest?param=mgmtCidr", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var got suggestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Address == "" {
		t.Fatal("expected a non-empty suggested address")
	}
}
