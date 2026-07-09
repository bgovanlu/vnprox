package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/store"
)

// capNetWrite is docs/api.md's documented write-capability flag name
// (internal/auth.CapNetWrite's underlying string), spelled out as a plain
// string for the same reason topology.go's capNetRead is (see that
// constant's doc comment): keeping this package's auth dependency to the
// AuthService method-seam interface.
const capNetWrite = "netWrite"

// maxChangesetBodyBytes bounds a draft create/update request body. A
// changeset with, say, a hundred ops is at most a few tens of KB even with
// verbose params (fw rule lists, SDN objects); this ceiling is generous
// headroom against an abusive/buggy client, not a realistic limit.
const maxChangesetBodyBytes = 4 << 20 // 4 MiB

// ChangesetService is the subset of *change.Service the router needs: T-201's
// draft CRUD plus T-202's Validate. Declared as an interface (the same seam
// pattern as AuthService/TopologyService/LayoutStore above) so this
// package's dependency on the concrete change.Service stays small and
// testable without a real SQLite file. Diff/Apply/Confirm/Rollback are
// deliberately not part of this seam — those routes remain registered but
// stubbed 501 (see mountChangesetsRoutes), since T-205 owns the logic
// behind them.
type ChangesetService interface {
	List(ctx context.Context, status string) ([]change.Changeset, error)
	Get(ctx context.Context, id string) (change.Changeset, error)
	Create(ctx context.Context, author, title string, ops []change.Op) (change.Changeset, error)
	UpdateDraft(ctx context.Context, id, author string, title *string, ops []change.Op) (change.Changeset, error)
	Discard(ctx context.Context, id, author string) error
	Validate(ctx context.Context, id, author string) (change.Changeset, error)
}

// CSRFEnforcer is implemented by AuthService backends that can check the
// double-submit CSRF header (internal/auth.Service.CSRFMiddleware, per
// docs/api.md's conventions section: "X-VNPROX-CSRF header on mutating
// requests"). It is checked with a type assertion — the same pattern
// UsernameLookup uses just above in layouts.go — rather than folded into
// the AuthService interface itself, so existing AuthService test doubles
// that don't need CSRF behavior (this package's own fakeAuth) don't have
// to grow a method just because the changesets routes need one. If auth
// doesn't implement this, the mutating changesets routes still mount
// (unlike the UsernameLookup case, where there'd be no safe author to
// record at all) but skip CSRF enforcement — acceptable only for test
// doubles; cmd/vnproxd's real authServiceAdapter always implements it via
// the embedded *auth.Service.
type CSRFEnforcer interface {
	CSRFMiddleware(next http.Handler) http.Handler
}

// changesetResponse is the wire shape of a changeset, per docs/api.md's
// changesets section ("GET /changesets/{id} — full changeset incl.
// findings, plan, apply log"). Findings is never emitted as a JSON null
// (an empty array instead) so frontend code can always range over it
// without a nil check.
type changesetResponse struct {
	Plan            json.RawMessage  `json:"plan,omitempty"`
	ApplyLog        json.RawMessage  `json:"applyLog,omitempty"`
	ConfirmDeadline *int64           `json:"confirmDeadline,omitempty"`
	ID              string           `json:"id"`
	Title           string           `json:"title"`
	Author          string           `json:"author"`
	Status          string           `json:"status"`
	Ops             []change.Op      `json:"ops"`
	Findings        []change.Finding `json:"findings"`
	CreatedAt       int64            `json:"createdAt"`
	UpdatedAt       int64            `json:"updatedAt"`
}

func toChangesetResponse(c change.Changeset) changesetResponse {
	ops := c.Ops
	if ops == nil {
		ops = []change.Op{}
	}
	findings := c.Findings
	if findings == nil {
		findings = []change.Finding{}
	}
	return changesetResponse{
		ID: c.ID, Title: c.Title, Author: c.Author, Status: string(c.Status),
		Ops: ops, Findings: findings, Plan: c.Plan, ApplyLog: c.ApplyLog,
		ConfirmDeadline: c.ConfirmDeadline, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

// mountChangesetsRoutes registers docs/api.md's changesets routes: the
// T-201 draft CRUD (list/create/get/update-draft/delete-draft) and T-202's
// validate for real, and diff/apply/confirm/rollback as registered-but-501
// stubs so the API surface matches the doc now, ahead of T-205 filling
// them in. Read routes require netRead; every mutating route requires
// netWrite plus (when the auth backend supports it — see CSRFEnforcer) a
// valid CSRF header.
//
// svc and auth are nil-safe to call with (routes simply aren't mounted),
// matching mountTopologyRoutes/mountLayoutsRoutes' pattern. If auth
// doesn't also implement UsernameLookup, the routes are likewise not
// mounted — same reasoning as mountLayoutsRoutes: there would be no safe
// way to attribute a created/discarded changeset to a user for the
// audit trail docs/security.md requires.
func mountChangesetsRoutes(r chi.Router, svc ChangesetService, auth AuthService) {
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
		r.Get("/changesets", handleListChangesets(svc))
		r.Get("/changesets/{id}", handleGetChangeset(svc))
		r.Get("/changesets/{id}/diff", handleChangesetNotImplemented())
	})

	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capNetWrite))
		r.Post("/changesets", handleCreateChangeset(svc, lookup))
		r.Put("/changesets/{id}", handleUpdateChangeset(svc, lookup))
		r.Delete("/changesets/{id}", handleDiscardChangeset(svc, lookup))
		r.Post("/changesets/{id}/validate", handleValidateChangeset(svc, lookup))
		r.Post("/changesets/{id}/apply", handleChangesetNotImplemented())
		r.Post("/changesets/{id}/confirm", handleChangesetNotImplemented())
		r.Post("/changesets/{id}/rollback", handleChangesetNotImplemented())
	})
}

