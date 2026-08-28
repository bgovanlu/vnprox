// SPDX-License-Identifier: Apache-2.0

package fwlog

import (
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/fw"
	"github.com/bgovanlu/vnprox/internal/inventory"
)

func analyticsGuestRef() inventory.Ref {
	return inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: "200"}
}

// analyticsSnapshot builds a fw.Snapshot with one guest ruleset (no cluster
// splice, to keep expected Pos values simple: guest rules start at Pos 0)
// containing three enabled rules: R0 (in/DROP/tcp/23 — will be hit), R1
// (in/ACCEPT/tcp/22 — hit once, long ago), R2 (out/ACCEPT — never hit).
func analyticsSnapshot() fw.Snapshot {
	g := analyticsGuestRef()
	rs := &inventory.FwRuleset{
		Ref:   inventory.Ref{Kind: inventory.KindFwRuleset, Node: "pve1", ID: "guest/qemu/200"},
		Scope: inventory.FwScopeGuest, Enabled: true,
		Rules: []inventory.FwRule{
			{Pos: 0, Enabled: true, Direction: "in", Action: "DROP", Proto: "tcp", Dport: "23", Comment: "R0"},
			{Pos: 1, Enabled: true, Direction: "in", Action: "ACCEPT", Proto: "tcp", Dport: "22", Comment: "R1"},
			{Pos: 2, Enabled: true, Direction: "out", Action: "ACCEPT", Comment: "R2"},
		},
	}
	return fw.Snapshot{
		Nodes:  map[string]*inventory.FwRuleset{},
		Guests: map[inventory.Ref]*inventory.FwRuleset{g: rs},
	}
}

func mustResolve(t *testing.T, snap fw.Snapshot, g inventory.Ref) fw.ResolvedView {
	t.Helper()
	v, err := fw.Resolve(snap, g)
	if err != nil {
		t.Fatalf("fw.Resolve: %v", err)
	}
	return v
}

func streamEntry(seq int64, ts time.Time, node string, vmid int, direction, action, proto, dport, src, dst string) StreamEntry {
	return StreamEntry{
		Seq: seq,
		Entry: Entry{
			Node: node, VMID: vmid, Guest: true, HasTimestamp: true, Timestamp: ts,
			Direction: direction, Action: action, Proto: proto, Dport: dport, Source: src, Dest: dst,
		},
	}
}

