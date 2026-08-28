// SPDX-License-Identifier: Apache-2.0

package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/topology"
)

// FDBService is the subset of *topology.Service the router needs for
// T-306's MAC/FDB browser (docs/features/lldp-discovery.md §4): the plain
// cluster-wide listing and the ranked partial-MAC search, both already
// ownership-labeled (guest/vnprox-known/unknown) by internal/topology.
type FDBService interface {
	FDB() []topology.FDBRow
	FDBSearch(q string) []topology.FDBRow
}

// mountFDBRoutes registers a single netRead-gated GET /fdb — this route is
// not yet named in docs/api.md; flagged in the T-306 completion report as a
// doc addition made in this change (docs/development.md's "definition of
// done" #4 allows this when the doc is updated in the same change, the same
// pattern T-302's ports/vlan-check routes and T-303's `links` peer route
// used).
func mountFDBRoutes(r chi.Router, svc FDBService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/fdb", handleFDB(svc))
	})
}

// handleFDB serves GET /fdb: with no ?mac= query, the full cluster-wide FDB
// listing (Tools → MAC search's initial "browse everything" state); with
// ?mac=<query>, FDBSearch's ranked partial-match results (spec §4: "search
// any MAC/partial → per-node bridge/port hits").
func handleFDB(svc FDBService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := strings.TrimSpace(r.URL.Query().Get("mac"))
		rows := svc.FDB()
		if q != "" {
			rows = svc.FDBSearch(q)
		}
		if rows == nil {
			rows = []topology.FDBRow{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": rows})
	}
}
