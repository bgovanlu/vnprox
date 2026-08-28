// SPDX-License-Identifier: Apache-2.0

package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// FirewallGraph is the subset of *inventory.Graph the firewall routes
// need: a snapshot to build an internal/fw.Snapshot from. Declared as an
// interface (the same small-seam pattern every other *Service in this
// package uses) so this file's dependency on the concrete graph type stays
// a one-method seam; internal/fw itself never touches *inventory.Graph
// (T-501's purity requirement — see .golangci.yml's depguard rule), so
// this package is where the live graph gets turned into the pure
// fw.Snapshot value every fw.* function actually operates on.
type FirewallGraph interface {
	Snapshot() inventory.Snapshot
}

// mountFirewallRoutes registers docs/api.md's `GET /firewall/rulesets?scope=`
// and `GET /firewall/objects`, both netRead-gated (read-only views; see
// capNetRead's doc comment) — T-502 owns the write side (fw.* changeset
// ops). graph is nil-safe (routes simply not mounted), matching every
// other mountXRoutes function in this package.
func mountFirewallRoutes(r chi.Router, graph FirewallGraph, auth AuthService) {
	if graph == nil || auth == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(auth.SessionMiddleware)
		r.Use(auth.RequireCap(capNetRead))
		r.Get("/firewall/rulesets", handleFirewallRulesets(graph))
		r.Get("/firewall/objects", handleFirewallObjects(graph))
		r.Get("/firewall/effects", handleFirewallEffects(graph))
	})
}

func firewallSnapshot(graph FirewallGraph) fw.Snapshot {
	return fw.BuildSnapshot(graph.Snapshot().All())
}

// --- GET /firewall/rulesets?scope= ------------------------------------------

// rulesetView is the JSON shape of one ruleset (raw rules, as configured —
// the per-scope, read-only rule table the UI renders per
// docs/features/firewall.md §2).
type rulesetView struct {
	Ref        string `json:"ref"`
	Scope      string `json:"scope"`
	Node       string `json:"node,omitempty"`
	DefaultIn  string `json:"defaultIn,omitempty"`
	DefaultOut string `json:"defaultOut,omitempty"`
	// Vnet (T-3103) is the owning SDN vnet's own Ref string
	// ("sdn-vnet::<zone>/<vnet>"), populated only for scope=vnet — the
	// vnet-scope counterpart of Node, since a vnet ruleset's own Ref has no
	// Node component (it is cluster-scoped, like the SDN vnet it belongs
	// to) and the UI needs something to label/link the ruleset by.
	Vnet string `json:"vnet,omitempty"`
	// DefaultForward/LogLevelForward (T-3103) are the forward chain's own
	// fallthrough policy/log level. DefaultForward is populated at
	// cluster/node/vnet scope; LogLevelForward only at vnet scope (see
	// inventory.FwRuleset's doc comments — the asymmetry is hardware-
	// captured, not an oversight).
	DefaultForward  string       `json:"defaultForward,omitempty"`
	LogLevelForward string       `json:"logLevelForward,omitempty"`
	Rules           []ruleView   `json:"rules"`
	Banners         []bannerView `json:"banners,omitempty"`
	Enabled         bool         `json:"enabled"`
}

type ruleView struct {
	Dport          string          `json:"dport,omitempty"`
	Log            string          `json:"log,omitempty"`
	Proto          string          `json:"proto,omitempty"`
	Source         string          `json:"source,omitempty"`
	Dest           string          `json:"dest,omitempty"`
	Sport          string          `json:"sport,omitempty"`
	Macro          string          `json:"macro,omitempty"`
	Iface          string          `json:"iface,omitempty"`
	Action         string          `json:"action"`
	Comment        string          `json:"comment,omitempty"`
	Direction      string          `json:"direction"`
	MacroExpansion []macroPortView `json:"macroExpansion,omitempty"`
	Pos            int             `json:"pos"`
	Enabled        bool            `json:"enabled"`
}

type bannerView struct {
	Scope   string `json:"scope"`
	Message string `json:"message"`
}

