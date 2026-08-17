package fw_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

func guestRef(node, vmid string) inventory.Ref {
	return inventory.Ref{Kind: inventory.KindGuest, Node: node, ID: vmid}
}

func guestRulesetRef(node, kind, vmid string) inventory.Ref {
	return inventory.Ref{Kind: inventory.KindFwRuleset, Node: node, ID: "guest/" + kind + "/" + vmid}
}

func nodeRulesetRef(node string) inventory.Ref {
	return inventory.Ref{Kind: inventory.KindFwRuleset, Node: node, ID: "node"}
}

// vnetRulesetRef is vnetRulesetRef's T-3103 counterpart: a vnet-scope
// firewall ruleset's own Ref ("vnet/<zone>/<vnet>", per params_fw.go's doc
// comment) — distinct from vnetRef, the owning SDN vnet's own Ref
// ("<zone>/<vnet>", Kind==KindSDNVnet) that Snapshot.VNets is actually keyed
// by.
func vnetRulesetRef(zone, vnet string) inventory.Ref {
	return inventory.Ref{Kind: inventory.KindFwRuleset, ID: "vnet/" + zone + "/" + vnet}
}

func vnetRef(zone, vnet string) inventory.Ref {
	return inventory.Ref{Kind: inventory.KindSDNVnet, ID: zone + "/" + vnet}
}

// buildSnapshot constructs a fw.Snapshot the same way the API layer does
// (via BuildSnapshot over a flat entity list), so these tests exercise the
// exact assembly path production code uses, not a hand-built Snapshot.
func buildSnapshot(t *testing.T, entities ...inventory.Entity) fw.Snapshot {
	t.Helper()
	return fw.BuildSnapshot(entities)
}

// --- acceptance criterion 1: golden resolved views ------------------------
//
// Five fixture guests, one per documented scenario: cluster-only rules,
// group inclusion, guest overrides, disabled-scope, default-policy
// fallthrough.