// TestAnalyze_HitCountsTopBlockedAndUnusedRules is T-1006 AC1's table-driven
// coverage: hit counts match expected per-rule totals; top-blocked rankings
// match expected order; a rule with zero matches over the window appears in
// unusedRules with the correct daysSinceLastHit; a rule that matched within
// the window does not.
func TestAnalyze_HitCountsTopBlockedAndUnusedRules(t *testing.T) {
	snap := analyticsSnapshot()
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

	entries := []StreamEntry{
		// R0 hit twice within the window (DROP, tcp/23).
		streamEntry(1, now.Add(-1*time.Hour), "pve1", 200, "in", "DROP", "tcp", "23", "1.1.1.1", "9.9.9.9"),
		streamEntry(2, now.Add(-2*time.Hour), "pve1", 200, "in", "DROP", "tcp", "23", "1.1.1.1", "9.9.9.9"),
		// An uncorrelated DROP line (direction=out has no enabled DROP rule)
		// — still counts toward topBlocked, which doesn't require
		// correlation.
		streamEntry(3, now.Add(-30*time.Minute), "pve1", 200, "out", "DROP", "", "", "3.3.3.3", "8.8.8.8"),
		// R1 hit once, 49 days ago — outside the default 24h window, so it
		// must NOT count toward hitCounts, but must set its lastSeenAt for
		// daysSinceLastHit.
		streamEntry(4, now.Add(-49*24*time.Hour), "pve1", 200, "in", "ACCEPT", "tcp", "22", "5.5.5.5", "6.6.6.6"),
		// R2 (out/ACCEPT) never appears in any entry at all.
	}

	got := Analyze(entries, snap, now, DefaultAnalyticsWindow, DefaultTopN)

	if len(got.HitCounts) != 1 {
		t.Fatalf("HitCounts = %+v, want exactly 1 entry (R0)", got.HitCounts)
	}
	if got.HitCounts[0].Rule.Pos != 0 || got.HitCounts[0].Hits != 2 {
		t.Errorf("HitCounts[0] = %+v, want {Pos:0 Hits:2}", got.HitCounts[0])
	}
	if !got.HitCounts[0].LastSeenAt.Equal(now.Add(-1 * time.Hour)) {
		t.Errorf("HitCounts[0].LastSeenAt = %v, want %v", got.HitCounts[0].LastSeenAt, now.Add(-1*time.Hour))
	}

	if len(got.TopBlocked.Sources) != 2 || got.TopBlocked.Sources[0] != (EndpointCount{Value: "1.1.1.1", Count: 2}) {
		t.Errorf("TopBlocked.Sources = %+v, want [{1.1.1.1 2} {3.3.3.3 1}]", got.TopBlocked.Sources)
	}
	if len(got.TopBlocked.Destinations) != 2 || got.TopBlocked.Destinations[0] != (EndpointCount{Value: "9.9.9.9", Count: 2}) {
		t.Errorf("TopBlocked.Destinations = %+v, want [{9.9.9.9 2} {8.8.8.8 1}]", got.TopBlocked.Destinations)
	}

	if len(got.UnusedRules) != 2 {
		t.Fatalf("UnusedRules = %+v, want exactly 2 entries (R1, R2)", got.UnusedRules)
	}
	// Sorted by (guestRef, origin, groupName, pos): R1 (pos1) before R2 (pos2).
	if got.UnusedRules[0].Rule.Pos != 1 || got.UnusedRules[0].DaysSinceLastHit != 49 {
		t.Errorf("UnusedRules[0] = %+v, want {Pos:1 DaysSinceLastHit:49}", got.UnusedRules[0])
	}
	if got.UnusedRules[1].Rule.Pos != 2 || got.UnusedRules[1].DaysSinceLastHit != -1 {
		t.Errorf("UnusedRules[1] = %+v, want {Pos:2 DaysSinceLastHit:-1} (never observed)", got.UnusedRules[1])
	}
	for _, u := range got.UnusedRules {
		if u.Rule.Pos == 0 {
			t.Fatalf("R0 (hit within the window) must not appear in UnusedRules: %+v", got.UnusedRules)
		}
	}
}

// TestAnalyze_RuleIdentityFollowsAMove is T-1006 AC2: moving a rule to a
// new position in the same changeset (fw.rule.move) carries its hit
// history forward. Analyze does not trust each entry's own ingestion-time-
// cached Correlation; it re-runs Correlate fresh against the CURRENT
// resolved ruleset every time it is called (see Analyze's doc comment).
// Since a rule's own direction/action/proto/port never change when it
// moves, re-resolving after the move finds the SAME conceptual rule at its
// NEW position with its hit count intact — proven here by resolving twice
// (before and after simulating the move) and confirming the freshly
// re-resolved RuleRef's count is unchanged.
func TestAnalyze_RuleIdentityFollowsAMove(t *testing.T) {
	g := analyticsGuestRef()
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

	entries := []StreamEntry{
		streamEntry(1, now.Add(-1*time.Hour), "pve1", 200, "in", "DROP", "tcp", "23", "1.1.1.1", "9.9.9.9"),
	}

	// Before the move: X (DROP tcp/23) at pos 0, Y (ACCEPT tcp/22) at pos 1.
	before := fw.Snapshot{Guests: map[inventory.Ref]*inventory.FwRuleset{g: {
		Scope: inventory.FwScopeGuest, Enabled: true,
		Rules: []inventory.FwRule{
			{Pos: 0, Enabled: true, Direction: "in", Action: "DROP", Proto: "tcp", Dport: "23"},
			{Pos: 1, Enabled: true, Direction: "in", Action: "ACCEPT", Proto: "tcp", Dport: "22"},
		},
	}}}
	beforeAnalytics := Analyze(entries, before, now, DefaultAnalyticsWindow, DefaultTopN)
	if len(beforeAnalytics.HitCounts) != 1 || beforeAnalytics.HitCounts[0].Rule.Pos != 0 || beforeAnalytics.HitCounts[0].Hits != 1 {
		t.Fatalf("before the move: HitCounts = %+v, want [{Pos:0 Hits:1}]", beforeAnalytics.HitCounts)
	}

	// fw.rule.move: X moves below Y (X now at pos 1, Y at pos 0). X's own
	// direction/action/proto/port are unchanged — only its position moved.
	after := fw.Snapshot{Guests: map[inventory.Ref]*inventory.FwRuleset{g: {
		Scope: inventory.FwScopeGuest, Enabled: true,
		Rules: []inventory.FwRule{
			{Pos: 0, Enabled: true, Direction: "in", Action: "ACCEPT", Proto: "tcp", Dport: "22"},
			{Pos: 1, Enabled: true, Direction: "in", Action: "DROP", Proto: "tcp", Dport: "23"},
		},
	}}}
	afterAnalytics := Analyze(entries, after, now, DefaultAnalyticsWindow, DefaultTopN)
	if len(afterAnalytics.HitCounts) != 1 {
		t.Fatalf("after the move: HitCounts = %+v, want exactly 1 entry", afterAnalytics.HitCounts)
	}
	got := afterAnalytics.HitCounts[0]
	if got.Rule.Pos != 1 {
		t.Fatalf("after the move: hit history did not follow the rule to its new position: %+v", got)
	}
	if got.Hits != beforeAnalytics.HitCounts[0].Hits {
		t.Errorf("after the move: Hits = %d, want the same historical count %d", got.Hits, beforeAnalytics.HitCounts[0].Hits)
	}

	// Re-resolving the guest's view (mirroring how UnusedRules enumerates
	// LiveRuleRefs) must show X — now at pos 1 — as NOT unused, since it
	// was hit within the window.
	for _, u := range afterAnalytics.UnusedRules {
		if u.Rule.Pos == 1 {
			t.Fatalf("moved rule (now at pos 1, hit within the window) must not appear in UnusedRules: %+v", afterAnalytics.UnusedRules)
		}
	}
}

