// SPDX-License-Identifier: Apache-2.0

// nftables.go implements T-3904's GET /firewall/compiled (docs/api.md's
// "Compiled ruleset (nftables)" section): a live, per-node, read-only view
// of the nftables ruleset PVE actually installed, cross-linked (where
// determinable — see attributeRule below) to the vnprox-authored firewall
// rule that produced each compiled rule. Modeled directly on GET /mdb's
// local/peer live-fan-out shape (mdb.go) — the same
// disabled-firewall-vs-legacy-iptables ambiguity host.Reader.NftRuleset's
// doc comment documents applies equally at this layer, so this file never
// claims "no firewall configured" for an empty result, only "no compiled
// nftables ruleset found."
//
// **Permanent boundary, enforced structurally, not just by convention**:
// this file contains no route registration other than GET, no reference
// to internal/change, and no handler that accepts a request body. That is
// deliberate — docs/features.md's "still out of scope" section states
// vnprox never installs its own nftables ruleset, and this inspector adds
// no exception to that. router_test.go/nftables_test.go asserts the
// mounted route accepts GET only.

package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/peer"
)

// NftRulesetLocalSource is GET /firewall/compiled's local-node read seam.
// host.Reader satisfies this directly.
type NftRulesetLocalSource interface {
	NftRuleset(ctx context.Context, node string) ([]byte, error)
}

// PeerNftRulesetSource is GET /firewall/compiled's cluster fan-out
// dependency. *peer.Client satisfies this directly.
type PeerNftRulesetSource interface {
	ClusterPeers
	NftRuleset(ctx context.Context, p peer.Peer, node string) ([]byte, error)
}

// --- response shapes ---------------------------------------------------

type nftTableResponse struct {
	Family      string `json:"family"`
	Name        string `json:"name"`
	PVEAuthored bool   `json:"pveAuthored"`
}

type nftChainResponse struct {
	Name     string `json:"name"`
	Type     string `json:"type,omitempty"`
	Hook     string `json:"hook,omitempty"`
	Priority string `json:"priority,omitempty"`
	Policy   string `json:"policy,omitempty"`
	// Field order below (fixed-size Table struct, then the single bool)
	// satisfies golangci-lint's fieldalignment check.
	Table nftTableResponse `json:"table"`
	// Builtin is true when Name is one of proxmox-firewall's own fixed
	// protection/plumbing chains (host.IsPVEBuiltinChain) — present
	// whether or not the operator authored any rule at all.
	Builtin bool `json:"builtin"`
}

// nftRuleAttribution is a compiled rule's best-effort link back to the
// vnprox-authored FwRule that produced it, or an explicit statement that
// none could be determined — acceptance criterion 2's "or is labeled
// 'not vnprox-authored'". Never a guess presented as fact: Determined is
// the single field the UI must check before rendering a link.
type nftRuleAttribution struct {
	// Reason is always populated when Determined is false — a short,
	// specific, honest explanation (e.g. "PVE built-in chain — not from
	// any authored rule", "guest/vnet-scope attribution not implemented,
	// see evidence file", "no unique matching rule found",
	// "N candidate rules matched — not unique"). Never blank when
	// Determined is false.
	Reason string `json:"reason,omitempty"`
	// Scope/Ref/Pos/Origin identify the matched rule using exactly the
	// same identity triple web/src/firewall/focusRule.ts's deep-link
	// contract already uses (docs/features/firewall.md's rule-editor deep
	// links) — set only when Determined is true.
	Scope      string `json:"scope,omitempty"`
	Ref        string `json:"ref,omitempty"`
	Origin     string `json:"origin,omitempty"`
	Pos        int    `json:"pos,omitempty"`
	Determined bool   `json:"determined"`
}

