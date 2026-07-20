package federation

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/pvemock"
	"github.com/bgovanlu/vnprox/internal/store"
)

// fixtureScaleLab is the single-cluster genscale target (docs/features/
// topology.md §4, T-607): 8 nodes × 6 NICs, 4 bridges/node, 300 guests,
// 40 VNets. The multi-cluster genscale profile (T-1208, docs/performance.md
// §8) is N of these attached to one Aggregator.
const fixtureScaleLab = "../../testdata/clusters/scale-lab.yaml"

// scaleClusterCount is the T-1208 multi-cluster genscale profile: three
// scale-lab clusters (the roadmap's "three clusters on one screen" exit-demo
// number), i.e. 24 nodes / 900 guests / 120 VNets aggregate. Kept as a
// benchmark rather than a committed multi-GB fixture — StartClusterGroup boots
// N in-process pvemock servers off the one committed scale-lab.yaml, so the
// profile stays a code-stated number (this const), reproducible with:
//
//	go test ./internal/federation/ -run '^$' -bench 'BenchmarkFederation' -benchmem -benchtime=50x
const scaleClusterCount = 3

// attachScaleGroup boots n scale-lab-backed mock clusters and registers each
// with a fresh federation Service, returning an Aggregator over them.
// testing.TB so both Test and Benchmark callers can share it.
func attachScaleGroup(tb testing.TB, n int) *Aggregator {
	tb.Helper()

	db, err := store.Open(context.Background(), tb.TempDir()+"/vnprox.db")
	if err != nil {
		tb.Fatalf("store.Open: %v", err)
	}
	tb.Cleanup(func() { _ = db.Close() })

	key := make([]byte, store.KeySize)
	if _, err = rand.Read(key); err != nil {
		tb.Fatalf("rand key: %v", err)
	}
	cipher, err := store.NewSessionCipher(key)
	if err != nil {
		tb.Fatalf("NewSessionCipher: %v", err)
	}
	svc, err := NewService(Config{
		Clusters: store.NewClusterRepo(db),
		Cipher:   cipher,
		Now:      func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		tb.Fatalf("NewService: %v", err)
	}

	specs := make([]pvemock.MockClusterSpec, n)
	for i := range specs {
		specs[i] = pvemock.MockClusterSpec{
			Name:        "scale-" + string(rune('a'+i)),
			FixturePath: fixtureScaleLab,
		}
	}
	group, err := pvemock.StartClusterGroup(specs)
	if err != nil {
		tb.Fatalf("StartClusterGroup: %v", err)
	}
	tb.Cleanup(group.Close)

	for _, mc := range group.Clusters {
		if _, err = svc.Add(context.Background(), mc.Name, mc.URL,
			Credential{Kind: CredentialTicket, Username: "root@pam", Password: "vnprox-mock"}, "admin@pam"); err != nil {
			tb.Fatalf("Add cluster %s: %v", mc.Name, err)
		}
	}
	return NewAggregator(svc)
}

// TestScaleProfile_Attaches is a cheap guard that the multi-cluster genscale
// profile actually stands up (so a broken fixture path fails a normal `go
// test`, not only a benchmark run).
func TestScaleProfile_Attaches(t *testing.T) {
	agg := attachScaleGroup(t, scaleClusterCount)
	summaries, partial, failed, err := agg.TopologySummary(context.Background())
	if err != nil {
		t.Fatalf("TopologySummary: %v", err)
	}
	if partial || len(failed) != 0 {
		t.Fatalf("all %d scale clusters reachable, got partial=%v failed=%v", scaleClusterCount, partial, failed)
	}
	if len(summaries) != scaleClusterCount {
		t.Fatalf("got %d summaries, want %d", len(summaries), scaleClusterCount)
	}
	for _, s := range summaries {
		if s.Nodes != 8 {
			t.Errorf("cluster %q summarized %d nodes, want 8 (scale-lab target)", s.ClusterName, s.Nodes)
		}
	}
}

// BenchmarkFederationTopologySummary times GET /federation/topology's backing
// aggregator call across the multi-cluster genscale profile.
func BenchmarkFederationTopologySummary(b *testing.B) {
	agg := attachScaleGroup(b, scaleClusterCount)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, partial, _, err := agg.TopologySummary(ctx); err != nil || partial {
			b.Fatalf("TopologySummary: err=%v partial=%v", err, partial)
		}
	}
}

// BenchmarkFederationSearch times GET /federation/search's backing aggregator
// call (a cross-cluster fan-out) across the profile.
func BenchmarkFederationSearch(b *testing.B) {
	agg := attachScaleGroup(b, scaleClusterCount)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, _, err := agg.Search(ctx, "vmbr0", 50); err != nil {
			b.Fatalf("Search: %v", err)
		}
	}
}
