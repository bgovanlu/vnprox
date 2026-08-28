// SPDX-License-Identifier: Apache-2.0

package latmesh_test

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/latmesh"
	"github.com/bgovanlu/vnprox/internal/store"
)

// fakeRing is an in-memory latmesh.Ring double — no SQLite involved, so
// Service tests stay fast and don't need internal/store's test DB helper.
type fakeRing struct {
	rows []store.LatencySample
	mu   sync.Mutex
}

func (f *fakeRing) InsertBatch(_ context.Context, samples []store.LatencySample) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, samples...)
	return nil
}

func (f *fakeRing) QueryRange(_ context.Context, linkID string, fromTs, toTs int64) ([]store.LatencySample, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.LatencySample
	for _, r := range f.rows {
		if r.LinkID == linkID && r.At >= fromTs && r.At <= toTs {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out, nil
}

func (f *fakeRing) LatestPerLink(_ context.Context) ([]store.LatencySample, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	latest := map[string]store.LatencySample{}
	for _, r := range f.rows {
		cur, ok := latest[r.LinkID]
		if !ok || r.At > cur.At {
			latest[r.LinkID] = r
		}
	}
	out := make([]store.LatencySample, 0, len(latest))
	for _, v := range latest {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LinkID < out[j].LinkID })
	return out, nil
}

func (f *fakeRing) PruneOlderThan(_ context.Context, cutoff int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var kept []store.LatencySample
	var pruned int64
	for _, r := range f.rows {
		if r.At < cutoff {
			pruned++
			continue
		}
		kept = append(kept, r)
	}
	f.rows = kept
	return pruned, nil
}

func (f *fakeRing) PruneToCap(_ context.Context, maxRows int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if maxRows <= 0 || int64(len(f.rows)) <= maxRows {
		return 0, nil
	}
	sort.Slice(f.rows, func(i, j int) bool { return f.rows[i].At < f.rows[j].At })
	pruned := int64(len(f.rows)) - maxRows
	f.rows = f.rows[pruned:]
	return pruned, nil
}

func (f *fakeRing) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

// countingProber records how many times each pair was probed, per tick and
// in total, so a scheduler test can assert "no probe pair is duplicated or
// skipped" (AC1) precisely.
type countingProber struct {
	total   map[string]int
	perTick []map[string]int
	mu      sync.Mutex
}

func newCountingProber() *countingProber {
	return &countingProber{total: map[string]int{}}
}

func (p *countingProber) Probe(_ context.Context, pair latmesh.Pair) (latmesh.Reading, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.total[pair.LinkID]++
	if len(p.perTick) == 0 {
		p.perTick = append(p.perTick, map[string]int{})
	}
	p.perTick[len(p.perTick)-1][pair.LinkID]++
	return latmesh.Reading{RttMs: 10, LossPct: 0}, nil
}

func (p *countingProber) newTick() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.perTick = append(p.perTick, map[string]int{})
}

func fixedPairs() []latmesh.Pair {
	pairs := []latmesh.Pair{
		{Fabric: latmesh.FabricCorosync, Label: "ring0", FromNode: "pve1", ToNode: "pve2"},
		{Fabric: latmesh.FabricCorosync, Label: "ring0", FromNode: "pve1", ToNode: "pve3"},
		{Fabric: latmesh.FabricGuest, Label: "vmbr0", FromNode: "pve1", ToNode: "pve2"},
		{Fabric: latmesh.FabricGuest, Label: "vmbr0", FromNode: "pve1", ToNode: "pve3"},
	}
	for i := range pairs {
		pairs[i].LinkID = latmesh.ComputeLinkID(pairs[i].Fabric, pairs[i].Label, pairs[i].FromNode, pairs[i].ToNode)
	}
	return pairs
}

// TestService_Tick_NoDuplicateOrSkippedPair: AC1 — across several ticks (an
// injected clock advancing by the configured interval each time), every
// discovered pair is probed exactly once per tick, never zero and never
// more than once.
func TestService_Tick_NoDuplicateOrSkippedPair(t *testing.T) {
	pairs := fixedPairs()
	prober := newCountingProber()
	ring := &fakeRing{}
	now := time.Unix(1_000_000, 0)

	svc := latmesh.New(latmesh.Config{
		Store:            ring,
		Discoverer:       latmesh.DiscovererFunc(func() []latmesh.Pair { return pairs }),
		Prober:           prober,
		ProbeIntervalSec: 10,
		Now:              func() time.Time { return now },
	})

	const ticks = 3
	for i := 0; i < ticks; i++ {
		prober.newTick()
		svc.Tick(context.Background())
		now = now.Add(10 * time.Second)
	}

	if len(prober.perTick) != ticks {
		t.Fatalf("recorded %d ticks, want %d", len(prober.perTick), ticks)
	}
	for tickIdx, byLink := range prober.perTick {
		if len(byLink) != len(pairs) {
			t.Errorf("tick %d: probed %d distinct pairs, want %d", tickIdx, len(byLink), len(pairs))
		}
		for _, p := range pairs {
			if n := byLink[p.LinkID]; n != 1 {
				t.Errorf("tick %d: pair %s probed %d times, want exactly 1", tickIdx, p.LinkID, n)
			}
		}
	}
	for _, p := range pairs {
		if n := prober.total[p.LinkID]; n != ticks {
			t.Errorf("pair %s probed %d times total, want %d", p.LinkID, n, ticks)
		}
	}
	if got := ring.count(); got != len(pairs)*ticks {
		t.Errorf("store has %d rows, want %d", got, len(pairs)*ticks)
	}
}

