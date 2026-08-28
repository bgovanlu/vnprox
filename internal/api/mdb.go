// SPDX-License-Identifier: Apache-2.0

// mdb.go implements T-3902's GET /mdb (docs/api.md's Multicast/MDB
// section): a live, per-node bridge multicast-forwarding-database read,
// fanned out across the cluster and filtered — the sibling of GET
// /conntrack's live fan-out shape (internal/api/conntrack.go), not GET
// /fdb's inventory-backed shape: unlike FDB (embedded in Links()'
// BridgeDetail and served by internal/topology.Service off the collected
// inventory graph), MDB has no netlink source at all
// (internal/host/mdb.go's doc comment) and nothing in internal/collect
// ingests it into inventory, so this route re-reads every node's live MDB
// state fresh on every request, exactly like GET /conntrack does for the
// live conntrack table.

package api

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/peer"
)

// MDBLocalSource is GET /mdb's local-node read seam: raw MDB show output
// plus link state (to surface each bridge's snooping/querier/router-mode
// config, already carried on BridgeDetail as of T-3902). host.Reader
// satisfies this directly: Go's structural typing means no adapter is
// needed since host.Reader already declares both methods.
type MDBLocalSource interface {
	MDB(ctx context.Context, node string) ([]byte, error)
	Links(ctx context.Context, node string) ([]host.LinkState, error)
}

// PeerMDBSource is GET /mdb's cluster fan-out dependency: peer discovery
// plus one MDB fetch and one Links fetch per peer. *peer.Client satisfies
// this directly (both methods already exist on that type).
type PeerMDBSource interface {
	ClusterPeers
	MDB(ctx context.Context, p peer.Peer, node string) ([]byte, error)
	Links(ctx context.Context, p peer.Peer, node string) ([]host.LinkState, error)
}

