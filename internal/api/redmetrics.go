package api

// redmetrics.go implements T-1903's HTTP RED (Rate/Errors/Duration)
// middleware: vnprox_http_requests_total and
// vnprox_http_request_duration_seconds (docs/features/monitoring.md §9),
// recorded for every request that reaches this router.
//
// AC1 (the acceptance criterion most likely to be got wrong): the "route"
// label is always a route *pattern* ("/api/v1/changesets/{id}"), never the
// raw request path ("/api/v1/changesets/01ARZ3NDEKTSV4RRFFQ69G5FAV") — see
// routeLabel's doc comment for why that holds structurally, not just by
// convention, and redmetrics_test.go's TestRouteLabel_NeverLeaksRawPath for
// the regression test a future author who reintroduces r.URL.Path here
// would trip.

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/bgovanlu/vnprox/internal/metrics"
)

// redMetricsMiddleware records one HTTP RED observation per request. reg
// nil (metrics disabled, or a router built without SelfMetrics wired — e.g.
// most of this package's existing unit tests) skips recording entirely and
// returns next unwrapped, the same nil-safe optional-dependency convention
// OnStats/OnDelta/Broadcaster and every other cross-package hook in this
// codebase already uses.
func redMetricsMiddleware(reg *metrics.Registry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if reg == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			reg.ObserveHTTPRequest(routeLabel(r), r.Method, ww.Status(), time.Since(start))
		})
	}
}

// routeLabel returns r's matched chi route *pattern*, never its raw path.
//
// This holds structurally, not just by convention: chi.Context.RoutePattern
// (github.com/go-chi/chi/v5/context.go) is built by joining the *pattern*
// string each matched sub-router registered a handler under (e.g.
// "/changesets/{id}") — it is never populated from a URL parameter's
// resolved value, so there is no code path here that could leak a raw
// changeset ULID, guest ref, or node name into this label even by mistake.
// Because redMetricsMiddleware is registered via r.Use on the *root*
// router (before any /api/v1 sub-routing), chi's routing context exists
// for the whole request and RoutePattern() reflects the fully-matched
// pattern by the time this runs (after next.ServeHTTP returns).
//
// A request no route matched at all (a bare 404, or the SPA/static-file
// fallback for a client-side route) has an empty RoutePattern — those all
// collapse into the single bounded "unmatched" label rather than being
// skipped (an operator still wants to see 404 rate) or, worse, falling
// back to the raw path.
func routeLabel(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		if p := rc.RoutePattern(); p != "" {
			return p
		}
	}
	return "unmatched"
}
