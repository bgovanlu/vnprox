package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

func firewallTestAuth(caps map[string]bool) fakeAuthWithCaps {
	return fakeAuthWithCaps{
		caps: caps, csrf: true,
		fakeAuthWithUser: fakeAuthWithUser{username: "root@pam", fakeAuth: fakeAuth{authenticated: true}},
	}
}

// buildTestGraph applies one poll of entities to a fresh *inventory.Graph
// and returns it — *inventory.Graph satisfies FirewallGraph directly (its
// real Snapshot method), so router tests exercise the exact same
// graph->fw.Snapshot assembly path production wiring uses.
func buildTestGraph(t *testing.T, entities ...inventory.Entity) *inventory.Graph {
	t.Helper()
	g := inventory.NewGraph()
	g.ApplyPoll(inventory.SourcePVEFirewall, inventory.Scope{Kinds: []inventory.Kind{inventory.KindFwRuleset}}, entities)
	return g
}

func TestFirewallRulesets_Unauthenticated401(t *testing.T) {
	graph := buildTestGraph(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: false}, Firewall: graph,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/rulesets?scope=cluster", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestFirewallRulesets_ClusterScope(t *testing.T) {
	cluster := &inventory.FwRuleset{
		Ref: inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"}, Scope: inventory.FwScopeCluster,
		Enabled: false, DefaultIn: "DROP", DefaultOut: "ACCEPT",
		Rules: []inventory.FwRule{{Pos: 0, Enabled: true, Direction: "in", Action: "ACCEPT", Dport: "22"}},
	}
	graph := buildTestGraph(t, cluster)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: firewallTestAuth(map[string]bool{"netRead": true}), Firewall: graph,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/rulesets?scope=cluster", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}

	var got rulesetView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Scope != "cluster" || len(got.Rules) != 1 {
		t.Fatalf("got %+v, want one cluster rule", got)
	}
	// Datacenter is off: AC3's documented banner must be present here, on
	// the Datacenter tab itself.
	if len(got.Banners) != 1 || got.Banners[0].Message == "" {
		t.Errorf("Banners = %+v, want the datacenter-off banner", got.Banners)
	}
}

