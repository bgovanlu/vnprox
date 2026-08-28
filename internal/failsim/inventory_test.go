// SPDX-License-Identifier: Apache-2.0

package failsim

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// TestInventory_ThreeNodeVLAN covers AC5: the SPOF inventory names every
// element whose removal has nonzero impact (each node, each sole-port bond,
// each uplink bridge) and excludes every purely-redundant element (the
// individual NICs of a 2-NIC bond).
func TestInventory_ThreeNodeVLAN(t *testing.T) {
	snap, cor := threeNodeVLAN()
	entries := Inventory(Input{Snapshot: snap, Corosync: cor})

	got := map[string]bool{}
	for _, e := range entries {
		got[e.Ref.String()] = true
		if !e.Impact.nonZero() {
			t.Errorf("SPOF entry %s has zero impact — should have been excluded", e.Ref)
		}
		if e.Ref.Kind == inventory.KindPhysNic {
			t.Errorf("SPOF entry %s is a redundant uplink NIC — should have been excluded", e.Ref)
		}
	}

	wantPresent := []string{
		"node:pve1:pve1", "node:pve2:pve2", "node:pve3:pve3",
		"bond:pve1:bond0", "bond:pve2:bond0", "bond:pve3:bond0",
		"bridge:pve1:vmbr0", "bridge:pve2:vmbr0", "bridge:pve3:vmbr0",
	}
	for _, w := range wantPresent {
		if !got[w] {
			t.Errorf("SPOF inventory missing expected element %s", w)
		}
	}
	if len(entries) != len(wantPresent) {
		t.Errorf("SPOF inventory has %d entries, want %d: %v", len(entries), len(wantPresent), keys(got))
	}

	// A redundant NIC must never appear.
	for _, absent := range []string{"physnic:pve1:eno1", "physnic:pve1:eno2"} {
		if got[absent] {
			t.Errorf("SPOF inventory wrongly includes redundant NIC %s", absent)
		}
	}

	// Score drops below a perfect 100 given real SPOFs, and stays in range.
	score := Score(entries)
	if score >= 100 || score < 0 {
		t.Errorf("Score = %d, want in [0,100) given %d SPOFs", score, len(entries))
	}
}

// TestScoreInventory_CleanCluster: a fully-redundant, switch-free single-ring
// setup where nothing is a SPOF scores a perfect 100 with no entries.
func TestScoreInventory_NoSpof(t *testing.T) {
	// Two nodes, each with a redundant 2-NIC bond and no guests/SDN: removing
	// a single NIC never disconnects anything.
	w := newWorld()
	for i := 1; i <= 2; i++ {
		n := "pve" + itoa(i)
		w.node(n, "10.0.0."+itoa(i))
		w.physnic(n, "eno1", true).physnic(n, "eno2", true)
		w.bond(n, "bond0", "eno1", "eno2")
		w.bridge(n, "vmbr0", nil, "bond0")
	}
	res := ScoreInventory(Input{Snapshot: w.build()})
	// Nodes/bonds/bridges with no guests, no mgmt carrier resolvable, no
	// corosync => nothing known-broken by their removal.
	for _, e := range res.Entries {
		t.Errorf("unexpected SPOF entry %s (impact %+v)", e.Ref, e.Impact)
	}
	if res.Score != 100 {
		t.Errorf("Score = %d, want 100 for a no-SPOF cluster", res.Score)
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