type nftRuleResponse struct {
	Table       nftTableResponse   `json:"table"`
	Chain       string             `json:"chain"`
	Comment     string             `json:"comment,omitempty"`
	Verdict     string             `json:"verdict,omitempty"`
	Proto       string             `json:"proto,omitempty"`
	SrcAddr     string             `json:"srcAddr,omitempty"`
	DstAddr     string             `json:"dstAddr,omitempty"`
	SrcPort     string             `json:"srcPort,omitempty"`
	DstPort     string             `json:"dstPort,omitempty"`
	IIfname     string             `json:"iifname,omitempty"`
	OIfname     string             `json:"oifname,omitempty"`
	Attribution nftRuleAttribution `json:"attribution"`
	Handle      int                `json:"handle"`
	Log         bool               `json:"log,omitempty"`
}

// nftRulesetResponse is GET /firewall/compiled's body.
type nftRulesetResponse struct {
	Node   string             `json:"node"`
	Tables []nftTableResponse `json:"tables"`
	Chains []nftChainResponse `json:"chains"`
	Rules  []nftRuleResponse  `json:"rules"`
	// Empty is true when no PVE-authored table was found at all —
	// deliberately NOT collapsed into "tables: []" alone, so the UI can
	// render the ambiguity host.Reader.NftRuleset's doc comment
	// documents (disabled firewall vs. legacy-iptables engine) as an
	// explicit, worded state rather than a bare empty table.
	Empty bool `json:"empty"`
}

// mountNftRulesetRoutes registers GET /firewall/compiled?node= (T-3904),
// netRead-gated like every other live-network-observability read route.
// local is required (nil skips mounting); graph/peers/localNode are
// nil-safe. No other HTTP verb is ever registered for this path — see
// this file's doc comment on the permanent PVE-firewall-engine boundary.
func mountNftRulesetRoutes(r chi.Router, local NftRulesetLocalSource, peers PeerNftRulesetSource, graph FirewallGraph, localNode func() string, auth AuthService) {
	if local == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/firewall/compiled", handleNftRuleset(local, peers, graph, localNode))
	})
}

func handleNftRuleset(local NftRulesetLocalSource, peers PeerNftRulesetSource, graph FirewallGraph, localNode func() string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		node := strings.TrimSpace(r.URL.Query().Get("node"))
		if node == "" && localNode != nil {
			node = localNode()
		}

		raw, err := fetchNftRuleset(r.Context(), node, local, peers, localNode)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "pve_unreachable", "could not read compiled firewall ruleset: "+err.Error())
			return
		}

		rs, err := host.ParseNftRuleset(raw)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, "parse_failed", "could not parse compiled firewall ruleset: "+err.Error())
			return
		}

		var snap fw.Snapshot
		if graph != nil {
			snap = firewallSnapshot(graph)
		}

		writeJSON(w, http.StatusOK, toNftRulesetResponse(node, rs, snap))
	}
}

