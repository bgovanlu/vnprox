// conntrack.go implements T-1305's GET /conntrack (docs/api.md's Conntrack
// section): a live, per-node conntrack/NAT table read, fanned out across
// the cluster and filtered — the "what is this connection doing right now"
// complement to GET /flows' sampled/historical view. Read-only: there is no
// mutation route anywhere in this file, or anywhere else in the API
// surface, for a conntrack entry (see this task's completion report for the
// grep-verifiable regression check).

package api

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/peer"
)

// ConntrackLocalSource is the local-node read seam GET /conntrack needs:
// this node's own live conntrack table. host.Reader satisfies this
// directly (Go's structural typing: no adapter needed).
type ConntrackLocalSource interface {
	Conntrack(ctx context.Context, node string) ([]host.ConntrackEntry, error)
}

// PeerConntrackSource is GET /conntrack's cluster fan-out dependency: peer
// discovery plus one conntrack-table fetch per peer. *peer.Client satisfies
// this directly.
type PeerConntrackSource interface {
	ClusterPeers
	Conntrack(ctx context.Context, p peer.Peer, node string) ([]host.ConntrackEntry, error)
}

// ConntrackGuestResolver is GET /conntrack's `guest=` filter dependency:
// resolving a guest ref to the IP addresses vnprox currently has evidence
// for (docs/api.md: "resolves guest= against inventory to the entity's
// known IPs before filtering"). *ipam.Service.GuestIPs satisfies this
// directly. Optional (nil-safe): a daemon with no IPAM service wired
// simply treats every `guest=` filter as matching nothing, the same
// "unrecognized/unparsable filter value matches nothing, never a 400"
// convention every other filter in this codebase follows.
type ConntrackGuestResolver interface {
	GuestIPs(ctx context.Context, guestRef string) ([]string, error)
}

// natAddrResponse is one NAT-translated endpoint — docs/api.md's
// Conntrack section's `natSrc`/`natDst` shape.
type natAddrResponse struct {
	IP   string `json:"ip"`
	Port int    `json:"port,omitempty"`
}

// conntrackEntryResponse is one item of GET /conntrack's response
// (docs/api.md's Conntrack section): host.ConntrackEntry's fields plus the
// node it was observed on (needed once entries from multiple cluster nodes
// are merged into one list — host.ConntrackEntry itself carries no node,
// since a single host.Reader.Conntrack call is already scoped to one node).
type conntrackEntryResponse struct {
	NatSrc     *natAddrResponse `json:"natSrc,omitempty"`
	NatDst     *natAddrResponse `json:"natDst,omitempty"`
	Node       string           `json:"node"`
	SrcIP      string           `json:"srcIp"`
	DstIP      string           `json:"dstIp"`
	State      string           `json:"state,omitempty"`
	Proto      int              `json:"proto"`
	SrcPort    int              `json:"srcPort,omitempty"`
	DstPort    int              `json:"dstPort,omitempty"`
	TimeoutSec int              `json:"timeoutSec,omitempty"`
}

// conntrackListResponse is GET /conntrack's response envelope:
// {items, partial?, failedNodes?} — the same cluster-fan-out envelope
// docs/api.md's GET /audit/GET /flows already document, minus pagination
// fields (a live table snapshot has no cursor to resume: every request re-
// reads the current state fresh, the same "this is a live view, not
// cacheable app state" posture GET /firewall/log's tail already takes for
// its own live-ish reads, adapted here to "no paging at all" since a
// conntrack table is bounded by the kernel's own table size, not an
// ever-growing log).
type conntrackListResponse struct {
	Items       []conntrackEntryResponse `json:"items"`
	FailedNodes []string                 `json:"failedNodes,omitempty"`
	Partial     bool                     `json:"partial,omitempty"`
}

func toNatAddrResponse(a *host.NatAddr) *natAddrResponse {
	if a == nil {
		return nil
	}
	return &natAddrResponse{IP: a.IP, Port: a.Port}
}

func toConntrackEntryResponse(node string, e host.ConntrackEntry) conntrackEntryResponse {
	return conntrackEntryResponse{
		Node: node, SrcIP: e.SrcIP, DstIP: e.DstIP, State: e.State,
		NatSrc: toNatAddrResponse(e.NatSrc), NatDst: toNatAddrResponse(e.NatDst),
		Proto: e.Proto, SrcPort: e.SrcPort, DstPort: e.DstPort, TimeoutSec: e.TimeoutSec,
	}
}

// mountConntrackRoutes registers GET /conntrack (T-1305, docs/api.md's
// Conntrack section): netRead-gated, matching every other live-network-
// observability read route. local is required (nil skips mounting, the
// same convention every other optional Options-backed route family
// follows); peers/guests/localNode are all nil-safe.
func mountConntrackRoutes(r chi.Router, local ConntrackLocalSource, peers PeerConntrackSource, guests ConntrackGuestResolver, localNode func() string, auth AuthService) {
	if local == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/conntrack", handleListConntrack(local, peers, guests, localNode))
	})
}

