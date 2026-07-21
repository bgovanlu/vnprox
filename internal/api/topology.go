package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/drift"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// MgmtStatusService is the read-only seam handleTopology needs to paint
// T-702's mgmt/corosync/mgmt-path badges (docs/features/topology.md §3):
// exactly ProtectedService's MgmtStatus method, declared standalone so this
// file doesn't need the whole ProtectedService interface — any value
// satisfying ProtectedService (i.e. Options.Protected, already wired for
// the /protected-interfaces routes) satisfies this one too. nil-safe like
// every other optional badge-painting input on this route (Drift/Findings).
type MgmtStatusService interface {
	MgmtStatus(ctx context.Context) (change.MgmtStatus, error)
}

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
	// CloseByTokenID is T-1104's revoke-forces-disconnect seam: DELETE
	// /tokens/{id} calls this right after persisting the revocation so any
	// WS connection that token authenticated is torn down within the same
	// request tick (see internal/topology.Hub.CloseByTokenID's doc
	// comment).
	CloseByTokenID(id string) int
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
func mountTopologyRoutes(r chi.Router, svc TopologyService, auth AuthService, ch CollectorHealth, driftSvc DriftService, findingsSvc FindingsService, mgmtSvc MgmtStatusService, qosSvc QosShapeSource, pbsSvc PBSService, scopeMW func(http.Handler) http.Handler) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		if scopeMW != nil {
			r.Use(scopeMW)
		}
		r.Get("/topology", handleTopology(svc, ch, driftSvc, findingsSvc, mgmtSvc, qosSvc, pbsSvc))
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

func handleTopology(svc TopologyService, ch CollectorHealth, driftSvc DriftService, findingsSvc FindingsService, mgmtSvc MgmtStatusService, qosSvc QosShapeSource, pbsSvc PBSService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t := svc.Topology(parseTopologyFilter(r))
		if ch != nil {
			t.Staleness = stalenessFrom(ch.CollectorStatus())
		}
		// findingsSvc (T-602's unified stream, when wired) supersedes the
		// drift-only painting below entirely — it already includes every
		// drift finding (adapted, source=drift) plus lldp/ipam/health, so
		// painting from both would double up the badge on any ref a drift
		// finding also names. driftSvc stays the fallback for any caller
		// (chiefly this package's own pre-T-602 tests) that only wires
		// Drift, not Findings — see paintDrift's doc comment.
		switch {
		case findingsSvc != nil:
			paintFindings(&t, findingsSvc.Findings())
		case driftSvc != nil:
			paintDrift(&t, driftSvc.Findings())
		}
		if mgmtSvc != nil {
			paintMgmtStatus(r.Context(), &t, mgmtSvc)
		}
		if qosSvc != nil {
			paintQosBadges(r.Context(), &t, qosSvc)
		}
		if pbsSvc != nil {
			paintPBS(r.Context(), &t, pbsSvc)
		}
		// T-1703: a tenant-scoped caller sees only its own visible entities.
		// Applied to the projection before serialization (data-access-layer
		// enforcement); an unscoped caller is unaffected.
		if scope, ok := scopeFromContext(r.Context()); ok {
			t = filterTopologyForScope(t, scope)
		}
		writeJSON(w, http.StatusOK, t)
	}
}

// paintMgmtStatus decorates t with T-702's mgmt/corosync/mgmt-path badges
// (docs/features/topology.md §3), computed from the exact same
// change.Service.MgmtStatus call GET /protected-interfaces/status answers
// from (protected.go's handleMgmtStatus) — the two surfaces can never
// disagree. A MgmtStatus computation error degrades to "no mgmt badges this
// request" (logged nowhere, matching paintDrift/paintFindings' own
// tolerance of an empty producer) rather than failing the whole /topology
// request over a display-only decoration.
func paintMgmtStatus(ctx context.Context, t *topology.Topology, mgmtSvc MgmtStatusService) {
	status, err := mgmtSvc.MgmtStatus(ctx)
	if err != nil {
		return
	}
	topology.ApplyMgmtBadges(t, status.Nodes)
}

