package api

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/doctor"
)

// DoctorLiveService runs the four checks that need a credential only the
// daemon holds — an authenticated PVE token, the peer HMAC secret, and a
// reference clock reachable through the first of those. *doctor* itself has no
// state; cmd/vnproxd supplies the probes.
type DoctorLiveService interface {
	RunLive(ctx context.Context) []doctor.Result
}

// mountDoctorRoutes registers `GET /doctor/live` (T-2406, closing
// T-1904-followup-02).
//
// Capability: the same one `/audit` and the support bundle need. This route
// reports whether the configured PVE token holds each privilege vnprox uses,
// and how many distinct cluster secrets exist across nodes. Neither is a
// secret, but both are exactly the kind of posture detail that belongs behind
// the audit capability rather than plain netRead.
func mountDoctorRoutes(r chi.Router, svc DoctorLiveService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capAudit))
		r.Get("/doctor/live", handleDoctorLive(svc))
	})
}

type doctorLiveResponse struct {
	Results []doctor.Result `json:"results"`
}

func handleDoctorLive(svc DoctorLiveService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		results := svc.RunLive(r.Context())
		if results == nil {
			results = []doctor.Result{}
		}
		writeJSON(w, http.StatusOK, doctorLiveResponse{Results: results})
	}
}