func toRuleView(r inventory.FwRule) ruleView {
	v := ruleView{
		Pos: r.Pos, Enabled: r.Enabled, Direction: r.Direction, Action: r.Action,
		Proto: r.Proto, Source: r.Source, Dest: r.Dest, Sport: r.Sport, Dport: r.Dport,
		Iface: r.Iface, Macro: r.Macro, Log: r.Log, Comment: r.Comment,
	}
	if r.Macro != "" {
		if m, ok := fw.MacroExpansion(r.Macro); ok {
			v.MacroExpansion = toMacroPortViews(m.Ports)
		}
	}
	return v
}

func toRuleViews(rules []inventory.FwRule) []ruleView {
	out := make([]ruleView, len(rules))
	for i, r := range rules {
		out[i] = toRuleView(r)
	}
	return out
}

func toBannerViews(gates []fw.EnablementGate) []bannerView {
	if len(gates) == 0 {
		return nil
	}
	out := make([]bannerView, len(gates))
	for i, g := range gates {
		out[i] = bannerView{Scope: string(g.Scope), Message: g.Message}
	}
	return out
}

func toRulesetView(rs *inventory.FwRuleset, banners []fw.EnablementGate) rulesetView {
	v := rulesetView{
		Ref: rs.String(), Scope: string(rs.Scope), Node: rs.Node,
		Enabled: rs.Enabled, DefaultIn: rs.DefaultIn, DefaultOut: rs.DefaultOut,
		DefaultForward: rs.DefaultForward, LogLevelForward: rs.LogLevelForward,
		Rules: toRuleViews(rs.Rules), Banners: toBannerViews(banners),
	}
	if rs.Scope == inventory.FwScopeVNet {
		if vnetRef, ok := vnetRefFromFwRulesetRef(rs.Ref); ok {
			v.Vnet = vnetRef.String()
		}
	}
	return v
}

// vnetRefFromFwRulesetRef recovers a vnet-scope firewall ruleset's owning
// SDN vnet Ref (Kind==KindSDNVnet, ID "<zone>/<vnet>") from its own Ref,
// whose ID is "vnet/<zone>/<vnet>" (internal/collect's pollFirewall) —
// the server-side counterpart of web/src/firewall/refs.ts's
// guestRefFromFwRulesetRef, for the same "which owning object does this
// ruleset belong to" purpose one level up (vnet instead of guest).
func vnetRefFromFwRulesetRef(rsRef inventory.Ref) (inventory.Ref, bool) {
	parts := strings.SplitN(rsRef.ID, "/", 3)
	if len(parts) != 3 || parts[0] != "vnet" {
		return inventory.Ref{}, false
	}
	return inventory.Ref{Kind: inventory.KindSDNVnet, ID: parts[1] + "/" + parts[2]}, true
}

type resolvedRuleView struct {
	Origin    string   `json:"origin"`
	GroupName string   `json:"groupName,omitempty"`
	Rule      ruleView `json:"rule"`
	Pos       int      `json:"pos"`
}

type defaultPolicyView struct {
	Direction string `json:"direction"`
	Policy    string `json:"policy"`
	Origin    string `json:"origin"`
}

type resolvedViewJSON struct {
	DefaultIn  defaultPolicyView  `json:"defaultIn"`
	DefaultOut defaultPolicyView  `json:"defaultOut"`
	Guest      string             `json:"guest"`
	Gates      []bannerView       `json:"gates,omitempty"`
	Rules      []resolvedRuleView `json:"rules"`
	Active     bool               `json:"active"`
}

func toResolvedView(v fw.ResolvedView) resolvedViewJSON {
	rules := make([]resolvedRuleView, len(v.Rules))
	for i, r := range v.Rules {
		rules[i] = resolvedRuleView{Origin: string(r.Origin), GroupName: r.GroupName, Pos: r.Pos, Rule: toRuleView(r.Rule)}
	}
	return resolvedViewJSON{
		Guest: v.Guest.String(), Active: v.Active, Gates: toBannerViews(v.Gates), Rules: rules,
		DefaultIn:  defaultPolicyView{Direction: v.DefaultIn.Direction, Policy: v.DefaultIn.Policy, Origin: string(v.DefaultIn.Origin)},
		DefaultOut: defaultPolicyView{Direction: v.DefaultOut.Direction, Policy: v.DefaultOut.Policy, Origin: string(v.DefaultOut.Origin)},
	}
}

