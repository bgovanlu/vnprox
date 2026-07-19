package latmesh_test

import (
	"context"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/latmesh"
)

// familyAwareProber returns a distinct, fixed Reading per pair Family, so
// a test can assert the two families' resulting samples never blend.
type familyAwareProber struct{}

func (familyAwareProber) Probe(_ context.Context, p latmesh.Pair) (latmesh.Reading, error) {
	if p.Family == latmesh.FamilyV6 {
		return latmesh.Reading{RttMs: 90, LossPct: 5}, nil
	}
	return latmesh.Reading{RttMs: 8, LossPct: 0}, nil
}

// TestService_DualStackSegment_IndependentV4V6Series is T-1404 acceptance
// criterion 4: a dual-stack segment (one bridge, a v4 pair and a v6 pair)
// produces two independent LinkHeat entries with their own distinct
// readings, not one merged series.
func TestService_DualStackSegment_IndependentV4V6Series(t *testing.T) {
	v4 := latmesh.Pair{Fabric: latmesh.FabricGuest, Label: "vmbr0-v4", FromNode: "pve1", ToNode: "pve2", Family: latmesh.FamilyV4}
	v6 := latmesh.Pair{Fabric: latmesh.FabricGuest, Label: "vmbr0-v6", FromNode: "pve1", ToNode: "pve2", Family: latmesh.FamilyV6}
	v4.LinkID = latmesh.ComputeLinkID(v4.Fabric, v4.Label, v4.FromNode, v4.ToNode)
	v6.LinkID = latmesh.ComputeLinkID(v6.Fabric, v6.Label, v6.FromNode, v6.ToNode)
	if v4.LinkID == v6.LinkID {
		t.Fatalf("setup: v4/v6 pairs must have distinct LinkIDs, both got %q", v4.LinkID)
	}

	ring := &fakeRing{}
	now := time.Unix(2_000_000, 0)
	svc := latmesh.New(latmesh.Config{
		Store:            ring,
		Discoverer:       latmesh.DiscovererFunc(func() []latmesh.Pair { return []latmesh.Pair{v4, v6} }),
		Prober:           familyAwareProber{},
		ProbeIntervalSec: 10,
		Now:              func() time.Time { return now },
	})

	svc.Tick(context.Background())

	heat, err := svc.Heatmap(context.Background())
	if err != nil {
		t.Fatalf("Heatmap: %v", err)
	}
	if len(heat) != 2 {
		t.Fatalf("got %d heatmap entries, want 2 (independent v4/v6 series): %+v", len(heat), heat)
	}

	byLink := map[string]latmesh.LinkHeat{}
	for _, h := range heat {
		byLink[h.LinkID] = h
	}
	gotV4, ok := byLink[v4.LinkID]
	if !ok {
		t.Fatalf("missing v4 link %q in heatmap: %+v", v4.LinkID, heat)
	}
	gotV6, ok := byLink[v6.LinkID]
	if !ok {
		t.Fatalf("missing v6 link %q in heatmap: %+v", v6.LinkID, heat)
	}
	if gotV4.RttMs != 8 || gotV4.LossPct != 0 {
		t.Errorf("v4 link reading = %+v, want rtt=8 loss=0 (unaffected by the v6 reading)", gotV4)
	}
	if gotV6.RttMs != 90 || gotV6.LossPct != 5 {
		t.Errorf("v6 link reading = %+v, want rtt=90 loss=5 (unaffected by the v4 reading)", gotV6)
	}
}
