package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/store"
)

// LayoutStore is the subset of *store.LayoutRepo the router needs: per-user
// named layout blobs (docs/data-model.md §2's `layouts` table). Declared as
// an interface (the same seam pattern as AuthService/TopologyService above)
// so this package's dependency on the concrete repo stays small and testable
// without a real SQLite file.
type LayoutStore interface {
	Get(ctx context.Context, username, name string) (store.Layout, error)
	Put(ctx context.Context, l store.Layout) error
	// List and Delete back T-907's GET /layouts (the saved-views picker)
	// and DELETE /layouts/{name} — additive to the original GET/PUT-only
	// contract; *store.LayoutRepo already implemented both (List backed
	// the never-mounted "list your saved layouts" case since T-003, Delete
	// existed for symmetry with Put) so this is a route-layer addition
	// only, not a store-layer one.
	List(ctx context.Context, username string) ([]store.Layout, error)
	Delete(ctx context.Context, username, name string) error
}

// UsernameLookup is implemented by AuthService backends that can resolve the
// authenticated username from a request context (internal/auth's concrete
// Service, via cmd/vnproxd's authServiceAdapter). It is checked with a type
// assertion rather than folded into the AuthService interface itself so
// existing AuthService test doubles (e.g. internal/api/topology_test.go's
// fakeAuth) don't need updating just because a capability only the layouts
// routes use was added.
type UsernameLookup interface {
	Username(ctx context.Context) (string, bool)
}

// layoutResponse is the wire shape of GET/PUT /api/v1/layouts/{name}. Layout
// is caller-defined opaque JSON (the frontend's canvas node positions plus
// active filters, per docs/features/topology.md §2: "manual repositioning
// persists per user") — this package never interprets it, only stores and
// returns it verbatim.
type layoutResponse struct {
	Name      string          `json:"name"`
	Layout    json.RawMessage `json:"layout"`
	UpdatedAt int64           `json:"updatedAt"`
}

// layoutsListResponse is the wire shape of GET /api/v1/layouts (T-907):
// every layout/saved-view the requesting user has saved, ordered by name
// ascending per store.LayoutRepo.List. Includes the auto-persisted
// "topology" canvas-position layout (and "onboarding", if present)
// alongside any named saved views — the frontend distinguishes a "saved
// view" from the auto-saved canvas layout by its own JSON shape (a
// `"kind": "view"` tag, see docs/api.md's Saved views & annotations
// section), never by anything this package interprets.
type layoutsListResponse struct {
	Items []layoutResponse `json:"items"`
}

// layoutPutRequest is the PUT request body: {"layout": {...arbitrary...}}.
type layoutPutRequest struct {
	Layout json.RawMessage `json:"layout"`
}

// mountLayoutsRoutes registers GET /api/v1/layouts (T-907: list the caller's
// saved layouts/views), GET/PUT/DELETE /api/v1/layouts/{name}, gated by the
// same session + netRead capability as the topology routes (saving a canvas
// layout is a personal UI preference, not a network-mutating action, so no
// higher capability or CSRF requirement applies — see docs/security.md's
// "CSRF protection applies to mutating requests only" alongside this
// package's identical reasoning for the topology routes).
//
// store and auth are nil-safe to call with (routes are simply not mounted),
// matching mountTopologyRoutes' pattern; if auth doesn't also implement
// UsernameLookup, the routes are likewise not mounted, since there would be
// no safe way to key a saved layout to a user.
func mountLayoutsRoutes(r chi.Router, layouts LayoutStore, auth AuthService) {
	if layouts == nil || auth == nil {
		return
	}
	lookup, ok := auth.(UsernameLookup)
	if !ok {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/layouts", handleListLayouts(layouts, lookup))
		r.Get("/layouts/{name}", handleGetLayout(layouts, lookup))
		r.Put("/layouts/{name}", handlePutLayout(layouts, lookup))
		r.Delete("/layouts/{name}", handleDeleteLayout(layouts, lookup))
	})
}

func handleListLayouts(layouts LayoutStore, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		list, err := layouts.List(r.Context(), username)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list layouts")
			return
		}
		items := make([]layoutResponse, 0, len(list))
		for _, l := range list {
			items = append(items, layoutResponse{Name: l.Name, Layout: json.RawMessage(l.LayoutJSON), UpdatedAt: l.UpdatedAt})
		}
		writeJSON(w, http.StatusOK, layoutsListResponse{Items: items})
	}
}

func handleDeleteLayout(layouts LayoutStore, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		name := chi.URLParam(r, "name")
		if err := layouts.Delete(r.Context(), username, name); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not delete layout")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleGetLayout(layouts LayoutStore, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		name := chi.URLParam(r, "name")
		l, err := layouts.Get(r.Context(), username, name)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeJSONError(w, http.StatusNotFound, "not_found", "no saved layout with that name")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not load layout")
			return
		}
		writeJSON(w, http.StatusOK, layoutResponse{Name: l.Name, Layout: json.RawMessage(l.LayoutJSON), UpdatedAt: l.UpdatedAt})
	}
}

func handlePutLayout(layouts LayoutStore, lookup UsernameLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := lookup.Username(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		name := chi.URLParam(r, "name")

		var req layoutPutRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		isNullOrEmpty := len(req.Layout) == 0 || bytes.Equal(bytes.TrimSpace(req.Layout), []byte("null"))
		if err != nil || isNullOrEmpty {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "request body must be {\"layout\": {...}}")
			return
		}

		now := time.Now().Unix()
		l := store.Layout{Username: username, Name: name, LayoutJSON: string(req.Layout), UpdatedAt: now}
		if err := layouts.Put(r.Context(), l); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not save layout")
			return
		}
		writeJSON(w, http.StatusOK, layoutResponse{Name: name, Layout: req.Layout, UpdatedAt: now})
	}
}