// guestRulesetView is scope=guest's response: the guest's own raw ruleset
// (for the read-only per-scope rule table) plus its full resolved view
// (docs/api.md: "cluster/node/guest rulesets, resolved with group
// expansion") in one payload, so the UI's guest tab does not need a second
// round trip to render both.
type guestRulesetView struct {
	Resolved resolvedViewJSON `json:"resolved"`
	Ruleset  rulesetView      `json:"ruleset"`
}

// groupRulesetView is scope=group's response (T-2002): a security group's
// own name/comment/rule list — the policy a group actually carries, for
// the group inspector surface T-1603's report flagged as missing. Distinct
// shape from rulesetView (not a *inventory.FwRuleset — a security group
// has no Ref/Scope/Enabled/DefaultIn/DefaultOut of its own; it is a named
// rule list referenced by a "type: group" rule elsewhere, see
// inventory.FwGroup's doc comment).
type groupRulesetView struct {
	Name    string     `json:"name"`
	Comment string     `json:"comment,omitempty"`
	Rules   []ruleView `json:"rules"`
}

func toGroupRulesetView(g inventory.FwGroup) groupRulesetView {
	return groupRulesetView{Name: g.Name, Comment: g.Comment, Rules: toRuleViews(g.Rules)}
}

// handleFirewallRulesets implements `GET /firewall/rulesets?scope=`.
//
//   - scope=cluster: the single datacenter-wide ruleset.
//   - scope=node: with `?node=<name>`, that node's ruleset; without it,
//     every observed node's ruleset (for the hierarchy navigation view).
//   - scope=guest: with `?ref=<guest ref>` (docs/api.md's "kind:node:id"
//     triplet, e.g. "guest:pve1:100"), that guest's ruleset plus its
//     resolved view; without it, every observed guest's ruleset (raw
//     only — computing every guest's resolved view on a list call is
//     unnecessary work the per-guest call already covers).
//   - scope=group (T-2002): with `?name=<group>`, that security group's
//     own rule list — the group inspector's read side. Security groups
//     have no "list every group" branch here (GET /firewall/objects
//     already lists every group by name via its usage-tracking response;
//     this scope is for drilling into one).
func handleFirewallRulesets(graph FirewallGraph) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snap := firewallSnapshot(graph)
		scope := inventory.FwScope(r.URL.Query().Get("scope"))

		switch scope {
		case inventory.FwScopeCluster:
			if snap.Cluster == nil {
				writeJSONError(w, http.StatusNotFound, "not_found", "cluster firewall ruleset not yet observed")
				return
			}
			banners := fw.ScopeBanners(snap, inventory.FwScopeCluster, "", snap.Cluster)
			writeJSON(w, http.StatusOK, toRulesetView(snap.Cluster, banners))

		case inventory.FwScopeNode:
			if node := r.URL.Query().Get("node"); node != "" {
				rs, ok := snap.Nodes[node]
				if !ok {
					writeJSONError(w, http.StatusNotFound, "not_found", "no firewall ruleset observed for that node")
					return
				}
				banners := fw.ScopeBanners(snap, inventory.FwScopeNode, node, rs)
				writeJSON(w, http.StatusOK, toRulesetView(rs, banners))
				return
			}
			out := make([]rulesetView, 0, len(snap.Nodes))
			for _, n := range sortedNodeNames(snap) {
				rs := snap.Nodes[n]
				banners := fw.ScopeBanners(snap, inventory.FwScopeNode, n, rs)
				out = append(out, toRulesetView(rs, banners))
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": out})

		case inventory.FwScopeGuest:
			if rawRef := r.URL.Query().Get("ref"); rawRef != "" {
				ref, err := inventory.ParseRef(rawRef)
				if err != nil || ref.Kind != inventory.KindGuest {
					writeJSONError(w, http.StatusBadRequest, "validation_failed", "ref must be a valid guest ref (kind:node:id)")
					return
				}
				rs, ok := snap.Guests[ref]
				if !ok {
					writeJSONError(w, http.StatusNotFound, "not_found", "no firewall ruleset observed for that guest")
					return
				}
				view, resolveErr := fw.Resolve(snap, ref)
				if resolveErr != nil {
					writeJSONError(w, http.StatusInternalServerError, "internal_error", resolveErr.Error())
					return
				}
				banners := fw.ScopeBanners(snap, inventory.FwScopeGuest, ref.Node, rs)
				writeJSON(w, http.StatusOK, guestRulesetView{Ruleset: toRulesetView(rs, banners), Resolved: toResolvedView(view)})
				return
			}
			out := make([]rulesetView, 0, len(snap.Guests))
			for _, g := range sortedGuestRefs(snap) {
				rs := snap.Guests[g]
				banners := fw.ScopeBanners(snap, inventory.FwScopeGuest, g.Node, rs)
				out = append(out, toRulesetView(rs, banners))
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": out})

		case inventory.FwScopeVNet:
			// T-3103: addressed by `ref` (the SDN vnet's own Ref), the same
			// convention scope=guest uses — a vnet's ruleset id is a
			// composite ("<zone>/<vnet>"), not a plain name a `?vnet=`
			// query param could carry unambiguously the way `?node=` does.
			// No resolved (cluster+group cascade) view for vnet scope: this
			// package has no hardware-confirmed model of how a vnet's
			// forward chain composes with cluster rules — see
			// fw.Snapshot.VNets' doc comment — so it serves the raw
			// ruleset only, the same shape scope=node already returns.
			if rawRef := r.URL.Query().Get("ref"); rawRef != "" {
				ref, err := inventory.ParseRef(rawRef)
				if err != nil || ref.Kind != inventory.KindSDNVnet {
					writeJSONError(w, http.StatusBadRequest, "validation_failed", "ref must be a valid sdn-vnet ref (kind:node:id)")
					return
				}
				rs, ok := snap.VNets[ref]
				if !ok {
					writeJSONError(w, http.StatusNotFound, "not_found", "no firewall ruleset observed for that vnet")
					return
				}
				banners := fw.ScopeBanners(snap, inventory.FwScopeVNet, ref.String(), rs)
				writeJSON(w, http.StatusOK, toRulesetView(rs, banners))
				return
			}
			out := make([]rulesetView, 0, len(snap.VNets))
			for _, v := range sortedVNetRefs(snap) {
				rs := snap.VNets[v]
				banners := fw.ScopeBanners(snap, inventory.FwScopeVNet, v.String(), rs)
				out = append(out, toRulesetView(rs, banners))
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": out})

		case "group":
			name := r.URL.Query().Get("name")
			if name == "" {
				writeJSONError(w, http.StatusBadRequest, "validation_failed", "name is required")
				return
			}
			group, ok := snap.Group(name)
			if !ok {
				writeJSONError(w, http.StatusNotFound, "not_found", "no security group observed with that name")
				return
			}
			writeJSON(w, http.StatusOK, toGroupRulesetView(group))

		default:
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "scope must be one of cluster, node, guest, vnet, group")
		}
	}
}

