// SPDX-License-Identifier: Apache-2.0

package change_test

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/topology"
)

// impactGraph builds a small cluster: one node, two bridges, and `guests`
// guests attached to vmbr0 (none to vmbr1) — so a test can distinguish
// "affects a carrier" from "affects the guests on it".
func impactGraph(t *testing.T, guests int) inventory.Snapshot {
	t.Helper()
	g := inventory.NewGraph()
	ents := []inventory.Entity{
		&inventory.Node{Ref: inventory.Ref{Kind: inventory.KindNode, Node: "pve1", ID: "pve1"}, Name: "pve1", Status: "online"},
		&inventory.Bridge{Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}, Name: "vmbr0", Virt: inventory.BridgeLinux},
		&inventory.Bridge{Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr1"}, Name: "vmbr1", Virt: inventory.BridgeLinux},
	}
	for i := range guests {
		vmid := 100 + i
		guestRef := inventory.Ref{Kind: inventory.KindGuest, Node: "pve1", ID: itoaTest(vmid)}
		ents = append(ents,
			&inventory.Guest{Ref: guestRef, Name: "app" + itoaTest(i), Type: "qemu", Node: "pve1", VMID: vmid, Status: "running"},
			&inventory.GuestNic{
				Ref:          inventory.Ref{Kind: inventory.KindGuestNic, Node: "pve1", ID: itoaTest(vmid) + "/net0"},
				Guest:        guestRef,
				Key:          "net0",
				BridgeOrVnet: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"},
			},
		)
	}
	g.ApplyPoll(inventory.SourcePVECluster, inventory.Scope{}, ents)
	return g.Snapshot()
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func bridgeRefFor(node, id string) inventory.Ref {
	return inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: id}
}

// AC1: deleting a bridge with three guests reports those three guests and
// `outage`, and the reason names the bridge.
func TestImpact_DeletingABridgeWithGuestsIsAnOutageNamingThem(t *testing.T) {
	snap := impactGraph(t, 3)
	ops := []change.Op{{
		Type:   change.OpBridgeDelete,
		Target: bridgeRefFor("pve1", "vmbr0"),
		Params: &change.BridgeDeleteParams{},
	}}

	imp := change.ComputeImpact(ops, snap, nil, nil, nil)

	if imp.Disruption != change.DisruptionOutage {
		t.Fatalf("disruption = %q, want %q", imp.Disruption, change.DisruptionOutage)
	}
	if len(imp.Guests) != 3 {
		t.Fatalf("guests = %d, want 3: %+v", len(imp.Guests), imp.Guests)
	}
	if len(imp.Ops) != 1 {
		t.Fatalf("op impacts = %d, want 1", len(imp.Ops))
	}
	// Every verdict carries its reason, and the reason names what caused it.
	reason := imp.Ops[0].Reason
	if reason == "" {
		t.Fatal("an op impact with no reason: a verdict the UI cannot explain")
	}
	if !containsStr(reason, "vmbr0") {
		t.Fatalf("the reason must name the bridge: %q", reason)
	}
	if !containsStr(reason, "3 guests") {
		t.Fatalf("the reason must name how many guests: %q", reason)
	}
	// Guest identity, not just a count — an operator needs to know WHICH.
	for _, g := range imp.Guests {
		if g.Name == "" || g.VMID == 0 || g.Carrier == "" {
			t.Fatalf("guest impact is missing identity: %+v", g)
		}
	}
	if len(imp.Nodes) != 1 || imp.Nodes[0] != "pve1" {
		t.Fatalf("nodes = %v, want [pve1]", imp.Nodes)
	}
}

