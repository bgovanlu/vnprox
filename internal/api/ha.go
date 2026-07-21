// ha.go implements T-1704's GET /ha/status: this daemon's HA role (active/
// standby), current lease term and expiry, and replication lag. Read-only and
// netRead-gated — HA control (promotion/demotion) is never an API action, it
// is driven solely by the fenced lease arbitration in internal/ha.

package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/ha"
)

// HAStatusService is the GET /ha/status seam — *ha.Manager satisfies it via
// its Status method. Nil (HA disabled) simply skips mounting the route, the
// same degraded-mode convention every other optional Options field uses.
type HAStatusService interface {
	Status() ha.Status
}

// mountHARoutes registers GET /ha/status behind session auth + netRead.
func mountHARoutes(r chi.Router, svc HAStatusService, auth AuthService) {
	if svc == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/ha/status", handleHAStatus(svc))
	})
}

func handleHAStatus(svc HAStatusService) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, svc.Status())
	}
}
