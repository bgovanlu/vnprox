// SPDX-License-Identifier: Apache-2.0

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

// fakeAuthWithUser wraps fakeAuth (this package's existing AuthService test
// double, defined in topology_test.go) and additionally implements
// UsernameLookup, so it can exercise the layouts routes' username
// resolution without a real internal/auth.Service or SQLite session store.
type fakeAuthWithUser struct {
	username string
	fakeAuth
}

func (f fakeAuthWithUser) Username(context.Context) (string, bool) {
	if f.username == "" {
		return "", false
	}
	return f.username, true
}

// fakeLayoutStore is an in-memory LayoutStore test double.
type fakeLayoutStore struct {
	data map[string]store.Layout
}

func newFakeLayoutStore() *fakeLayoutStore {
	return &fakeLayoutStore{data: map[string]store.Layout{}}
}

func (f *fakeLayoutStore) key(username, name string) string { return username + "/" + name }

func (f *fakeLayoutStore) Get(_ context.Context, username, name string) (store.Layout, error) {
	l, ok := f.data[f.key(username, name)]
	if !ok {
		return store.Layout{}, store.ErrNotFound
	}
	return l, nil
}

func (f *fakeLayoutStore) Put(_ context.Context, l store.Layout) error {
	f.data[f.key(l.Username, l.Name)] = l
	return nil
}

func (f *fakeLayoutStore) List(_ context.Context, username string) ([]store.Layout, error) {
	var out []store.Layout
	for _, l := range f.data {
		if l.Username == username {
			out = append(out, l)
		}
	}
	return out, nil
}

func (f *fakeLayoutStore) Delete(_ context.Context, username, name string) error {
	delete(f.data, f.key(username, name))
	return nil
}

// TestLayoutsRoutes_NotMountedWithoutUsernameLookup proves that an
// AuthService that can't resolve a username (doesn't implement
// UsernameLookup) leaves the layouts routes unmounted entirely — there
// would be no safe way to key a saved layout to a user.
func TestLayoutsRoutes_NotMountedWithoutUsernameLookup(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{}, Layouts: newFakeLayoutStore(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/layouts/topology", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route should not be mounted), body: %s", rec.Code, rec.Body.String())
	}
}

// TestLayoutsRoutes_Unauthenticated401 proves the layouts routes are gated
// by the same session middleware as topology.
func TestLayoutsRoutes_Unauthenticated401(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:     fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: false}, username: "alice"},
		Topology: fakeTopologyService{}, Layouts: newFakeLayoutStore(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/layouts/topology", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401, body: %s", rec.Code, rec.Body.String())
	}
}

// TestLayoutsRoutes_GetMissing_404 proves a never-saved (user, name) layout
// 404s rather than returning a zero-value body.
func TestLayoutsRoutes_GetMissing_404(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:     fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"},
		Topology: fakeTopologyService{}, Layouts: newFakeLayoutStore(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/layouts/topology", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404, body: %s", rec.Code, rec.Body.String())
	}
}

// TestLayoutsRoutes_PutThenGet_RoundTrips proves a saved layout comes back
// unchanged, and that two different users' layouts of the same name don't
// collide.
func TestLayoutsRoutes_PutThenGet_RoundTrips(t *testing.T) {
	layouts := newFakeLayoutStore()
	newRouter := func(username string) http.Handler {
		return NewRouter(Options{
			Version: "test", DistFS: testDistFS(), Logger: testLogger(),
			Auth:     fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: username},
			Topology: fakeTopologyService{}, Layouts: layouts,
		})
	}

	body := bytes.NewBufferString(`{"layout":{"positions":{"bridge:pve1:vmbr0":{"x":10,"y":20}},"activeLayers":["phys","l2"]}}`)
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/layouts/topology", body)
	putRec := httptest.NewRecorder()
	newRouter("alice").ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200, body: %s", putRec.Code, putRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/layouts/topology", nil)
	getRec := httptest.NewRecorder()
	newRouter("alice").ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200, body: %s", getRec.Code, getRec.Body.String())
	}

	var got struct {
		Name   string `json:"name"`
		Layout struct {
			Positions map[string]struct {
				X float64 `json:"x"`
				Y float64 `json:"y"`
			} `json:"positions"`
			ActiveLayers []string `json:"activeLayers"`
		} `json:"layout"`
		UpdatedAt int64 `json:"updatedAt"`
	}
	if err := json.NewDecoder(getRec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding GET body: %v", err)
	}
	if got.Name != "topology" {
		t.Errorf("name = %q, want topology", got.Name)
	}
	if got.Layout.Positions["bridge:pve1:vmbr0"].X != 10 || got.Layout.Positions["bridge:pve1:vmbr0"].Y != 20 {
		t.Errorf("positions round-tripped wrong: %+v", got.Layout.Positions)
	}
	if got.UpdatedAt == 0 {
		t.Error("updatedAt should be set")
	}

	// A different user's same-named layout must not see alice's data.
	bobReq := httptest.NewRequest(http.MethodGet, "/api/v1/layouts/topology", nil)
	bobRec := httptest.NewRecorder()
	newRouter("bob").ServeHTTP(bobRec, bobReq)
	if bobRec.Code != http.StatusNotFound {
		t.Errorf("bob's GET status = %d, want 404 (no cross-user leakage), body: %s", bobRec.Code, bobRec.Body.String())
	}
}