// TestService_Tick_ProbeErrorRecordsFullLoss: a Prober error (the probe
// itself could not be attempted) still produces a sample — 100% loss —
// rather than silently dropping that pair from the ring.
func TestService_Tick_ProbeErrorRecordsFullLoss(t *testing.T) {
	pairs := []latmesh.Pair{{Fabric: latmesh.FabricGuest, Label: "vmbr0", FromNode: "pve1", ToNode: "pve2", LinkID: "guest:vmbr0|pve1->pve2"}}
	ring := &fakeRing{}

	svc := latmesh.New(latmesh.Config{
		Store: ring, Discoverer: latmesh.DiscovererFunc(func() []latmesh.Pair { return pairs }),
		Prober: failingProber{}, Now: func() time.Time { return time.Unix(500, 0) },
	})
	svc.Tick(context.Background())

	latest, err := ring.LatestPerLink(context.Background())
	if err != nil {
		t.Fatalf("LatestPerLink: %v", err)
	}
	if len(latest) != 1 {
		t.Fatalf("got %d rows, want 1", len(latest))
	}
	if latest[0].LossPct != 100 {
		t.Fatalf("LossPct = %v, want 100 on a failed probe attempt", latest[0].LossPct)
	}
}

type failingProber struct{}

func (failingProber) Probe(context.Context, latmesh.Pair) (latmesh.Reading, error) {
	return latmesh.Reading{}, errProbeUnavailable
}

var errProbeUnavailable = errors.New("probe binary not found")

// TestService_Heatmap_RollingWindow: Heatmap reports the most recent
// sample's own values plus a rolling mean over Config.RollingWindow,
// excluding samples older than the window.
func TestService_Heatmap_RollingWindow(t *testing.T) {
	ring := &fakeRing{}
	linkID := "corosync:ring0|pve1->pve2"
	// Samples at t=0,10,...,50; "now" is 50. A 25s rolling window should
	// only include at>=25, i.e. at=30,40,50.
	for i, at := range []int64{0, 10, 20, 30, 40, 50} {
		rtt := float64(10 + i)
		if err := ring.InsertBatch(context.Background(), []store.LatencySample{{
			LinkID: linkID, Fabric: "corosync", FromNode: "pve1", ToNode: "pve2",
			At: at, RttMs: rtt, LossPct: 0,
		}}); err != nil {
			t.Fatalf("InsertBatch: %v", err)
		}
	}

	svc := latmesh.New(latmesh.Config{
		Store: ring, RollingWindow: 25 * time.Second,
		Now: func() time.Time { return time.Unix(50, 0) },
	})

	items, err := svc.Heatmap(context.Background())
	if err != nil {
		t.Fatalf("Heatmap: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d links, want 1", len(items))
	}
	got := items[0]
	if got.At != 50 || got.RttMs != 15 {
		t.Fatalf("current sample = %+v, want at=50 rttMs=15", got)
	}
	if got.SampleCount != 3 {
		t.Fatalf("rolling SampleCount = %d, want 3 (at=30,40,50 within a 25s window of now=50)", got.SampleCount)
	}
	wantRollingRtt := (13.0 + 14.0 + 15.0) / 3.0
	if got.RollingRttMs != wantRollingRtt {
		t.Fatalf("RollingRttMs = %v, want %v", got.RollingRttMs, wantRollingRtt)
	}
}

// TestService_LatMeshHeatmap_SatisfiesInterfaceShape confirms *Service's
// LatMeshHeatmap method has the exact no-context signature
// internal/findings.LatMeshProvider expects, so that package can depend on
// this one without an adapter — a compile-time check as much as a runtime
// one (a signature drift here would fail to build findings' provider wiring
// test, but this makes the intent explicit in this package too).
func TestService_LatMeshHeatmap_SatisfiesInterfaceShape(t *testing.T) {
	var _ interface {
		LatMeshHeatmap() ([]latmesh.LinkHeat, error)
	} = latmesh.New(latmesh.Config{})
}
