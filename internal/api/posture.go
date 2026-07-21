package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/docexport"
	"github.com/bgovanlu/vnprox/internal/posture"
)

// PostureService is the router's seam onto the posture read-models (T-1607):
// the latest computed score and the bounded history. Declared as an interface
// (the same seam pattern as every other *Service in this package) so
// cmd/vnproxd wires the concrete store-backed adapter and tests substitute a
// fake. All three routes are read-only, netRead-gated.
type PostureService interface {
	// Latest returns the most recent computation. ok is false when none has run
	// yet (a freshly-started daemon before the first scheduled tick).
	Latest(ctx context.Context) (p posture.Posture, ok bool, err error)
	// History returns up to limit most-recent computations, newest first.
	History(ctx context.Context, limit int) ([]posture.Posture, error)
}

// postureHistoryDefaultLimit / postureHistoryMaxLimit bound GET
// /posture/history's page size, mirroring the store's own keep-count default.
const (
	postureHistoryDefaultLimit = 90
	postureHistoryMaxLimit     = 400
)

// postureHistoryResponse is GET /posture/history's envelope.
type postureHistoryResponse struct {
	Items []posture.Posture `json:"items"`
}

// mountPostureRoutes registers GET /posture, GET /posture/history, and GET
// /export/posture?format=md|html (T-1607). All netRead-gated, read-only —
// posture is a derived artifact recomputed on a schedule, never written through
// a request. svc/auth nil-safe (routes not mounted), the standard degraded-mode
// convention.
func mountPostureRoutes(r chi.Router, svc PostureService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/posture", handlePostureLatest(svc))
		r.Get("/posture/history", handlePostureHistory(svc))
		r.Get("/export/posture", handlePostureExport(svc))
	})
}

func handlePostureLatest(svc PostureService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok, err := svc.Latest(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not read posture score")
			return
		}
		if !ok {
			writeJSONError(w, http.StatusNotFound, "not_found", "no posture score computed yet")
			return
		}
		writeJSON(w, http.StatusOK, p)
	}
}

func handlePostureHistory(svc PostureService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := parsePostureLimit(r.URL.Query().Get("limit"))
		items, err := svc.History(r.Context(), limit)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not read posture history")
			return
		}
		if items == nil {
			items = []posture.Posture{}
		}
		writeJSON(w, http.StatusOK, postureHistoryResponse{Items: items})
	}
}

// handlePostureExport renders the posture report (score + factor table + trend
// sparkline) in Markdown or HTML, reusing T-605's internal/docexport machinery.
func handlePostureExport(svc PostureService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		format := r.URL.Query().Get("format")
		if format != "md" && format != "html" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", `format must be "md" or "html"`)
			return
		}

		latest, ok, err := svc.Latest(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not read posture score")
			return
		}
		if !ok {
			writeJSONError(w, http.StatusNotFound, "not_found", "no posture score computed yet")
			return
		}
		history, err := svc.History(r.Context(), postureHistoryDefaultLimit)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not read posture history")
			return
		}

		report := docexport.PostureReport{Latest: latest, Trend: trendFromHistory(history)}
		stamp := time.Unix(latest.ComputedAt, 0).UTC().Format("20060102-150405")

		switch format {
		case "md":
			w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="vnprox-posture-%s.md"`, stamp))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(docexport.PostureMarkdown(report)))
		case "html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="vnprox-posture-%s.html"`, stamp))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(docexport.PostureHTML(report)))
		}
	}
}

// trendFromHistory converts newest-first history into oldest-to-newest trend
// points for the sparkline (a left-to-right time axis).
func trendFromHistory(history []posture.Posture) []docexport.PostureTrendPoint {
	out := make([]docexport.PostureTrendPoint, 0, len(history))
	for i := len(history) - 1; i >= 0; i-- {
		out = append(out, docexport.PostureTrendPoint{
			ComputedAt: history[i].ComputedAt,
			Overall:    history[i].Overall,
		})
	}
	return out
}

func parsePostureLimit(raw string) int {
	if raw == "" {
		return postureHistoryDefaultLimit
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return postureHistoryDefaultLimit
	}
	if n > postureHistoryMaxLimit {
		return postureHistoryMaxLimit
	}
	return n
}
