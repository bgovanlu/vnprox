package sim

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

// TestSimulate_QosShapedCaveat is T-1505 acceptance criterion 4: a path
// crossing a shaped bridge surfaces the CodeQosShaped caveat; an otherwise
// identical, unshaped path does not — table test over the shared
// twoGuestBridge world (both guests attached to pve1's vmbr0, untagged,
// firewall off — the same "same-node-untagged" shape TestVerdictMatrix's
// sameL2Cases already prove Allow for).
func TestSimulate_QosShapedCaveat(t *testing.T) {
	vmbr0 := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}
	req := Request{Src: guestEP(nicRef("pve1", "100")), Dst: guestEP(nicRef("pve1", "101")), Proto: "tcp", Port: 22}

	tests := []struct {
		shapedRefs map[inventory.Ref]bool
		name       string
		wantCaveat bool
	}{
		{
			name:       "shaped bridge surfaces the caveat",
			shapedRefs: map[inventory.Ref]bool{vmbr0: true},
			wantCaveat: true,
		},
		{
			name:       "no shape at all — no caveat",
			shapedRefs: nil,
			wantCaveat: false,
		},
		{
			name: "a shape on a DIFFERENT bridge — no caveat (never crossed)",
			shapedRefs: map[inventory.Ref]bool{
				{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr9"}: true,
			},
			wantCaveat: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := twoGuestBridge(true, 0, 0, false).build()
			in.ShapedRefs = tt.shapedRefs

			res := Simulate(in, req)
			if res.Verdict != VerdictAllow {
				t.Fatalf("verdict = %q, want allow (missing=%+v)", res.Verdict, res.Missing)
			}
			got := hasCaveat(res, CodeQosShaped)
			if got != tt.wantCaveat {
				t.Fatalf("hasCaveat(qos-shaped) = %v, want %v; caveats: %s", got, tt.wantCaveat, caveatCodes(res))
			}
			if tt.wantCaveat {
				for _, c := range res.Caveats {
					if c.Code == CodeQosShaped && c.Severity != CaveatInfo {
						t.Errorf("qos-shaped caveat severity = %q, want info (a shape doesn't change reachability)", c.Severity)
					}
				}
			}
		})
	}
}

// TestSimulate_QosShapedCaveat_DedupesAcrossHops proves a path that
// attaches to the same shaped bridge at more than one hop (both src's and
// dst's own attachment hop, same-bridge same-node) still surfaces exactly
// one qos-shaped caveat (Result.addCaveat's dedup), not one per hop.
func TestSimulate_QosShapedCaveat_DedupesAcrossHops(t *testing.T) {
	vmbr0 := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr0"}
	in := twoGuestBridge(true, 0, 0, false).build()
	in.ShapedRefs = map[inventory.Ref]bool{vmbr0: true}

	res := Simulate(in, Request{Src: guestEP(nicRef("pve1", "100")), Dst: guestEP(nicRef("pve1", "101")), Proto: "tcp", Port: 22})
	if res.Verdict != VerdictAllow {
		t.Fatalf("verdict = %q, want allow", res.Verdict)
	}
	count := 0
	for _, c := range res.Caveats {
		if c.Code == CodeQosShaped {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("qos-shaped caveat count = %d, want exactly 1 (dedup across hops); caveats: %s", count, caveatCodes(res))
	}
}
