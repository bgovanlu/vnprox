package metrics

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/store"
)

// TestDBGrowthCeiling covers AC5: "DB growth bounded: 24h at scale-target
// entity count stays under a documented size ceiling (measured in
// report)." The scale target is docs/features/topology.md §4's
// "8 nodes x 6 NICs, 4 bridges/node" fixture (40 VNets/300 guests are SDN/
// guest entities this package never samples — only physical/bond/bridge/
// VLAN L2 interfaces have counters at all). This package samples every
// PhysNic, Bond, Bridge, and VlanIface; the scale-lab topology's L2 layer
// is approximated here as, per node: 6 PhysNics + 4 Bridges + 2 Bonds = 12
// sampled entities, x8 nodes = 96 entities — rounded up to 100 for margin.
//
// At store.MetricSampleRepo's 30s-downsampled resolution, 24h is 2880
// samples/entity (100 entities -> 288,000 rows total). Inserting the full
// 288,000 rows one at a time (matching how Sampler actually persists in
// production — one autocommit Insert per downsampled tick, never batched)
// takes ~40s, which is a lot for a package test that runs on every `go test
// ./...`. Instead this test populates a representative 1/10th-scale sample
// (100 entities x 288 buckets = 2.4h worth) through the exact same Insert
// path, measures the real on-disk bytes-per-row it produces, and linearly
// extrapolates to the full 24h row count — schema is fixed-width per row
// and SQLite's own b-tree/page overhead amortizes *better*, not worse, at
// higher row counts, so this extrapolation is a safe (if anything,
// slightly pessimistic) upper bound on the true 24h footprint. The
// completion report cites both this test's number and a full, one-off
// 288,000-row run's actual measured size for cross-check.
func TestDBGrowthCeiling(t *testing.T) {
	const (
		entities         = 100
		fullBucketsIn24h = int(24 * time.Hour / PersistBucket) // 2880
		sampleFraction   = 10
		sampleBuckets    = fullBucketsIn24h / sampleFraction // 288 (2.4h)
	)

	dbPath := filepath.Join(t.TempDir(), "growth.db")
	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	repo := store.NewMetricSampleRepo(db)
	ctx := context.Background()

	start := time.Now()
	base := int64(1_700_000_000)
	rows := 0
	for e := 0; e < entities; e++ {
		ref := fmt.Sprintf("physnic:pve%d:eno%d", e/12, e%12)
		var rx, tx uint64
		for b := 0; b < sampleBuckets; b++ {
			rx += 125_000 // ~1Mbps average steady growth
			tx += 62_500
			at := base + int64(b)*int64(PersistBucket/time.Second)
			if insertErr := repo.Insert(ctx, toMetricSample(ref, at, Counters{
				RxBytes: rx, TxBytes: tx, RxPkts: rx / 1000, TxPkts: tx / 1000,
			})); insertErr != nil {
				t.Fatalf("Insert(entity=%d, bucket=%d): %v", e, b, insertErr)
			}
			rows++
		}
	}
	insertElapsed := time.Since(start)

	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	sampledBytes := fileFootprint(t, dbPath)
	bytesPerRow := float64(sampledBytes) / float64(rows)
	projected24h := int64(bytesPerRow * float64(entities*fullBucketsIn24h))

	const ceilingBytes = 200 * 1024 * 1024 // 200MiB documented ceiling (see planning/reports/T-601.md)
	t.Logf("DB growth (sampled %d/%d of 24h): %d rows, %d bytes on disk in %v (%.1f bytes/row)",
		sampleFraction, sampleFraction, rows, sampledBytes, insertElapsed, bytesPerRow)
	t.Logf("DB growth (projected full 24h, %d entities x %d buckets = %d rows): ~%d bytes (%.1f MiB)",
		entities, fullBucketsIn24h, entities*fullBucketsIn24h, projected24h, float64(projected24h)/(1024*1024))
	if projected24h > ceilingBytes {
		t.Errorf("projected 24h metric_samples DB size = %d bytes, exceeds documented ceiling of %d bytes", projected24h, ceilingBytes)
	}
}

// fileFootprint reports dbPath's on-disk size, including any not-yet-
// checkpointed -wal/-shm sidecar files (real disk usage a growth ceiling
// must account for).
func fileFootprint(t *testing.T, dbPath string) int64 {
	t.Helper()
	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("os.Stat(%s): %v", dbPath, err)
	}
	total := info.Size()
	for _, suffix := range []string{"-wal", "-shm"} {
		if wi, statErr := os.Stat(dbPath + suffix); statErr == nil {
			total += wi.Size()
		}
	}
	return total
}
