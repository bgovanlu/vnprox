// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/plugin"
	"github.com/bgovanlu/vnprox/internal/store"
)

// fakePluginService is a canned PluginService for exercising the plugin routes
// without a store or a real registry.
type fakePluginService struct {
	lastAction   string
	lastActor    string
	lastID       string
	rows         []store.PluginRow
	notInstalled bool
}

func (f *fakePluginService) List(_ context.Context) ([]store.PluginRow, error) {
	return f.rows, nil
}
func (f *fakePluginService) Enable(_ context.Context, actor, id string) error {
	return f.record("enable", actor, id)
}
func (f *fakePluginService) Disable(_ context.Context, actor, id string) error {
	return f.record("disable", actor, id)
}
func (f *fakePluginService) Uninstall(_ context.Context, actor, id string) error {
	return f.record("uninstall", actor, id)
}
func (f *fakePluginService) record(action, actor, id string) error {
	if f.notInstalled {
		return plugin.ErrNotInstalled
	}
	f.lastAction, f.lastActor, f.lastID = action, actor, id
	return nil
}

func newPluginTestRouter(svc PluginService, auth AuthService) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, Topology: fakeTopologyService{}, Plugins: svc,
	})
}

func TestPluginRoutes_NotMountedWithoutService(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fullCapsAuth("alice"), Topology: fakeTopologyService{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/plugins", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (routes not mounted)", rec.Code)
	}
}

func TestPluginRoutes_ListReportsScope(t *testing.T) {
	svc := &fakePluginService{rows: []store.PluginRow{{
		ID: "com.acme.driver", Name: "Acme", APIVersion: "v1", Transport: "grpc",
		ExtensionPoints: []string{"switchDriver"}, Capabilities: []string{"netRead", "netWrite"},
		InstalledBy: "root@pam", InstalledAt: 1, Enabled: true,
	}}}
	r := newPluginTestRouter(svc, fullCapsAuth("alice"))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/plugins", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	var resp pluginsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].ID != "com.acme.driver" {
		t.Fatalf("items = %+v", resp.Items)
	}
	if len(resp.Items[0].Capabilities) != 2 || resp.Items[0].ExtensionPoints[0] != "switchDriver" {
		t.Errorf("scope not surfaced: %+v", resp.Items[0])
	}
}

func TestPluginRoutes_LifecycleRequiresNetWrite(t *testing.T) {
	svc := &fakePluginService{}
	// netRead-only session must be forbidden from the write routes.
	readOnly := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "bob"},
		caps:             map[string]bool{capNetRead: true},
	}
	r := newPluginTestRouter(svc, readOnly)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/plugins/com.acme.driver/disable", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (netWrite required)", rec.Code)
	}
}

func TestPluginRoutes_DisableSucceedsWithActor(t *testing.T) {
	svc := &fakePluginService{}
	r := newPluginTestRouter(svc, fullCapsAuth("alice"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/plugins/com.acme.driver/disable", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body: %s", rec.Code, rec.Body.String())
	}
	if svc.lastAction != "disable" || svc.lastActor != "alice" || svc.lastID != "com.acme.driver" {
		t.Errorf("action=%q actor=%q id=%q, want disable/alice/com.acme.driver", svc.lastAction, svc.lastActor, svc.lastID)
	}
}

func TestPluginRoutes_UninstallUnknown404(t *testing.T) {
	svc := &fakePluginService{notInstalled: true}
	r := newPluginTestRouter(svc, fullCapsAuth("alice"))
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/plugins/nope", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// compile-time proof the concrete registry satisfies the router seam.
var _ PluginService = (*plugin.Registry)(nil)