// TestLayoutsRoutes_List_ScopedToUser proves GET /layouts (T-907) returns
// only the requesting user's saved layouts/views, not another user's.
func TestLayoutsRoutes_List_ScopedToUser(t *testing.T) {
	layouts := newFakeLayoutStore()
	newRouter := func(username string) http.Handler {
		return NewRouter(Options{
			Version: "test", DistFS: testDistFS(), Logger: testLogger(),
			Auth:     fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: username},
			Topology: fakeTopologyService{}, Layouts: layouts,
		})
	}

	for _, put := range []struct{ user, name, body string }{
		{"alice", "topology", `{"layout":{"positions":{}}}`},
		{"alice", "my-view", `{"layout":{"kind":"view","layers":["phys"]}}`},
		{"bob", "topology", `{"layout":{"positions":{}}}`},
	} {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/layouts/"+put.name, bytes.NewBufferString(put.body))
		rec := httptest.NewRecorder()
		newRouter(put.user).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT %s/%s status = %d, want 200, body: %s", put.user, put.name, rec.Code, rec.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/layouts", nil)
	rec := httptest.NewRecorder()
	newRouter("alice").ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /layouts status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	var got layoutsListResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("Items len = %d, want 2 (alice's two layouts only), got: %+v", len(got.Items), got.Items)
	}
	names := map[string]bool{}
	for _, it := range got.Items {
		names[it.Name] = true
	}
	if !names["topology"] || !names["my-view"] {
		t.Errorf("expected alice's items to include topology and my-view, got %+v", got.Items)
	}
}

// TestLayoutsRoutes_Delete_RemovesOnlyThatUsersLayout proves DELETE
// /layouts/{name} removes the requesting user's own row and leaves a
// same-named layout belonging to another user untouched.
func TestLayoutsRoutes_Delete_RemovesOnlyThatUsersLayout(t *testing.T) {
	layouts := newFakeLayoutStore()
	newRouter := func(username string) http.Handler {
		return NewRouter(Options{
			Version: "test", DistFS: testDistFS(), Logger: testLogger(),
			Auth:     fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: username},
			Topology: fakeTopologyService{}, Layouts: layouts,
		})
	}

	for _, user := range []string{"alice", "bob"} {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/layouts/my-view", bytes.NewBufferString(`{"layout":{"kind":"view"}}`))
		rec := httptest.NewRecorder()
		newRouter(user).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT (%s) status = %d, want 200", user, rec.Code)
		}
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/layouts/my-view", nil)
	delRec := httptest.NewRecorder()
	newRouter("alice").ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204, body: %s", delRec.Code, delRec.Body.String())
	}

	aliceGet := httptest.NewRequest(http.MethodGet, "/api/v1/layouts/my-view", nil)
	aliceRec := httptest.NewRecorder()
	newRouter("alice").ServeHTTP(aliceRec, aliceGet)
	if aliceRec.Code != http.StatusNotFound {
		t.Errorf("alice's GET after delete status = %d, want 404", aliceRec.Code)
	}

	bobGet := httptest.NewRequest(http.MethodGet, "/api/v1/layouts/my-view", nil)
	bobRec := httptest.NewRecorder()
	newRouter("bob").ServeHTTP(bobRec, bobGet)
	if bobRec.Code != http.StatusOK {
		t.Errorf("bob's GET after alice's delete status = %d, want 200 (unaffected), body: %s", bobRec.Code, bobRec.Body.String())
	}
}

// TestLayoutsRoutes_PutMalformedBody_400 proves a body that isn't
// {"layout": {...}} is rejected rather than silently stored.
func TestLayoutsRoutes_PutMalformedBody_400(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:     fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"},
		Topology: fakeTopologyService{}, Layouts: newFakeLayoutStore(),
	})

	for _, body := range []string{``, `{}`, `not json`, `{"layout":null}`} {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/layouts/topology", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %q: status = %d, want 400, response: %s", body, rec.Code, rec.Body.String())
		}
	}
}
