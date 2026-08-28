// SPDX-License-Identifier: Apache-2.0

package collect_test

import (
	"context"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

const fixtureFirewallScenarios = "../../testdata/clusters/firewall-scenarios.yaml"

// TestFirewallScenarios_GoldenResolvedViews is T-501 acceptance criterion
// 1's fixture-driven counterpart to internal/fw's own unit tests: it runs
// the real collector against testdata/clusters/firewall-scenarios.yaml
// (via pvemock) and resolves each of its five purpose-built guests,
// proving the whole pipeline — mock PVE API -> collector -> inventory
// graph -> fw.BuildSnapshot -> fw.Resolve — produces the documented
// resolved view, not just the pure fw package in isolation.
func TestFirewallScenarios_GoldenResolvedViews(t *testing.T) {
	srv := loadFixtureServer(t, fixtureFirewallScenarios)
	c, graph, _ := newTestCollector(t, srv)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.RunPVELoop(ctx) }()

	guestRef := func(vmid string) inventory.Ref {
		return inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: vmid}
	}

	waitFor(t, 3*time.Second, "all five scenario guests to converge", func() bool {
		snap := fw.BuildSnapshot(graph.Snapshot().All())
		if snap.Cluster == nil {
			return false
		}
		for _, vmid := range []string{"100", "101", "102", "103", "104"} {
			if _, ok := snap.Guests[guestRef(vmid)]; !ok {
				return false
			}
		}
		return true
	})

	snap := fw.BuildSnapshot(graph.Snapshot().All())

	t.Run("100 cluster-only: cluster rule + cluster-referenced group, no guest rules", func(t *testing.T) {
		view, err := fw.Resolve(snap, guestRef("100"))
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if !view.Active {
			t.Fatalf("Active = false, want true: gates=%+v", view.Gates)
		}
		// cluster SSH-via-alias(0), group-ref(1), base-services http(2), base-services ipset-drop(3).
		if len(view.Rules) != 4 {
			t.Fatalf("len(Rules) = %d, want 4: %+v", len(view.Rules), view.Rules)
		}
		for _, r := range view.Rules {
			if r.Origin == fw.OriginGuest {
				t.Errorf("found a guest-origin rule on a guest with no rules of its own: %+v", r)
			}
		}
	})

	t.Run("101 group-guest: guest-scope group reference expands after the cluster block", func(t *testing.T) {
		view, err := fw.Resolve(snap, guestRef("101"))
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		// cluster block (4, as above) + guest's own group-ref(4) + webservers' one rule(5).
		if len(view.Rules) != 6 {
			t.Fatalf("len(Rules) = %d, want 6: %+v", len(view.Rules), view.Rules)
		}
		last := view.Rules[len(view.Rules)-1]
		if last.Origin != fw.OriginGroup || last.GroupName != "webservers" || last.Rule.Dport != "8080" {
			t.Errorf("last rule = %+v, want webservers group's own 8080 rule", last)
		}
	})

	t.Run("102 override-guest: guest's own DROP follows the cluster+group ACCEPT for the same port", func(t *testing.T) {
		view, err := fw.Resolve(snap, guestRef("102"))
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		last := view.Rules[len(view.Rules)-1]
		if last.Origin != fw.OriginGuest || last.Rule.Action != "DROP" || last.Rule.Dport != "80" {
			t.Errorf("last rule = %+v, want the guest's own DROP override", last)
		}
		// base-services' ACCEPT tcp/80 must still be present (and precede
		// the override), proving this is a shadow, not a rewrite.
		foundEarlierAccept := false
		for _, r := range view.Rules[:len(view.Rules)-1] {
			if r.Rule.Action == "ACCEPT" && r.Rule.Dport == "80" {
				foundEarlierAccept = true
			}
		}
		if !foundEarlierAccept {
			t.Error("expected the earlier base-services ACCEPT tcp/80 rule to still be present before the override")
		}
	})

	t.Run("103 disabled-guest: guest firewall off, Active false, rule still visible", func(t *testing.T) {
		view, err := fw.Resolve(snap, guestRef("103"))
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if view.Active {
			t.Fatal("Active = true, want false (guest-scope firewall is off)")
		}
		found := false
		for _, g := range view.Gates {
			if g.Scope == inventory.FwScopeGuest {
				found = true
			}
		}
		if !found {
			t.Errorf("Gates = %+v, want a guest-scope gate", view.Gates)
		}
	})

	t.Run("104 fallthrough-guest: no rules, no own policy, falls back to cluster policy", func(t *testing.T) {
		view, err := fw.Resolve(snap, guestRef("104"))
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

	t.Run("usage counts: base-services group and blocklist ipset each referenced correctly", func(t *testing.T) {
		usage := fw.UsageCounts(snap)
		byName := map[string]fw.ObjectUsage{}
		for _, u := range usage {
			byName[string(u.Kind)+"/"+u.Name] = u
		}
		// base-services: referenced by the cluster's own group-ref rule
		// only (webservers is a separate group, guest-referenced directly).
		if got := byName["group/base-services"]; got.Count != 1 {
			t.Errorf("base-services usage count = %d, want 1", got.Count)
		}
		if got := byName["group/webservers"]; got.Count != 1 {
			t.Errorf("webservers usage count = %d, want 1 (referenced by guest 101 only)", got.Count)
		}
		// blocklist ipset is referenced from within base-services' own rule
		// list, which allRulesetRules includes.
		if got := byName["ipset/blocklist"]; got.Count != 1 {
			t.Errorf("blocklist usage count = %d, want 1", got.Count)
		}
		if got := byName["alias/office_net"]; got.Count != 1 {
			t.Errorf("office_net usage count = %d, want 1", got.Count)
		}
	})
}
