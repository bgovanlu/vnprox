// SPDX-License-Identifier: Apache-2.0

package api

import (
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testDistFS() fs.FS {
	return fstest.MapFS{
		"index.html":     {Data: []byte("<html>vnprox test shell</html>")},
		"assets/app.css": {Data: []byte("body{}")},
	}
}

func TestHealthEndpoint(t *testing.T) {
	r := NewRouter(Options{Version: "1.2.3-test", DistFS: testDistFS(), Logger: testLogger()})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status field = %q, want ok", body.Status)
	}
	if body.Version != "1.2.3-test" {
		t.Errorf("version field = %q, want 1.2.3-test", body.Version)
	}
}

// stubPeerServer is a minimal PeerServer that mounts one canary route, so
// TestPeerRoutesMounted can assert internal/api's wiring calls MountRoutes
// (rather than, say, guarding it behind Auth being configured) and that the
// mounted route is reachable with no session/CSRF machinery involved.
type stubPeerServer struct{ called bool }

func (s *stubPeerServer) MountRoutes(r chi.Router) {
	s.called = true
	r.Get("/api/peer/canary", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestPeerRoutesMounted(t *testing.T) {
	peer := &stubPeerServer{}
	r := NewRouter(Options{Version: "1.2.3-test", DistFS: testDistFS(), Logger: testLogger(), Peer: peer})

	req := httptest.NewRequest(http.MethodGet, "/api/peer/canary", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if !peer.called {
		t.Fatal("NewRouter did not call PeerServer.MountRoutes")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (no session/CSRF gate should apply to peer routes)", rec.Code)
	}
}

func TestPeerRoutesOmittedWhenNil(t *testing.T) {
	r := NewRouter(Options{Version: "1.2.3-test", DistFS: testDistFS(), Logger: testLogger()})

	req := httptest.NewRequest(http.MethodGet, "/api/peer/canary", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (JSON API 404, not the SPA fallback) when no PeerServer is configured", rec.Code)
	}
}

type stubCollectorHealth struct{ sources []CollectorSourceStatus }

func (s stubCollectorHealth) CollectorStatus() []CollectorSourceStatus { return s.sources }

func TestHealthEndpoint_WithCollectors(t *testing.T) {
	want := []CollectorSourceStatus{
		{Name: "pve", ConsecutiveFailures: 2, LastError: "boom"},
		{Name: "host"},
	}
	r := NewRouter(Options{
		Version:    "1.2.3-test",
		DistFS:     testDistFS(),
		Logger:     testLogger(),
		Collectors: stubCollectorHealth{sources: want},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var body struct {
		Collectors []CollectorSourceStatus `json:"collectors"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if len(body.Collectors) != 2 || body.Collectors[0].Name != "pve" || body.Collectors[0].ConsecutiveFailures != 2 {
		t.Errorf("collectors = %+v, want %+v", body.Collectors, want)
	}
}

func TestSecurityHeaders(t *testing.T) {
	r := NewRouter(Options{Version: "test", DistFS: testDistFS(), Logger: testLogger()})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	tests := []struct {
		header string
		want   string
		// contains, when true, checks substring match instead of equality
		contains bool
	}{
		{"Strict-Transport-Security", "max-age=", true},
		{"X-Content-Type-Options", "nosniff", false},
		{"X-Frame-Options", "DENY", false},
		{"Content-Security-Policy", "default-src 'self'", true},
	}
	for _, tt := range tests {
		got := rec.Header().Get(tt.header)
		if got == "" {
			t.Errorf("header %s missing", tt.header)
			continue
		}
		if tt.contains {
			if !strings.Contains(got, tt.want) {
				t.Errorf("header %s = %q, want to contain %q", tt.header, got, tt.want)
			}
		} else if got != tt.want {
			t.Errorf("header %s = %q, want %q", tt.header, got, tt.want)
		}
	}

	// T-2901 AC1: the FULL policy, asserted exactly — not substrings. Every
	// directive is deliberate (see securityHeadersMiddleware's doc comment):
	// worker-src/manifest-src 'self' because T-2005's PWA registers sw.js
	// and links manifest.webmanifest (both same-origin; 'none' here is what
	// broke the installable app, T-2901); object-src/frame-src 'none'
	// because the SPA renders no plugins and no iframes; connect-src 'self'
	// with no bare ws:/wss: scheme sources (docs/security.md "WS to self" —
	// same-origin wss:// matches 'self' under CSP3's scheme-upgrade rule);
	// frame-ancestors 'none' because the app itself is never framed — only
	// /embed/* relaxes that, per-route.
	const wantCSP = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; frame-src 'none'; worker-src 'self'; manifest-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'self'"
	if csp := rec.Header().Get("Content-Security-Policy"); csp != wantCSP {
		t.Errorf("CSP on an app route = %q, want exactly %q", csp, wantCSP)
	}
}

func TestRequestIDHeader(t *testing.T) {
	r := NewRouter(Options{Version: "test", DistFS: testDistFS(), Logger: testLogger()})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Header().Get("X-Request-Id") == "" {
		t.Error("expected X-Request-Id response header to be set")
	}
}

func TestPanicRecovery(t *testing.T) {
	logger := testLogger()

	// recovererMiddleware is exercised directly (rather than through
	// NewRouter) since there's no route we can hit that panics on demand.
	panicky := recovererMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	req := httptest.NewRequest(http.MethodGet, "/whatever", nil)
	rec := httptest.NewRecorder()

	func() {
		defer func() {
			if rec := recover(); rec != nil {
				t.Fatalf("panic escaped recovererMiddleware: %v", rec)
			}
		}()
		panicky.ServeHTTP(rec, req)
	}()

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestUnknownAPIRouteReturnsJSON404(t *testing.T) {
	r := NewRouter(Options{Version: "test", DistFS: testDistFS(), Logger: testLogger()})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/does-not-exist", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json for an unmatched API route", ct)
	}
}