func TestFirewallRulesets_ClusterScope_NotObserved404(t *testing.T) {
	graph := buildTestGraph(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: firewallTestAuth(map[string]bool{"netRead": true}), Firewall: graph,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/rulesets?scope=cluster", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestFirewallRulesets_InvalidScope400(t *testing.T) {
	graph := buildTestGraph(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: firewallTestAuth(map[string]bool{"netRead": true}), Firewall: graph,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/rulesets?scope=bogus", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestFirewallRulesets_GroupScope(t *testing.T) {
	cluster := &inventory.FwRuleset{
		Ref: inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"}, Scope: inventory.FwScopeCluster, Enabled: true,
		Groups: []inventory.FwGroup{{
			Name: "base-services", Comment: "common inbound",
			Rules: []inventory.FwRule{{Pos: 0, Enabled: true, Direction: "in", Action: "ACCEPT", Dport: "80"}},
		}},
	}
	graph := buildTestGraph(t, cluster)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: firewallTestAuth(map[string]bool{"netRead": true}), Firewall: graph,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/rulesets?scope=group&name=base-services", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var got groupRulesetView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "base-services" || got.Comment != "common inbound" || len(got.Rules) != 1 {
		t.Fatalf("got %+v, want the base-services group's own rule list", got)
	}
}

func TestFirewallRulesets_GroupScope_MissingName400(t *testing.T) {
	graph := buildTestGraph(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: firewallTestAuth(map[string]bool{"netRead": true}), Firewall: graph,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/rulesets?scope=group", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestFirewallRulesets_GroupScope_UnknownGroup404(t *testing.T) {
	cluster := &inventory.FwRuleset{
		Ref: inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"}, Scope: inventory.FwScopeCluster, Enabled: true,
	}
	graph := buildTestGraph(t, cluster)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: firewallTestAuth(map[string]bool{"netRead": true}), Firewall: graph,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/rulesets?scope=group&name=does-not-exist", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestFirewallRulesets_GuestScope_ResolvedView(t *testing.T) {
	cluster := &inventory.FwRuleset{
		Ref: inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"}, Scope: inventory.FwScopeCluster, Enabled: true,
		Rules: []inventory.FwRule{{Pos: 0, Enabled: true, Direction: "in", Action: "ACCEPT", Dport: "22"}},
	}
	guest := &inventory.FwRuleset{
		Ref: inventory.Ref{Kind: inventory.KindFwRuleset, Node: "pve1", ID: "guest/qemu/100"}, Scope: inventory.FwScopeGuest, Enabled: true,
		Rules: []inventory.FwRule{{Pos: 0, Enabled: true, Direction: "in", Action: "ACCEPT", Dport: "8080"}},
	}
	graph := buildTestGraph(t, cluster, guest)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: firewallTestAuth(map[string]bool{"netRead": true}), Firewall: graph,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/rulesets?scope=guest&ref=guest%3Apve1%3A100", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}

	var got guestRulesetView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Resolved.Active {
		t.Errorf("Resolved.Active = false, want true")
	}
	if len(got.Resolved.Rules) != 2 {
		t.Fatalf("Resolved.Rules = %+v, want 2 (cluster + guest)", got.Resolved.Rules)
	}
	if got.Resolved.Rules[0].Origin != "cluster" || got.Resolved.Rules[1].Origin != "guest" {
		t.Errorf("Resolved.Rules origins = [%s, %s], want [cluster, guest]",
			got.Resolved.Rules[0].Origin, got.Resolved.Rules[1].Origin)
	}
}

func TestFirewallRulesets_GuestScope_MissingRef400(t *testing.T) {
	graph := buildTestGraph(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: firewallTestAuth(map[string]bool{"netRead": true}), Firewall: graph,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/rulesets?scope=guest&ref=not-a-ref", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestFirewallRulesets_NodeScope_ListsAllNodes(t *testing.T) {
	n1 := &inventory.FwRuleset{Ref: inventory.Ref{Kind: inventory.KindFwRuleset, Node: "pve1", ID: "node"}, Scope: inventory.FwScopeNode, Enabled: true}
	n2 := &inventory.FwRuleset{Ref: inventory.Ref{Kind: inventory.KindFwRuleset, Node: "pve2", ID: "node"}, Scope: inventory.FwScopeNode, Enabled: false}
	graph := buildTestGraph(t, n1, n2)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: firewallTestAuth(map[string]bool{"netRead": true}), Firewall: graph,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/rulesets?scope=node", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []rulesetView `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) != 2 {
		t.Fatalf("items = %+v, want 2 nodes", body.Items)
	}
}

// TestFirewallRulesets_VNetScope is T-3103's own version of
// TestFirewallRulesets_NodeScope_ListsAllNodes/TestFirewallRulesets_GuestScope_ResolvedView:
// vnet scope is addressed by `ref` (an sdn-vnet Ref, since a vnet ruleset's
// id is a "<zone>/<vnet>" composite, not a plain name a `?node=`-style
// query param could carry unambiguously) rather than resolved (cluster+
// group cascade) the way scope=guest is — see fw.Snapshot.VNets' doc
// comment for why this package has no hardware-confirmed model for that.
func TestFirewallRulesets_VNetScope(t *testing.T) {
	vnetRef := inventory.Ref{Kind: inventory.KindSDNVnet, ID: "zone1/vnet1"}
	rs := &inventory.FwRuleset{
		Ref: inventory.Ref{Kind: inventory.KindFwRuleset, ID: "vnet/zone1/vnet1"}, Scope: inventory.FwScopeVNet,
		Enabled: true, DefaultForward: "DROP", LogLevelForward: "debug",
		Rules: []inventory.FwRule{{Pos: 0, Enabled: true, Direction: "forward", Action: "ACCEPT", Source: "10.100.0.0/24"}},
	}
	graph := buildTestGraph(t, rs)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: firewallTestAuth(map[string]bool{"netRead": true}), Firewall: graph,
	})

	t.Run("list", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/rulesets?scope=vnet", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Items []rulesetView `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(body.Items) != 1 || body.Items[0].Vnet != vnetRef.String() {
			t.Fatalf("items = %+v, want one item with vnet=%q", body.Items, vnetRef.String())
		}
	})

	t.Run("single by ref", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/rulesets?scope=vnet&ref="+vnetRef.String(), nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
		}
		var got rulesetView
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Scope != "vnet" || len(got.Rules) != 1 || got.Rules[0].Direction != "forward" {
			t.Fatalf("got %+v, want one forward rule", got)
		}
		if got.DefaultForward != "DROP" || got.LogLevelForward != "debug" {
			t.Errorf("got.DefaultForward/LogLevelForward = %q/%q, want DROP/debug", got.DefaultForward, got.LogLevelForward)
		}
	})

	t.Run("unknown ref 404s", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/rulesets?scope=vnet&ref=sdn-vnet::zone1/ghost", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}

func TestFirewallObjects_UsageCountsAndMacros(t *testing.T) {
	cluster := &inventory.FwRuleset{
		Ref: inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"}, Scope: inventory.FwScopeCluster, Enabled: true,
		Aliases: []inventory.FwAlias{{Name: "office_net", CIDR: "192.168.1.0/24"}},
		Rules: []inventory.FwRule{
			{Pos: 0, Enabled: true, Direction: "in", Action: "ACCEPT", Source: "office_net"},
			{Pos: 1, Enabled: true, Direction: "in", Action: "ACCEPT", Macro: "HTTP"},
		},
	}
	graph := buildTestGraph(t, cluster)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: firewallTestAuth(map[string]bool{"netRead": true}), Firewall: graph,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/objects", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}

	var got objectsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Aliases) != 1 || got.Aliases[0].Count != 1 {
		t.Fatalf("Aliases = %+v, want one alias referenced once", got.Aliases)
	}
	found := false
	for _, m := range got.Macros {
		if m.Name == "HTTP" {
			found = true
			if len(m.Ports) != 1 || m.Ports[0].Dport != "80" {
				t.Errorf("HTTP macro ports = %+v, want tcp/80", m.Ports)
			}
		}
	}
	if !found {
		t.Error("Macros does not include HTTP")
	}
}

