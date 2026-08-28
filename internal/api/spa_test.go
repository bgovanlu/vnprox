// SPDX-License-Identifier: Apache-2.0

package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSPA_ServesKnownFile(t *testing.T) {
	r := NewRouter(Options{Version: "test", DistFS: testDistFS(), Logger: testLogger()})

	req := httptest.NewRequest(http.MethodGet, "/assets/app.css", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "body{}") {
		t.Errorf("body = %q, want the css asset contents", rec.Body.String())
	}
}

func TestSPA_FallsBackToIndexForUnknownRoute(t *testing.T) {
	r := NewRouter(Options{Version: "test", DistFS: testDistFS(), Logger: testLogger()})

	req := httptest.NewRequest(http.MethodGet, "/topology/some/deep/client/route", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (SPA fallback)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "vnprox test shell") {
		t.Errorf("body = %q, want the fallback index.html contents", rec.Body.String())
	}
}

func TestSPA_RootServesIndex(t *testing.T) {
	r := NewRouter(Options{Version: "test", DistFS: testDistFS(), Logger: testLogger()})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "vnprox test shell") {
		t.Errorf("body = %q, want index.html contents", rec.Body.String())
	}
}
