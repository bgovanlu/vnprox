package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// TopologyService is the subset of T-106's *topology.Service the router
// needs: the projected topology, single-entity detail, search, and the WS
// upgrade. Declared as an interface (the same pattern as AuthService and
// CollectorHealth above) so this package's dependency on the concrete
// topology.Service stays a small, explicit seam.
type TopologyService interface {
	Topology(f topology.Filter) topology.Topology
	InventoryDetail(ref inventory.Ref) (topology.EntityDetail, bool)
	Search(q string) []topology.SearchResult
	ServeWS(w http.ResponseWriter, r *http.Request)
}

// mountTopologyRoutes registers docs/api.md's topology/inventory routes and
// the /api/ws upgrade. All of them require an authenticated session with
// the netRead capability (docs/api.md's Auth section documents netRead as
// the read-capability flag; see internal/auth/caps.go) — none of these are
// mutating requests, so CSRFMiddleware is deliberately not in this chain
// (docs/security.md: CSRF protection applies to mutating requests only).
//
// auth is nil-safe to call with (routes are simply not mounted) so the
// health-only test router configurations elsewhere in this package's tests
// keep working unchanged.
func mountTopologyRoutes(r chi.Router, svc TopologyService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/topology", handleTopology(svc))
		r.Get("/inventory/search", handleInventorySearch(svc))
		// A trailing chi wildcard (not a "{ref}" single-segment param) is
		// required here: docs/api.md's Ref triplet scheme allows literal
		// '/' inside the ID (an SDN vnet's "zone1/vnet1", a subnet CIDR),
		// and net/http already percent-decodes the request path before chi
		// ever sees it — so a caller can put the ref's raw '/' characters
		// straight into the path without percent-encoding, and this route
		// still matches all of it as one value. chi's static route
		// "/inventory/search" above always wins over this wildcard
		// regardless of registration order (chi prioritizes static >
		// param > wildcard).
		r.Get("/inventory/*", handleInventoryDetail(svc))
	})
}

// mountWSRoute registers the top-level (not /api/v1-scoped) /api/ws
// upgrade, gated by the same session + capability requirement as the REST
// topology routes (T-106 acceptance criterion 5: "a WS upgrade ... with no
// session cookie -> 401").
func mountWSRoute(r chi.Router, svc TopologyService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/api/ws", svc.ServeWS)
	})
}

// capNetRead is docs/api.md's documented read-capability flag name
// (internal/auth.CapNetRead's underlying string) — spelled out as a plain
// string here rather than importing internal/auth's Cap type, keeping this
// package's auth dependency to the AuthService method-seam interface
// (see router.go's doc comment on AuthService).
const capNetRead = "netRead"

func parseLayers(raw string) []topology.Layer {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]topology.Layer, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, topology.Layer(p))
	}
	return out
}

func parseTopologyFilter(r *http.Request) topology.Filter {
	q := r.URL.Query()
	f := topology.Filter{
		Layers: parseLayers(q.Get("layers")),
		Node:   q.Get("node"),
	}
	if vlan := q.Get("vlan"); vlan != "" {
		if v, err := strconv.Atoi(vlan); err == nil {
			f.VLAN = v
		}
	}
	return f
}

func handleTopology(svc TopologyService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t := svc.Topology(parseTopologyFilter(r))
		writeJSON(w, http.StatusOK, t)
	}
}

func handleInventoryDetail(svc TopologyService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := chi.URLParam(r, "*")
		ref, err := inventory.ParseRef(raw)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed inventory ref")
			return
		}
		detail, ok := svc.InventoryDetail(ref)
		if !ok {
			writeJSONError(w, http.StatusNotFound, "not_found", "no such inventory entity")
			return
		}
		writeJSON(w, http.StatusOK, detail)
	}
}

func handleInventorySearch(svc TopologyService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		writeJSON(w, http.StatusOK, map[string]any{"results": svc.Search(q)})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