// AC2: an MTU-only update on an UNUSED bridge is `brief` with zero guests.
//
// This is the control for the test above: without it, an implementation that
// reported "outage, 3 guests" for everything would still pass AC1.
func TestImpact_UpdateOnAnUnusedBridgeIsBriefWithNoGuests(t *testing.T) {
	snap := impactGraph(t, 3)
	mtu := 9000
	ops := []change.Op{{
		Type:   change.OpBridgeUpdate,
		Target: bridgeRefFor("pve1", "vmbr1"), // vmbr1 has no guests
		Params: &change.BridgeUpdateParams{MTU: &mtu},
	}}

	imp := change.ComputeImpact(ops, snap, nil, nil, nil)

	if imp.Disruption != change.DisruptionBrief {
		t.Fatalf("disruption = %q, want %q", imp.Disruption, change.DisruptionBrief)
	}
	if len(imp.Guests) != 0 {
		t.Fatalf("guests = %d, want 0 — vmbr1 carries none: %+v", len(imp.Guests), imp.Guests)
	}
	if imp.Ops[0].Reason == "" {
		t.Fatal("an op impact with no reason")
	}
}

// A bridge UPDATE on a carrier that does have guests is brief, not an outage —
// the interruption is real but recovers. Getting this wrong in the alarming
// direction is how a preview stops being read.
func TestImpact_UpdateOnAUsedBridgeIsBriefNotAnOutage(t *testing.T) {
	snap := impactGraph(t, 2)
	mtu := 1500
	ops := []change.Op{{
		Type:   change.OpBridgeUpdate,
		Target: bridgeRefFor("pve1", "vmbr0"),
		Params: &change.BridgeUpdateParams{MTU: &mtu},
	}}

	imp := change.ComputeImpact(ops, snap, nil, nil, nil)
	if imp.Disruption != change.DisruptionBrief {
		t.Fatalf("disruption = %q, want %q", imp.Disruption, change.DisruptionBrief)
	}
	if len(imp.Guests) != 2 {
		t.Fatalf("guests = %d, want 2", len(imp.Guests))
	}
}

// Creating a bridge touches nothing that exists.
func TestImpact_CreatingABridgeIsNotDisruptive(t *testing.T) {
	snap := impactGraph(t, 3)
	ops := []change.Op{{
		Type:   change.OpBridgeCreate,
		Target: bridgeRefFor("pve1", "vmbr9"),
		Params: &change.BridgeCreateParams{},
	}}

	imp := change.ComputeImpact(ops, snap, nil, nil, nil)
	if imp.Disruption != change.DisruptionNone {
		t.Fatalf("disruption = %q, want %q", imp.Disruption, change.DisruptionNone)
	}
	if len(imp.Guests) != 0 {
		t.Fatalf("creating a bridge reported %d affected guests", len(imp.Guests))
	}
}

// The worst op wins: a changeset that both creates and deletes reports the
// delete. An operator reads the headline, so the headline must be the bad news.
func TestImpact_ReportsTheWorstOpAsTheHeadline(t *testing.T) {
	snap := impactGraph(t, 1)
	ops := []change.Op{
		{Type: change.OpBridgeCreate, Target: bridgeRefFor("pve1", "vmbr9"), Params: &change.BridgeCreateParams{}},
		{Type: change.OpBridgeDelete, Target: bridgeRefFor("pve1", "vmbr0"), Params: &change.BridgeDeleteParams{}},
	}
	imp := change.ComputeImpact(ops, snap, nil, nil, nil)
	if imp.Disruption != change.DisruptionOutage {
		t.Fatalf("headline disruption = %q, want %q", imp.Disruption, change.DisruptionOutage)
	}
	if len(imp.Ops) != 2 {
		t.Fatalf("op impacts = %d, want 2", len(imp.Ops))
	}
}

