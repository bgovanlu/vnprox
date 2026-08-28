// SPDX-License-Identifier: Apache-2.0

// route.go implements T-3903's route explorer API: a per-node routing
// snapshot (kernel FIB, policy rules, FRR RIB) and the "which path would
// this address take" lookup. Read-only throughout — no route in this file
// stages, validates, or applies any change (internal/change is never
// imported here); see internal/route's package doc comment for how this
// complements, rather than duplicates, internal/sim's declined-routing
// caveat (FeatureExternalRouting, internal/sim/l3.go).

package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/route"
)

// RouteService is the subset of *route.Service the router needs.
// Declared as an interface (the same seam pattern EVPNService/SDNService
// use) so this package's dependency on the concrete service stays small.
type RouteService interface {
	// Nodes lists every node a Snapshot/Lookup call can currently target:
	// the local node (if known) plus every reachable peer.
	Nodes(ctx context.Context) []string
	Snapshot(ctx context.Context, node string) (route.Snapshot, error)
	Lookup(ctx context.Context, node, dst, ifaceHint string) (route.LookupResult, error)
}

// mountRouteRoutes registers GET /route/nodes, GET /route/snapshot, and
// GET /route/lookup — gated on capNetRead, the same capability every
// other live-network-observability read route in this package uses.
// svc == nil (route.Service not wired) mirrors every other mountXRoutes
// function here: the routes simply aren't mounted.
func mountRouteRoutes(r chi.Router, svc RouteService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/route/nodes", handleRouteNodes(svc))
		r.Get("/route/snapshot", handleRouteSnapshot(svc))
		r.Get("/route/lookup", handleRouteLookup(svc))
	})
}

