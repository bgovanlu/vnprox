// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/runbook"
)

// RunbookPreparer is the subset of *runbook.Service the router needs:
// T-4003's single stage-only surface. Declared as a one-method seam (the
// same "small interface, adapted by the caller" convention every other
// cross-package Options field in this file already follows) so this
// package's dependency on internal/runbook stays narrow — *runbook.Service
// satisfies this directly, no adapter needed.
type RunbookPreparer interface {
	Prepare(ctx context.Context, author, findingID, runbookName string) (change.Changeset, error)
}

// mountRunbookRoutes registers `POST /findings/{id}/runbooks/{name}/prepare`
// (T-4003; not in the original docs/api.md contract — documented there in
// this same change per docs/development.md's definition-of-done #4, the
// same pattern mountFindingsRoutes' own POST /findings/{id}/fix addition
// used). Nil svc (not wired) leaves the route unmounted, the same
// degradation every other optional Options field in this router uses.
func mountRunbookRoutes(r chi.Router, svc RunbookPreparer, auth AuthService) {
	if svc == nil || auth == nil {
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
		// Preparing a runbook stages (and validates) a draft changeset —
		// the same capability every other changeset-staging write on this
		// surface (handleFindingsFix, POST /changesets) already requires.
		// It can never reach apply/confirm/rollback: internal/runbook's
		// changeCreator seam structurally has no such method (see that
		// package's stageonly.go).
		r.Use(auth.RequireCap(capNetWrite))
		r.Post("/findings/{id}/runbooks/{name}/prepare", handleRunbookPrepare(svc, lookup))
	})
}

func handleRunbookPrepare(svc RunbookPreparer, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		findingID := chi.URLParam(r, "id")
		runbookName := chi.URLParam(r, "name")

		cs, err := svc.Prepare(r.Context(), username, findingID, runbookName)
		switch {
		case err == nil:
			writeJSON(w, http.StatusCreated, toChangesetResponse(cs))
		case errors.Is(err, runbook.ErrFindingNotFound):
			writeJSONError(w, http.StatusNotFound, "not_found", "no such finding")
		case errors.Is(err, runbook.ErrRunbookNotFound):
			writeJSONError(w, http.StatusNotFound, "not_found", "no such runbook")
		case errors.Is(err, runbook.ErrNotAttached):
			writeJSONError(w, http.StatusNotFound, "not_found", "that runbook is not attached to this finding's check")
		case errors.Is(err, runbook.ErrNothingToDo):
			// Not a failure: the read-check found the underlying condition
			// already resolved (T-4002's own "matches live state -> stage
			// nothing" idempotency discipline, applied here). 409 rather
			// than 200, since — unlike an idempotent Ansible-style re-run —
			// nothing was staged for the caller to look at.
			writeJSONError(w, http.StatusConflict, "nothing_to_remediate", err.Error())
		default:
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not prepare the runbook")
		}
	}
}
