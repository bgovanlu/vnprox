// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/metrics"
)

// MetricsService is the subset of *metrics.Sampler the router needs for
// docs/api.md's Metrics routes (T-601): current rates for a set of
// entities, and 24h-ring history for one entity.
type MetricsService interface {
	Live(refs []string) []metrics.LiveMetric
	History(ctx context.Context, ref string, fromTs, toTs int64) ([]metrics.HistoryPoint, error)
}

// mountMetricsRoutes registers docs/api.md's `GET /metrics/live` and
// `GET /metrics/history` — netRead-gated reads, mounted the same way
// mountFDBRoutes/mountLLDPRoutes are. The `metrics.sample` WS event these
// two REST routes complement is pushed directly by internal/metrics.Sampler
// over the shared /api/ws hub (mountWSRoute above); there is no separate
// mounting step for it here.
func mountMetricsRoutes(r chi.Router, svc MetricsService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/metrics/live", handleMetricsLive(svc))
		r.Get("/metrics/history", handleMetricsHistory(svc))
	})
}

// handleMetricsLive serves `GET /metrics/live?refs=a,b,c` (docs/api.md's
// Metrics section): current rates for each of the requested entities. Refs
// never yet sampled twice (no rate computable) are simply omitted from the
// result, per Sampler.Live's documented contract — the response is never an
// error for a valid-looking but not-yet-live ref.
func handleMetricsLive(svc MetricsService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimSpace(r.URL.Query().Get("refs"))
		if raw == "" {
			writeJSON(w, http.StatusOK, map[string]any{"items": []metrics.LiveMetric{}})
			return
		}
		parts := strings.Split(raw, ",")
		refs := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				refs = append(refs, p)
			}
		}
		items := svc.Live(refs)
		if items == nil {
			items = []metrics.LiveMetric{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// handleMetricsHistory serves `GET /metrics/history?ref=&fromTs=&toTs=`
// (docs/api.md's Metrics section): 24h-ring rate history for one entity.
// Missing/unparsable fromTs/toTs default to "no bound on that side" (0 and
// the max int64 respectively) rather than erroring — a caller that only
// cares about "everything since X" or "everything up to Y" doesn't need to
// know the other bound.
func handleMetricsHistory(svc MetricsService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ref := strings.TrimSpace(r.URL.Query().Get("ref"))
		if ref == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "ref is required")
			return
		}
		fromTs := parseTsOrDefault(r.URL.Query().Get("fromTs"), 0)
		toTs := parseTsOrDefault(r.URL.Query().Get("toTs"), 1<<62)

		points, err := svc.History(r.Context(), ref, fromTs, toTs)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal", "reading metric history")
			return
		}
		if points == nil {
			points = []metrics.HistoryPoint{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ref": ref, "items": points})
	}
}

func parseTsOrDefault(raw string, def int64) int64 {
	if raw == "" {
		return def
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return def
	}
	return v
}
