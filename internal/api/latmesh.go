package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/latmesh"
)

// LatMeshService is the subset of *latmesh.Service the router needs for
// docs/api.md's Latency mesh section (T-1303): current-plus-rolling
// per-link status, and one link's raw sample history.
type LatMeshService interface {
	Heatmap(ctx context.Context) ([]latmesh.LinkHeat, error)
	History(ctx context.Context, linkID string, fromTs, toTs int64) ([]latmesh.Sample, error)
}

// latMeshLinkResponse is one GET /latmesh/heatmap item — exactly
// docs/api.md's LatMeshLink shape.
type latMeshLinkResponse struct {
	LinkID         string  `json:"linkId"`
	Fabric         string  `json:"fabric"`
	FromNode       string  `json:"fromNode"`
	ToNode         string  `json:"toNode"`
	At             int64   `json:"at"`
	RttMs          float64 `json:"rttMs"`
	LossPct        float64 `json:"lossPct"`
	RollingRttMs   float64 `json:"rollingRttMs"`
	RollingLossPct float64 `json:"rollingLossPct"`
	SampleCount    int     `json:"sampleCount"`
}

func toLatMeshLinkResponse(l latmesh.LinkHeat) latMeshLinkResponse {
	return latMeshLinkResponse{
		LinkID: l.LinkID, Fabric: string(l.Fabric), FromNode: l.FromNode, ToNode: l.ToNode,
		At: l.At, RttMs: l.RttMs, LossPct: l.LossPct,
		RollingRttMs: l.RollingRttMs, RollingLossPct: l.RollingLossPct, SampleCount: l.SampleCount,
	}
}

// latMeshSampleResponse is one GET /latmesh/history item.
type latMeshSampleResponse struct {
	At      int64   `json:"at"`
	RttMs   float64 `json:"rttMs"`
	LossPct float64 `json:"lossPct"`
}

func toLatMeshSampleResponse(s latmesh.Sample) latMeshSampleResponse {
	return latMeshSampleResponse{At: s.At, RttMs: s.RttMs, LossPct: s.LossPct}
}

// mountLatMeshRoutes registers docs/api.md's Latency mesh section
// (T-1303): netRead-gated, node-local-only reads (see internal/latmesh's
// package doc comment for why — no peer fan-out in this task). Nil svc/auth
// simply skips mounting both routes, the same degraded-mode convention
// every other optional Options field in this package follows.
func mountLatMeshRoutes(r chi.Router, svc LatMeshService, auth AuthService) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/latmesh/heatmap", handleLatMeshHeatmap(svc))
		r.Get("/latmesh/history", handleLatMeshHistory(svc))
	})
}

func handleLatMeshHeatmap(svc LatMeshService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		links, err := svc.Heatmap(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not read latency mesh heatmap")
			return
		}
		items := make([]latMeshLinkResponse, len(links))
		for i, l := range links {
			items[i] = toLatMeshLinkResponse(l)
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

func handleLatMeshHistory(svc LatMeshService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		linkID := strings.TrimSpace(r.URL.Query().Get("linkId"))
		if linkID == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "linkId is required")
			return
		}
		fromTs := parseTsOrDefault(r.URL.Query().Get("fromTs"), 0)
		toTs := parseTsOrDefault(r.URL.Query().Get("toTs"), 1<<62)

		samples, err := svc.History(r.Context(), linkID, fromTs, toTs)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not read latency mesh history")
			return
		}
		items := make([]latMeshSampleResponse, len(samples))
		for i, s := range samples {
			items[i] = toLatMeshSampleResponse(s)
		}
		writeJSON(w, http.StatusOK, map[string]any{"linkId": linkID, "items": items})
	}
}
