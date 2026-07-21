package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/flow"
	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/store"
)

// defaultFlowPageLimit/maxFlowPageLimit bound GET /flows' ?limit=, mirroring
// every other paginated route's convention in this package.
const (
	defaultFlowPageLimit = 100
	maxFlowPageLimit     = 500
)

// FlowLocalSource is the subset of *store.FlowSampleRepo the router needs
// for GET /flows' local-node read: a single filtered, cursor-paginated
// query directly against the flow_samples ring T-1002 built (the same
// "small interface, real type satisfies it for free" shape AuditService
// establishes for *store.AuditRepo — no adapter needed).
type FlowLocalSource interface {
	Query(ctx context.Context, filter store.FlowFilter, cursor string, limit int) ([]store.FlowSample, string, error)
}

// PeerFlowSource is GET /flows' cluster fan-out dependency (T-1002,
// docs/architecture.md §7's pattern): peer discovery plus one flow-page
// fetch per peer. *peer.Client satisfies this directly.
type PeerFlowSource interface {
	ClusterPeers
	Flows(ctx context.Context, p peer.Peer, filter peer.FlowFilter, cursor string, limit int) ([]peer.FlowRecord, string, error)
}

// flowRecordResponse is one item of GET /flows' response — exactly
// docs/api.md's flow.Record shape (no internal database id: unlike
// GET /audit's rows, a flow.Record has no product-meaningful identifier of
// its own; the store-assigned row id that mergeClusterPage's cluster-sort
// needs as a tiebreak is used internally by fetchClusterFlows and never
// serialized here — see toFlowRecordResponse/peerFlowRecordToResponse).
type flowRecordResponse struct {
	Node           string `json:"node"`
	SrcIP          string `json:"srcIp"`
	DstIP          string `json:"dstIp"`
	SrcRef         string `json:"srcRef,omitempty"`
	DstRef         string `json:"dstRef,omitempty"`
	Source         string `json:"source"`
	ServiceClass   string `json:"serviceClass,omitempty"`
	Packets        int64  `json:"packets"`
	Bytes          int64  `json:"bytes"`
	SrcPort        int    `json:"srcPort,omitempty"`
	DstPort        int    `json:"dstPort,omitempty"`
	Proto          int    `json:"proto"`
	VLAN           int    `json:"vlan,omitempty"`
	IngressIfIndex int    `json:"ingressIfIndex,omitempty"`
	EgressIfIndex  int    `json:"egressIfIndex,omitempty"`
	At             int64  `json:"at"`
}

// classifyRecordFromStore/classifyRecordFromPeer extract just the metadata
// fields internal/flow.Classifier.Verdict needs (IP/port/proto/VLAN) from a
// stored sample / peer-wire record — a transient, never-persisted
// flow.Record used purely for this request's classification, not a second
// copy of anything durable.
func classifyRecordFromStore(s store.FlowSample) flow.Record {
	return flow.Record{SrcIP: s.SrcIP, DstIP: s.DstIP, SrcPort: s.SrcPort, DstPort: s.DstPort, Proto: s.Proto, VLAN: s.VLAN, Node: s.Node}
}

func classifyRecordFromPeer(r peer.FlowRecord) flow.Record {
	return flow.Record{SrcIP: r.SrcIP, DstIP: r.DstIP, SrcPort: r.SrcPort, DstPort: r.DstPort, Proto: r.Proto, VLAN: r.VLAN, Node: r.Node}
}

func toFlowRecordResponse(s store.FlowSample, classifier *flow.Classifier) flowRecordResponse {
	resp := flowRecordResponse{
		Node: s.Node, SrcIP: s.SrcIP, DstIP: s.DstIP, SrcRef: s.SrcRef, DstRef: s.DstRef,
		Source: s.Source, At: s.At, Bytes: s.Bytes, Packets: s.Packets,
		SrcPort: s.SrcPort, DstPort: s.DstPort, Proto: s.Proto, VLAN: s.VLAN,
		IngressIfIndex: s.IngressIf, EgressIfIndex: s.EgressIf,
	}
	if classifier != nil {
		resp.ServiceClass = string(classifier.Classify(classifyRecordFromStore(s)))
	}
	return resp
}

