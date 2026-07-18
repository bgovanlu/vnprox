package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
// don't need a real inventory graph. When gotFilter is non-nil, Topology
// records the parsed filter it was called with there, so tests can assert
// query-param parsing end to end through the router.
type fakeTopologyService struct {
	gotFilter *topology.Filter
	searchHit []topology.SearchResult
	nodes     []topology.Node
	detail    topology.EntityDetail
	detailOK  bool
}

func (f fakeTopologyService) Topology(fl topology.Filter) topology.Topology {
	if f.gotFilter != nil {
		*f.gotFilter = fl
	}
	nodes := f.nodes
	if nodes == nil {
		nodes = []topology.Node{}
	}
	return topology.Topology{Nodes: nodes, Edges: []topology.Edge{}, Layers: topology.AllLayers, GeneratedAt: 1}
}

func (f fakeTopologyService) InventoryDetail(inventory.Ref) (topology.EntityDetail, bool) {
	return f.detail, f.detailOK
}

func (f fakeTopologyService) Search(string) []topology.SearchResult { return f.searchHit }

func (f fakeTopologyService) ServeWS(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (f fakeTopologyService) CloseByTokenID(string) int { return 0 }

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

// TestTopologyRoute_QueryParamParsing proves GET /topology's
// ?vlan=&layers=&node= query params reach the TopologyService as the
// documented Filter (docs/api.md: `?layers=phys,l2,sdn,guest&node=<name>&
// vlan=<vid>`), including comma-splitting/whitespace-trimming of layers and
// ignoring a non-numeric vlan.
func TestTopologyRoute_QueryParamParsing(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  topology.Filter
	}{
		{name: "no params means zero filter", query: "", want: topology.Filter{}},
		{
			name:  "all three params",
			query: "?vlan=30&layers=l2,sdn&node=pve2",
			want:  topology.Filter{VLAN: 30, Layers: []topology.Layer{topology.LayerL2, topology.LayerSDN}, Node: "pve2"},
		},
		{
			name:  "layers tolerate whitespace and empty segments",
			query: "?layers=%20phys%20,,guest",
			want:  topology.Filter{Layers: []topology.Layer{topology.LayerPhysical, topology.LayerGuest}},
		},
		{name: "non-numeric vlan is ignored", query: "?vlan=abc&node=pve1", want: topology.Filter{Node: "pve1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got topology.Filter
			r := NewRouter(Options{
				Version: "test", DistFS: testDistFS(), Logger: testLogger(),
				Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{gotFilter: &got},
			})
			req := httptest.NewRequest(http.MethodGet, "/api/v1/topology"+tt.query, nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
			}
			if got.VLAN != tt.want.VLAN || got.Node != tt.want.Node || !equalLayers(got.Layers, tt.want.Layers) {
				t.Errorf("parsed filter = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func equalLayers(a, b []topology.Layer) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestTopologyRoute_Staleness covers the /topology staleness decoration
// (docs/features/topology.md §5, audit finding F-18b): a healthy collector
// yields no stale flags, a source with a long failure streak flags itself
// (and the top-level bit) stale, and a router with no collector wired omits
// the section entirely.
func TestTopologyRoute_Staleness(t *testing.T) {
	now := time.Now()

	getTopology := func(t *testing.T, ch CollectorHealth) map[string]any {
		t.Helper()
		r := NewRouter(Options{
			Version: "test", DistFS: testDistFS(), Logger: testLogger(),
			Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{},
			Collectors: ch,
		})
		req := httptest.NewRequest(http.MethodGet, "/api/v1/topology", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decoding response: %v", err)
		}
		return body
	}

	t.Run("healthy collector reports no staleness", func(t *testing.T) {
		body := getTopology(t, stubCollectorHealth{sources: []CollectorSourceStatus{
			{Name: "pve", LastSuccess: now, LastAttempt: now},
			{Name: "host", Node: "pve1", LastSuccess: now, LastAttempt: now},
			{Name: "lldp", Node: "pve1", LastSuccess: now, LastAttempt: now},
		}})
		st, ok := body["staleness"].(map[string]any)
		if !ok {
			t.Fatalf("response has no staleness object: %v", body)
		}
		if st["stale"] != false {
			t.Errorf("staleness.stale = %v, want false", st["stale"])
		}
		sources, ok := st["sources"].([]any)
		if !ok || len(sources) != 3 {
			t.Fatalf("staleness.sources = %v, want 3 entries", st["sources"])
		}
		for _, s := range sources {
			src := s.(map[string]any)
			if src["stale"] != false {
				t.Errorf("source %v stale = %v, want false", src["name"], src["stale"])
			}
			if src["lastSuccess"] == nil {
				t.Errorf("source %v has no lastSuccess", src["name"])
			}
		}
	})

	t.Run("failing source flags staleness", func(t *testing.T) {
		body := getTopology(t, stubCollectorHealth{sources: []CollectorSourceStatus{
			{Name: "pve", LastSuccess: now, LastAttempt: now},
			{Name: "host", Node: "pve1", LastSuccess: now.Add(-10 * time.Minute), LastAttempt: now,
				ConsecutiveFailures: 5, LastError: "host poll failed: connection refused"},
			{Name: "lldp", Node: "pve1", LastSuccess: now, LastAttempt: now},
		}})
		st, ok := body["staleness"].(map[string]any)
		if !ok {
			t.Fatalf("response has no staleness object: %v", body)
		}
		if st["stale"] != true {
			t.Errorf("staleness.stale = %v, want true", st["stale"])
		}
		var hostSrc map[string]any
		for _, s := range st["sources"].([]any) {
			if src := s.(map[string]any); src["name"] == "host" {
				hostSrc = src
			} else if src["stale"] != false {
				t.Errorf("source %v stale = %v, want false", src["name"], src["stale"])
			}
		}
		if hostSrc == nil {
			t.Fatalf("no host source in %v", st["sources"])
		}
		if hostSrc["stale"] != true {
			t.Errorf("host stale = %v, want true", hostSrc["stale"])
		}
		if hostSrc["node"] != "pve1" {
			t.Errorf("host node = %v, want pve1", hostSrc["node"])
		}
		if hostSrc["lastError"] == nil || hostSrc["lastError"] == "" {
			t.Errorf("host lastError missing: %v", hostSrc)
		}
		wantTS := float64(now.Add(-10 * time.Minute).Unix())
		if hostSrc["lastSuccess"] != wantTS {
			t.Errorf("host lastSuccess = %v, want %v", hostSrc["lastSuccess"], wantTS)
		}
	})

	t.Run("transient failures below the threshold are not stale", func(t *testing.T) {
		body := getTopology(t, stubCollectorHealth{sources: []CollectorSourceStatus{
			{Name: "pve", LastSuccess: now, LastAttempt: now, ConsecutiveFailures: 2, LastError: "blip"},
		}})
		st := body["staleness"].(map[string]any)
		if st["stale"] != false {
			t.Errorf("staleness.stale = %v, want false for a 2-failure streak", st["stale"])
		}
	})

	t.Run("no collector wired omits the section", func(t *testing.T) {
		body := getTopology(t, nil)
		if _, has := body["staleness"]; has {
			t.Errorf("response unexpectedly has staleness with no collector wired: %v", body["staleness"])
		}
	})
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

// TestInventoryDetailRoute_PercentEncodedRef proves a percent-encoded ref
// (encodeURIComponent escapes ":" as %3A) resolves identically to the
// unescaped form: chi's wildcard param preserves the encoding, and the
// handler must unescape before ParseRef.
func TestInventoryDetailRoute_PercentEncodedRef(t *testing.T) {
	svc := fakeTopologyService{detailOK: true}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: svc,
	})
	for _, path := range []string{
		"/api/v1/inventory/bridge:pve1:vmbr0",
		"/api/v1/inventory/bridge%3Apve1%3Avmbr0",
		"/api/v1/inventory/sdn-vnet%3A%3Azone1%2Fvnet1",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status = %d, want 200, body: %s", path, rec.Code, rec.Body.String())
		}
	}
}