func TestResolve_GoldenScenarios(t *testing.T) {
	cluster := &inventory.FwRuleset{
		Ref:        inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"},
		Scope:      inventory.FwScopeCluster,
		Enabled:    true,
		DefaultIn:  "DROP",
		DefaultOut: "ACCEPT",
		Rules: []inventory.FwRule{
			{Pos: 0, Enabled: true, Direction: "in", Action: "ACCEPT", Proto: "tcp", Dport: "22", Comment: "cluster-wide SSH"},
			// A security-group reference: Direction "group", Action names the
			// group (this repo's documented convention — see appendRule's doc
			// comment).
			{Pos: 1, Enabled: true, Direction: "group", Action: "webservers", Comment: "web tier group"},
		},
		Groups: []inventory.FwGroup{
			{
				Name:    "webservers",
				Comment: "web tier",
				Rules: []inventory.FwRule{
					{Pos: 0, Enabled: true, Direction: "in", Action: "ACCEPT", Proto: "tcp", Dport: "80", Comment: "http"},
					{Pos: 1, Enabled: true, Direction: "in", Action: "ACCEPT", Proto: "tcp", Dport: "443", Comment: "https"},
				},
			},
		},
	}

	// Scenario A: cluster-only rules, no guest-scope ruleset overrides
	// anything (guest firewall enabled, empty own rule list).
	guestA := guestRef("pve1", "100")
	rsA := &inventory.FwRuleset{
		Ref: guestRulesetRef("pve1", "qemu", "100"), Scope: inventory.FwScopeGuest, Enabled: true,
	}

	// Scenario B: group inclusion via the cluster rule above — asserted
	// against guestA too (cluster rules always carry the group reference),
	// but scenario B's own guest additionally has its own rules following
	// the group's expansion.
	guestB := guestRef("pve1", "101")
	rsB := &inventory.FwRuleset{
		Ref: guestRulesetRef("pve1", "qemu", "101"), Scope: inventory.FwScopeGuest, Enabled: true,
		Rules: []inventory.FwRule{
			{Pos: 0, Enabled: true, Direction: "in", Action: "ACCEPT", Proto: "tcp", Dport: "8080", Comment: "app port"},
		},
	}

	// Scenario C: guest overrides — guest defines its own DROP rule for a
	// port the cluster/group already ACCEPTed, and the guest's own rule
	// must appear after the cluster+group block, at a stable position.
	guestC := guestRef("pve2", "200")
	rsC := &inventory.FwRuleset{
		Ref: guestRulesetRef("pve2", "qemu", "200"), Scope: inventory.FwScopeGuest, Enabled: true,
		DefaultIn: "DROP",
		Rules: []inventory.FwRule{
			{Pos: 0, Enabled: true, Direction: "in", Action: "DROP", Proto: "tcp", Dport: "80", Comment: "override: block http on this guest specifically"},
		},
	}

	// Scenario D: disabled-scope — guest's own firewall is off, so nothing
	// is Active even though rules exist and are individually enabled.
	guestD := guestRef("pve2", "201")
	rsD := &inventory.FwRuleset{
		Ref: guestRulesetRef("pve2", "lxc", "201"), Scope: inventory.FwScopeGuest, Enabled: false,
		Rules: []inventory.FwRule{
			{Pos: 0, Enabled: true, Direction: "in", Action: "ACCEPT", Proto: "tcp", Dport: "22"},
		},
	}

	// Scenario E: default-policy fallthrough — guest ruleset defines no
	// rules and no explicit policy, so DefaultIn/Out must fall back to the
	// cluster's policy_in=DROP/policy_out=ACCEPT.
	guestE := guestRef("pve3", "300")
	rsE := &inventory.FwRuleset{
		Ref: guestRulesetRef("pve3", "qemu", "300"), Scope: inventory.FwScopeGuest, Enabled: true,
	}

	snap := buildSnapshot(t, cluster, rsA, rsB, rsC, rsD, rsE)

	t.Run("A cluster-only", func(t *testing.T) {
		view, err := fw.Resolve(snap, guestA)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if !view.Active {
			t.Fatalf("Active = false, want true (both scopes enabled): gates=%+v", view.Gates)
		}
		// cluster SSH rule, group-reference line, 2 expanded group rules.
		if len(view.Rules) != 4 {
			t.Fatalf("len(Rules) = %d, want 4: %+v", len(view.Rules), view.Rules)
		}
		if view.Rules[0].Origin != fw.OriginCluster || view.Rules[0].Rule.Dport != "22" {
			t.Errorf("Rules[0] = %+v, want cluster SSH rule", view.Rules[0])
		}
	})

	t.Run("B group inclusion", func(t *testing.T) {
		view, err := fw.Resolve(snap, guestB)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		// cluster SSH(0), group-ref(1), group http(2), group https(3), guest own rule(4)
		if len(view.Rules) != 5 {
			t.Fatalf("len(Rules) = %d, want 5: %+v", len(view.Rules), view.Rules)
		}
		if view.Rules[4].Origin != fw.OriginGuest || view.Rules[4].Rule.Dport != "8080" {
			t.Errorf("Rules[4] = %+v, want guest's own 8080 rule last", view.Rules[4])
		}
	})

	t.Run("C guest overrides", func(t *testing.T) {
		view, err := fw.Resolve(snap, guestC)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		last := view.Rules[len(view.Rules)-1]
		if last.Origin != fw.OriginGuest || last.Rule.Action != "DROP" || last.Rule.Dport != "80" {
			t.Errorf("last rule = %+v, want guest's own DROP override", last)
		}
		if view.DefaultIn.Policy != "DROP" || view.DefaultIn.Origin != fw.OriginGuest {
			t.Errorf("DefaultIn = %+v, want guest's own explicit DROP", view.DefaultIn)
		}
	})

	t.Run("D disabled guest scope", func(t *testing.T) {
		view, err := fw.Resolve(snap, guestD)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if view.Active {
			t.Fatalf("Active = true, want false (guest firewall is off)")
		}
		foundGate := false
		for _, g := range view.Gates {
			if g.Scope == inventory.FwScopeGuest {
				foundGate = true
			}
		}
		if !foundGate {
			t.Errorf("Gates = %+v, want a guest-scope gate", view.Gates)
		}
		// Rules are still populated (transparency), including the guest's
		// own configured-but-inert rule.
		if len(view.Rules) == 0 {
			t.Errorf("Rules is empty, want the configured (inert) rules still shown")
		}
	})

	t.Run("E default-policy fallthrough", func(t *testing.T) {
		view, err := fw.Resolve(snap, guestE)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if view.DefaultIn.Policy != "DROP" || view.DefaultIn.Origin != fw.OriginCluster {
			t.Errorf("DefaultIn = %+v, want cluster fallback DROP", view.DefaultIn)
		}
		if view.DefaultOut.Policy != "ACCEPT" || view.DefaultOut.Origin != fw.OriginCluster {
			t.Errorf("DefaultOut = %+v, want cluster fallback ACCEPT", view.DefaultOut)
		}
	})
}