// fetchNftRuleset resolves node's raw nftables ruleset: the local node
// when node matches localNode() (or localNode is nil), else the matching
// peer. Unlike GET /mdb, this route serves exactly one node per request
// (a compiled ruleset is inherently a single-node artifact — there is no
// cluster-wide merge that means anything for it), so "fan out" here means
// "route to the right single node," not "merge every node's rows."
func fetchNftRuleset(ctx context.Context, node string, local NftRulesetLocalSource, peers PeerNftRulesetSource, localNode func() string) ([]byte, error) {
	if localNode == nil || node == "" || node == localNode() {
		return local.NftRuleset(ctx, node)
	}
	if peers == nil {
		return local.NftRuleset(ctx, node)
	}
	list, err := peers.Peers(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range list {
		if p.Node == node {
			return peers.NftRuleset(ctx, p, node)
		}
	}
	// Unknown node name: fall back to the local reader rather than
	// erroring outright — mirrors how other node-scoped routes in this
	// package (e.g. route.Service.Snapshot) treat an unrecognized node as
	// the local reader's own problem to report, not a routing failure
	// this file should mask differently.
	return local.NftRuleset(ctx, node)
}

func toNftTableResponse(t host.NftTable) nftTableResponse {
	return nftTableResponse{Family: t.Family, Name: t.Name, PVEAuthored: t.IsPVEAuthored()}
}

func toNftChainResponse(c host.NftChain) nftChainResponse {
	return nftChainResponse{
		Table: toNftTableResponse(c.Table), Name: c.Name, Type: c.Type,
		Hook: c.Hook, Priority: c.Priority, Policy: c.Policy,
		Builtin: host.IsPVEBuiltinChain(c.Name),
	}
}

func toNftRulesetResponse(node string, rs host.NftRuleset, snap fw.Snapshot) nftRulesetResponse {
	resp := nftRulesetResponse{
		Node:   node,
		Tables: make([]nftTableResponse, 0, len(rs.Tables)),
		Chains: make([]nftChainResponse, 0, len(rs.Chains)),
		Rules:  make([]nftRuleResponse, 0, len(rs.Rules)),
	}
	for _, t := range rs.Tables {
		resp.Tables = append(resp.Tables, toNftTableResponse(t))
	}
	for _, c := range rs.Chains {
		resp.Chains = append(resp.Chains, toNftChainResponse(c))
	}
	for _, rule := range rs.Rules {
		resp.Rules = append(resp.Rules, nftRuleResponse{
			Table: toNftTableResponse(rule.Table), Chain: rule.Chain,
			Comment: rule.Comment, Verdict: rule.Verdict, Proto: rule.Proto,
			SrcAddr: rule.SrcAddr, DstAddr: rule.DstAddr,
			SrcPort: rule.SrcPort, DstPort: rule.DstPort,
			IIfname: rule.IIfname, OIfname: rule.OIfname,
			Handle: rule.Handle, Log: rule.Log,
			Attribution: attributeRule(rule, node, snap),
		})
	}
	resp.Empty = len(resp.Tables) == 0
	return resp
}

// --- attribution ---------------------------------------------------------

// attributeRule attempts to link one compiled nftables rule back to the
// vnprox-authored inventory.FwRule that produced it. This is
// **deliberately conservative** — see this file's and internal/host/
// nftables.go's doc comments on why: no populated real compiled ruleset
// was ever observed (enabling a live production host's firewall to
// capture one is exactly the mutation this task's rules forbid), so
// nothing here claims a mapping it cannot actually justify.
//
// What IS reliably determinable without guessing:
//   - A rule inside one of proxmox-firewall's own fixed protection/
//     plumbing chains (host.IsPVEBuiltinChain) exists whether or not the
//     operator authored anything — labeled accordingly, not "no match
//     found" (a stronger, more useful statement than a failed search).
//   - A rule inside the bridge/proxmox-firewall-guests table (guest/vnet
//     scope) cannot currently be resolved to a specific guest: the
//     evidence file's binary inspection found the per-guest traffic
//     chains (vm-in/vm-out/pre-vm-in/pre-vm-out) are a small FIXED set
//     shared across every guest via interface/set matching inside them,
//     not one chain per guest — so there is no reliable "which guest"
//     signal at the chain-name level, and no populated rule was ever
//     observed to check whether the per-rule interface match
//     (IIfname/OIfname, a tap/veth device name) could substitute. Rather
//     than guess a guest from a tap name this function has never seen
//     confirmed, guest/vnet-scope rules are uniformly labeled
//     not-determined.
//   - A rule inside the host table's "input"/"output" base chains is
//     matched, best-effort, against the cluster+node scope's own enabled
//     rules (the same two scopes docs/features/firewall.md §1 documents
//     as compiling directly into the node's own host chain) by comparing
//     protocol, source/destination port, and action — the fields this
//     parser can extract with reasonable confidence from a standard nft
//     match statement. A unique match is reported with
//     Determined: true; zero or multiple candidate matches are reported
//     as not-determined with the count, never a guessed pick among ties.
func attributeRule(rule host.NftRule, node string, snap fw.Snapshot) nftRuleAttribution {
	if host.IsPVEBuiltinChain(rule.Chain) && !rule.Table.IsPVEAuthored() {
		return nftRuleAttribution{Determined: false, Reason: "unrecognized table — not attributable"}
	}
	if rule.Table.Name == "proxmox-firewall-guests" {
		return nftRuleAttribution{
			Determined: false,
			Reason:     "guest/vnet-scope attribution is not implemented — no populated real ruleset was available to confirm which per-rule field (if any) reliably identifies the originating guest; see planning/reports/evidence/pve-9.2.4-nftables-firewall-engine-2026-08-28.txt",
		}
	}
	direction := ""
	switch rule.Chain {
	case "input":
		direction = "in"
	case "output":
		direction = "out"
	default:
		return nftRuleAttribution{
			Determined: false,
			Reason:     "chain \"" + rule.Chain + "\" is a PVE built-in plumbing/protection chain — not produced by any authored rule",
		}
	}

	verdict := strings.ToLower(strings.TrimSpace(rule.Verdict))
	if verdict == "" {
		return nftRuleAttribution{Determined: false, Reason: "rule has no recognizable verdict statement to match against"}
	}

	type candidate struct {
		scope string
		ref   string
		pos   int
	}
	var matches []candidate

	tryMatch := func(scope, ref string, rules []inventoryFwRuleLike) {
		for _, fr := range rules {
			if !fr.Enabled {
				continue
			}
			if fr.Direction != direction {
				continue
			}
			if !strings.EqualFold(fr.Action, verdict) {
				continue
			}
			if rule.Proto != "" && fr.Proto != "" && !strings.EqualFold(fr.Proto, rule.Proto) {
				continue
			}
			if rule.DstPort != "" && fr.Dport != "" && fr.Dport != rule.DstPort {
				continue
			}
			if rule.SrcPort != "" && fr.Sport != "" && fr.Sport != rule.SrcPort {
				continue
			}
			matches = append(matches, candidate{scope: scope, ref: fr.Ref, pos: fr.Pos})
		}
	}

	if snap.Cluster != nil {
		tryMatch("cluster", snap.Cluster.String(), fwRulesToLike(snap.Cluster.Rules))
	}
	if node != "" {
		if ns := snap.Nodes[node]; ns != nil {
			tryMatch("node", ns.String(), fwRulesToLike(ns.Rules))
		}
	}

	switch len(matches) {
	case 0:
		return nftRuleAttribution{Determined: false, Reason: "no unique matching cluster/node rule found"}
	case 1:
		return nftRuleAttribution{
			Determined: true, Scope: matches[0].scope, Ref: matches[0].ref,
			Origin: matches[0].scope, Pos: matches[0].pos,
			Reason: "heuristic match on direction/action/protocol/ports — not a byte-for-byte compiled reference",
		}
	default:
		return nftRuleAttribution{Determined: false, Reason: "multiple candidate rules matched — not unique"}
	}
}

// fwRulesToLike converts a ruleset's ordered rule list to
// inventoryFwRuleLike, deriving Pos from each rule's index (matching
// inventory.FwRule's own convention that a ruleset's Rules slice is
// already Pos-ordered — see FwRuleset's doc comment).
func fwRulesToLike(rules []inventory.FwRule) []inventoryFwRuleLike {
	out := make([]inventoryFwRuleLike, len(rules))
	for i, r := range rules {
		out[i] = inventoryFwRuleLike{
			Direction: r.Direction, Action: r.Action, Proto: r.Proto,
			Sport: r.Sport, Dport: r.Dport, Pos: r.Pos, Enabled: r.Enabled,
		}
	}
	return out
}

// inventoryFwRuleLike is the flattened subset of inventory.FwRule
// attributeRule's matcher needs, decoupled from that type's own field
// names/order so this file's matching logic reads independently of it.
type inventoryFwRuleLike struct {
	Ref       string
	Direction string
	Action    string
	Proto     string
	Sport     string
	Dport     string
	Pos       int
	Enabled   bool
}
