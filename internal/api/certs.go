// SPDX-License-Identifier: Apache-2.0

package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/certs"
)

// CertsService is the subset of *certs.Service the router needs.
type CertsService interface {
	Report() certs.Report
}

// mountCertsRoutes registers `GET /certs` (T-2304): the cluster-wide
// certificate inventory plus everything currently wrong with it.
//
// Read-only and gated on ordinary network read — the same capability the rest
// of the read surface uses. There is deliberately no write route: vnprox does
// not renew or reissue certificates (planning/tasks/phase-23.md's scope note),
// so the response carries the exact command for each problem instead.
//
// The response body is built entirely from parsed certificate fields. No file
// contents, no PEM, and no key material can reach it — certs.Certificate
// carries no raw bytes at all, which makes that a property of the type rather
// than of care taken here.
func mountCertsRoutes(r chi.Router, svc CertsService, auth AuthService, scopeMW func(http.Handler) http.Handler) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		if scopeMW != nil {
			r.Use(scopeMW)
		}
		r.Get("/certs", handleCerts(svc))
	})
}

func handleCerts(svc CertsService) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		rep := svc.Report()
		// Normalize nils so the client always sees arrays, never null — the
		// same convention the rest of this package's list responses use.
		if rep.Inventory.Certificates == nil {
			rep.Inventory.Certificates = []certs.Certificate{}
		}
		if rep.Issues == nil {
			rep.Issues = []certs.Issue{}
		}
		writeJSON(w, http.StatusOK, rep)
	}
}