// --- GET /firewall/objects ---------------------------------------------------

type ruleRefView struct {
	Scope string `json:"scope"`
	Ref   string `json:"ref"`
	Pos   int    `json:"pos"`
}

type objectUsageView struct {
	Kind         string        `json:"kind"`
	Scope        string        `json:"scope"`
	Name         string        `json:"name"`
	Comment      string        `json:"comment,omitempty"`
	ReferencedBy []ruleRefView `json:"referencedBy,omitempty"`
	Count        int           `json:"count"`
}

type macroPortView struct {
	Proto string `json:"proto,omitempty"`
	Dport string `json:"dport,omitempty"`
}

type macroView struct {
	Name    string          `json:"name"`
	Comment string          `json:"comment,omitempty"`
	Ports   []macroPortView `json:"ports"`
}

func toMacroPortViews(ports []fw.MacroPort) []macroPortView {
	out := make([]macroPortView, len(ports))
	for i, p := range ports {
		out[i] = macroPortView{Proto: p.Proto, Dport: p.Dport}
	}
	return out
}

type objectsResponse struct {
	Aliases []objectUsageView `json:"aliases"`
	IPSets  []objectUsageView `json:"ipsets"`
	Groups  []objectUsageView `json:"groups"`
	Macros  []macroView       `json:"macros"`
}

