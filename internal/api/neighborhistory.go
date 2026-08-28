// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/store"
)

// defaultNeighborHistoryPageLimit/maxNeighborHistoryPageLimit bound
// GET /neighbors/history's ?limit=, mirroring every other paginated
// route's convention in this package (see defaultFlowPageLimit).
const (
	defaultNeighborHistoryPageLimit = 100
	maxNeighborHistoryPageLimit     = 500
)

// NeighborHistoryLocalSource is the subset of *neighbor.HistoryRecorder
// GET /neighbors/history's local-node read needs — the same
// "small interface, real type satisfies it for free" shape FlowLocalSource
// establishes over *store.FlowSampleRepo (here, over HistoryRecorder.Query,
// which itself wraps *store.NeighborBindingRepo.Query — see
// internal/neighbor/history.go).
type NeighborHistoryLocalSource interface {
	Query(ctx context.Context, filter store.NeighborBindingFilter, cursor string, limit int) ([]store.NeighborBinding, string, error)
}

// PeerNeighborHistorySource is GET /neighbors/history's cluster fan-out
// dependency (T-3905, docs/architecture.md §7's pattern — the same one
// PeerFlowSource follows for flow_samples): peer discovery plus one page
// fetch per peer. *peer.Client satisfies this directly.
type PeerNeighborHistorySource interface {
	ClusterPeers
	NeighborBindingHistory(ctx context.Context, p peer.Peer, filter peer.NeighborBindingFilter, cursor string, limit int) ([]peer.NeighborBindingRecord, string, error)
}

// neighborBindingResponse is one item of GET /neighbors/history's response
// — docs/api.md's neighborBinding shape. No internal database id (the
// store-assigned row id mergeClusterPage's cluster-sort needs as a tiebreak
// is used internally by fetchClusterNeighborBindings and never serialized
// here — the same convention flowRecordResponse's doc comment documents).
type neighborBindingResponse struct {
	Node    string `json:"node"`
	IP      string `json:"ip"`
	MAC     string `json:"mac"`
	PrevMAC string `json:"prevMac,omitempty"`
	Iface   string `json:"iface,omitempty"`
	State   string `json:"state,omitempty"`
	At      int64  `json:"at"`
	// FirstSeen is true when this row has no PrevMAC — this (node, ip)'s
	// first-ever recorded binding, not a rebind (see
	// internal/neighbor/history.go's BindingChange.FirstSeen).
	FirstSeen bool `json:"firstSeen"`
}

func toNeighborBindingResponse(b store.NeighborBinding) neighborBindingResponse {
	resp := neighborBindingResponse{
		Node: b.Node, IP: b.IP, MAC: b.MAC, Iface: b.Iface, State: b.State, At: b.At,
	}
	if b.PrevMAC.Valid {
		resp.PrevMAC = b.PrevMAC.String
	} else {
		resp.FirstSeen = true
	}
	return resp
}

func peerNeighborBindingRecordToResponse(r peer.NeighborBindingRecord) neighborBindingResponse {
	return neighborBindingResponse{
		Node: r.Node, IP: r.IP, MAC: r.MAC, PrevMAC: r.PrevMAC, Iface: r.Iface, State: r.State,
		At: r.At, FirstSeen: r.PrevMAC == "",
	}
}

// neighborBindingListResponse is GET /neighbors/history's response
// envelope: {items, nextCursor?, partial?, failedNodes?} — the same
// cluster-fan-out envelope GET /flows/GET /audit/GET /snapshots document.
type neighborBindingListResponse struct {
	NextCursor  string                    `json:"nextCursor,omitempty"`
	Items       []neighborBindingResponse `json:"items"`
	FailedNodes []string                  `json:"failedNodes,omitempty"`
	Partial     bool                      `json:"partial,omitempty"`
}

// mountNeighborHistoryRoutes registers GET /neighbors/history (T-3905):
// netRead-gated, matching every other live-network-observability read
// route. peers is nil-safe (falls back to node-local-only, exactly like
// PeerFlows/PeerAudit).
func mountNeighborHistoryRoutes(r chi.Router, svc NeighborHistoryLocalSource, auth AuthService, peers PeerNeighborHistorySource) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/neighbors/history", handleListNeighborHistory(svc, peers))
	})
}

func handleListNeighborHistory(svc NeighborHistoryLocalSource, peers PeerNeighborHistorySource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		limit := defaultNeighborHistoryPageLimit
		if v := q.Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", "limit must be a positive integer")
				return
			}
			if n > maxNeighborHistoryPageLimit {
				n = maxNeighborHistoryPageLimit
			}
			limit = n
		}

		filter := store.NeighborBindingFilter{IP: q.Get("ip"), MAC: q.Get("mac")}
		if v := q.Get("fromTs"); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", "fromTs must be a unix-seconds integer")
				return
			}
			filter.FromTs = n
		}
		if v := q.Get("toTs"); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", "toTs must be a unix-seconds integer")
				return
			}
			filter.ToTs = n
		}
		// node= narrows the cluster-merged result to one node's contribution
		// (never sent upstream to a peer — each peer only ever knows its own
		// node's rows, so this filters the already-merged/local set).
		wantNode := q.Get("node")

		if peers == nil {
			bindings, next, err := svc.Query(r.Context(), filter, q.Get("cursor"), limit)
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list neighbor binding history")
				return
			}
			items := make([]neighborBindingResponse, 0, len(bindings))
			for _, b := range bindings {
				if wantNode != "" && b.Node != wantNode {
					continue
				}
				items = append(items, toNeighborBindingResponse(b))
			}
			writeJSON(w, http.StatusOK, neighborBindingListResponse{Items: items, NextCursor: next})
			return
		}

		items, next, partial, failed, err := fetchClusterNeighborBindings(r.Context(), svc, peers, filter, q.Get("cursor"), limit)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list neighbor binding history")
			return
		}
		if wantNode != "" {
			filtered := make([]neighborBindingResponse, 0, len(items))
			for _, it := range items {
				if it.Node == wantNode {
					filtered = append(filtered, it)
				}
			}
			items = filtered
		}
		if items == nil {
			items = []neighborBindingResponse{}
		}
		writeJSON(w, http.StatusOK, neighborBindingListResponse{Items: items, NextCursor: next, Partial: partial, FailedNodes: failed})
	}
}
