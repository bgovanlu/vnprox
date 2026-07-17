package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/store"
)

// fakeAnnotationStore is an in-memory AnnotationStore test double.
type fakeAnnotationStore struct {
	items map[string]store.Annotation
}

func newFakeAnnotationStore() *fakeAnnotationStore {
	return &fakeAnnotationStore{items: map[string]store.Annotation{}}
}

func (f *fakeAnnotationStore) List(context.Context) ([]store.Annotation, error) {
	var out []store.Annotation
	for _, a := range f.items {
		out = append(out, a)
	}
	return out, nil
}

func (f *fakeAnnotationStore) Insert(_ context.Context, a store.Annotation) error {
	f.items[a.ID] = a
	return nil
}

func (f *fakeAnnotationStore) Delete(_ context.Context, id string) error {
	delete(f.items, id)
	return nil
}

// TestAnnotationsRoutes_NotMountedWithoutUsernameLookup mirrors
// TestLayoutsRoutes_NotMountedWithoutUsernameLookup: no safe way to stamp
// createdBy without username resolution, so the routes aren't mounted.
func TestAnnotationsRoutes_NotMountedWithoutUsernameLookup(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{}, Annotations: newFakeAnnotationStore(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/annotations", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route should not be mounted), body: %s", rec.Code, rec.Body.String())
	}
}

// TestAnnotationsRoutes_Unauthenticated401 proves the annotations routes
// are gated by the same session middleware as layouts/topology.
func TestAnnotationsRoutes_Unauthenticated401(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:        fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: false}, username: "alice"},
		Topology:    fakeTopologyService{},
		Annotations: newFakeAnnotationStore(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/annotations", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401, body: %s", rec.Code, rec.Body.String())
	}
}

// TestAnnotationsRoutes_CreateListDelete_RoundTrips is the end-to-end
// pin/list/unpin round trip (T-907 AC3): pinning a note against an entity
// ref persists it, GET /annotations re-lists it (stamped with the creating
// user), and DELETE removes it.
func TestAnnotationsRoutes_CreateListDelete_RoundTrips(t *testing.T) {
	annotations := newFakeAnnotationStore()
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:        fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice@pve"},
		Topology:    fakeTopologyService{},
		Annotations: annotations,
	})

	createBody := bytes.NewBufferString(`{"ref":"bridge:pve1:vmbr0","content":"double-check VLAN tags before Friday"}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/annotations", createBody)
	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201, body: %s", createRec.Code, createRec.Body.String())
	}
	var created annotationResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decoding POST body: %v", err)
	}
	if created.Ref != "bridge:pve1:vmbr0" || created.Content != "double-check VLAN tags before Friday" {
		t.Errorf("created = %+v, fields don't match request", created)
	}
	if created.CreatedBy != "alice@pve" {
		t.Errorf("createdBy = %q, want alice@pve (server-stamped, not client-supplied)", created.CreatedBy)
	}
	if created.ID == "" {
		t.Error("expected a non-empty server-assigned id")
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/annotations", nil)
	listRec := httptest.NewRecorder()
	r.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200, body: %s", listRec.Code, listRec.Body.String())
	}
	var listed annotationsListResponse
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decoding GET body: %v", err)
	}
	if len(listed.Items) != 1 || listed.Items[0].ID != created.ID {
		t.Fatalf("Items = %+v, want exactly the just-created annotation", listed.Items)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/annotations/"+created.ID, nil)
	delRec := httptest.NewRecorder()
	r.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204, body: %s", delRec.Code, delRec.Body.String())
	}

	listReq2 := httptest.NewRequest(http.MethodGet, "/api/v1/annotations", nil)
	listRec2 := httptest.NewRecorder()
	r.ServeHTTP(listRec2, listReq2)
	var listedAfterDelete annotationsListResponse
	if err := json.NewDecoder(listRec2.Body).Decode(&listedAfterDelete); err != nil {
		t.Fatalf("decoding second GET body: %v", err)
	}
	if len(listedAfterDelete.Items) != 0 {
		t.Errorf("Items after delete = %+v, want empty", listedAfterDelete.Items)
	}
}

// TestAnnotationsRoutes_CreateMissingFields_400 proves ref/content are
// required — an annotation pinned to nothing, or with no text, is rejected
// rather than silently stored.
func TestAnnotationsRoutes_CreateMissingFields_400(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:        fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"},
		Topology:    fakeTopologyService{},
		Annotations: newFakeAnnotationStore(),
	})

	for _, body := range []string{``, `{}`, `not json`, `{"ref":""}`, `{"ref":"bridge:pve1:vmbr0"}`, `{"content":"orphaned"}`} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/annotations", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %q: status = %d, want 400, response: %s", body, rec.Code, rec.Body.String())
		}
	}
}
