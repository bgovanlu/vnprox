package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/drift"
)

// DriftService is the subset of *drift.Service the router needs: the
// current findings list (docs/api.md's `GET /drift`) and the computed op
// patch for a fixable finding (T-305's "create fixing changeset" action —
// see mountDriftRoutes' doc comment on why the ops themselves are looked
// up server-side by finding ID rather than trusted from the request body).
type DriftService interface {
	Findings() []drift.Finding
	FixOps(id string) (ops []change.Op, title string, ok bool)
}

// mountDriftRoutes registers docs/api.md's `GET /drift` plus T-305's
// `POST /drift/{id}/fix` (not in the original api.md contract; documented
// there in this same change per docs/development.md's definition-of-done
// #4, the same pattern T-302's /lldp/vlan-check and /ports additions
// used). GET /drift is netRead-gated like every other read route; the fix
// route is netWrite + CSRF-gated like every changeset-creating route,
// since it creates a changeset draft through the normal engine (never
// applies anything itself — the returned draft still goes through the
// normal validate/apply/confirm review the changeset drawer always
// requires).
//
// The fix route looks its op patch up server-side by Finding.ID (via
// DriftService.FixOps, which internal/drift recomputes fresh against the
// live snapshot rather than trusting a cached value) instead of accepting
// a client-supplied op list: a finding can only ever be "fixed" with the
// exact ops this package's own check logic computed for it, closing off
// a class of "arbitrary op injection under the guise of a fix" bugs.
func mountDriftRoutes(r chi.Router, svc DriftService, changesets ChangesetService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/drift", handleDriftFindings(svc))
	})

	if changesets == nil {
		return
	}
	lookup, ok := auth.(UsernameLookup)
	if !ok {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capNetWrite))
		r.Post("/drift/{id}/fix", handleDriftFix(svc, changesets, lookup))
	})
}

// handleDriftFindings serves docs/api.md's documented bare-array shape
// (`[{check, severity, nodes, detail}]`, plus T-305's additive id/refs/
// fixable fields) — unlike most list routes in this package, GET /drift is
// not wrapped in an {items:[...]} envelope, matching the contract exactly.
func handleDriftFindings(svc DriftService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		findings := svc.Findings()
		if findings == nil {
			findings = []drift.Finding{}
		}
		writeJSON(w, http.StatusOK, findings)
	}
}

func handleDriftFix(svc DriftService, changesets ChangesetService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")
		ops, title, ok := svc.FixOps(id)
		if !ok {
			writeJSONError(w, http.StatusNotFound, "not_found", "no fixable drift finding with that id")
			return
		}
		c, err := changesets.Create(r.Context(), username, title, ops)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not create fixing changeset")
			return
		}
		writeJSON(w, http.StatusCreated, toChangesetResponse(c))
	}
}