// findingBadge marks a topology node/edge as carrying an open finding from
// any unified-stream producer (drift, lldp, ipam, or health — T-602)
// (docs/features/topology.md §2: "drift = dashed outline", generalized by
// T-602 to reflect the whole findings stream, not only drift specifically —
// the wire value stays "drift" for backward compatibility with the
// frontend's existing EntityNode.tsx/EntityEdge.tsx check and with
// docs/api.md's documented badge vocabulary, rather than introducing a
// second, parallel badge string for the exact same "dashed outline" visual
// treatment). This decoration is deliberately handler-level, not inside
// internal/topology itself (the same pattern stalenessFrom above already
// uses): the pure Project function stays a function of the inventory
// snapshot alone, and findings are a second, independent input composed on
// top here.
const findingBadge = "drift"

// paintDrift adds findingBadge to every node in t whose id is named by one of
// findings' affected Refs (T-305's Finding.Refs — always concrete entity
// refs, e.g. "bridge:pve2:vmbr0", never synthetic guest-group ids). Kept
// unchanged as handleTopology's fallback when no FindingsService is wired.
func paintDrift(t *topology.Topology, fs []drift.Finding) {
	affected := make(map[string]bool)
	for _, f := range fs {
		for _, ref := range f.Refs {
			affected[ref] = true
		}
	}
	paintBadge(t, affected)
}

// paintFindings is paintDrift's T-602 generalization: same mechanism, fed
// from the unified findings stream's Refs instead of drift's alone.
func paintFindings(t *topology.Topology, fs []findings.Finding) {
	affected := make(map[string]bool)
	for _, f := range fs {
		for _, ref := range f.Refs {
			affected[ref] = true
		}
	}
	paintBadge(t, affected)
}

// qosShapedBadge is T-1505's shaping-active badge token — additive to
// whatever badges Project/paintFindings/paintMgmtStatus already assigned,
// the same "one more token in badges[]" convention every other overlay in
// this file uses (T-901's badge-rendering convention).
const qosShapedBadge = "qos-shaped"

// paintQosBadges decorates t with T-1505's shaping-active badge: every
// node whose id names a bridge currently carrying an applied qos.shape
// (qosSvc.ShapedBridgeRefs) gets qosShapedBadge appended to its Badges. A
// read error degrades to "no qos badges this request" (logged nowhere,
// matching paintMgmtStatus's identical tolerance) rather than failing the
// whole /topology request over a display-only decoration.
func paintQosBadges(ctx context.Context, t *topology.Topology, qosSvc QosShapeSource) {
	refs, err := qosSvc.ShapedBridgeRefs(ctx)
	if err != nil || len(refs) == 0 {
		return
	}
	affected := make(map[string]bool, len(refs))
	for ref, shaped := range refs {
		if shaped {
			affected[ref.String()] = true
		}
	}
	if len(affected) == 0 {
		return
	}
	for i, n := range t.Nodes {
		if affected[n.ID] {
			t.Nodes[i].Badges = append(append([]string{}, n.Badges...), qosShapedBadge)
		}
	}
}

func paintBadge(t *topology.Topology, affected map[string]bool) {
	if len(affected) == 0 {
		return
	}
	for i, n := range t.Nodes {
		if affected[n.ID] {
			t.Nodes[i].Badges = append(append([]string{}, n.Badges...), findingBadge)
		}
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
		// T-1703: a tenant-scoped caller may only look up Refs within its
		// scope. An out-of-scope Ref returns 404 (existence is not confirmed) —
		// never a 403 that would leak that the entity exists.
		if scope, scoped := scopeFromContext(r.Context()); scoped && !scope.Visible(ref.String()) {
			writeJSONError(w, http.StatusNotFound, "not_found", "no such inventory entity")
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
		results := svc.Search(q)
		// T-1703: search must not leak out-of-scope entities to a tenant.
		if scope, scoped := scopeFromContext(r.Context()); scoped {
			filtered := make([]topology.SearchResult, 0, len(results))
			for _, res := range results {
				if scope.Visible(res.Ref) {
					filtered = append(filtered, res)
				}
			}
			results = filtered
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": results})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
