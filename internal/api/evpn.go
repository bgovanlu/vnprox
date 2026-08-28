// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/evpn"
)

// EVPNService is the subset of T-404's *evpn.Service the router needs:
// docs/api.md's GET /sdn/evpn/status (cluster-wide FRR/BGP peering state,
// EVPN VNI list, exit-node health, flapping findings). Declared as an
// interface (the same seam pattern as SDNService above) so this package's
// dependency on the concrete service stays small.
type EVPNService interface {
	Status(ctx context.Context) (evpn.Status, error)
}

// mountEVPNRoutes registers docs/api.md's `GET /sdn/evpn/status` — gated
// on the same sdnRead capability GET /sdn uses (this is EVPN/BGP
// observability for the SDN cockpit, not a distinct privilege domain).
// svc == nil (collectors' PVE/peer clients not wired) mirrors every other
// mountXRoutes function in this package: the route simply isn't mounted.
func mountEVPNRoutes(r chi.Router, svc EVPNService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capSDNRead))
		r.Get("/sdn/evpn/status", handleEVPNStatus(svc))
	})
}

// handleEVPNStatus serves GET /sdn/evpn/status. Status() itself tolerates
// individual node/peer failures (Status.Partial/FailedNodes, mirroring
// GET /audit/GET /snapshots' cluster fan-out convention) rather than
// erroring the whole request, so a non-nil error here only ever means a
// structurally broken call (e.g. a nil dependency Status forgot to guard) —
// treated as 503, the same "degraded upstream" mapping GET /sdn gives a
// Tree() failure.
func handleEVPNStatus(svc EVPNService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, err := svc.Status(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "pve_unreachable", "could not read EVPN/BGP status")
			return
		}
		writeJSON(w, http.StatusOK, status)
	}
}
