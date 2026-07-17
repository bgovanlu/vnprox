// annotations.go implements T-907's sticky-note annotations: free-text
// notes pinned to a map entity Ref, persisted additively (new store table,
// docs/data-model.md §2's `annotations` table — see internal/store/
// migrations/0006_annotations.sql's doc comment for why this is a new
// table rather than an extension of `layouts`: annotations are a shared,
// multi-row-per-entity team scratchpad, not a single per-user opaque
// blob). App-owned UI state per CLAUDE.md's storage rule — `content` is
// free text this daemon never interprets, never a copy of any PVE-owned
// field.
//
//   - GET    /annotations       — list every pinned note, cluster/topology-wide
//   - POST   /annotations       — pin a new note {ref, content}
//   - DELETE /annotations/{id}  — unpin a note
//
// Gated by the same session + netRead capability as the layouts routes,
// with no CSRF requirement — matching that package's precedent doc
// comment exactly: pinning/unpinning a sticky note is a personal-team UI
// action, not a network-mutating one, so no higher capability or CSRF
// requirement applies (see docs/api.md's Saved views & annotations
// section for the full rationale). Any authenticated netRead-capable user
// may list, create, or delete any annotation — there is no per-note
// ownership ACL, since this is a shared team scratchpad, not private
// per-user data like layouts; `createdBy` is display/audit metadata only.

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/store"
)

// maxAnnotationBodyBytes bounds a create-annotation request body — a
// generous cap for a free-text sticky note, mirroring the reasoning
// behind blueprints.go's maxBlueprintBodyBytes.
const maxAnnotationBodyBytes = 64 << 10 // 64 KiB

// maxAnnotationContentLen bounds a single note's content length so a
// pathological client can't wedge an enormous blob into the shared,
// always-fully-listed annotations set.
const maxAnnotationContentLen = 4000

// AnnotationStore is the subset of *store.AnnotationRepo the router needs.
// Declared as an interface (the same seam pattern LayoutStore above uses)
// so this package's dependency on the concrete repo stays small and
// testable without a real SQLite file.
type AnnotationStore interface {
	List(ctx context.Context) ([]store.Annotation, error)
	Insert(ctx context.Context, a store.Annotation) error
	Delete(ctx context.Context, id string) error
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
}

type annotationsListResponse struct {
	Items []annotationResponse `json:"items"`
}

// annotationCreateRequest is POST /annotations' request body.
type annotationCreateRequest struct {
	Ref     string `json:"ref"`
	Content string `json:"content"`
}

func toAnnotationResponse(a store.Annotation) annotationResponse {
	return annotationResponse{
		ID: a.ID, Ref: a.Ref, Content: a.Content,
		CreatedBy: a.CreatedBy, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
	}
}

// mountAnnotationsRoutes registers the routes above. store/auth are
// nil-safe to call with (routes simply aren't mounted, matching
// mountLayoutsRoutes' pattern); if auth doesn't also implement
// UsernameLookup, the routes are likewise not mounted, since there would
// be no way to stamp createdBy on a new note.
func mountAnnotationsRoutes(r chi.Router, annotations AnnotationStore, auth AuthService) {
	if annotations == nil || auth == nil {
		return
	}
	lookup, ok := auth.(UsernameLookup)
	if !ok {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/annotations", handleListAnnotations(annotations))
		r.Post("/annotations", handleCreateAnnotation(annotations, lookup))
		r.Delete("/annotations/{id}", handleDeleteAnnotation(annotations))
	})
}

func handleListAnnotations(annotations AnnotationStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := annotations.List(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list annotations")
			return
		}
		items := make([]annotationResponse, 0, len(list))
		for _, a := range list {
			items = append(items, toAnnotationResponse(a))
		}
		writeJSON(w, http.StatusOK, annotationsListResponse{Items: items})
	}
}

func handleCreateAnnotation(annotations AnnotationStore, lookup UsernameLookup) http.HandlerFunc {
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
		if req.Ref == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "ref is required")
			return
		}
		if req.Content == "" || len(req.Content) > maxAnnotationContentLen {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "content must be 1..4000 characters")
			return
		}
		now := time.Now().Unix()
		a := store.Annotation{
			ID: store.NewULID(), Ref: req.Ref, Content: req.Content,
			CreatedBy: username, CreatedAt: now, UpdatedAt: now,
		}
		if err := annotations.Insert(r.Context(), a); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not save annotation")
			return
		}
		writeJSON(w, http.StatusCreated, toAnnotationResponse(a))
	}
}

func handleDeleteAnnotation(annotations AnnotationStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := annotations.Delete(r.Context(), id); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not delete annotation")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
