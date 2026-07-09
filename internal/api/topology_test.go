package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// fakeAuth is a minimal AuthService stand-in: SessionMiddleware either lets
// every request through or 401s every request, and RequireCap is always a
// no-op pass-through (capability gating itself is internal/auth's own
// tested responsibility; this package only needs to prove the routes are
// wired behind SessionMiddleware at all).
type fakeAuth struct{ authenticated bool }

func (fakeAuth) MountRoutes(chi.Router) {}

func (f fakeAuth) SessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !f.authenticated {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (fakeAuth) RequireCap(string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

// fakeTopologyService is a minimal TopologyService stand-in so router tests
// don't need a real inventory graph.
type fakeTopologyService struct {
	searchHit []topology.SearchResult
	detail    topology.EntityDetail
	detailOK  bool
}

func (f fakeTopologyService) Topology(topology.Filter) topology.Topology {
	return topology.Topology{Nodes: []topology.Node{}, Edges: []topology.Edge{}, Layers: topology.AllLayers, GeneratedAt: 1}
}

func (f fakeTopologyService) InventoryDetail(inventory.Ref) (topology.EntityDetail, bool) {
	return f.detail, f.detailOK
}

func (f fakeTopologyService) Search(string) []topology.SearchResult { return f.searchHit }

func (f fakeTopologyService) ServeWS(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// TestTopologyRoutes_Unauthenticated401 is T-106 acceptance criterion 5:
// GET /api/v1/topology (and, in the next test, a WS upgrade) with no
// session -> 401.
func TestTopologyRoutes_Unauthenticated401(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: false}, Topology: fakeTopologyService{},
	})

	for _, path := range []string{"/api/v1/topology", "/api/v1/inventory/search", "/api/v1/inventory/bridge:pve1:vmbr0"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s (unauthenticated) status = %d, want 401", path, rec.Code)
		}
	}
}

// TestWSRoute_Unauthenticated401 covers the /api/ws upgrade specifically:
// it must never reach the hub without a valid session.
func TestWSRoute_Unauthenticated401(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: false}, Topology: fakeTopologyService{},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/ws", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/ws (unauthenticated) status = %d, want 401", rec.Code)
	}
}

// TestTopologyRoutes_Authenticated200 proves an authenticated request
// reaches the underlying TopologyService (as opposed to always 401ing
// regardless of session, which would make the previous two tests
// meaningless).
func TestTopologyRoutes_Authenticated200(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/topology", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/topology (authenticated) status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}

	wsReq := httptest.NewRequest(http.MethodGet, "/api/ws", nil)
	wsRec := httptest.NewRecorder()
	r.ServeHTTP(wsRec, wsReq)
	if wsRec.Code != http.StatusOK {
		t.Errorf("GET /api/ws (authenticated, fake ServeWS) status = %d, want 200", wsRec.Code)
	}
}

// TestInventoryDetailRoute_NotFound proves the wildcard ref route and 404
// handling both work for an unknown ref.
func TestInventoryDetailRoute_NotFound(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{detailOK: false},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/inventory/bridge:pve1:does-not-exist", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404, body: %s", rec.Code, rec.Body.String())
	}
}

// TestInventoryDetailRoute_MalformedRef proves a ref that doesn't parse
// gets a 400, not a panic or a 404.
func TestInventoryDetailRoute_MalformedRef(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/inventory/not-a-valid-ref", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
}
