package api

import (
	"encoding/json"
	"net/http"
	"net/url"
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
// keep working unchanged. ch may be nil (no collector wired — tests, or the
// collector failed to initialize): /topology then simply omits its
// staleness section.
func mountTopologyRoutes(r chi.Router, svc TopologyService, auth AuthService, ch CollectorHealth) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/topology", handleTopology(svc, ch))
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

func handleTopology(svc TopologyService, ch CollectorHealth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t := svc.Topology(parseTopologyFilter(r))
		if ch != nil {
			t.Staleness = stalenessFrom(ch.CollectorStatus())
		}
		writeJSON(w, http.StatusOK, t)
	}
}

// staleConsecutiveFailures is how many consecutive poll failures a collector
// source must accumulate before /topology flags its data stale (docs/
// features/topology.md §5's greyed band + staleness banner). Three intervals
// tolerates a transient blip (one failed poll must not grey the whole map)
// while still flagging a genuinely unreachable source within a few poll
// cycles — the "older than N poll intervals" rule, expressed via the failure
// streak the collector already tracks (attempts are per-interval, so N
// straight failures ≈ data N intervals old).
const staleConsecutiveFailures = 3

// stalenessFrom derives the /topology response's staleness section from the
// collector's per-source status (the same data GET /api/v1/health exposes).
// The projection itself stays pure — this handler-level decoration is the
// only place topology data meets collector health. Returns nil (field
// omitted) when there are no sources at all.
func stalenessFrom(sources []CollectorSourceStatus) *topology.Staleness {
	if len(sources) == 0 {
		return nil
	}
	out := &topology.Staleness{Sources: make([]topology.SourceStaleness, 0, len(sources))}
	for _, s := range sources {
		ss := topology.SourceStaleness{
			Name:      s.Name,
			Node:      s.Node,
			Stale:     s.ConsecutiveFailures >= staleConsecutiveFailures,
			LastError: s.LastError,
		}
		if !s.LastSuccess.IsZero() {
			ss.LastSuccess = s.LastSuccess.Unix()
		}
		if ss.Stale {
			out.Stale = true
		}
		out.Sources = append(out.Sources, ss)
	}
	return out
}

func handleInventoryDetail(svc TopologyService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := chi.URLParam(r, "*")
		// chi's wildcard param preserves percent-encoding, so a client that
		// conservatively escapes ":" (encodeURIComponent) sends bridge%3Apve1%3A…
		// here. Unescape before parsing; on bad escapes fall back to the raw
		// form so unescaped refs keep working unchanged.
		if unescaped, uerr := url.PathUnescape(raw); uerr == nil {
			raw = unescaped
		}
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
