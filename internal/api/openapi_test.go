package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/apidoc"
)

func openAPITestRouter(t *testing.T, auth AuthService) http.Handler {
	t.Helper()
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, Topology: fakeTopologyService{},
	})
}

// TestOpenAPI_ServedWithoutASessionWhileTheRoutesItDescribesAreNot is T-2405
// AC4, asserted from both sides in one test so neither half can pass alone.
func TestOpenAPI_ServedWithoutASessionWhileTheRoutesItDescribesAreNot(t *testing.T) {
	// An auth backend that refuses everything: no session, no capabilities.
	auth := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: false}, username: "alice"},
		caps:             map[string]bool{capNetRead: true},
	}
	r := openAPITestRouter(t, auth)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/openapi.json = %d, want 200: the contract must be readable without credentials (body: %s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var doc apidoc.Document
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("the served document does not parse: %v", err)
	}
	if len(doc.Paths) == 0 {
		t.Fatal("the served document describes no paths")
	}

	// The other side: a route the document describes, requested the same way,
	// is refused.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/topology", nil))
	if rec.Code == http.StatusOK {
		t.Errorf("GET /api/v1/topology without a session = 200; the document says it needs one")
	}
}

// TestOpenAPI_GateFailsForARouteAddedToTheRealRouter is AC1 proven the way
// the card demands: by adding a route to the production router and watching
// the gate name it. Asserting that today's routes all pass would keep passing
// if the gate silently stopped inspecting anything.
func TestOpenAPI_GateFailsForARouteAddedToTheRealRouter(t *testing.T) {
	handler := openAPITestRouter(t, fullCapsAuth("alice"))

	before, err := WalkRoutes(handler)
	if err != nil {
		t.Fatalf("WalkRoutes: %v", err)
	}
	if got := apidoc.Missing(before); len(got) != 0 {
		t.Fatalf("the router as built already has undescribed routes: %v", got)
	}

	mux, ok := handler.(chi.Router)
	if !ok {
		t.Fatalf("the production router is not a chi.Router (%T); this test cannot add a route to it", handler)
	}
	const added = "/api/v1/a-route-nobody-described"
	mux.Get(added, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	after, err := WalkRoutes(handler)
	if err != nil {
		t.Fatalf("WalkRoutes after adding a route: %v", err)
	}
	missing := apidoc.Missing(after)
	if len(missing) != 1 || missing[0] != "GET "+added {
		t.Fatalf("Missing() = %v, want exactly [GET %s] — the gate did not notice a new route", missing, added)
	}
}

// TestOpenAPI_GeneratedFromTheRealRegisteredRoutes is the property the whole
// design rests on. If the document were assembled from a hand-kept list, it
// could describe a path the router does not have; because it is walked, it
// cannot.
func TestOpenAPI_GeneratedFromTheRealRegisteredRoutes(t *testing.T) {
	handler := openAPITestRouter(t, fullCapsAuth("alice"))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil))
	var doc apidoc.Document
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decoding the served document: %v", err)
	}

	routes, err := WalkRoutes(handler)
	if err != nil {
		t.Fatalf("WalkRoutes: %v", err)
	}
	walked := map[string]bool{}
	for _, rt := range routes {
		walked[rt.Key()] = true
	}

	for path, item := range doc.Paths {
		for method, op := range map[string]*apidoc.Op{
			http.MethodGet: item.Get, http.MethodPut: item.Put, http.MethodPost: item.Post,
			http.MethodDelete: item.Delete, http.MethodPatch: item.Patch, http.MethodHead: item.Head,
		} {
			if op == nil {
				continue
			}
			if !walked[apidoc.Key(method, path)] {
				t.Errorf("the document describes %s %s, which the router does not serve", method, path)
			}
		}
	}
}

// TestWalkRoutes_RefusesANonChiHandler keeps the gate from silently reporting
// "no routes" — and therefore "nothing undescribed" — if the router is ever
// wrapped in something that is not walkable.
func TestWalkRoutes_RefusesANonChiHandler(t *testing.T) {
	plain := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	routes, err := WalkRoutes(plain)
	if err == nil {
		t.Fatalf("WalkRoutes on a plain handler returned %d routes and no error; an unwalkable router must be an error, not an empty gate", len(routes))
	}
}
