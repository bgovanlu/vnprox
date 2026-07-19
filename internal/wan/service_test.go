package wan

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/latmesh"
	"github.com/bgovanlu/vnprox/internal/store"
)

// fakeRing is an in-memory latmesh.Ring double — mirrors internal/latmesh's
// own service_test.go fakeRing (unexported/unimportable across packages,
// hence this small duplicate) so Service tests stay fast and don't need a
// real SQLite file.
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
	var n int64
	for _, r := range f.rows {
		if r.At < cutoff {
			n++
			continue
		}
		kept = append(kept, r)
	}
	f.rows = kept
	return n, nil
}

func (f *fakeRing) PruneToCap(_ context.Context, maxRows int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if maxRows <= 0 || int64(len(f.rows)) <= maxRows {
		return 0, nil
	}
	n := int64(len(f.rows)) - maxRows
	f.rows = f.rows[n:]
	return n, nil
}

// scriptedProber returns a fixed Reading per target host, defaulting to a
// clean 0%-loss reading for any host it wasn't scripted for.
type scriptedProber struct {
	byHost map[string]latmesh.Reading
}

func (p scriptedProber) Probe(_ context.Context, pair latmesh.Pair) (latmesh.Reading, error) {
	if r, ok := p.byHost[pair.ToNode]; ok {
		return r, nil
	}
	return latmesh.Reading{RttMs: 5, LossPct: 0}, nil
}

func newTestService(t *testing.T, targets *fakeTargetStore, prober latmesh.Prober, ring *fakeRing) *Service {
	t.Helper()
	now := time.Unix(1000, 0)
	return New(Config{
		Store:     ring,
		Targets:   targets,
		LocalNode: func() string { return "pve1" },
		Prober:    prober,
		Now:       func() time.Time { return now },
	})
}

// TestService_TickThenHeatmap: one probe tick against two configured
// targets on one node persists both readings and Heatmap reports them
// independently — the basic Discoverer->latmesh.Service->Ring wiring this
// task's whole "reuse the scheduler" design depends on.
func TestService_TickThenHeatmap(t *testing.T) {
	ts := &fakeTargetStore{byNode: map[string][]store.WanTarget{
		"pve1": {
			{Node: "pve1", Uplink: "vmbr0", Host: "1.1.1.1"},
			{Node: "pve1", Uplink: "vmbr1", Host: "8.8.8.8"},
		},
	}}
	prober := scriptedProber{byHost: map[string]latmesh.Reading{
		"1.1.1.1": {RttMs: 10, LossPct: 0},
		"8.8.8.8": {RttMs: 200, LossPct: 60},
	}}
	ring := &fakeRing{}
	svc := newTestService(t, ts, prober, ring)

	ctx := context.Background()
	svc.Tick(ctx)

	heat, err := svc.Heatmap(ctx)
	if err != nil {
		t.Fatalf("Heatmap: %v", err)
	}
	if len(heat) != 2 {
		t.Fatalf("got %d links, want 2: %+v", len(heat), heat)
	}
}

// TestService_Status_MultiUplinkIndependent: T-1405 AC2 — two configured
// uplinks on one node, one degraded, report independently in
// Status.Uplinks.
func TestService_Status_MultiUplinkIndependent(t *testing.T) {
	ts := &fakeTargetStore{byNode: map[string][]store.WanTarget{
		"pve1": {
			{Node: "pve1", Uplink: "vmbr0", Host: "1.1.1.1"},
			{Node: "pve1", Uplink: "vmbr1", Host: "8.8.8.8"},
		},
	}}
	prober := scriptedProber{byHost: map[string]latmesh.Reading{
		"1.1.1.1": {RttMs: 10, LossPct: 0},
		"8.8.8.8": {RttMs: 0, LossPct: 100},
	}}
	ring := &fakeRing{}
	svc := newTestService(t, ts, prober, ring)

	ctx := context.Background()
	svc.Tick(ctx)

	status, err := svc.Status(ctx, 1000)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(status.Uplinks) != 2 {
		t.Fatalf("got %d uplinks, want 2: %+v", len(status.Uplinks), status.Uplinks)
	}

	byUplink := map[string]UplinkStatus{}
	for _, u := range status.Uplinks {
		byUplink[u.Uplink] = u
	}

	healthy, ok := byUplink["vmbr0"]
	if !ok {
		t.Fatalf("vmbr0 missing from Status.Uplinks: %+v", status.Uplinks)
	}
	if healthy.Status != UplinkHealthy {
		t.Errorf("vmbr0 Status = %q, want healthy", healthy.Status)
	}

	unreachable, ok := byUplink["vmbr1"]
	if !ok {
		t.Fatalf("vmbr1 missing from Status.Uplinks: %+v", status.Uplinks)
	}
	if unreachable.Status != UplinkUnreachable {
		t.Errorf("vmbr1 Status = %q, want unreachable (100%% loss target)", unreachable.Status)
	}
	if healthy.Status == unreachable.Status {
		t.Fatalf("healthy and unreachable uplinks reported the same status — not independent")
	}
}

// TestService_ListAndReplaceTargets: the ListTargets/ReplaceTargets
// pass-through (GET/PUT /wan/targets' data source).
func TestService_ListAndReplaceTargets(t *testing.T) {
	ts := &fakeTargetStore{}
	svc := newTestService(t, ts, scriptedProber{}, &fakeRing{})
	ctx := context.Background()

	if err := svc.ReplaceTargets(ctx, "pve1", []Target{{Uplink: "vmbr0", Host: "1.1.1.1"}}, 1000); err != nil {
		t.Fatalf("ReplaceTargets: %v", err)
	}
	got, err := svc.ListTargets(ctx, "pve1")
	if err != nil {
		t.Fatalf("ListTargets: %v", err)
	}
	if len(got) != 1 || got[0].Host != "1.1.1.1" || got[0].Uplink != "vmbr0" {
		t.Fatalf("got %+v, want one vmbr0/1.1.1.1 target", got)
	}
}

// TestService_NilTargets_Degrades: a Service built with no TargetStore
// degrades quietly rather than panicking — the same optional-dependency
// convention every other Config in this codebase follows.
func TestService_NilTargets_Degrades(t *testing.T) {
	svc := New(Config{Store: &fakeRing{}, LocalNode: func() string { return "pve1" }})
	ctx := context.Background()

	if got, err := svc.ListTargets(ctx, "pve1"); got != nil || err != nil {
		t.Fatalf("ListTargets with nil Targets = (%v, %v), want (nil, nil)", got, err)
	}
	svc.Tick(ctx) // must not panic
}