func TestEnabledRuleRefs_SkipsDisabled(t *testing.T) {
	snap := analyticsSnapshot()
	g := analyticsGuestRef()
	rs := snap.Guests[g]
	rs.Rules = append(rs.Rules, inventory.FwRule{Pos: 3, Enabled: false, Direction: "in", Action: "DROP"})
	resolved := mustResolve(t, snap, g)
	refs := EnabledRuleRefs(resolved)
	for _, r := range refs {
		if r.Pos == 3 {
			t.Fatalf("EnabledRuleRefs included a disabled rule: %+v", refs)
		}
	}
	if len(refs) != 3 {
		t.Fatalf("EnabledRuleRefs = %+v, want 3 enabled rules", refs)
	}
}

func TestLiveRuleRefs_SkipsMalformedGuestRef(t *testing.T) {
	snap := analyticsSnapshot()
	// A guest-scope ruleset entry keyed by a non-guest Ref would only occur
	// via a bug elsewhere; LiveRuleRefs must degrade (skip it) rather than
	// panic, mirroring Service.correlate's own degrade-on-error contract.
	bad := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}
	snap.Guests[bad] = &inventory.FwRuleset{Scope: inventory.FwScopeGuest, Enabled: true}
	refs := LiveRuleRefs(snap)
	for _, r := range refs {
		if r.GuestRef == bad.String() {
			t.Fatalf("LiveRuleRefs must skip a malformed guest ref: %+v", refs)
		}
	}
}

func TestAnalyze_UntimestampedEntriesExcluded(t *testing.T) {
	snap := analyticsSnapshot()
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	entries := []StreamEntry{
		{Entry: Entry{Node: "pve1", VMID: 200, Guest: true, HasTimestamp: false, Direction: "in", Action: "DROP", Proto: "tcp", Dport: "23", Source: "1.1.1.1"}},
	}
	got := Analyze(entries, snap, now, DefaultAnalyticsWindow, DefaultTopN)
	if len(got.HitCounts) != 0 {
		t.Errorf("HitCounts = %+v, want none (untimestamped entry excluded)", got.HitCounts)
	}
	if len(got.TopBlocked.Sources) != 0 {
		t.Errorf("TopBlocked.Sources = %+v, want none (untimestamped entry excluded)", got.TopBlocked.Sources)
	}
}

func TestTopEndpoints_CapsAtTopN(t *testing.T) {
	counts := map[string]int{"a": 5, "b": 4, "c": 3, "d": 2}
	got := topEndpoints(counts, 2)
	if len(got) != 2 || got[0].Value != "a" || got[1].Value != "b" {
		t.Errorf("topEndpoints = %+v, want top 2 [a b]", got)
	}
}