// fetchClusterConntrack merges the local node's live conntrack table with
// every reachable peer's (docs/architecture.md §7), for GET /conntrack. A
// per-source failure (local or peer) degrades only that source's
// contribution — partial is set and the failing node's name is added to
// failedNodes — never the whole request.
func fetchClusterConntrack(ctx context.Context, local ConntrackLocalSource, peers PeerConntrackSource, localNode func() string) (items []conntrackEntryResponse, partial bool, failedNodes []string) {
	nodeName := ""
	if localNode != nil {
		nodeName = localNode()
	}
	if entries, err := local.Conntrack(ctx, nodeName); err != nil {
		partial = true
		failedNodes = append(failedNodes, nodeName)
	} else {
		for _, e := range entries {
			items = append(items, toConntrackEntryResponse(nodeName, e))
		}
	}

	if peers == nil {
		return items, partial, failedNodes
	}
	list, err := peers.Peers(ctx)
	if err != nil {
		return items, true, append(failedNodes, "<cluster peer discovery>")
	}
	for _, p := range list {
		entries, err := peers.Conntrack(ctx, p, p.Node)
		if err != nil {
			partial = true
			failedNodes = append(failedNodes, p.Node)
			continue
		}
		for _, e := range entries {
			items = append(items, toConntrackEntryResponse(p.Node, e))
		}
	}
	return items, partial, failedNodes
}

// conntrackFilter is GET /conntrack's parsed, optional-and-ANDed filter set
// (docs/api.md convention: every filter param is optional and ANDed
// together; an unrecognized/unparsable value matches nothing, never a
// 400 — except a genuinely malformed ?port=, which mirrors GET /flows'
// own ?port= handling and does 400, since "not an integer at all" is a
// caller bug, not a legitimate filter value with no matches).
type conntrackFilter struct {
	guestIPs       map[string]bool
	node, srcIP    string
	dstIP, state   string
	port           int
	hasPort        bool
	hasGuestFilter bool
}

func (f conntrackFilter) matches(e conntrackEntryResponse) bool {
	if f.node != "" && e.Node != f.node {
		return false
	}
	if f.srcIP != "" && e.SrcIP != f.srcIP {
		return false
	}
	if f.dstIP != "" && e.DstIP != f.dstIP {
		return false
	}
	if f.state != "" && !strings.EqualFold(e.State, f.state) {
		return false
	}
	if f.hasPort && e.SrcPort != f.port && e.DstPort != f.port {
		return false
	}
	if f.hasGuestFilter && !f.guestIPs[e.SrcIP] && !f.guestIPs[e.DstIP] {
		return false
	}
	return true
}

func handleListConntrack(local ConntrackLocalSource, peers PeerConntrackSource, guests ConntrackGuestResolver, localNode func() string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		filter := conntrackFilter{
			node: q.Get("node"), srcIP: q.Get("srcIp"), dstIP: q.Get("dstIp"), state: q.Get("state"),
		}
		if v := q.Get("port"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", "port must be an integer")
				return
			}
			filter.port = n
			filter.hasPort = true
		}
		if guestRef := q.Get("guest"); guestRef != "" {
			filter.hasGuestFilter = true
			filter.guestIPs = map[string]bool{}
			if guests != nil {
				if ips, err := guests.GuestIPs(r.Context(), guestRef); err == nil {
					for _, ip := range ips {
						filter.guestIPs[ip] = true
					}
				}
				// A resolution error (or a guest ref with no known
				// addresses) leaves guestIPs empty — this filter then
				// matches nothing, the documented convention, never a 500
				// for a filter value that simply doesn't resolve to
				// anything yet.
			}
		}

		items, partial, failed := fetchClusterConntrack(r.Context(), local, peers, localNode)

		filtered := make([]conntrackEntryResponse, 0, len(items))
		for _, e := range items {
			if filter.matches(e) {
				filtered = append(filtered, e)
			}
		}
		sort.Slice(filtered, func(i, j int) bool {
			a, b := filtered[i], filtered[j]
			if a.Node != b.Node {
				return a.Node < b.Node
			}
			if a.SrcIP != b.SrcIP {
				return a.SrcIP < b.SrcIP
			}
			if a.SrcPort != b.SrcPort {
				return a.SrcPort < b.SrcPort
			}
			if a.DstIP != b.DstIP {
				return a.DstIP < b.DstIP
			}
			return a.DstPort < b.DstPort
		})

		writeJSON(w, http.StatusOK, conntrackListResponse{Items: filtered, Partial: partial, FailedNodes: failed})
	}
}
