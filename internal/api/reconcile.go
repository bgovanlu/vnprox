// reconcile.go implements T-2703's two symmetric routes on a drift finding,
// plus the read that links what adopting already opened.
//
//	POST /drift/{id}/restore-intent   stage a changeset bringing the cluster
//	                                  back to what the spec declares
//	POST /drift/{id}/adopt-reality    propose a spec commit matching the
//	                                  cluster (a pull request)
//	GET  /drift/{id}/adoption         the pull request this finding was
//	                                  already adopted as, or 404
//
// Both writes take the SAME gate as every other changeset-creating route
// (session + netWrite + CSRF), and both name only a finding id: the ops and
// the refs are looked up server-side by internal/drift, never accepted from
// the body, exactly like POST /drift/{id}/fix. That is what stops a caller
// from widening an adoption past the entity its finding is about.
//
// Neither is an apply. Restoring intent produces a DRAFT the operator still
// takes through validate/apply/confirm; adopting changes nothing about the
// cluster at all. The routes are mounted whether or not the spec repository is
// configured, like GET /gitsync/status and the propose routes — "adopting is
// not set up on this deployment" is an answer an operator needs, and a route
// that appears and disappears with configuration is worse for a client than
// one that always answers honestly.

package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/gitsync"
	"github.com/bgovanlu/vnprox/internal/reconcile"
)

// DriftReconciler is the seam these routes are served from —
// *reconcile.Service satisfies it directly.
//
// Note what it does NOT carry: no apply, no confirm, no merge. Restoring
// intent can only stage, and adopting can only open a request, because the
// service behind this interface holds interfaces with no other verb.
type DriftReconciler interface {
	AdoptEnabled() bool
	RestoreIntent(ctx context.Context, findingID, actor string) (change.Changeset, error)
	AdoptReality(ctx context.Context, findingID, actor string) (gitsync.Proposal, error)
	Adoption(ctx context.Context, findingID string) (gitsync.Proposal, error)
}

// mountReconcileRoutes registers the three routes above.
func mountReconcileRoutes(r chi.Router, svc DriftReconciler, auth AuthService) {
	if auth == nil {
		return
	}
	lookup, ok := auth.(UsernameLookup)
	if !ok {
		// No safe way to attribute the action to a user, so no action — the
		// same reasoning every other mutating route in this package applies.
		return
	}

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/drift/{id}/adoption", handleGetAdoption(svc))
	})

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capNetWrite))
		r.Post("/drift/{id}/restore-intent", handleRestoreIntent(svc, lookup))
		r.Post("/drift/{id}/adopt-reality", handleAdoptReality(svc, lookup))
	})
}

func handleRestoreIntent(svc DriftReconciler, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeJSONError(w, http.StatusNotFound, "not_found", "no drift finding with that id offers restoring intent")
			return
		}
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		cs, err := svc.RestoreIntent(r.Context(), chi.URLParam(r, "id"), username)
		if err != nil {
			writeReconcileError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toChangesetResponse(cs))
	}
}

func handleAdoptReality(svc DriftReconciler, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil || !svc.AdoptEnabled() {
			writeJSONError(w, http.StatusNotImplemented, "not_implemented",
				"adopting live state into the spec repository is not configured on this deployment ([gitsync] push_token_file)")
			return
		}
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		proposal, err := svc.AdoptReality(r.Context(), chi.URLParam(r, "id"), username)
		if err != nil {
			writeReconcileError(w, err)
			return
		}
		status := http.StatusOK
		if proposal.Created {
			status = http.StatusCreated
		}
		writeJSON(w, status, proposal)
	}
}

func handleGetAdoption(svc DriftReconciler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeJSONError(w, http.StatusNotFound, "not_found", "this finding has not been adopted")
			return
		}
		proposal, err := svc.Adoption(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeReconcileError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, proposal)
	}
}

// writeReconcileError maps the reconcile path's sentinels onto docs/api.md's
// error shape.
//
// The distinction that matters: a finding that does not offer the action is a
// 404 — the resource being asked for (this finding's restore-intent artifact)
// does not exist, and never did. Everything past that is the propose path's
// own vocabulary, mapped exactly as POST /changesets/{id}/propose maps it, so
// a client handles one set of codes for both.
func writeReconcileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, reconcile.ErrNotOffered):
		writeJSONError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, reconcile.ErrAdoptNotConfigured):
		writeJSONError(w, http.StatusNotImplemented, "not_implemented", err.Error())
	default:
		writeProposeError(w, err)
	}
}