// mdbEntryResponse is one item of GET /mdb's `entries` list: host.MDBRow's
// fields plus the node it was observed on (needed once entries from
// multiple cluster nodes are merged), mirroring conntrackEntryResponse's
// node-tagging convention.
type mdbEntryResponse struct {
	Node     string `json:"node"`
	Bridge   string `json:"bridge"`
	Group    string `json:"group"`
	Port     string `json:"port,omitempty"`
	State    string `json:"state,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	Vlan     int    `json:"vlan,omitempty"`
}

// mdbBridgeResponse is one bridge's IGMP/MLD-snooping configuration
// (docs/api.md's Multicast/MDB section) — the acceptance-criteria-mandated
// "per-bridge snooping enabled/disabled state", sourced from the same
// Links() read every other bridge-detail route already uses.
type mdbBridgeResponse struct {
	Node       string `json:"node"`
	Bridge     string `json:"bridge"`
	Snooping   bool   `json:"snooping"`
	Querier    bool   `json:"querier"`
	RouterMode int    `json:"routerMode"`
}

// mdbListResponse is GET /mdb's response envelope:
// {entries, bridges, partial?, failedNodes?} — the same cluster-fan-out
// envelope GET /conntrack documents, minus its unavailableNodes split
// (host.ErrMDBUnavailable is treated as an ordinary read failure here, per
// mdb.go's doc comment: the `bridge` binary being missing is an anomaly,
// not an expected/common degraded state the way "no FRR here" is).
type mdbListResponse struct {
	Entries     []mdbEntryResponse  `json:"entries"`
	Bridges     []mdbBridgeResponse `json:"bridges"`
	FailedNodes []string            `json:"failedNodes,omitempty"`
	Partial     bool                `json:"partial,omitempty"`
}

func toMDBEntryResponse(node string, row host.MDBRow) mdbEntryResponse {
	return mdbEntryResponse{
		Node: node, Bridge: row.Bridge, Group: row.Group, Port: row.Port,
		State: row.State, Protocol: row.Protocol, Vlan: row.Vlan,
	}
}

// mdbBridgesFromLinks extracts every bridge-kind link's multicast config
// out of a Links() read, tagged with node.
func mdbBridgesFromLinks(node string, links []host.LinkState) []mdbBridgeResponse {
	var out []mdbBridgeResponse
	for _, l := range links {
		if l.Bridge == nil {
			continue
		}
		out = append(out, mdbBridgeResponse{
			Node: node, Bridge: l.Name,
			Snooping: l.Bridge.MulticastSnooping, Querier: l.Bridge.MulticastQuerier,
			RouterMode: l.Bridge.MulticastRouterMode,
		})
	}
	return out
}

// mountMDBRoutes registers GET /mdb (T-3902, docs/api.md's Multicast/MDB
// section): netRead-gated, matching every other live-network-observability
// read route. local is required (nil skips mounting, the same convention
// every other optional Options-backed route family follows); peers/
// localNode are nil-safe.
func mountMDBRoutes(r chi.Router, local MDBLocalSource, peers PeerMDBSource, localNode func() string, auth AuthService) {
	if local == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/mdb", handleListMDB(local, peers, localNode))
	})
}

// fetchClusterMDB merges the local node's live MDB entries/bridge config
// with every reachable peer's, for GET /mdb. A per-source failure (local or
// peer) degrades only that source's contribution — partial is set and the
// failing node's name is added to failedNodes — never the whole request,
// mirroring fetchClusterConntrack's exact shape.
func fetchClusterMDB(ctx context.Context, local MDBLocalSource, peers PeerMDBSource, localNode func() string) (entries []mdbEntryResponse, bridges []mdbBridgeResponse, partial bool, failedNodes []string) {
	nodeName := ""
	if localNode != nil {
		nodeName = localNode()
	}

	if raw, err := local.MDB(ctx, nodeName); err != nil {
		partial = true
		failedNodes = append(failedNodes, nodeName)
	} else if rows, err := host.ParseMDB(raw); err != nil {
		partial = true
		failedNodes = append(failedNodes, nodeName)
	} else {
		for _, row := range rows {
			entries = append(entries, toMDBEntryResponse(nodeName, row))
		}
	}
	if links, err := local.Links(ctx, nodeName); err == nil {
		bridges = append(bridges, mdbBridgesFromLinks(nodeName, links)...)
	}

	if peers == nil {
		return entries, bridges, partial, failedNodes
	}
	list, err := peers.Peers(ctx)
	if err != nil {
		return entries, bridges, true, append(failedNodes, "<cluster peer discovery>")
	}
	for _, p := range list {
		raw, err := peers.MDB(ctx, p, p.Node)
		if err != nil {
			partial = true
			failedNodes = append(failedNodes, p.Node)
		} else if rows, err := host.ParseMDB(raw); err != nil {
			partial = true
			failedNodes = append(failedNodes, p.Node)
		} else {
			for _, row := range rows {
				entries = append(entries, toMDBEntryResponse(p.Node, row))
			}
		}
		if links, err := peers.Links(ctx, p, p.Node); err == nil {
			bridges = append(bridges, mdbBridgesFromLinks(p.Node, links)...)
		}
	}
	return entries, bridges, partial, failedNodes
}

func handleListMDB(local MDBLocalSource, peers PeerMDBSource, localNode func() string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		nodeFilter := q.Get("node")
		groupFilter := strings.ToLower(strings.TrimSpace(q.Get("group")))

		entries, bridges, partial, failed := fetchClusterMDB(r.Context(), local, peers, localNode)

		filteredEntries := make([]mdbEntryResponse, 0, len(entries))
		for _, e := range entries {
			if nodeFilter != "" && e.Node != nodeFilter {
				continue
			}
			if groupFilter != "" && !strings.Contains(strings.ToLower(e.Group), groupFilter) {
				continue
			}
			filteredEntries = append(filteredEntries, e)
		}
		filteredBridges := make([]mdbBridgeResponse, 0, len(bridges))
		for _, b := range bridges {
			if nodeFilter != "" && b.Node != nodeFilter {
				continue
			}
			filteredBridges = append(filteredBridges, b)
		}

		sort.Slice(filteredEntries, func(i, j int) bool {
			a, b := filteredEntries[i], filteredEntries[j]
			if a.Node != b.Node {
				return a.Node < b.Node
			}
			if a.Bridge != b.Bridge {
				return a.Bridge < b.Bridge
			}
			return a.Group < b.Group
		})
		sort.Slice(filteredBridges, func(i, j int) bool {
			a, b := filteredBridges[i], filteredBridges[j]
			if a.Node != b.Node {
				return a.Node < b.Node
			}
			return a.Bridge < b.Bridge
		})

		writeJSON(w, http.StatusOK, mdbListResponse{
			Entries: filteredEntries, Bridges: filteredBridges,
			Partial: partial, FailedNodes: failed,
		})
	}
}
