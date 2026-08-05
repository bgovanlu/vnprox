package api

// selfmetrics_reality_test.go is T-1903 AC2: "Metrics reflect reality:
// driving requests, a failing collector, and a rolled-back changeset
// through the test harness moves the expected series." Each sub-test below
// drives one of those three through a real router/service, not a synthetic
// registry populated by hand, and scrapes GET /metrics to check the result.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/metrics"
	"github.com/bgovanlu/vnprox/internal/store"
)

// newSelfMetricsTestService builds a fully apply-capable change.Service
// wired to reg (T-1903's Config.Metrics), mirroring
// snapshots_test.go's newSnapshotTestService.
func newSelfMetricsTestService(t *testing.T, reg *metrics.Registry) *change.Service {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vnprox.db")
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("db.Close: %v", closeErr)
		}
	})
	svc, err := change.NewService(change.Config{
		Changesets: store.NewChangesetRepo(db),
		Audit:      store.NewAuditRepo(db),
		Snapshots:  store.NewSnapshotRepo(db),
		Blobs:      store.NewBlobRepo(db),
		Nodes:      newFakeNodeAgentAPI(map[string]string{"pve1": snapshotTestBaseInterfaces}),
		Inventory:  snapshotFakeInventory{snap: oneNodeInventorySnapshot()},
		Now:        func() time.Time { return time.Unix(1_700_000_000, 0) },
		TimerFunc:  func(time.Duration, func()) change.Stopper { return inertTimer{} },
		Metrics:    reg,
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}
	return svc
}

func bridgeOp(node, name string) change.Op {
	return change.Op{
		Type:   change.OpBridgeCreate,
		Target: inventory.Ref{Kind: inventory.KindBridge, Node: node, ID: name},
		Params: &change.BridgeCreateParams{},
	}
}

func scrapeBody(t *testing.T, r http.Handler, token string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("scrape status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// TestSelfMetrics_RealHTTPTrafficMovesRED covers the "driving requests"
// third of AC2.
func TestSelfMetrics_RealHTTPTrafficMovesRED(t *testing.T) {
	reg := metrics.NewRegistry(testLogger())
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: false}, Topology: fakeTopologyService{},
		MetricsCounters: fakeMetricsCounterService{}, SelfMetrics: reg,
		MetricsExporter: MetricsExporterConfig{Token: []byte("tok"), BuildVersion: "test"},
	})

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		r.ServeHTTP(httptest.NewRecorder(), req)
	}

	body := scrapeBody(t, r, "tok")
	if !strings.Contains(body, `vnprox_http_requests_total{route="/api/v1/health",method="GET",status_class="2xx"} 3`) {
		t.Fatalf("scrape body missing 3 recorded /api/v1/health requests; body:\n%s", body)
	}
}

// TestSelfMetrics_FailingCollectorMovesConsecutiveFailuresGauge covers the
// "a failing collector" third of AC2 — a pull-model read of
// CollectorHealth, matching what GET /health already surfaces (this task's
// "mirror, don't invent a second notion").
func TestSelfMetrics_FailingCollectorMovesConsecutiveFailuresGauge(t *testing.T) {
	ch := stubCollectorHealth{sources: []CollectorSourceStatus{
		{Name: "pve", ConsecutiveFailures: 0},
		{Name: "host", Node: "pve1", ConsecutiveFailures: 7, LastError: "connection refused"},
	}}
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: false}, Topology: fakeTopologyService{},
		MetricsCounters: fakeMetricsCounterService{}, Collectors: ch,
		MetricsExporter: MetricsExporterConfig{Token: []byte("tok"), BuildVersion: "test"},
	})

	body := scrapeBody(t, r, "tok")
	if !strings.Contains(body, `vnprox_collector_consecutive_failures{source="host",node="pve1"} 7`) {
		t.Fatalf("scrape body missing the failing collector's gauge; body:\n%s", body)
	}
	if !strings.Contains(body, `vnprox_collector_consecutive_failures{source="pve",node=""} 0`) {
		t.Fatalf("scrape body missing the healthy collector's zero-value gauge; body:\n%s", body)
	}
}

// fakeStoreInfo is a minimal StoreInfoProvider stand-in.
type fakeStoreInfo struct {
	size            int64
	current, latest int
}

func (f fakeStoreInfo) SizeBytes() (int64, error) { return f.size, nil }
func (f fakeStoreInfo) SchemaVersion(context.Context) (int, int, error) {
	return f.current, f.latest, nil
}

// TestSelfMetrics_StoreAndWSPullModelGaugesRender covers the store-size/
// schema and WS-connection-count pull-model series end to end through the
// router (StoreInfoProvider and TopologyService.ConnCount), independent of
// the push-model Registry.
func TestSelfMetrics_StoreAndWSPullModelGaugesRender(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:            fakeAuth{authenticated: false},
		Topology:        fakeTopologyService{connCount: 4},
		MetricsCounters: fakeMetricsCounterService{},
		Store:           fakeStoreInfo{size: 123456, current: 30, latest: 30},
		MetricsExporter: MetricsExporterConfig{Token: []byte("tok"), BuildVersion: "test"},
	})

	body := scrapeBody(t, r, "tok")
	for _, want := range []string{
		"vnprox_store_size_bytes 123456",
		"vnprox_store_schema_version 30",
		"vnprox_store_schema_migration_pending 0",
		"vnprox_ws_connections 4",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape body missing %q; body:\n%s", want, body)
		}
	}
}

// TestSelfMetrics_RolledBackChangesetMovesChangeOutcomes covers the "a
// rolled-back changeset" third of AC2 — the card's own example.
func TestSelfMetrics_RolledBackChangesetMovesChangeOutcomes(t *testing.T) {
	reg := metrics.NewRegistry(testLogger())
	svc := newSelfMetricsTestService(t, reg)
	ctx := context.Background()

	cs, err := svc.Create(ctx, "root@pam", "add vmbr1", []change.Op{bridgeOp("pve1", "vmbr1")})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.Apply(ctx, cs.ID, "root@pam", nil, 0); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := svc.Rollback(ctx, cs.ID, "root@pam", nil); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: false}, Topology: fakeTopologyService{},
		MetricsCounters: fakeMetricsCounterService{}, SelfMetrics: reg,
		MetricsExporter: MetricsExporterConfig{Token: []byte("tok"), BuildVersion: "test"},
	})

	body := scrapeBody(t, r, "tok")
	for _, want := range []string{
		`vnprox_change_outcomes_total{op="apply",outcome="success"} 1`,
		`vnprox_change_outcomes_total{op="rollback",outcome="success"} 1`,
		`vnprox_change_awaiting_confirm_seconds_count{outcome="rolled_back"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape body missing %q; body:\n%s", want, body)
		}
	}
}
