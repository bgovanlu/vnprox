package api

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/sdn"
)

// SDNService is the subset of T-401's *sdn.Service the router needs:
// docs/api.md's GET /sdn tree (zones -> vnets -> subnets, per-node apply/
// health status, and the staged-vs-running pending diff). Declared as an
// interface (the same pattern as TopologyService/FDBService above) so this
// package's dependency on the concrete service stays a small, explicit
// seam.
type SDNService interface {
	Tree(ctx context.Context) (sdn.Tree, error)
}

// capSDNRead is docs/api.md's documented SDN-read capability flag name
// (internal/auth.CapSDNRead's underlying string) — see capNetRead's doc
// comment in topology.go for why this is spelled out as a plain string
// rather than importing internal/auth's Cap type.
const capSDNRead = "sdnRead"

// mountSDNRoutes registers docs/api.md's `GET /sdn` — gated on the sdnRead
// capability specifically (not netRead), matching /auth/me's documented
// per-node capability flags and the fact that a user can have netRead
// without SDN.Audit (docs/architecture.md §6's PVE-privilege-to-capability
// mapping). svc == nil (SDN read service not wired — e.g. the collectors'
// PVE client failed to initialize, see cmd/vnproxd/collect.go) mirrors
// every other mountXRoutes function in this package: the route simply
// isn't mounted, giving a plain 404 rather than a 5xx for a daemon running
// in that degraded mode.
func mountSDNRoutes(r chi.Router, svc SDNService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capSDNRead))
		r.Get("/sdn", handleSDNTree(svc))
	})
}

// handleSDNTree serves GET /sdn. A Tree() failure (PVE unreachable, auth
// failure against the daemon's own read identity, ...) maps to 503 — the
// same "collectors/PVE degraded" treatment other read routes give a failed
// upstream, rather than a 500 that would imply a vnproxd-side bug.
func handleSDNTree(svc SDNService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tree, err := svc.Tree(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "pve_unreachable", "could not read SDN configuration from PVE")
			return
		}
		writeJSON(w, http.StatusOK, tree)
	}
}
