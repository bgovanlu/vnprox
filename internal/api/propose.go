// propose.go implements T-2702's two routes: proposing a changeset as a pull
// request against the spec repository, and reading back what was opened.
//
// `POST /changesets/{id}/propose` is a WRITE — it commits and pushes to a
// remote repository — so it sits with the other netWrite+CSRF changeset
// routes. It is emphatically NOT an apply: nothing about the cluster changes,
// and the changeset itself is not mutated (internal/gitsync's ChangesetReader
// seam has one method, Get). What comes back is a URL a human opens.
//
// `GET /changesets/{id}/proposal` is the review-surface link: the recorded
// pull request for a changeset, or 404 when it has never been proposed.
//
// Both routes are mounted whether or not the subsystem is configured, exactly
// like `GET /gitsync/status` (T-2701): "proposing is not set up on this
// deployment" is an answer an operator needs, and a route that appears and
// disappears with configuration is worse for a client than one that always
// answers honestly.

package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/gitsync"
	"github.com/bgovanlu/vnprox/internal/store"
)

// ChangesetProposer is the seam these routes are served from —
// *gitsync.Proposer satisfies it directly. It carries no apply, confirm or
// stage verb: proposing reads a changeset and writes to a git host, and this
// interface is the whole of what the HTTP layer can ask for.
type ChangesetProposer interface {
	Enabled() bool
	Propose(ctx context.Context, changesetID, actor string) (gitsync.Proposal, error)
	Get(ctx context.Context, changesetID string) (gitsync.Proposal, error)
}

// mountProposeRoutes registers the two routes above.
func mountProposeRoutes(r chi.Router, svc ChangesetProposer, auth AuthService) {
	if auth == nil {
		return
	}
	lookup, ok := auth.(UsernameLookup)
	if !ok {
		// No safe way to attribute a proposal to a user, so no proposing —
		// the same reasoning mountChangesetsRoutes applies to every mutating
		// changeset route.
		return
	}

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/changesets/{id}/proposal", handleGetProposal(svc))
	})

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capNetWrite))
		r.Post("/changesets/{id}/propose", handlePropose(svc, lookup))
	})
}

func handlePropose(svc ChangesetProposer, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil || !svc.Enabled() {
			writeJSONError(w, http.StatusNotImplemented, "not_implemented",
				"proposing a changeset to a git repository is not configured on this deployment ([gitsync] push_token_file)")
			return
		}
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		proposal, err := svc.Propose(r.Context(), chi.URLParam(r, "id"), username)
		if err != nil {
			writeProposeError(w, err)
			return
		}
		status := http.StatusOK
		if proposal.Created {
			status = http.StatusCreated
		}
		writeJSON(w, status, proposal)
	}
}

func handleGetProposal(svc ChangesetProposer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeJSONError(w, http.StatusNotFound, "not_found", "this changeset has not been proposed")
			return
		}
		proposal, err := svc.Get(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeProposeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, proposal)
	}
}

// writeProposeError maps the propose path's sentinels onto docs/api.md's
// error shape.
//
// The distinction that matters: a changeset the spec cannot express, or one
// that would not round-trip, is a 422 naming exactly what is wrong — an
// operator can act on "this changeset deletes a bridge, and the spec has no
// way to say that". A host that refused us is a 502: nothing about the
// request was wrong.
func writeProposeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, gitsync.ErrProposeNotConfigured):
		writeJSONError(w, http.StatusNotImplemented, "not_implemented",
			"proposing a changeset to a git repository is not configured on this deployment ([gitsync] push_token_file)")
	case errors.Is(err, gitsync.ErrNoProposal), errors.Is(err, store.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, gitsync.ErrNothingToPropose):
		writeJSONError(w, http.StatusUnprocessableEntity, "nothing_to_propose", err.Error())
	case errors.Is(err, gitsync.ErrNotProposable):
		writeJSONError(w, http.StatusUnprocessableEntity, "not_proposable", err.Error())
	case errors.Is(err, gitsync.ErrNotExpressible):
		writeJSONError(w, http.StatusUnprocessableEntity, "not_expressible_in_spec", err.Error())
	case errors.Is(err, gitsync.ErrRoundTrip):
		writeJSONError(w, http.StatusUnprocessableEntity, "spec_round_trip_failed", err.Error())
	case errors.Is(err, gitsync.ErrNoSpecDocument):
		writeJSONError(w, http.StatusUnprocessableEntity, "no_spec_document", err.Error())
	case errors.Is(err, gitsync.ErrUnreachable), errors.Is(err, gitsync.ErrRemoteStatus):
		writeJSONError(w, http.StatusBadGateway, "remote_unreachable", err.Error())
	default:
		writeJSONError(w, http.StatusBadGateway, "propose_failed", err.Error())
	}
}
