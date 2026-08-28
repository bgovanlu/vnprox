// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/plugin"
)

// DashboardTileService is the router's seam onto T-1702's plugin registry
// for T-3911's composable dashboard: every enabled dashboardTile plugin's
// current tiles, aggregated. This mirrors *plugin.Registry's own exported
// method exactly (internal/plugin/registry.go's `DashboardTiles(ctx
// context.Context) []Tile`) — declared as a one-method interface here
// (the same seam pattern as every other *Service in this package) so this
// package's dependency on the concrete registry stays small and testable
// without a real plugin runtime, and so cmd/vnproxd can wire the concrete
// *plugin.Registry (which already satisfies this interface) unmodified.
//
// This package never redefines or wraps DashboardTiles' aggregation logic:
// the registry itself already applies T-904/T-1702's degrade-one-provider
// contract (a provider whose Tiles() call errors is logged and its tiles
// omitted; nothing else on the dashboard fails) before this seam is ever
// called, so a plugin that is absent, disabled, or erroring already never
// reaches this handler at all — it simply contributes no tiles that render.
type DashboardTileService interface {
	DashboardTiles(ctx context.Context) []plugin.Tile
}

// dashboardTileResponse is the wire shape of one tile in GET
// /dashboard/tiles' response — a decoupled mirror of plugin.Tile (this
// package's convention: never expose an internal domain type directly over
// the wire, see pluginResponse's own doc comment for the same reasoning)
// with the exact same fields docs/plugins/dashboard-tile.md documents.
type dashboardTileResponse struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Value    string `json:"value"`
	Detail   string `json:"detail,omitempty"`
	Link     string `json:"link,omitempty"`
	Severity string `json:"severity,omitempty"`
}

// dashboardTilesResponse is GET /dashboard/tiles' envelope.
type dashboardTilesResponse struct {
	Items []dashboardTileResponse `json:"items"`
}

// mountDashboardRoutes registers GET /dashboard/tiles (T-3911): every
// enabled dashboardTile plugin's current tiles, for the home dashboard's
// tile grid to compose alongside its built-in tiles. netRead-gated, no CSRF
// requirement (a read), matching every other plugin-facing read route in
// this package. svc/auth nil-safe (routes not mounted) — the standard
// degraded-mode convention every mount* function here follows; an absent
// service means "no plugin tiles" is answered by the frontend's own
// built-in-only default, not a 404/500.
func mountDashboardRoutes(r chi.Router, svc DashboardTileService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/dashboard/tiles", handleDashboardTiles(svc))
	})
}

func handleDashboardTiles(svc DashboardTileService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tiles := svc.DashboardTiles(r.Context())
		items := make([]dashboardTileResponse, 0, len(tiles))
		for _, t := range tiles {
			items = append(items, dashboardTileResponse{
				ID:       t.ID,
				Title:    t.Title,
				Value:    t.Value,
				Detail:   t.Detail,
				Link:     t.Link,
				Severity: t.Severity,
			})
		}
		writeJSON(w, http.StatusOK, dashboardTilesResponse{Items: items})
	}
}
