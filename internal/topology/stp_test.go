// SPDX-License-Identifier: Apache-2.0

package topology

// T-3901's map-layer coverage: a bridge node's "stp-root" badge and a
// bridge-port (EdgePortOf) edge's "stp-state="/"stp-role=" badges, and the
// StpState != 0 gate that keeps every ordinary STP-disabled PVE bridge
// (pvecube's actual observed shape — see
// planning/reports/evidence/pve-9.2.4-bridge-stp-2026-08-27.txt) from
// painting itself as "root". Uses the same snapshotOf/resolved helpers
// status_internal_test.go's badgesOf table tests already establish.

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/inventory"
)

func TestBadgesOf_Bridge_STPRoot(t *testing.T) {
	ref := inventory.Ref{Kind: inventory.KindBridge, Node: "n1", ID: "vmbr0"}

	t.Run("STP enabled and root -> stp-root badge", func(t *testing.T) {
		snap := snapshotOf(t, sourceBatch{inventory.SourceHostNetlink, []inventory.Entity{
			&inventory.Bridge{
				Ref: ref, Name: "vmbr0", Virt: inventory.BridgeLinux,
				STPState: &inventory.BridgeSTP{
					RootID: "8000.aaaaaaaaaaaa", BridgeID: "8000.aaaaaaaaaaaa",
					StpState: 1, IsRoot: true,
				},
			},
		}})
		badges := badgesOf(snap, resolved(t, snap, ref))
		if !hasBadge(badges, "stp-root") {
			t.Errorf("badges = %v, want to contain stp-root", badges)
		}
	})

	t.Run("STP disabled -> no stp-root badge even though IsRoot is trivially true", func(t *testing.T) {
		// The exact "misleading" case the evidence transcript documents:
		// every standalone bridge reports RootID==BridgeID when STP is off
		// (stp_state 0), since there's no protocol electing a real root.
		snap := snapshotOf(t, sourceBatch{inventory.SourceHostNetlink, []inventory.Entity{
			&inventory.Bridge{
				Ref: ref, Name: "vmbr0", Virt: inventory.BridgeLinux,
				STPState: &inventory.BridgeSTP{
					RootID: "8000.aaaaaaaaaaaa", BridgeID: "8000.aaaaaaaaaaaa",
					StpState: 0, IsRoot: true,
				},
			},
		}})
		badges := badgesOf(snap, resolved(t, snap, ref))
		if hasBadge(badges, "stp-root") {
			t.Errorf("badges = %v, want no stp-root badge (StpState 0)", badges)
		}
	})

	t.Run("no STP state read at all -> no badge, no panic", func(t *testing.T) {
		snap := snapshotOf(t, sourceBatch{inventory.SourceHostNetlink, []inventory.Entity{
			&inventory.Bridge{Ref: ref, Name: "vmbr0", Virt: inventory.BridgeLinux},
		}})
		badges := badgesOf(snap, resolved(t, snap, ref))
		if hasBadge(badges, "stp-root") {
			t.Errorf("badges = %v, want no stp-root badge (nil STPState)", badges)
		}
	})
}

// TestStpPortBadges_ProjectedEdges exercises the full Project pipeline
// (buildEdges -> stpPortBadges) against a bridge with one root port, one
// designated port, and one blocking port — the classic loop-breaking shape
// (constructed, not observed on pvecube — see stp_test.go's package doc
// comment and internal/host/stp_test.go's identical caveat).
func TestStpPortBadges_ProjectedEdges(t *testing.T) {
	bridgeRef := inventory.Ref{Kind: inventory.KindBridge, Node: "n1", ID: "vmbr1"}
	rootPortRef := inventory.Ref{Kind: inventory.KindPhysNic, Node: "n1", ID: "eth0"}
	designatedRef := inventory.Ref{Kind: inventory.KindPhysNic, Node: "n1", ID: "eth1"}
	blockingRef := inventory.Ref{Kind: inventory.KindPhysNic, Node: "n1", ID: "eth2"}

	snap := snapshotOf(t, sourceBatch{inventory.SourceHostNetlink, []inventory.Entity{
		&inventory.PhysNic{Ref: rootPortRef, Name: "eth0"},
		&inventory.PhysNic{Ref: designatedRef, Name: "eth1"},
		&inventory.PhysNic{Ref: blockingRef, Name: "eth2"},
		&inventory.Bridge{
			Ref: bridgeRef, Name: "vmbr1", Virt: inventory.BridgeLinux,
			PortNames: []string{"eth0", "eth1", "eth2"},
			STPState: &inventory.BridgeSTP{
				RootID: "8000.aaaaaaaaaaaa", BridgeID: "8000.bbbbbbbbbbbb",
				StpState: 1, RootPort: 1, IsRoot: false,
				Ports: []inventory.BridgePortSTP{
					{Port: "eth0", PortNo: 1, State: "forwarding", Role: "root"},
					{Port: "eth1", PortNo: 2, State: "forwarding", Role: "designated"},
					{Port: "eth2", PortNo: 3, State: "blocking", Role: "blocking"},
				},
			},
		},
	}})

	topo := Project(snap, Filter{})

	edgeBadges := func(from string) []string {
		for _, e := range topo.Edges {
			if e.From == from && e.To == bridgeRef.String() {
				return e.Badges
			}
		}
		t.Fatalf("no port-of edge found from %s to %s", from, bridgeRef.String())
		return nil
	}

	root := edgeBadges(rootPortRef.String())
	if !hasBadge(root, "stp-role=root") || !hasBadge(root, "stp-state=forwarding") {
		t.Errorf("eth0 edge badges = %v, want stp-role=root and stp-state=forwarding", root)
	}

	designated := edgeBadges(designatedRef.String())
	if !hasBadge(designated, "stp-role=designated") {
		t.Errorf("eth1 edge badges = %v, want stp-role=designated", designated)
	}

	blocking := edgeBadges(blockingRef.String())
	if !hasBadge(blocking, "stp-role=blocking") || !hasBadge(blocking, "stp-state=blocking") {
		t.Errorf("eth2 edge badges = %v, want stp-role=blocking and stp-state=blocking", blocking)
	}
}