// TestResolve_HardDefaultFallback covers the case docs/features/
// firewall.md's default-policy fallthrough scenario extends to: neither
// guest nor cluster ever set an explicit policy at all, so pve-firewall's
// own hardcoded default applies (DROP in, ACCEPT out).
func TestResolve_HardDefaultFallback(t *testing.T) {
	cluster := &inventory.FwRuleset{
		Ref: inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"}, Scope: inventory.FwScopeCluster, Enabled: true,
	}
	g := guestRef("pve1", "500")
	rs := &inventory.FwRuleset{Ref: guestRulesetRef("pve1", "qemu", "500"), Scope: inventory.FwScopeGuest, Enabled: true}
	snap := buildSnapshot(t, cluster, rs)

	view, err := fw.Resolve(snap, g)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if view.DefaultIn.Policy != "DROP" || view.DefaultIn.Origin != fw.OriginDefault {
		t.Errorf("DefaultIn = %+v, want hardcoded DROP", view.DefaultIn)
	}
	if view.DefaultOut.Policy != "ACCEPT" || view.DefaultOut.Origin != fw.OriginDefault {
		t.Errorf("DefaultOut = %+v, want hardcoded ACCEPT", view.DefaultOut)
	}
}

// --- acceptance criterion 2: each documented evaluation step's position ---
//
// docs/features/firewall.md §1: "cluster rules → security groups → guest
// rules → default policies". Each subtest proves one step's position in
// isolation.