// handleChangesetNotImplemented backs the diff/apply/confirm/rollback
// routes: they exist (matching docs/api.md's route shape) so dependent
// frontend/task work can wire against the real paths today, but return 501
// until T-205 implements the actual logic.
func handleChangesetNotImplemented() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSONError(w, http.StatusNotImplemented, "not_implemented", "not implemented yet")
	}
}

func handleListChangesets(svc ChangesetService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := r.URL.Query().Get("status")
		changesets, err := svc.List(r.Context(), status)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list changesets")
			return
		}
		out := make([]changesetResponse, len(changesets))
		for i, c := range changesets {
			out[i] = toChangesetResponse(c)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func handleGetChangeset(svc ChangesetService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		c, err := svc.Get(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeJSONError(w, http.StatusNotFound, "not_found", "no such changeset")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not load changeset")
			return
		}
		writeJSON(w, http.StatusOK, toChangesetResponse(c))
	}
}

// createChangesetRequest is docs/api.md's POST /changesets body:
// `{title, ops:[Op]}`.
type createChangesetRequest struct {
	Title string      `json:"title"`
	Ops   []change.Op `json:"ops"`
}

func handleCreateChangeset(svc ChangesetService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}

		req, decErr := decodeChangesetRequest(w, r)
		if decErr != nil {
			writeOpDecodeError(w, decErr)
			return
		}

		c, err := svc.Create(r.Context(), username, req.Title, req.Ops)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not create changeset")
			return
		}
		writeJSON(w, http.StatusCreated, toChangesetResponse(c))
	}
}

// updateChangesetRequest is docs/api.md's PUT /changesets/{id} body:
// `{ops:[Op]}`. Title is an additional, optional field this package
// accepts so a parked draft can be renamed via the same endpoint
// (docs/features/change-management.md §1: "Multiple named drafts can be
// parked and resumed") without a second route docs/api.md doesn't
// document.
type updateChangesetRequest struct {
	Title *string     `json:"title,omitempty"`
	Ops   []change.Op `json:"ops"`
}

func handleUpdateChangeset(svc ChangesetService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")

		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxChangesetBodyBytes))
		dec.DisallowUnknownFields()
		var req updateChangesetRequest
		if err := dec.Decode(&req); err != nil {
			var opErr *change.OpDecodeError
			if errors.As(err, &opErr) {
				writeOpDecodeError(w, opErr)
				return
			}
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed request body")
			return
		}

		c, err := svc.UpdateDraft(r.Context(), id, username, req.Title, req.Ops)
		if err != nil {
			writeChangesetMutationError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toChangesetResponse(c))
	}
}

func handleDiscardChangeset(svc ChangesetService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")

		if err := svc.Discard(r.Context(), id, username); err != nil {
			writeChangesetMutationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleValidateChangeset backs `POST /changesets/{id}/validate`
// (docs/api.md: "re-run validation, returns findings") with T-202's real
// pipeline.
func handleValidateChangeset(svc ChangesetService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")

		c, err := svc.Validate(r.Context(), id, username)
		if err != nil {
			writeChangesetMutationError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toChangesetResponse(c))
	}
}

// decodeChangesetRequest strictly decodes a POST /changesets body,
// bounding it to maxChangesetBodyBytes.
func decodeChangesetRequest(w http.ResponseWriter, r *http.Request) (createChangesetRequest, *change.OpDecodeError) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxChangesetBodyBytes))
	dec.DisallowUnknownFields()
	var req createChangesetRequest
	if err := dec.Decode(&req); err != nil {
		var opErr *change.OpDecodeError
		if errors.As(err, &opErr) {
			return createChangesetRequest{}, opErr
		}
		return createChangesetRequest{}, &change.OpDecodeError{Path: "", Message: "malformed request body"}
	}
	return req, nil
}

// writeOpDecodeError translates a *change.OpDecodeError into docs/api.md's
// `validation_failed` error envelope, with the offending JSON path in
// `details.path` (T-201 acceptance criterion 1).
func writeOpDecodeError(w http.ResponseWriter, err *change.OpDecodeError) {
	writeJSONErrorDetails(w, http.StatusBadRequest, "validation_failed", err.Message, map[string]any{"path": err.Path})
}

// writeJSONErrorDetails is writeJSONError (router.go) plus docs/api.md's
// optional `details` object — router.go's own helper has no details
// parameter, and this package's other routes have never needed one, so
// this is kept local to the one handler set that does rather than
// widening every existing writeJSONError call site's signature.
func writeJSONErrorDetails(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
			"details": details,
		},
	})
}

// writeChangesetMutationError maps UpdateDraft/Discard errors to HTTP
// status per docs/api.md's error envelope: a missing changeset is 404; an
// illegal status transition (e.g. discarding an already-applied
// changeset) is 409 — a stable code this package introduces since
// docs/api.md's error-code list is explicitly non-exhaustive ("...").
func writeChangesetMutationError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "not_found", "no such changeset")
		return
	}
	var illegal *change.ErrIllegalTransition
	if errors.As(err, &illegal) {
		writeJSONError(w, http.StatusConflict, "invalid_transition", err.Error())
		return
	}
	writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not update changeset")
}
