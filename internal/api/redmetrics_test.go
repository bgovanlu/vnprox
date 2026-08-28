// SPDX-License-Identifier: Apache-2.0

package api

// redmetrics_test.go is AC1's own test: "a test asserts route labels use
// patterns and that an unbounded label source (raw path, guest name,
// changeset id) never reaches a metric." TestHTTPRED_RouteLabelNeverLeaksRawID
// is the hard-to-fool one — it drives a real request through the real
// router (not a synthetic chi.Router built just for this test) at a
// parameterized route with a concrete, distinctive ID in the URL, scrapes
// GET /metrics, and asserts that ID string appears nowhere in the body at
// all: a future handler/middleware author who reintroduces r.URL.Path here
// trips this test the moment they run it, not just in code review.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/metrics"
)

func TestRouteLabel_UsesPatternForAMatchedRoute(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/api/v1/changesets/{id}", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(200)
	})
	var got string
	r2 := chi.NewRouter()
	r2.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req)
			got = routeLabel(req)
		})
	})
	r2.Mount("/", r)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/changesets/01ARZ3NDEKTSV4RRFFQ69G5FAV", nil)
	rec := httptest.NewRecorder()
	r2.ServeHTTP(rec, req)

	if got != "/api/v1/changesets/{id}" {
		t.Fatalf("routeLabel = %q, want the route pattern, not the raw path", got)
	}
	if strings.Contains(got, "01ARZ3NDEKTSV4RRFFQ69G5FAV") {
		t.Fatalf("routeLabel leaked the raw path's ULID: %q", got)
	}
}

func TestRouteLabel_UnmatchedRouteIsBoundedLabel(t *testing.T) {
	r := chi.NewRouter()
	var got string
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req)
			got = routeLabel(req)
		})
	})
	// At least one real route must exist for chi to build its routing
	// context at all (an all-NotFound router with no registered routes
	// never populates a *chi.Context in the request context in the first
	// place — a degenerate case the production router, which always has
	// real routes, never hits); routeLabel's own nil-rc fallback covers
	// that case too, but this test's job is the realistic one: a path
	// nothing in a real router matches.
	r.Get("/api/v1/health", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(200)
	})
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(404)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/definitely-not-a-real-route/01ARZ3NDEKTSV4RRFFQ69G5FAV", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if got != "unmatched" {
		t.Fatalf("routeLabel for an unmatched route = %q, want \"unmatched\"", got)
	}
}

// TestHTTPRED_RouteLabelNeverLeaksRawID is AC1's headline test: a real
// request through the real production router (NewRouter, not a synthetic
// one built for this test) at a parameterized route, carrying a concrete,
// distinctive changeset ULID in its URL — the exported HTTP RED series
// must label it by the route *pattern*, and the ULID itself must not
// appear anywhere in the scrape body.
func TestHTTPRED_RouteLabelNeverLeaksRawID(t *testing.T) {
	const distinctiveID = "01HQZXAMPLE00000000000001"

	reg := metrics.NewRegistry(testLogger())
	changesets := newChangesetTestService(t)
	// A session-authenticated, UsernameLookup-satisfying auth double —
	// mountChangesetsRoutes (changesets.go) requires both (auth != nil AND
	// a UsernameLookup type assertion) to mount at all, unlike the plain
	// fakeAuth{authenticated:false} most of this file's other router tests
	// use, which would make this specific route never even match.
	auth := fakeAuthWithUser{username: "alice@pam", fakeAuth: fakeAuth{authenticated: true}}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:            auth,
		Topology:        fakeTopologyService{},
		Changesets:      changesets,
		MetricsCounters: fakeMetricsCounterService{},
		SelfMetrics:     reg,
		MetricsExporter: MetricsExporterConfig{Token: []byte("secret-token"), BuildVersion: "test"},
	})

	// Drive the request the metric must label by pattern, not by this
	// literal ID.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/changesets/"+distinctiveID, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	// Scrape.
	scrapeReq := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	scrapeReq.Header.Set("Authorization", "Bearer secret-token")
	scrapeRec := httptest.NewRecorder()
	r.ServeHTTP(scrapeRec, scrapeReq)
	if scrapeRec.Code != http.StatusOK {
		t.Fatalf("scrape status = %d, want 200, body: %s", scrapeRec.Code, scrapeRec.Body.String())
	}
	body := scrapeRec.Body.String()

	if !strings.Contains(body, `route="/api/v1/changesets/{id}"`) {
		t.Fatalf("scrape body missing the expected route-pattern label; body:\n%s", body)
	}
	if strings.Contains(body, distinctiveID) {
		t.Fatalf("scrape body leaked the raw changeset id %q — AC1 violation; body:\n%s", distinctiveID, body)
	}
}

// TestHTTPRED_StatusClassIsBoundedNotRawCode drives requests producing
// several different concrete status codes and asserts the label space
// stays exactly the six-value status_class vocabulary — never the raw
// numeric code (which would otherwise be a second, needless cardinality
// multiplier on top of the route dimension).
func TestHTTPRED_StatusClassIsBoundedNotRawCode(t *testing.T) {
	reg := metrics.NewRegistry(testLogger())
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:            fakeAuth{authenticated: false},
		Topology:        fakeTopologyService{},
		MetricsCounters: fakeMetricsCounterService{},
		SelfMetrics:     reg,
		MetricsExporter: MetricsExporterConfig{Token: []byte("secret-token"), BuildVersion: "test"},
	})

	// A route that will 401 (session-gated, unauthenticated) and a route
	// that 404s (no such API path) — two different concrete status codes,
	// both class "4xx" once bucketed.
	for _, path := range []string{"/api/v1/config", "/api/v1/this-route-does-not-exist"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
	}

	scrapeReq := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	scrapeReq.Header.Set("Authorization", "Bearer secret-token")
	scrapeRec := httptest.NewRecorder()
	r.ServeHTTP(scrapeRec, scrapeReq)
	body := scrapeRec.Body.String()

	if !strings.Contains(body, `status_class="4xx"`) {
		t.Fatalf("scrape body missing status_class=\"4xx\"; body:\n%s", body)
	}
	for _, code := range []string{`status_class="401"`, `status_class="404"`} {
		if strings.Contains(body, code) {
			t.Fatalf("scrape body used a raw status code as a label value (%s) instead of a status class; body:\n%s", code, body)
		}
	}
}
