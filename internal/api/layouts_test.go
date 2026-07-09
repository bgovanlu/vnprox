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
