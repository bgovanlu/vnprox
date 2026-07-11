package inventory

import (
	"testing"
	"time"
)

// TestRetainStaleLLDP checks the staleness-lifecycle retention helper
// (T-302 AC3): a previously-seen neighbor absent from a fresh poll lingers
// until it crosses the 10-minute drop threshold (spec §3), scoped to its
// own node, and never duplicates an entry the fresh poll itself refreshed.
func TestRetainStaleLLDP(t *testing.T) {
	base := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

	g := NewGraph()
	oldRef := Ref{Kind: KindLldpNeighbor, Node: "pve1", ID: "eno1/aa:bb/Gi0/1"}
	other := Ref{Kind: KindLldpNeighbor, Node: "pve2", ID: "eno1/cc:dd/Gi0/2"}
	g.ApplyPoll(SourceHostLLDP, Scope{Node: "pve1"}, []Entity{
		&LldpNeighbor{Ref: oldRef, LocalIface: "eno1", Node: "pve1", ChassisID: "aa:bb", PortID: "Gi0/1", TTL: 120, LastSeen: base.Unix()},
	})
	g.ApplyPoll(SourceHostLLDP, Scope{Node: "pve2"}, []Entity{
		&LldpNeighbor{Ref: other, LocalIface: "eno1", Node: "pve2", ChassisID: "cc:dd", PortID: "Gi0/2", TTL: 120, LastSeen: base.Unix()},
	})

	t.Run("retained before drop threshold", func(t *testing.T) {
		now := base.Add(9 * time.Minute)
		snap := g.Snapshot()
		merged := RetainStaleLLDP(snap, "pve1", nil, now)
		if len(merged) != 1 {
			t.Fatalf("got %d entities, want 1 (the retained neighbor): %+v", len(merged), merged)
		}
		got := merged[0].(*LldpNeighbor)
		if got.GetRef() != oldRef {
			t.Errorf("retained ref = %v, want %v", got.GetRef(), oldRef)
		}
		if got.LastSeen != base.Unix() {
			t.Errorf("retention must not refresh LastSeen: got %d, want %d", got.LastSeen, base.Unix())
		}
	})

	t.Run("dropped after 10 minutes", func(t *testing.T) {
		now := base.Add(11 * time.Minute)
		snap := g.Snapshot()
		merged := RetainStaleLLDP(snap, "pve1", nil, now)
		if len(merged) != 0 {
			t.Fatalf("got %d entities, want 0 (past drop threshold): %+v", len(merged), merged)
		}
	})

	t.Run("scoped to node, does not leak other nodes' neighbors", func(t *testing.T) {
		now := base.Add(1 * time.Minute)
		snap := g.Snapshot()
		merged := RetainStaleLLDP(snap, "pve1", nil, now)
		for _, e := range merged {
			if e.GetRef().Node != "pve1" {
				t.Errorf("RetainStaleLLDP(node=pve1) leaked a neighbor from node %q", e.GetRef().Node)
			}
		}
	})

	t.Run("fresh entries are not duplicated", func(t *testing.T) {
		now := base.Add(30 * time.Second)
		snap := g.Snapshot()
		refreshed := &LldpNeighbor{Ref: oldRef, LocalIface: "eno1", Node: "pve1", ChassisID: "aa:bb", PortID: "Gi0/1", TTL: 120, LastSeen: now.Unix()}
		merged := RetainStaleLLDP(snap, "pve1", []Entity{refreshed}, now)
		if len(merged) != 1 {
			t.Fatalf("got %d entities, want exactly 1 (no duplicate of the freshly-reported neighbor): %+v", len(merged), merged)
		}
		if merged[0].(*LldpNeighbor).LastSeen != now.Unix() {
			t.Errorf("fresh entry's LastSeen must win over any retained copy")
		}
	})
}
