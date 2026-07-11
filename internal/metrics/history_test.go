package metrics

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/store"
)

// openTestStore opens a real, temp-file-backed SQLite store.MetricSampleRepo
// (the same *store.DB production code uses — internal/store's migrations
// create metric_samples), so this package's downsampling/retention tests
// exercise the real persistence path end to end, not a fake.
func openTestStore(t *testing.T) *store.MetricSampleRepo {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return store.NewMetricSampleRepo(db)
}

// bondFixtureLinks returns a two-slave 802.3ad bond ("bond0" over "eno1"/
// "eno2", both 1000Mbps) plus a physical uplink ("eno3") not in any bond —
// a small fixture shared by the downsampling golden test and (in
// bond_test.go) the traffic-mode/imbalance test.
func bondFixtureLinks(node string) []host.LinkState {
	return []host.LinkState{
		{Kind: "physical", Name: "eno1", SpeedMbps: 1000, LinkUp: true},
		{Kind: "physical", Name: "eno2", SpeedMbps: 1000, LinkUp: true},
		{Kind: "physical", Name: "eno3", SpeedMbps: 1000, LinkUp: true},
		{
			Kind: "bond", Name: "bond0", Members: []string{"eno1", "eno2"},
			Bond: &host.BondDetail{
				Mode: "802.3ad (4)", MIIStatus: "up", ActiveSlave: "",
				Slaves: []host.BondSlave{
					{Name: "eno1", MIIStatus: "up", Active: true},
					{Name: "eno2", MIIStatus: "up", Active: true},
				},
			},
		},
	}
}

// TestSampler_DownsamplingGolden covers AC3's "downsampling correct
// (golden)": ticks arrive every 5s (matching the real host-loop cadence),
// but exactly one row per ref should be persisted per 30s wall-clock
// bucket, holding the counters observed at the first tick of that bucket.
func TestSampler_DownsamplingGolden(t *testing.T) {
	repo := openTestStore(t)
	ctx := context.Background()
	sampler := New(Config{Store: repo, Logger: testLogger()})

	links := bondFixtureLinks("pve1")
	base := time.Unix(1_700_000_000, 0).Truncate(30 * time.Second) // bucket-aligned start

	// 24 ticks at 5s spacing = 120s = 4 whole 30s buckets (0,30,60,90).
	// eno1's rx_bytes increases by 1000 per tick, a clean, predictable
	// progression to assert the golden values against.
	const ticks = 24
	for i := 0; i < ticks; i++ {
		at := base.Add(time.Duration(i) * 5 * time.Second)
		stats := map[string]host.IfaceStats{
			"eno1": {RxBytes: uint64(i) * 1000},
			"eno2": {RxBytes: uint64(i) * 500},
			"eno3": {RxBytes: uint64(i) * 10},
		}
		sampler.Ingest(ctx, "pve1", at, links, stats)
	}

	ref := "physnic:pve1:eno1"
	rows, err := repo.List(ctx, ref, base.Unix()-1, base.Add(200*time.Second).Unix())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("persisted rows = %d, want 4 (one per 30s bucket over 120s of 5s ticks)", len(rows))
	}

	// Golden: bucket i starts at tick i*6 (i*6*5s == i*30s), so its
	// persisted rx_bytes is (i*6)*1000.
	wantBuckets := []struct {
		at      int64
		rxBytes int64
	}{
		{base.Unix() + 0, 0},
		{base.Unix() + 30, 6 * 1000},
		{base.Unix() + 60, 12 * 1000},
		{base.Unix() + 90, 18 * 1000},
	}
	for i, want := range wantBuckets {
		if rows[i].At != want.at {
			t.Errorf("rows[%d].At = %d, want %d", i, rows[i].At, want.at)
		}
		if !rows[i].RxBytes.Valid || rows[i].RxBytes.Int64 != want.rxBytes {
			t.Errorf("rows[%d].RxBytes = %+v, want %d", i, rows[i].RxBytes, want.rxBytes)
		}
	}
}

// TestSampler_History_ComputesRatesBetweenStoredBuckets covers the
// GET /metrics/history read path: rate is derived between consecutive
// stored (already-downsampled) rows.
func TestSampler_History_ComputesRatesBetweenStoredBuckets(t *testing.T) {
	repo := openTestStore(t)
	ctx := context.Background()
	sampler := New(Config{Store: repo, Logger: testLogger()})

	ref := "physnic:pve1:eno1"
	if err := repo.Insert(ctx, toMetricSample(ref, 0, Counters{RxBytes: 0})); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := repo.Insert(ctx, toMetricSample(ref, 30, Counters{RxBytes: 30_000})); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := repo.Insert(ctx, toMetricSample(ref, 60, Counters{RxBytes: 90_000})); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	points, err := sampler.History(ctx, ref, 0, 60)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("History() len = %d, want 2 (3 stored rows -> 2 deltas)", len(points))
	}
	wantRxBps0 := float64(30_000) * 8 / 30
	if points[0].At != 30 || points[0].Rates.RxBps != wantRxBps0 {
		t.Errorf("points[0] = %+v, want at=30 rxBps=%v", points[0], wantRxBps0)
	}
	wantRxBps1 := float64(60_000) * 8 / 30
	if points[1].At != 60 || points[1].Rates.RxBps != wantRxBps1 {
		t.Errorf("points[1] = %+v, want at=60 rxBps=%v", points[1], wantRxBps1)
	}
}

// TestSampler_RetentionTimeTravel covers AC3's "24h retention verified in a
// time-travel test": samples older than store.MetricRetention (24h) are
// pruned; samples within the window survive.
func TestSampler_RetentionTimeTravel(t *testing.T) {
	repo := openTestStore(t)
	ctx := context.Background()

	ref := "physnic:pve1:eno1"
	now := time.Unix(1_700_000_000, 0)

	old := now.Add(-25 * time.Hour)
	recent := now.Add(-1 * time.Hour)
	if err := repo.Insert(ctx, toMetricSample(ref, old.Unix(), Counters{RxBytes: 1})); err != nil {
		t.Fatalf("Insert old: %v", err)
	}
	if err := repo.Insert(ctx, toMetricSample(ref, recent.Unix(), Counters{RxBytes: 2})); err != nil {
		t.Fatalf("Insert recent: %v", err)
	}

	if _, err := repo.PruneRetention(ctx, now); err != nil {
		t.Fatalf("PruneRetention: %v", err)
	}

	rows, err := repo.List(ctx, ref, 0, now.Unix())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows after PruneRetention = %d, want 1 (only the sample within 24h)", len(rows))
	}
	if rows[0].At != recent.Unix() {
		t.Errorf("surviving row At = %d, want %d (the recent one, not the 25h-old one)", rows[0].At, recent.Unix())
	}
}
