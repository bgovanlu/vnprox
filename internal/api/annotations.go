// SPDX-License-Identifier: Apache-2.0

// annotations.go implements the map annotation layer: T-907's free-text
// sticky notes pinned to a map entity Ref, and T-2806's labelled canvas
// regions plus the notes' optional expiry and orphan reporting. Both are
// app-owned data per CLAUDE.md's storage rule — `content`/`label` are free
// text this daemon never interprets, never a copy of any PVE-owned field
// (docs/data-model.md §2, migrations 0006_annotations.sql and
// 0045_map_annotations.sql).
//
//   - GET    /annotations        — list live notes (`?includeExpired=true` for all)
//   - POST   /annotations        — pin a note {ref, content, expiresAt?}
//   - DELETE /annotations/{id}   — unpin a note
//   - GET    /map-regions        — list live regions (`?includeExpired=true` for all)
//   - POST   /map-regions        — draw a region {label, x, y, w, h, color?, expiresAt?}
//   - DELETE /map-regions/{id}   — remove a region
//
// Gated by the same session + netRead capability as the layouts routes,
// with no CSRF requirement — matching that package's precedent doc
// comment exactly: pinning a sticky note or drawing a region on the shared
// map is a personal-team UI action, not a network-mutating one, so no
// higher capability or CSRF requirement applies (see docs/api.md's Saved
// views & annotations section for the full rationale). Any authenticated
// netRead-capable user may list, create, or delete any annotation or
// region — there is no per-note ownership ACL, since this is a shared team
// scratchpad, not private per-user data like layouts; `createdBy` is
// display/audit metadata only.
//
// Two contract points this file deliberately does NOT implement itself,
// because internal/annotate owns them for the whole feature (its doc.go
// has the reasoning): expiry is computed at read time against one injected
// clock, and orphan status is derived from the live inventory rather than
// stored. This file only renders what that read model returns.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/annotate"
)

// maxAnnotationBodyBytes bounds a create request body — a generous cap for
// a free-text sticky note, mirroring the reasoning behind blueprints.go's
// maxBlueprintBodyBytes.
const maxAnnotationBodyBytes = 64 << 10 // 64 KiB

// AnnotationService is the subset of *annotate.Service the router needs.
// Declared as an interface (the same seam pattern LayoutStore above uses)
// so this package's dependency on that read model stays small and testable.
type AnnotationService interface {
	Notes(ctx context.Context, includeExpired bool) ([]annotate.Note, error)
	CreateNote(ctx context.Context, in annotate.NoteInput) (annotate.Note, error)
	DeleteNote(ctx context.Context, id string) error
	Regions(ctx context.Context, includeExpired bool) ([]annotate.Region, error)
	CreateRegion(ctx context.Context, in annotate.RegionInput) (annotate.Region, error)
	DeleteRegion(ctx context.Context, id string) error
}

// annotationResponse is the wire shape of one annotation, on both
// GET /annotations' items and POST /annotations' created response.
type annotationResponse struct {
	ID        string `json:"id"`
	Ref       string `json:"ref"`
	Content   string `json:"content"`
	CreatedBy string `json:"createdBy"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	ExpiresAt int64  `json:"expiresAt"`
	Expired   bool   `json:"expired"`
	Orphaned  bool   `json:"orphaned"`
}

type annotationsListResponse struct {
	Items []annotationResponse `json:"items"`
}

// annotationCreateRequest is POST /annotations' request body.
type annotationCreateRequest struct {
	Ref       string `json:"ref"`
	Content   string `json:"content"`
	ExpiresAt int64  `json:"expiresAt"`
}

// mapRegionResponse is the wire shape of one canvas region.
type mapRegionResponse struct {
	ID        string  `json:"id"`
	Label     string  `json:"label"`
	Color     string  `json:"color"`
	CreatedBy string  `json:"createdBy"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	W         float64 `json:"w"`
	H         float64 `json:"h"`
	CreatedAt int64   `json:"createdAt"`
	UpdatedAt int64   `json:"updatedAt"`
	ExpiresAt int64   `json:"expiresAt"`
	Expired   bool    `json:"expired"`
}

type mapRegionsListResponse struct {
	Items []mapRegionResponse `json:"items"`
}

