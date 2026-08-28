// SPDX-License-Identifier: Apache-2.0

package api

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/apidoc"
)

// openAPIHolder carries the generated document from the end of NewRouter —
// where the router is finally complete and walkable — back to the handler
// registered near the start of it.
//
// The indirection exists because the document describes the router that is
// still being built at the moment the route is registered. Walking a
// half-built router would produce a document missing every route mounted
// after /openapi.json, which is the majority of them.
type openAPIHolder struct {
	body []byte
	mu   sync.RWMutex
}

func (h *openAPIHolder) set(body []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.body = body
}

func (h *openAPIHolder) get() []byte {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.body
}

// WalkRoutes enumerates the (method, pattern) pairs a built router registers.
//
// It takes the http.Handler NewRouter returns rather than a chi.Router so
// that callers — including the completeness gate — enumerate the *production*
// router, not a router assembled for the occasion. A router assembled for the
// occasion is exactly what a route-coverage gate must not trust: services left
// nil are silently not mounted, and their routes then cannot be reported
// missing.
func WalkRoutes(h http.Handler) ([]apidoc.Route, error) {
	routes, ok := h.(chi.Routes)
	if !ok {
		return nil, errNotWalkable
	}
	var out []apidoc.Route
	err := chi.Walk(routes, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		out = append(out, apidoc.Route{Method: method, Pattern: pattern})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

type walkError string

func (e walkError) Error() string { return string(e) }

const errNotWalkable walkError = "router is not a chi.Routes; its routes cannot be enumerated"

// handleOpenAPI serves the generated document.
//
// Deliberately unauthenticated: this is the API's contract, not its data. It
// names no node, holds no configuration and reveals nothing an attacker could
// not learn by requesting paths and reading the 401s. Requiring a session to
// read it would mean a generated client cannot be built without one, which
// defeats the purpose of publishing it.
func handleOpenAPI(h *openAPIHolder) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		body := h.get()
		if body == nil {
			// Only reachable if a caller built a router by some path that
			// skips the walk. Reported as an error rather than an empty
			// document, because an empty document reads as "this API has no
			// routes".
			writeJSONError(w, http.StatusInternalServerError, "internal", "the API document has not been generated")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(body)
	}
}

// generateOpenAPI walks the finished router and fills the holder. Called once,
// at the end of NewRouter.
func generateOpenAPI(r chi.Routes, version string, h *openAPIHolder) {
	var routes []apidoc.Route
	// A walk failure must not take the daemon down: the document is a
	// convenience, and every other route still works without it. handleOpenAPI
	// reports the empty holder honestly.
	if err := chi.Walk(r, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes = append(routes, apidoc.Route{Method: method, Pattern: pattern})
		return nil
	}); err != nil {
		return
	}
	if version == "" {
		version = "0.0.0"
	}
	body, err := json.MarshalIndent(apidoc.Build(routes, version), "", "  ")
	if err != nil {
		return
	}
	h.set(append(body, '\n'))
}
