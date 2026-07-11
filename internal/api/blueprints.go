// blueprints.go implements docs/api.md's Blueprints section
// (`GET/POST /blueprints`, `POST /blueprints/{id}/instantiate`) plus a
// handful of additive routes this task's UI needs that weren't in the
// original contract — documented here per docs/development.md's
// definition-of-done #4, the same convention T-302/T-303/T-305/T-306
// already used for their own additive routes:
//
//   - GET    /blueprints/{id}                 — single blueprint detail/export
//   - DELETE /blueprints/{id}                 — remove a saved (non-starter) blueprint
//   - POST   /blueprints/capture               — capture-from-node ("blueprint-ify")
//   - GET    /blueprints/{id}/suggest?param=   — next-free-address suggestion (AC4)

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/change"
)

// maxBlueprintBodyBytes bounds a save/instantiate/capture request body —
// mirrors changesets.go's maxChangesetBodyBytes reasoning (generous
// headroom against an abusive/buggy client, not a realistic limit for a
// blueprint's entity list).
const maxBlueprintBodyBytes = 4 << 20 // 4 MiB

// BlueprintService is the subset of *blueprint.Service the router needs.
// Declared as an interface (the seam pattern every other route file in
// this package uses) so this package's dependency on the concrete
// blueprint.Service stays small and testable without a real SQLite file
// or inventory graph.
type BlueprintService interface {
	List(ctx context.Context) ([]*blueprint.Blueprint, error)
	Get(ctx context.Context, id string) (*blueprint.Blueprint, error)
	Save(ctx context.Context, author string, bp *blueprint.Blueprint) (*blueprint.Blueprint, error)
	Delete(ctx context.Context, id string) error
	Capture(node string) (*blueprint.Blueprint, error)
	Instantiate(ctx context.Context, id string, req blueprint.InstantiateRequest) (ops []change.Op, title string, err error)
	SuggestAddress(ctx context.Context, id, paramName string) (string, error)
}

type blueprintsListResponse struct {
	Items []*blueprint.Blueprint `json:"items"`
}

type captureRequest struct {
	Node string `json:"node"`
}

type suggestResponse struct {
	Address string `json:"address"`
}

// mountBlueprintsRoutes registers the routes above. Reads are gated by
// netRead (same as every other read-only route in this package); writes
// (save/delete/capture/instantiate) are gated by netWrite + CSRF, matching
// changesets.go's own gate — instantiate only ever produces a *draft*
// changeset (through the same ChangesetService.Create every other
// programmatic op-drafting path, e.g. drift's fix route, already uses),
// never applies anything itself.
func mountBlueprintsRoutes(r chi.Router, svc BlueprintService, changesets ChangesetService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/blueprints", handleListBlueprints(svc))
		r.Get("/blueprints/{id}", handleGetBlueprint(svc))
		r.Get("/blueprints/{id}/suggest", handleSuggestAddress(svc))
	})

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
		r.Post("/blueprints", handleSaveBlueprint(svc, lookup))
		r.Delete("/blueprints/{id}", handleDeleteBlueprint(svc))
		r.Post("/blueprints/capture", handleCaptureBlueprint(svc))
		if changesets != nil {
			r.Post("/blueprints/{id}/instantiate", handleInstantiateBlueprint(svc, changesets, lookup))
		}
	})
}

func handleListBlueprints(svc BlueprintService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := svc.List(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list blueprints")
			return
		}
		if list == nil {
			list = []*blueprint.Blueprint{}
		}
		writeJSON(w, http.StatusOK, blueprintsListResponse{Items: list})
	}
}

func handleGetBlueprint(svc BlueprintService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		bp, err := svc.Get(r.Context(), id)
		if err != nil {
			writeBlueprintError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, bp)
	}
}

func handleSaveBlueprint(svc BlueprintService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		var bp blueprint.Blueprint
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBlueprintBodyBytes))
		if err := dec.Decode(&bp); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed blueprint body: "+err.Error())
			return
		}
		saved, err := svc.Save(r.Context(), username, &bp)
		if err != nil {
			writeBlueprintError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, saved)
	}
}

func handleDeleteBlueprint(svc BlueprintService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := svc.Delete(r.Context(), id); err != nil {
			writeBlueprintError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleCaptureBlueprint(svc BlueprintService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req captureRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBlueprintBodyBytes))
		if err := dec.Decode(&req); err != nil || req.Node == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "request body must be {\"node\": \"<node>\"}")
			return
		}
		bp, err := svc.Capture(req.Node)
		if err != nil {
			writeBlueprintError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, bp)
	}
}

func handleSuggestAddress(svc BlueprintService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		param := r.URL.Query().Get("param")
		if param == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "?param= is required")
			return
		}
		addr, err := svc.SuggestAddress(r.Context(), id, param)
		if err != nil {
			writeBlueprintError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, suggestResponse{Address: addr})
	}
}

func handleInstantiateBlueprint(svc BlueprintService, changesets ChangesetService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		id := chi.URLParam(r, "id")
		var req blueprint.InstantiateRequest
		if r.ContentLength != 0 {
			dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBlueprintBodyBytes))
			if err := dec.Decode(&req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", "malformed instantiate body: "+err.Error())
				return
			}
		}
		ops, title, err := svc.Instantiate(r.Context(), id, req)
		if err != nil {
			writeBlueprintError(w, err)
			return
		}
		c, err := changesets.Create(r.Context(), username, title, ops)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not create changeset")
			return
		}
		writeJSON(w, http.StatusCreated, toChangesetResponse(c))
	}
}

// writeBlueprintError maps the blueprint package's sentinel errors to the
// documented error envelope/status codes.
func writeBlueprintError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, blueprint.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, blueprint.ErrReadOnly):
		writeJSONError(w, http.StatusForbidden, "blueprint_read_only", err.Error())
	case errors.Is(err, blueprint.ErrInvalidParams), errors.Is(err, blueprint.ErrInvalid):
		writeJSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
	default:
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "internal error")
	}
}