// mapRegionCreateRequest is POST /map-regions' request body.
type mapRegionCreateRequest struct {
	Label     string  `json:"label"`
	Color     string  `json:"color"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	W         float64 `json:"w"`
	H         float64 `json:"h"`
	ExpiresAt int64   `json:"expiresAt"`
}

func toAnnotationResponse(n annotate.Note) annotationResponse {
	return annotationResponse{
		ID: n.ID, Ref: n.Ref, Content: n.Content,
		CreatedBy: n.CreatedBy, CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt,
		ExpiresAt: n.ExpiresAt, Expired: n.Expired, Orphaned: n.Orphaned,
	}
}

func toMapRegionResponse(m annotate.Region) mapRegionResponse {
	return mapRegionResponse{
		ID: m.ID, Label: m.Label, Color: m.Color, CreatedBy: m.CreatedBy,
		X: m.X, Y: m.Y, W: m.W, H: m.H,
		CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
		ExpiresAt: m.ExpiresAt, Expired: m.Expired,
	}
}

// includeExpired reads the shared `?includeExpired=true` flag. Absent or
// anything else means the default, live-only view — the one every display
// surface uses.
func includeExpired(r *http.Request) bool {
	return r.URL.Query().Get("includeExpired") == "true"
}

// mountAnnotationsRoutes registers the routes above. svc/auth are nil-safe
// to call with (routes simply aren't mounted, matching mountLayoutsRoutes'
// pattern); if auth doesn't also implement UsernameLookup, the routes are
// likewise not mounted, since there would be no way to stamp createdBy on
// a new note or region.
func mountAnnotationsRoutes(r chi.Router, svc AnnotationService, auth AuthService) {
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
		r.Get("/annotations", handleListAnnotations(svc))
		r.Post("/annotations", handleCreateAnnotation(svc, lookup))
		r.Delete("/annotations/{id}", handleDeleteAnnotation(svc))
		r.Get("/map-regions", handleListMapRegions(svc))
		r.Post("/map-regions", handleCreateMapRegion(svc, lookup))
		r.Delete("/map-regions/{id}", handleDeleteMapRegion(svc))
	})
}

func handleListAnnotations(svc AnnotationService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := svc.Notes(r.Context(), includeExpired(r))
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list annotations")
			return
		}
		items := make([]annotationResponse, 0, len(list))
		for _, n := range list {
			items = append(items, toAnnotationResponse(n))
		}
		writeJSON(w, http.StatusOK, annotationsListResponse{Items: items})
	}
}

func handleCreateAnnotation(svc AnnotationService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		var req annotationCreateRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAnnotationBodyBytes))
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "request body must be {\"ref\": \"...\", \"content\": \"...\"}")
			return
		}
		note, err := svc.CreateNote(r.Context(), annotate.NoteInput{
			Ref: req.Ref, Content: req.Content, CreatedBy: username, ExpiresAt: req.ExpiresAt,
		})
		if err != nil {
			if errors.Is(err, annotate.ErrInvalid) {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not save annotation")
			return
		}
		writeJSON(w, http.StatusCreated, toAnnotationResponse(note))
	}
}

func handleDeleteAnnotation(svc AnnotationService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := svc.DeleteNote(r.Context(), id); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not delete annotation")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleListMapRegions(svc AnnotationService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := svc.Regions(r.Context(), includeExpired(r))
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list map regions")
			return
		}
		items := make([]mapRegionResponse, 0, len(list))
		for _, m := range list {
			items = append(items, toMapRegionResponse(m))
		}
		writeJSON(w, http.StatusOK, mapRegionsListResponse{Items: items})
	}
}

func handleCreateMapRegion(svc AnnotationService, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		var req mapRegionCreateRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAnnotationBodyBytes))
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "request body must be {\"label\": \"...\", \"x\": 0, \"y\": 0, \"w\": 1, \"h\": 1}")
			return
		}
		region, err := svc.CreateRegion(r.Context(), annotate.RegionInput{
			Label: req.Label, Color: req.Color, CreatedBy: username,
			X: req.X, Y: req.Y, W: req.W, H: req.H, ExpiresAt: req.ExpiresAt,
		})
		if err != nil {
			if errors.Is(err, annotate.ErrInvalid) {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", err.Error())
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not save map region")
			return
		}
		writeJSON(w, http.StatusCreated, toMapRegionResponse(region))
	}
}

func handleDeleteMapRegion(svc AnnotationService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := svc.DeleteRegion(r.Context(), id); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not delete map region")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