func TestResolve_EvaluationOrder(t *testing.T) {
	cluster := &inventory.FwRuleset{
		Ref: inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"}, Scope: inventory.FwScopeCluster, Enabled: true,
		DefaultIn: "DROP", DefaultOut: "ACCEPT",
		Rules: []inventory.FwRule{
			{Pos: 0, Enabled: true, Direction: "in", Action: "ACCEPT", Comment: "cluster-first"},
			{Pos: 1, Enabled: true, Direction: "group", Action: "grp"},
		},
		Groups: []inventory.FwGroup{
			{Name: "grp", Rules: []inventory.FwRule{
				{Pos: 0, Enabled: true, Direction: "in", Action: "ACCEPT", Comment: "group-rule"},
			}},
		},
	}
	g := guestRef("pve1", "1")
	rs := &inventory.FwRuleset{
		Ref: guestRulesetRef("pve1", "qemu", "1"), Scope: inventory.FwScopeGuest, Enabled: true,
		Rules: []inventory.FwRule{
			{Pos: 0, Enabled: true, Direction: "in", Action: "DROP", Comment: "guest-last"},
		},
	}
	snap := buildSnapshot(t, cluster, rs)
	view, err := fw.Resolve(snap, g)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	steps := []struct {
		name         string
		wantOrigin   fw.Origin
		wantComment  string
		checkGroupOf string
		wantPos      int
	}{
		{name: "step 1: cluster rule evaluated first", wantPos: 0, wantOrigin: fw.OriginCluster, wantComment: "cluster-first"},
		{name: "step 2: group-reference line itself, still cluster-block position", wantPos: 1, wantOrigin: fw.OriginCluster, checkGroupOf: "grp"},
		{name: "step 3: security group's own rule spliced in immediately after its reference", wantPos: 2, wantOrigin: fw.OriginGroup, wantComment: "group-rule"},
		{name: "step 4: guest's own rule evaluated after cluster+group block", wantPos: 3, wantOrigin: fw.OriginGuest, wantComment: "guest-last"},
	}
	if len(view.Rules) != 4 {
		t.Fatalf("len(Rules) = %d, want 4: %+v", len(view.Rules), view.Rules)
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			r := view.Rules[s.wantPos]
			if r.Pos != s.wantPos {
				t.Errorf("Pos = %d, want %d", r.Pos, s.wantPos)
			}
			if r.Origin != s.wantOrigin {
				t.Errorf("Origin = %q, want %q", r.Origin, s.wantOrigin)
			}
			if s.wantComment != "" && r.Rule.Comment != s.wantComment {
				t.Errorf("Rule.Comment = %q, want %q", r.Rule.Comment, s.wantComment)
			}
			if s.checkGroupOf != "" && r.GroupName != s.checkGroupOf {
				t.Errorf("GroupName = %q, want %q", r.GroupName, s.checkGroupOf)
			}
		})
	}

	t.Run("step 5: default policies are the final fallthrough, not part of Rules", func(t *testing.T) {
		if view.DefaultIn.Origin != fw.OriginCluster || view.DefaultIn.Policy != "DROP" {
			t.Errorf("DefaultIn = %+v", view.DefaultIn)
		}
		// No rule in Rules carries OriginDefault: defaults are a distinct,
		// always-last fallthrough, never a positioned rule entry.
		for _, r := range view.Rules {
			if r.Origin == fw.OriginDefault {
				t.Errorf("found a Rules entry with OriginDefault: %+v", r)
			}
		}
	})
}

func TestResolve_DisabledGroupReferenceDoesNotExpand(t *testing.T) {
	cluster := &inventory.FwRuleset{
		Ref: inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"}, Scope: inventory.FwScopeCluster, Enabled: true,
		Rules: []inventory.FwRule{
			{Pos: 0, Enabled: false, Direction: "group", Action: "grp"},
		},
		Groups: []inventory.FwGroup{
			{Name: "grp", Rules: []inventory.FwRule{{Pos: 0, Enabled: true, Direction: "in", Action: "ACCEPT"}}},
		},
	}
	g := guestRef("pve1", "1")
	rs := &inventory.FwRuleset{Ref: guestRulesetRef("pve1", "qemu", "1"), Scope: inventory.FwScopeGuest, Enabled: true}
	snap := buildSnapshot(t, cluster, rs)

	view, err := fw.Resolve(snap, g)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(view.Rules) != 1 {
		t.Fatalf("len(Rules) = %d, want 1 (disabled group reference must not expand): %+v", len(view.Rules), view.Rules)
	}
	if view.Rules[0].Rule.Enabled {
		t.Errorf("Rules[0].Rule.Enabled = true, want false")
	}
}

func TestResolve_DanglingGroupReferenceExpandsToNothing(t *testing.T) {
	cluster := &inventory.FwRuleset{
		Ref: inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"}, Scope: inventory.FwScopeCluster, Enabled: true,
		Rules: []inventory.FwRule{
			{Pos: 0, Enabled: true, Direction: "group", Action: "does-not-exist"},
		},
	}
	g := guestRef("pve1", "1")
	rs := &inventory.FwRuleset{Ref: guestRulesetRef("pve1", "qemu", "1"), Scope: inventory.FwScopeGuest, Enabled: true}
	snap := buildSnapshot(t, cluster, rs)

	view, err := fw.Resolve(snap, g)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(view.Rules) != 1 {
		t.Fatalf("len(Rules) = %d, want 1 (just the reference line, nothing expanded): %+v", len(view.Rules), view.Rules)
	}
}

