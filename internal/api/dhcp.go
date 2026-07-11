package api

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/ipam"
)

// DHCPService is the subset of T-406's *ipam.Service the router needs:
// docs/api.md's `GET /sdn/dhcp` (static reservations + live leases, per
// docs/features/sdn.md §5). Declared as an interface (the same seam
// pattern as SDNService/EVPNService above) so this package's dependency on
// the concrete service stays small; *ipam.Service's own DHCP method
// satisfies this directly.
type DHCPService interface {
	DHCP(ctx context.Context, zone string) (ipam.DHCPView, error)
}

// mountDHCPRoutes registers docs/api.md's `GET /sdn/dhcp?zone=` — gated on
// the same capIPAMRead (== sdnRead) capability GET /ipam/* uses, since DHCP
// reservations/leases are the same SDN-adjacent read surface. svc == nil
// (no PVE client wired) simply skips mounting, matching every other
// optional Options field's degraded-mode treatment.
func mountDHCPRoutes(r chi.Router, svc DHCPService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capIPAMRead))
		r.Get("/sdn/dhcp", handleDHCP(svc))
	})
}

func handleDHCP(svc DHCPService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		view, err := svc.DHCP(r.Context(), r.URL.Query().Get("zone"))
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "pve_unreachable", "could not read DHCP reservations/leases from PVE")
			return
		}
		writeJSON(w, http.StatusOK, view)
	}
}