func handleRouteNodes(svc RouteService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodes := svc.Nodes(r.Context())
		if nodes == nil {
			nodes = []string{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
	}
}

// fibRouteResponse is one FIBRoute, docs/api.md's `GET /route/snapshot`
// section.
type fibRouteResponse struct {
	AFI      string `json:"afi"`
	Table    string `json:"table"`
	Type     string `json:"type"`
	Dst      string `json:"dst"`
	Gateway  string `json:"gateway,omitempty"`
	Dev      string `json:"dev"`
	Protocol string `json:"protocol,omitempty"`
	Scope    string `json:"scope,omitempty"`
	PrefSrc  string `json:"prefSrc,omitempty"`
	Pref     string `json:"pref,omitempty"`
	Metric   int    `json:"metric,omitempty"`
}

type policyRuleResponse struct {
	AFI      string `json:"afi"`
	Src      string `json:"src"`
	Table    string `json:"table"`
	Priority int    `json:"priority"`
}

type ribNextHopResponse struct {
	IP                string `json:"ip,omitempty"`
	Interface         string `json:"interface"`
	DirectlyConnected bool   `json:"directlyConnected,omitempty"`
	Active            bool   `json:"active"`
	FIB               bool   `json:"fib"`
	Weight            int    `json:"weight,omitempty"`
}

type ribRouteResponse struct {
	AFI       string               `json:"afi"`
	VRF       string               `json:"vrf"`
	Prefix    string               `json:"prefix"`
	Protocol  string               `json:"protocol"`
	Uptime    string               `json:"uptime,omitempty"`
	Nexthops  []ribNextHopResponse `json:"nexthops"`
	Distance  int                  `json:"distance,omitempty"`
	Metric    int                  `json:"metric,omitempty"`
	Selected  bool                 `json:"selected,omitempty"`
	Installed bool                 `json:"installed,omitempty"`
}

// routeSnapshotResponse is docs/api.md's `GET /route/snapshot` body.
type routeSnapshotResponse struct {
	Node           string               `json:"node"`
	FIB            []fibRouteResponse   `json:"fib"`
	Rules          []policyRuleResponse `json:"rules"`
	RIB            []ribRouteResponse   `json:"rib,omitempty"`
	FRRUnavailable bool                 `json:"frrUnavailable"`
}

func toFIBRouteResponse(r route.FIBRoute) fibRouteResponse {
	return fibRouteResponse{
		AFI: string(r.AFI), Table: r.Table, Type: r.Type, Dst: r.Dst,
		Gateway: r.Gateway, Dev: r.Dev, Protocol: r.Protocol, Scope: r.Scope,
		PrefSrc: r.PrefSrc, Pref: r.Pref, Metric: r.Metric,
	}
}

func toPolicyRuleResponse(r route.PolicyRule) policyRuleResponse {
	return policyRuleResponse{AFI: string(r.AFI), Src: r.Src, Table: r.Table, Priority: r.Priority}
}

func toRIBRouteResponse(r route.RIBRoute) ribRouteResponse {
	nhs := make([]ribNextHopResponse, 0, len(r.Nexthops))
	for _, nh := range r.Nexthops {
		nhs = append(nhs, ribNextHopResponse{
			IP: nh.IP, Interface: nh.Interface, DirectlyConnected: nh.DirectlyConnected,
			Active: nh.Active, FIB: nh.FIB, Weight: nh.Weight,
		})
	}
	return ribRouteResponse{
		AFI: string(r.AFI), VRF: r.VRF, Prefix: r.Prefix, Protocol: r.Protocol,
		Distance: r.Distance, Metric: r.Metric, Selected: r.Selected, Installed: r.Installed,
		Uptime: r.Uptime, Nexthops: nhs,
	}
}

// handleRouteSnapshot serves GET /route/snapshot?node=<node>: the node's
// full kernel FIB + policy rules + FRR RIB (when available). node
// defaults to the local node when omitted (route.Service.Snapshot's own
// "" convention).
func handleRouteSnapshot(svc RouteService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		node := r.URL.Query().Get("node")
		snap, err := svc.Snapshot(r.Context(), node)
		if err != nil {
			writeRouteError(w, err)
			return
		}
		resp := routeSnapshotResponse{
			Node: snap.Node, FRRUnavailable: snap.FRRUnavailable,
			FIB: make([]fibRouteResponse, 0, len(snap.FIB)), Rules: make([]policyRuleResponse, 0, len(snap.Rules)),
		}
		for _, fr := range snap.FIB {
			resp.FIB = append(resp.FIB, toFIBRouteResponse(fr))
		}
		for _, pr := range snap.Rules {
			resp.Rules = append(resp.Rules, toPolicyRuleResponse(pr))
		}
		for _, rr := range snap.RIB {
			resp.RIB = append(resp.RIB, toRIBRouteResponse(rr))
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// routeLookupResponse is docs/api.md's `GET /route/lookup` body.
type routeLookupResponse struct {
	Dst          string              `json:"dst"`
	MatchedRoute *fibRouteResponse   `json:"matchedRoute,omitempty"`
	MatchedRule  *policyRuleResponse `json:"matchedRule,omitempty"`
	Trace        []string            `json:"trace,omitempty"`
	Ambiguous    []string            `json:"ambiguous,omitempty"`
	RulesSkipped []string            `json:"rulesSkipped,omitempty"`
	Reachable    bool                `json:"reachable"`
}

// handleRouteLookup serves GET /route/lookup?node=&dst=&iface= — T-3903's
// core operator question, "which path would this address take." dst is
// required (a plain IP address, not a CIDR); iface is an optional device
// hint disambiguating a genuine tie (see route.Lookup's doc comment).
func handleRouteLookup(svc RouteService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		dst := strings.TrimSpace(q.Get("dst"))
		if dst == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "dst is required")
			return
		}
		node := q.Get("node")
		iface := q.Get("iface")

		res, err := svc.Lookup(r.Context(), node, dst, iface)
		if err != nil {
			writeRouteError(w, err)
			return
		}
		resp := routeLookupResponse{
			Dst: res.Dst, Reachable: res.Reachable, Trace: res.Trace,
			Ambiguous: res.Ambiguous, RulesSkipped: res.RulesSkipped,
		}
		if res.MatchedRoute != nil {
			mr := toFIBRouteResponse(*res.MatchedRoute)
			resp.MatchedRoute = &mr
		}
		if res.MatchedRule != nil {
			mr := toPolicyRuleResponse(*res.MatchedRule)
			resp.MatchedRule = &mr
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// writeRouteError maps a Snapshot/Lookup failure to an HTTP status:
// route.ErrNodeNotFound (an unrecognized/unreachable node name — a
// caller mistake, not a server fault) to 404, an invalid dst (route.
// Lookup's own netip.ParseAddr failure) to 400 via a substring check on
// the wrapped message (route.Lookup's error text is stable and always
// names "is not a valid IP address" — see lookup.go), and everything
// else (a genuine fetch/parse failure against the node) to 503, matching
// GET /sdn/evpn/status's "degraded upstream" convention for the same
// class of failure.
func writeRouteError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, route.ErrNodeNotFound):
		writeJSONError(w, http.StatusNotFound, "not_found", err.Error())
	case strings.Contains(err.Error(), "is not a valid IP address"):
		writeJSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "pve_unreachable", "could not read routing state: "+err.Error())
	}
}