// handleFirewallObjects implements `GET /firewall/objects`: every alias,
// ipset, and security group visible anywhere in the cluster, each carrying
// its "referenced by N rules" usage count (docs/features/firewall.md §2)
// and the rule locations that reference it (for the editor's "view"
// deep-link), plus the built-in macro catalog with expansion previews.
func handleFirewallObjects(graph FirewallGraph) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snap := firewallSnapshot(graph)
		usage := fw.UsageCounts(snap)

		resp := objectsResponse{Macros: toMacroViews(fw.KnownMacros())}
		for _, u := range usage {
			view := toObjectUsageView(u)
			switch u.Kind {
			case fw.ObjectAlias:
				resp.Aliases = append(resp.Aliases, view)
			case fw.ObjectIPSet:
				resp.IPSets = append(resp.IPSets, view)
			case fw.ObjectGroup:
				resp.Groups = append(resp.Groups, view)
			}
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func toObjectUsageView(u fw.ObjectUsage) objectUsageView {
	refs := make([]ruleRefView, len(u.ReferencedBy))
	for i, rr := range u.ReferencedBy {
		refs[i] = ruleRefView{Scope: string(rr.Scope), Ref: rr.RulesetRef.String(), Pos: rr.Pos}
	}
	return objectUsageView{
		Kind: string(u.Kind), Scope: string(u.Scope), Name: u.Name, Comment: u.Comment,
		Count: u.Count, ReferencedBy: refs,
	}
}

func toMacroViews(macros []fw.Macro) []macroView {
	out := make([]macroView, len(macros))
	for i, m := range macros {
		out[i] = macroView{Name: m.Name, Comment: m.Comment, Ports: toMacroPortViews(m.Ports)}
	}
	return out
}

// --- GET /firewall/effects?group= -------------------------------------------

type effectsResponse struct {
	Group  string   `json:"group"`
	Guests []string `json:"guests"`
}

// handleFirewallEffects implements `GET /firewall/effects?group=<name>`
// (added by T-502): docs/features/firewall.md §2's P1 rule-effects
// preview for a security-group reference — every guest ref whose resolved
// evaluation order actually splices in group's own rules, computed by
// internal/fw.MatchingGuests (which itself calls the same fw.Resolve the
// read-side resolved view uses — no separate resolution logic). A group
// name matching zero guests (including one that doesn't exist) returns an
// empty list, not a 404: "no matches" is a legitimate, informative answer
// for a preview, not an error.
func handleFirewallEffects(graph FirewallGraph) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		group := r.URL.Query().Get("group")
		if group == "" {
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "group is required")
			return
		}
		snap := firewallSnapshot(graph)
		guests, err := fw.MatchingGuests(snap, group)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		out := make([]string, len(guests))
		for i, g := range guests {
			out[i] = g.String()
		}
		writeJSON(w, http.StatusOK, effectsResponse{Group: group, Guests: out})
	}
}

// sortedNodeNames/sortedGuestRefs give the "list every node/guest ruleset"
// branches of handleFirewallRulesets a deterministic order — fw.Snapshot's
// Nodes/Guests are plain maps (unordered iteration), and this package
// (unlike internal/fw itself) is free to just sort locally rather than
// needing a package-level helper.
func sortedNodeNames(snap fw.Snapshot) []string {
	out := make([]string, 0, len(snap.Nodes))
	for n := range snap.Nodes {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func sortedGuestRefs(snap fw.Snapshot) []inventory.Ref {
	out := make([]inventory.Ref, 0, len(snap.Guests))
	for g := range snap.Guests {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// sortedVNetRefs mirrors sortedGuestRefs, for scope=vnet's list branch
// (T-3103).
func sortedVNetRefs(snap fw.Snapshot) []inventory.Ref {
	out := make([]inventory.Ref, 0, len(snap.VNets))
	for v := range snap.VNets {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}
