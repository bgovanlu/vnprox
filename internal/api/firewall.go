package api

import (
	"net/http"
	"sort"

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
	Ref        string       `json:"ref"`
	Scope      string       `json:"scope"`
	Node       string       `json:"node,omitempty"`
	DefaultIn  string       `json:"defaultIn,omitempty"`
	DefaultOut string       `json:"defaultOut,omitempty"`
	Rules      []ruleView   `json:"rules"`
	Banners    []bannerView `json:"banners,omitempty"`
	Enabled    bool         `json:"enabled"`
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
	return rulesetView{
		Ref: rs.String(), Scope: string(rs.Scope), Node: rs.Node,
		Enabled: rs.Enabled, DefaultIn: rs.DefaultIn, DefaultOut: rs.DefaultOut,
		Rules: toRuleViews(rs.Rules), Banners: toBannerViews(banners),
	}
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

		default:
			writeJSONError(w, http.StatusBadRequest, "validation_failed", "scope must be one of cluster, node, guest")
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