// AC3: TouchesMgmtPath must agree with mgmttouch.go on every case, which is
// enforced structurally — Impact calls that function rather than re-deriving
// the answer. This table drives BOTH and asserts they match, so the two can
// never diverge silently.
func TestImpact_MgmtPathFlagAgreesWithTouchesMgmtPath(t *testing.T) {
	snap := impactGraph(t, 1)
	paths := map[string][]topology.MgmtPath{
		"pve1": {{Ref: inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}}},
	}
	mtu := 1500
	cases := []struct {
		name string
		ops  []change.Op
	}{
		{
			name: "op on the management carrier",
			ops:  []change.Op{{Type: change.OpBridgeUpdate, Target: bridgeRefFor("pve1", "vmbr0"), Params: &change.BridgeUpdateParams{MTU: &mtu}}},
		},
		{
			name: "op elsewhere",
			ops:  []change.Op{{Type: change.OpBridgeUpdate, Target: bridgeRefFor("pve1", "vmbr1"), Params: &change.BridgeUpdateParams{MTU: &mtu}}},
		},
		{
			name: "no ops",
			ops:  nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := change.TouchesMgmtPath(paths, nil, nil, tc.ops)
			got := change.ComputeImpact(tc.ops, snap, paths, nil, nil).TouchesMgmtPath
			if got != want {
				t.Fatalf("Impact.TouchesMgmtPath = %v, TouchesMgmtPath = %v — the two must never disagree", got, want)
			}
		})
	}
	// Anti-vacuity: the table must contain at least one true case, or the
	// assertion above is satisfied by two constant falses.
	if !change.TouchesMgmtPath(paths, nil, nil, cases[0].ops) {
		t.Fatal("the fixture no longer produces a management-path hit; this test is now vacuous")
	}
}

// AC5: an empty changeset yields a zero impact, with empty slices rather than
// nils — the UI indexes them.
func TestImpact_EmptyChangesetIsZeroNotNil(t *testing.T) {
	imp := change.ComputeImpact(nil, impactGraph(t, 3), nil, nil, nil)
	if imp.Disruption != change.DisruptionNone {
		t.Fatalf("disruption = %q, want %q", imp.Disruption, change.DisruptionNone)
	}
	if imp.Guests == nil || imp.Nodes == nil || imp.Carriers == nil || imp.Ops == nil {
		t.Fatalf("an empty impact must carry empty slices, not nils: %+v", imp)
	}
	if len(imp.Guests) != 0 || len(imp.Nodes) != 0 || len(imp.Ops) != 0 {
		t.Fatalf("an empty changeset reported a non-empty impact: %+v", imp)
	}
}

// Guest attribution reads the LIVE graph, so a guest attached after staging is
// counted. Asserted by computing the same ops against two graphs.
func TestImpact_ReadsTheLiveGraphNotTheStagedBelief(t *testing.T) {
	ops := []change.Op{{Type: change.OpBridgeDelete, Target: bridgeRefFor("pve1", "vmbr0"), Params: &change.BridgeDeleteParams{}}}

	before := change.ComputeImpact(ops, impactGraph(t, 0), nil, nil, nil)
	if len(before.Guests) != 0 {
		t.Fatalf("with no guests attached, impact reported %d", len(before.Guests))
	}
	after := change.ComputeImpact(ops, impactGraph(t, 4), nil, nil, nil)
	if len(after.Guests) != 4 {
		t.Fatalf("after four guests attached, impact reported %d", len(after.Guests))
	}
}

// Guest ordering is deterministic, so two reads of an unchanged cluster render
// identically rather than shuffling under the operator.
func TestImpact_GuestOrderIsDeterministic(t *testing.T) {
	ops := []change.Op{{Type: change.OpBridgeDelete, Target: bridgeRefFor("pve1", "vmbr0"), Params: &change.BridgeDeleteParams{}}}
	first := change.ComputeImpact(ops, impactGraph(t, 5), nil, nil, nil).Guests
	for range 5 {
		got := change.ComputeImpact(ops, impactGraph(t, 5), nil, nil, nil).Guests
		for i := range got {
			if got[i].Ref != first[i].Ref || got[i].NIC != first[i].NIC {
				t.Fatalf("guest order is not stable: %+v vs %+v", got, first)
			}
		}
	}
}

func containsStr(h, n string) bool {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return true
		}
	}
	return false
}
