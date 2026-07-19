package api

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/ipv6"
)

// IPv6Service is the subset of T-1404's *ipv6.Service the router needs:
// docs/api.md's GET /ipv6/segments (cluster-wide RA/SLAAC/DHCPv6
// visibility). Declared as an interface (the same seam pattern
// EVPNService/SDNService above use) so this package's dependency on the
// concrete service stays small.
type IPv6Service interface {
	Segments(ctx context.Context) (ipv6.SegmentsResponse, error)
}

// mountIPv6Routes registers docs/api.md's `GET /ipv6/segments` — gated on
// `netRead` per the task card's own explicit wording ("GET /ipv6/segments
// (netRead-gated)"), not `sdnRead` (the gate `GET /sdn`/`GET /ipam/*` use)
// — RA/DHCPv6 observation is host-local diagnostic data (the same
// category `GET /conntrack`/`GET /latmesh/heatmap` are gated on), not SDN
// config itself. svc == nil mirrors every other mountXRoutes function in
// this package: the route simply isn't mounted.
func mountIPv6Routes(r chi.Router, svc IPv6Service, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/ipv6/segments", handleIPv6Segments(svc))
	})
}

// handleIPv6Segments serves GET /ipv6/segments. Segments() itself tolerates
// individual node/peer failures (SegmentsResponse.Partial/FailedNodes,
// mirroring GET /sdn/evpn/status's cluster fan-out convention) rather than
// erroring the whole request, so a non-nil error here only ever means a
// structurally broken call — treated as 503, the same "degraded upstream"
// mapping handleEVPNStatus gives a Status() failure.
func handleIPv6Segments(svc IPv6Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp, err := svc.Segments(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "pve_unreachable", "could not read IPv6 segment observations")
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}