func peerFlowRecordToResponse(r peer.FlowRecord, classifier *flow.Classifier) flowRecordResponse {
	resp := flowRecordResponse{
		Node: r.Node, SrcIP: r.SrcIP, DstIP: r.DstIP, SrcRef: r.SrcRef, DstRef: r.DstRef,
		Source: r.Source, At: r.At, Bytes: r.Bytes, Packets: r.Packets,
		SrcPort: r.SrcPort, DstPort: r.DstPort, Proto: r.Proto, VLAN: r.VLAN,
		IngressIfIndex: r.IngressIfIndex, EgressIfIndex: r.EgressIfIndex,
	}
	if classifier != nil {
		resp.ServiceClass = string(classifier.Classify(classifyRecordFromPeer(r)))
	}
	return resp
}

// flowListResponse is GET /flows' response envelope: {items, nextCursor?,
// partial?, failedNodes?} — the same cluster-fan-out envelope
// docs/api.md's GET /audit and GET /snapshots already document.
type flowListResponse struct {
	NextCursor  string               `json:"nextCursor,omitempty"`
	Items       []flowRecordResponse `json:"items"`
	FailedNodes []string             `json:"failedNodes,omitempty"`
	Partial     bool                 `json:"partial,omitempty"`
}

// mountFlowRoutes registers GET /flows (T-1002, docs/api.md's Flows
// section): netRead-gated, matching every other live-network-observability
// read route (GET /metrics/live, GET /firewall/log, ...). peers is nil-safe
// (falls back to node-local-only, exactly like PeerAudit/PeerSnapshots).
// classifier is T-1504's optional serviceClass attribution (nil-safe: every
// item's serviceClass field is simply omitted — see flowRecordResponse's
// doc comment).
func mountFlowRoutes(r chi.Router, svc FlowLocalSource, auth AuthService, peers PeerFlowSource, classifier *flow.Classifier, scopeMW func(http.Handler) http.Handler) {
	if svc == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		if scopeMW != nil {
			r.Use(scopeMW)
		}
		r.Get("/flows", handleListFlows(svc, peers, classifier))
	})
}

func handleListFlows(svc FlowLocalSource, peers PeerFlowSource, classifier *flow.Classifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		limit := defaultFlowPageLimit
		if v := q.Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", "limit must be a positive integer")
				return
			}
			if n > maxFlowPageLimit {
				n = maxFlowPageLimit
			}
			limit = n
		}

		filter := store.FlowFilter{Guest: q.Get("guest"), Subnet: q.Get("subnet")}
		if v := q.Get("vlan"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", "vlan must be an integer")
				return
			}
			filter.VLAN = n
		}
		if v := q.Get("port"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", "port must be an integer")
				return
			}
			filter.Port = n
		}
		if v := q.Get("protocol"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				filter.Proto = n
			} else if n, ok := flow.ProtoNumberFromName(v); ok {
				filter.Proto = n
			} else {
				// Unrecognized protocol name/number: matches nothing (the
				// same "never a 400 for an unrecognized filter value"
				// convention GET /audit's filters follow) — force a
				// filter that can never match any stored proto (proto is
				// always a valid IP protocol number 0-255).
				filter.Proto = -1
			}
		}
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

		if peers == nil {
			samples, next, err := svc.Query(r.Context(), filter, q.Get("cursor"), limit)
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list flow records")
				return
			}
			items := make([]flowRecordResponse, len(samples))
			for i, s := range samples {
				items[i] = toFlowRecordResponse(s, classifier)
			}
			if scope, scoped := scopeFromContext(r.Context()); scoped {
				items = filterFlowsForScope(items, scope)
			}
			writeJSON(w, http.StatusOK, flowListResponse{Items: items, NextCursor: next})
			return
		}

		items, next, partial, failed, err := fetchClusterFlows(r.Context(), svc, peers, filter, q.Get("cursor"), limit, classifier)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "could not list flow records")
			return
		}
		if items == nil {
			items = []flowRecordResponse{}
		}
		// T-1703: a tenant sees only flows touching one of its visible refs.
		if scope, scoped := scopeFromContext(r.Context()); scoped {
			items = filterFlowsForScope(items, scope)
		}
		writeJSON(w, http.StatusOK, flowListResponse{Items: items, NextCursor: next, Partial: partial, FailedNodes: failed})
	}
}
