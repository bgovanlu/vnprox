// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/plugin"
	"github.com/bgovanlu/vnprox/internal/store"
)

// PluginService is the router's seam onto T-1702's plugin registry: list
// installed plugins and manage their lifecycle (enable/disable/uninstall). It is
// an interface (the same seam pattern as every other *Service here) so cmd/vnproxd
// wires the concrete *plugin.Registry and tests substitute a fake. Reads are
// netRead-gated; lifecycle mutations are netWrite-gated + CSRF-protected —
// installing a plugin (loading code / spawning a subprocess) is a config-time /
// Hub (T-1705) operation, not exposed as a bare API write here.
type PluginService interface {
	List(ctx context.Context) ([]store.PluginRow, error)
	Enable(ctx context.Context, actor, id string) error
	Disable(ctx context.Context, actor, id string) error
	Uninstall(ctx context.Context, actor, id string) error
}

// pluginResponse is the wire shape of one plugin in GET /plugins. It reports the
// capability scope and extension points so an operator can always see exactly
// what a plugin may touch (docs/security.md's plugin capability-scope model). The
// endpoint (an internal launch path) is deliberately omitted from the response.
type pluginResponse struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Version         string   `json:"version"`
	APIVersion      string   `json:"apiVersion"`
	Transport       string   `json:"transport"`
	InstalledBy     string   `json:"installedBy"`
	ExtensionPoints []string `json:"extensionPoints"`
	Capabilities    []string `json:"capabilities"`
	InstalledAt     int64    `json:"installedAt"`
	Enabled         bool     `json:"enabled"`
}

// pluginsListResponse is GET /plugins' envelope.
type pluginsListResponse struct {
	Items []pluginResponse `json:"items"`
}

// mountPluginRoutes registers the plugin-management routes (T-1702). svc/auth
// nil-safe (routes not mounted) — the standard degraded-mode convention.
func mountPluginRoutes(r chi.Router, svc PluginService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/plugins", handlePluginsList(svc))
	})
	lookup, _ := auth.(UsernameLookup)
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		if csrf, ok := auth.(CSRFEnforcer); ok {
			r.Use(csrf.CSRFMiddleware)
		}
		r.Use(auth.RequireCap(capNetWrite))
		r.Post("/plugins/{id}/enable", handlePluginLifecycle(svc, lookup, PluginService.Enable))
		r.Post("/plugins/{id}/disable", handlePluginLifecycle(svc, lookup, PluginService.Disable))
		r.Delete("/plugins/{id}", handlePluginLifecycle(svc, lookup, PluginService.Uninstall))
	})
}

func handlePluginsList(svc PluginService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := svc.List(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "listing plugins")
			return
		}
		items := make([]pluginResponse, 0, len(rows))
		for _, row := range rows {
			items = append(items, toPluginResponse(row))
		}
		writeJSON(w, http.StatusOK, pluginsListResponse{Items: items})
	}
}

// pluginAction is one lifecycle verb (Enable/Disable/Uninstall) as a method
// value, so the three routes share one handler and differ only by the action.
type pluginAction func(PluginService, context.Context, string, string) error

func handlePluginLifecycle(svc PluginService, lookup UsernameLookup, action pluginAction) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		actor := ""
		if lookup != nil {
			if name, ok := lookup.Username(r.Context()); ok {
				actor = name
			}
		}
		if actor == "" {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized", "no authenticated identity")
			return
		}
		if err := action(svc, r.Context(), actor, id); err != nil {
			if errors.Is(err, plugin.ErrNotInstalled) {
				writeJSONError(w, http.StatusNotFound, "not_found", "no such plugin")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "plugin lifecycle operation failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func toPluginResponse(row store.PluginRow) pluginResponse {
	return pluginResponse{
		ID:              row.ID,
		Name:            row.Name,
		Version:         row.Version,
		APIVersion:      row.APIVersion,
		Transport:       row.Transport,
		InstalledBy:     row.InstalledBy,
		ExtensionPoints: nonNilStrings(row.ExtensionPoints),
		Capabilities:    nonNilStrings(row.Capabilities),
		InstalledAt:     row.InstalledAt,
		Enabled:         row.Enabled,
	}
}

// nonNilStrings normalizes a nil slice to empty so the JSON array is "[]".
func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
