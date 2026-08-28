// SPDX-License-Identifier: Apache-2.0

// federationtopo.go implements T-1202's global read routes, backed by
// T-1201's federation.Aggregator (docs/api.md's Federation section):
//
//   - GET /federation/topology                 — per-cluster capsule summary (always)
//   - GET /federation/topology/clusters/{id}    — one cluster's full projected topology (lazy drill-down)
//   - GET /federation/search?q=                  — cluster-namespaced global entity search
//
// All three are netRead-gated reads (no mutation, so no CSRF) and never
// touch an attached cluster's config — they only aggregate reads. Each
// carries the standard partial/failedClusters failure-isolation envelope: a
// single unreachable cluster degrades its own capsule/omits its own hits,
// never blanks the whole response (mirroring the /audit peer fan-out's
// partial/failedNodes convention).

package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/federation"
	"github.com/bgovanlu/vnprox/internal/store"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// FederationAggregator is the subset of *federation.Aggregator the global
// read routes need. Declared as an interface (the same seam pattern
// FederationService uses) so this package's dependency on the concrete
// aggregator stays a small, explicit method set. nil skips mounting the
// whole family — a single-cluster deployment attaches no clusters, so the
// global routes are simply absent until federation is wired.
type FederationAggregator interface {
	TopologySummary(ctx context.Context) ([]federation.ClusterSummary, bool, []string, error)
	ClusterTopology(ctx context.Context, id string) (topology.Topology, error)
	Search(ctx context.Context, q string, limit int) ([]federation.SearchHit, bool, []string, error)
}

type federationTopologyResponse struct {
	Clusters       []federation.ClusterSummary `json:"clusters"`
	FailedClusters []string                    `json:"failedClusters,omitempty"`
	Partial        bool                        `json:"partial,omitempty"`
}

type federationSearchResponse struct {
	Results        []federation.SearchHit `json:"results"`
	FailedClusters []string               `json:"failedClusters,omitempty"`
	Partial        bool                   `json:"partial,omitempty"`
}

// mountFederationTopologyRoutes registers the global read routes. agg/auth
// are required together (either nil skips the whole family).
func mountFederationTopologyRoutes(r chi.Router, agg FederationAggregator, auth AuthService) {
	if agg == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capFederationRead))
		r.Get("/federation/topology", handleFederationTopology(agg))
		r.Get("/federation/topology/clusters/{id}", handleFederationClusterTopology(agg))
		r.Get("/federation/search", handleFederationSearch(agg))
	})
}

func handleFederationTopology(agg FederationAggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		clusters, partial, failed, err := agg.TopologySummary(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not aggregate cluster topology")
			return
		}
		if clusters == nil {
			clusters = []federation.ClusterSummary{}
		}
		writeJSON(w, http.StatusOK, federationTopologyResponse{Clusters: clusters, Partial: partial, FailedClusters: failed})
	}
}

func handleFederationClusterTopology(agg FederationAggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		t, err := agg.ClusterTopology(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeJSONError(w, http.StatusNotFound, "not_found", "no such cluster")
				return
			}
			// An attached-but-unreachable cluster fails the drill-down for
			// that one cluster (the summary capsule already told the operator
			// it was degraded) — a 502 distinguishes "the attached cluster is
			// down" from a local 500.
			writeJSONError(w, http.StatusBadGateway, "cluster_unreachable", "could not project cluster topology: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, t)
	}
}

func handleFederationSearch(agg FederationAggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		results, partial, failed, err := agg.Search(r.Context(), q, 0)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not run global search")
			return
		}
		if results == nil {
			results = []federation.SearchHit{}
		}
		writeJSON(w, http.StatusOK, federationSearchResponse{Results: results, Partial: partial, FailedClusters: failed})
	}
}