// TestFirewallEffects_MatchingGuests is T-502 acceptance criterion 4 at the
// router level: a group referenced by the cluster ruleset (T-501's
// documented cluster-cascades-to-every-guest simplification) matches every
// observed guest.
func TestFirewallEffects_MatchingGuests(t *testing.T) {
	cluster := &inventory.FwRuleset{
		Ref: inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"}, Scope: inventory.FwScopeCluster, Enabled: true,
		Groups: []inventory.FwGroup{{Name: "base-services", Rules: []inventory.FwRule{{Pos: 0, Enabled: true, Direction: "in", Action: "ACCEPT", Dport: "80"}}}},
		Rules:  []inventory.FwRule{{Pos: 0, Enabled: true, Direction: "group", Action: "base-services"}},
	}
	guestA := &inventory.FwRuleset{Ref: inventory.Ref{Kind: inventory.KindFwRuleset, Node: "pve1", ID: "guest/qemu/100"}, Scope: inventory.FwScopeGuest, Enabled: true}
	guestB := &inventory.FwRuleset{Ref: inventory.Ref{Kind: inventory.KindFwRuleset, Node: "pve1", ID: "guest/qemu/101"}, Scope: inventory.FwScopeGuest, Enabled: true}
	graph := buildTestGraph(t, cluster, guestA, guestB)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: firewallTestAuth(map[string]bool{"netRead": true}), Firewall: graph,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/effects?group=base-services", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var got effectsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Group != "base-services" || len(got.Guests) != 2 {
		t.Fatalf("got %+v, want 2 matching guests", got)
	}
}

func TestFirewallEffects_MissingGroupParam400(t *testing.T) {
	graph := buildTestGraph(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: firewallTestAuth(map[string]bool{"netRead": true}), Firewall: graph,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/effects", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestFirewallRulesets_MissingGraph_NotMounted(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: firewallTestAuth(map[string]bool{"netRead": true}),
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/rulesets?scope=cluster", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route not mounted, falls through to JSON 404)", rec.Code)
	}
}
