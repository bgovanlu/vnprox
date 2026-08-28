// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/plugin"
)

// fakeDashboardTileService is a canned DashboardTileService for exercising
// GET /dashboard/tiles without a real plugin registry.
type fakeDashboardTileService struct {
	tiles []plugin.Tile
}

func (f *fakeDashboardTileService) DashboardTiles(_ context.Context) []plugin.Tile {
	return f.tiles
}

func newDashboardTestRouter(svc DashboardTileService, auth AuthService) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, Topology: fakeTopologyService{}, DashboardTiles: svc,
	})
}

func TestDashboardRoutes_NotMountedWithoutService(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fullCapsAuth("alice"), Topology: fakeTopologyService{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/tiles", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (routes not mounted)", rec.Code)
	}
}

func TestDashboardRoutes_RequiresSession(t *testing.T) {
	svc := &fakeDashboardTileService{}
	r := newDashboardTestRouter(svc, fakeAuth{authenticated: false})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/tiles", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestDashboardRoutes_ListsPluginTiles(t *testing.T) {
	svc := &fakeDashboardTileService{tiles: []plugin.Tile{
		{ID: "sample", Title: "Sample", Value: "42", Link: "/topology", Severity: "info"},
	}}
	r := newDashboardTestRouter(svc, fullCapsAuth("alice"))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/tiles", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	var resp dashboardTilesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].ID != "sample" || resp.Items[0].Value != "42" {
		t.Fatalf("items = %+v", resp.Items)
	}
}

// A provider whose Tiles() call errors is already dropped by
// plugin.Registry.DashboardTiles before this handler ever sees it (T-904's
// degrade-one-provider contract, exercised directly in
// internal/plugin/registry_test.go and internal/plugin/plugintest's
// conformance tests) — this handler only ever receives the survivors. This
// test confirms the handler side of that contract: an empty/absent result
// renders as an explicit empty list, never an error, so a dashboard with no
// installed plugins (or every provider degraded) still returns 200.
func TestDashboardRoutes_EmptyTilesRendersEmptyListNotError(t *testing.T) {
	svc := &fakeDashboardTileService{tiles: nil}
	r := newDashboardTestRouter(svc, fullCapsAuth("alice"))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/tiles", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	var resp dashboardTilesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Items == nil {
		t.Errorf("items = nil, want non-nil empty slice (so JSON is [] not null)")
	}
	if len(resp.Items) != 0 {
		t.Errorf("items = %+v, want empty", resp.Items)
	}
}

// compile-time proof the concrete registry satisfies the router seam.
var _ DashboardTileService = (*plugin.Registry)(nil)
