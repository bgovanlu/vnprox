// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/store"
)

// defaultSnapshotPageLimit and maxSnapshotPageLimit bound GET /snapshots'
// ?limit= query param (docs/api.md's pagination convention).
const (
	defaultSnapshotPageLimit = 50
	maxSnapshotPageLimit     = 200
)

// SnapshotService is the subset of *change.Service the router needs for
// docs/api.md's Snapshots / time machine routes. Declared as an interface
// (the same seam pattern as ChangesetService above) so this package's
// dependency on the concrete change.Service stays small and testable.
type SnapshotService interface {
	ListSnapshots(ctx context.Context, cursor string, limit int) ([]change.SnapshotSummary, string, error)
	GetSnapshot(ctx context.Context, id string) (change.SnapshotDetail, error)
	CreateManualSnapshot(ctx context.Context, author, note string) (change.SnapshotSummary, error)
	DiffSnapshots(ctx context.Context, from, to string) (*change.SnapshotDiff, error)
	RestoreSnapshot(ctx context.Context, author, id string) (change.Changeset, error)
}

// snapshotListResponse is GET /snapshots' response envelope: the requested
// page plus an opaque nextCursor (omitted once there is no further page),
// per docs/api.md's `?limit=&cursor=` pagination convention, plus (T-303)
// the same partial/failedNodes cluster-fan-out fields auditListResponse
// carries.
type snapshotListResponse struct {
	NextCursor  string                   `json:"nextCursor,omitempty"`
	Items       []change.SnapshotSummary `json:"items"`
	FailedNodes []string                 `json:"failedNodes,omitempty"`
	Partial     bool                     `json:"partial,omitempty"`
}

// snapshotCreateRequest is POST /snapshots' body: `{note}`.
type snapshotCreateRequest struct {
	Note string `json:"note"`
}

// mountSnapshotsRoutes registers docs/api.md's Snapshots / time machine
// routes. Reads (list/detail/diff) require netRead; the two writes (manual
// create, restore-to-draft) require netWrite plus CSRF, matching
// mountChangesetsRoutes' pattern — a snapshot restore is staged as a normal
// draft changeset, so it carries the same write capability the changesets
// write routes do, not a separate one.
func mountSnapshotsRoutes(r chi.Router, svc SnapshotService, auth AuthService, peers PeerSnapshotSource) {
	if svc == nil || auth == nil {
		return
	}
	lookup, ok := auth.(UsernameLookup)
	if !ok {
		return
	}

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/snapshots", handleListSnapshots(svc, peers))
		r.Get("/snapshots/diff", handleDiffSnapshots(svc))
		r.Get("/snapshots/{id}", handleGetSnapshot(svc))
	})

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capNetWrite))
		r.Post("/snapshots", handleCreateSnapshot(svc, lookup))
		r.Post("/snapshots/{id}/restore", handleRestoreSnapshot(svc, lookup))
	})
}

func handleListSnapshots(svc SnapshotService, peers PeerSnapshotSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := defaultSnapshotPageLimit
		if v := r.URL.Query().Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", "limit must be a positive integer")
				return
			}
			if n > maxSnapshotPageLimit {
				n = maxSnapshotPageLimit
			}
			limit = n
		}
		cursor := r.URL.Query().Get("cursor")

		if peers == nil {
			items, next, err := svc.ListSnapshots(r.Context(), cursor, limit)
			if err != nil {
				writeSnapshotError(w, err)
				return
			}
			if items == nil {
				items = []change.SnapshotSummary{}
			}
			writeJSON(w, http.StatusOK, snapshotListResponse{Items: items, NextCursor: next})
			return
		}

		items, next, partial, failed, err := fetchClusterSnapshots(r.Context(), svc, peers, cursor, limit)
		if err != nil {
			writeSnapshotError(w, err)
			return
		}
		if items == nil {
			items = []change.SnapshotSummary{}
		}
		writeJSON(w, http.StatusOK, snapshotListResponse{Items: items, NextCursor: next, Partial: partial, FailedNodes: failed})
	}
}

func handleGetSnapshot(svc SnapshotService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		detail, err := svc.GetSnapshot(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeSnapshotError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, detail)
	}
}

func handleDiffSnapshots(svc SnapshotService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		from := r.URL.Query().Get("from")
		to := r.URL.Query().Get("to")
		if from == "" || to == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "both from and to query params are required")
			return
		}
		diff, err := svc.DiffSnapshots(r.Context(), from, to)
		if err != nil {
			writeSnapshotError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, diff)
	}
}

func handleCreateSnapshot(svc SnapshotService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		var req snapshotCreateRequest
		if r.ContentLength != 0 {
			dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed request body")
				return
			}
		}
		summary, err := svc.CreateManualSnapshot(r.Context(), username, req.Note)
		if err != nil {
			writeSnapshotError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, summary)
	}
}

func handleRestoreSnapshot(svc SnapshotService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		draft, err := svc.RestoreSnapshot(r.Context(), username, chi.URLParam(r, "id"))
		if err != nil {
			writeSnapshotError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toChangesetResponse(draft))
	}
}

// writeSnapshotError maps this file's service errors to docs/api.md's error
// envelope + stable codes, reusing writeApplyError's mapping (Apply-not-
// configured, unsupported-restore, illegal-transition) since
// change.Service's snapshot methods surface the same error types.
func writeSnapshotError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "not_found", "no such snapshot")
		return
	}
	writeApplyError(w, err)
}
