// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/docexport"
)

// fakeDocExportService is a canned DocExportService for exercising the
// GET /export/doc route without a real inventory graph.
type fakeDocExportService struct {
	data docexport.Data
}

func (f fakeDocExportService) Build(_ context.Context) docexport.Data { return f.data }

func newDocExportTestRouter(svc DocExportService, auth AuthService) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, Topology: fakeTopologyService{}, DocExport: svc,
	})
}

func TestDocExportRoutes_NotMountedWithoutService(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fullCapsAuth("alice"), Topology: fakeTopologyService{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/export/doc?format=md", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route should not be mounted)", rec.Code)
	}
}

func TestDocExportRoutes_RequiresNetRead(t *testing.T) {
	svc := fakeDocExportService{}
	auth := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"},
		caps:             map[string]bool{capNetRead: false},
	}
	r := newDocExportTestRouter(svc, auth)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/export/doc?format=md", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestDocExportRoutes_Markdown(t *testing.T) {
	svc := fakeDocExportService{data: docexport.Data{GeneratedAt: 1_700_000_000, Nodes: []string{"pve1"}}}
	r := newDocExportTestRouter(svc, fullCapsAuth("alice"))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/export/doc?format=md", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/markdown") {
		t.Errorf("Content-Type = %q, want text/markdown", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.HasSuffix(cd, `.md"`) {
		t.Errorf("Content-Disposition = %q, want an attachment ending in .md", cd)
	}
	if !strings.Contains(rec.Body.String(), "# vnprox network documentation") {
		t.Errorf("body missing title, got: %s", rec.Body.String())
	}
}

func TestDocExportRoutes_HTML(t *testing.T) {
	svc := fakeDocExportService{data: docexport.Data{GeneratedAt: 1_700_000_000}}
	r := newDocExportTestRouter(svc, fullCapsAuth("alice"))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/export/doc?format=html", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(rec.Body.String(), "<svg") {
		t.Error("body missing embedded <svg>")
	}
}

func TestDocExportRoutes_InvalidFormat(t *testing.T) {
	svc := fakeDocExportService{}
	r := newDocExportTestRouter(svc, fullCapsAuth("alice"))

	for _, format := range []string{"", "pdf", "MD"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/export/doc?format="+format, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("format=%q: status = %d, want 400", format, rec.Code)
		}
	}
}
