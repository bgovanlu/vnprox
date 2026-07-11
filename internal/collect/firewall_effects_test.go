package collect_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

// TestFirewallEffectsPreview_MatchingGuests is T-502 acceptance criterion 4:
// the rule-effects preview for a group rule lists exactly the fixture's
// matching guests, computed via fw.MatchingGuests (which in turn calls the
// same fw.Resolve engine the read-side resolved view uses — no duplicate
// resolution logic). Runs the real collector against
// firewall-scenarios.yaml, the same fixture T-501's golden-resolved-view
// test (TestFirewallScenarios_GoldenResolvedViews) uses, so both tests stay
// honest about what that fixture actually encodes.
func TestFirewallEffectsPreview_MatchingGuests(t *testing.T) {
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

	t.Run("base-services: referenced by the cluster ruleset, matches every guest", func(t *testing.T) {
		guests, err := fw.MatchingGuests(snap, "base-services")
		if err != nil {
			t.Fatalf("MatchingGuests: %v", err)
		}
		got := refIDs(guests)
		want := []string{"100", "101", "102", "103", "104"}
		assertSameIDs(t, got, want)
	})

	t.Run("webservers: referenced only by guest 101's own ruleset", func(t *testing.T) {
		guests, err := fw.MatchingGuests(snap, "webservers")
		if err != nil {
			t.Fatalf("MatchingGuests: %v", err)
		}
		got := refIDs(guests)
		want := []string{"101"}
		assertSameIDs(t, got, want)
	})

	t.Run("nonexistent group matches nobody", func(t *testing.T) {
		guests, err := fw.MatchingGuests(snap, "no-such-group")
		if err != nil {
			t.Fatalf("MatchingGuests: %v", err)
		}
		if len(guests) != 0 {
			t.Errorf("len(guests) = %d, want 0: %+v", len(guests), guests)
		}
	})
}

func refIDs(refs []inventory.Ref) []string {
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.ID
	}
	sort.Strings(out)
	return out
}

func assertSameIDs(t *testing.T, got, want []string) {
	t.Helper()
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
