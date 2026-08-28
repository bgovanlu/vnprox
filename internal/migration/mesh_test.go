// SPDX-License-Identifier: Apache-2.0

package migration

import (
	"testing"

	"github.com/bgovanlu/vnprox/internal/latmesh"
)

// TestSelectMeshLink_PrefersCorosyncForward: a corosync-fabric forward
// reading is preferred over a guest-fabric reading for the same pair.
func TestSelectMeshLink_PrefersCorosyncForward(t *testing.T) {
	links := []latmesh.LinkHeat{
		{Fabric: latmesh.FabricGuest, FromNode: "pve1", ToNode: "pve2", RollingLossPct: 1, RollingRttMs: 5},
		{Fabric: latmesh.FabricCorosync, FromNode: "pve1", ToNode: "pve2", RollingLossPct: 2, RollingRttMs: 8},
	}
	sig, ok := selectMeshLink(links, "pve1", "pve2")
	if !ok {
		t.Fatal("expected a match")
	}
	if sig.Fabric != latmesh.FabricCorosync || sig.Reversed {
		t.Errorf("sig = %+v, want forward corosync", sig)
	}
}

// TestSelectMeshLink_FallsBackToReverse: with no forward-direction sample
// at all, the reverse direction is used and flagged Reversed.
func TestSelectMeshLink_FallsBackToReverse(t *testing.T) {
	links := []latmesh.LinkHeat{
		{Fabric: latmesh.FabricCorosync, FromNode: "pve2", ToNode: "pve1", RollingLossPct: 3, RollingRttMs: 9},
	}
	sig, ok := selectMeshLink(links, "pve1", "pve2")
	if !ok {
		t.Fatal("expected a match")
	}
	if !sig.Reversed || sig.LossPct != 3 {
		t.Errorf("sig = %+v, want reversed corosync reading with LossPct=3", sig)
	}
}

// TestSelectMeshLink_PicksWorstWithinTier: two guest-fabric forward
// readings for the same pair (e.g. dual-stack v4/v6 links) — the worse
// (higher loss) one is surfaced, never averaged or arbitrarily chosen.
func TestSelectMeshLink_PicksWorstWithinTier(t *testing.T) {
	links := []latmesh.LinkHeat{
		{Fabric: latmesh.FabricGuest, FromNode: "pve1", ToNode: "pve2", RollingLossPct: 1, RollingRttMs: 5},
		{Fabric: latmesh.FabricGuest, FromNode: "pve1", ToNode: "pve2", RollingLossPct: 9, RollingRttMs: 40},
	}
	sig, ok := selectMeshLink(links, "pve1", "pve2")
	if !ok {
		t.Fatal("expected a match")
	}
	if sig.LossPct != 9 {
		t.Errorf("LossPct = %v, want 9 (the worse of the two readings)", sig.LossPct)
	}
}

// TestSelectMeshLink_NoData: no matching link at all reports ok=false.
func TestSelectMeshLink_NoData(t *testing.T) {
	if _, ok := selectMeshLink(nil, "pve1", "pve2"); ok {
		t.Error("expected ok=false for an empty link list")
	}
	links := []latmesh.LinkHeat{{Fabric: latmesh.FabricCorosync, FromNode: "pve1", ToNode: "pve3"}}
	if _, ok := selectMeshLink(links, "pve1", "pve2"); ok {
		t.Error("expected ok=false when no link matches this node pair")
	}
}