func TestResolve_RejectsNonGuestRef(t *testing.T) {
	snap := buildSnapshot(t)
	_, err := fw.Resolve(snap, inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"})
	if err == nil {
		t.Fatal("Resolve with a non-guest ref: want error, got nil")
	}
}

// --- acceptance criterion 3: enablement banners cascade -------------------

func TestScopeBanners_DatacenterOffCascades(t *testing.T) {
	cluster := &inventory.FwRuleset{
		Ref: inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"}, Scope: inventory.FwScopeCluster, Enabled: false,
	}
	nodeRS := &inventory.FwRuleset{Ref: nodeRulesetRef("pve1"), Scope: inventory.FwScopeNode, Enabled: true}
	guestRS := &inventory.FwRuleset{Ref: guestRulesetRef("pve1", "qemu", "100"), Scope: inventory.FwScopeGuest, Enabled: true}
	snap := buildSnapshot(t, cluster, nodeRS, guestRS)

	t.Run("datacenter tab itself", func(t *testing.T) {
		gates := fw.ScopeBanners(snap, inventory.FwScopeCluster, "", snap.Cluster)
		if len(gates) != 1 || gates[0].Scope != inventory.FwScopeCluster {
			t.Fatalf("gates = %+v, want exactly one cluster-scope gate", gates)
		}
	})

	t.Run("node tab, even though node's own firewall is on", func(t *testing.T) {
		gates := fw.ScopeBanners(snap, inventory.FwScopeNode, "pve1", snap.Nodes["pve1"])
		if len(gates) != 1 {
			t.Fatalf("gates = %+v, want exactly one (cascaded) gate", gates)
		}
		if gates[0].Scope != inventory.FwScopeCluster {
			t.Errorf("gates[0].Scope = %q, want cluster (cascaded cause)", gates[0].Scope)
		}
	})

	t.Run("guest tab, even though guest's own firewall is on", func(t *testing.T) {
		gates := fw.ScopeBanners(snap, inventory.FwScopeGuest, "pve1", snap.Guests[guestRef("pve1", "100")])
		if len(gates) != 1 {
			t.Fatalf("gates = %+v, want exactly one (cascaded) gate", gates)
		}
		if gates[0].Scope != inventory.FwScopeCluster {
			t.Errorf("gates[0].Scope = %q, want cluster (cascaded cause)", gates[0].Scope)
		}
	})

	// T-3103: vnet scope must cascade the same way node/guest do — a
	// `default: return nil` arm added instead of an explicit vnet case
	// would silently swallow this gate.
	t.Run("vnet tab, even though vnet's own firewall is on", func(t *testing.T) {
		vnetRS := &inventory.FwRuleset{Ref: vnetRulesetRef("zone1", "vnet1"), Scope: inventory.FwScopeVNet, Enabled: true}
		vnetSnap := buildSnapshot(t, cluster, vnetRS)
		gates := fw.ScopeBanners(vnetSnap, inventory.FwScopeVNet, "zone1/vnet1", vnetRS)
		if len(gates) != 1 {
			t.Fatalf("gates = %+v, want exactly one (cascaded) gate", gates)
		}
		if gates[0].Scope != inventory.FwScopeCluster {
			t.Errorf("gates[0].Scope = %q, want cluster (cascaded cause)", gates[0].Scope)
		}
	})
}

func TestScopeBanners_NodeAndGuestOwnGatesStack(t *testing.T) {
	cluster := &inventory.FwRuleset{Ref: inventory.Ref{Kind: inventory.KindFwRuleset, ID: "cluster"}, Scope: inventory.FwScopeCluster, Enabled: true}
	nodeRS := &inventory.FwRuleset{Ref: nodeRulesetRef("pve1"), Scope: inventory.FwScopeNode, Enabled: false}
	snap := buildSnapshot(t, cluster, nodeRS)

	gates := fw.ScopeBanners(snap, inventory.FwScopeNode, "pve1", snap.Nodes["pve1"])
	if len(gates) != 1 || gates[0].Scope != inventory.FwScopeNode {
		t.Fatalf("gates = %+v, want exactly one node-scope gate (cluster is on)", gates)
	}
}
