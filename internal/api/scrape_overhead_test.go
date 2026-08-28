// SPDX-License-Identifier: Apache-2.0

package api

// scrape_overhead_test.go is T-1903 AC4: "Scrape overhead is measured and
// stated; the metrics endpoint stays well under a documented budget with
// the full series set." populatedSelfMetrics below builds a registry with
// a realistic-to-generous full series set (every family this task adds,
// at a size larger than any deployment this product targets is likely to
// reach — see its own doc comment for the exact counts) so the measurement
// is a worst-case, not a best-case, number.
//
// Budget: GET /metrics must serve in well under 500ms even at this
// populated size — a sub-second bound, imperceptible against Prometheus's
// multi-second scrape cadence (default scrape_timeout is 10s; most
// operators configure 5-60s), for a route hit by at most one or two
// scrapers on a schedule, never a hot request path. See
// planning/reports/T-1903.md for the measured number this test's
// TestMetricsScrape_OverheadBudget logs, and BenchmarkMetricsScrape for a
// per-call ns/op figure.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/metrics"
)

// scrapeOverheadBudget is this task's documented budget (AC4). Chosen with
// wide headroom over both a Prometheus scrape timeout and this route's
// actual call frequency — see this file's doc comment.
const scrapeOverheadBudget = 500 * time.Millisecond

// populatedSelfMetrics builds a *metrics.Registry with a full, realistically
// large series set: 60 HTTP routes x 3 methods x a mix of status classes,
// every collector source across a 32-node cluster, a peer RPC matrix across
// 30 endpoints x 31 peers, and the change-engine/store families driven
// through their whole label vocabularies — comfortably larger than any
// vnprox deployment (a self-hosted single-cluster appliance, not a
// multi-tenant SaaS) is expected to reach in practice.
func populatedSelfMetrics() *metrics.Registry {
	reg := metrics.NewRegistry(testLogger())

	routes := make([]string, 60)
	for i := range routes {
		routes[i] = fmt.Sprintf("/api/v1/resource%d/{id}", i)
	}
	methods := []string{"GET", "POST", "PUT"}
	statuses := []int{200, 201, 400, 404, 500}
	for _, route := range routes {
		for _, method := range methods {
			for _, status := range statuses {
				reg.ObserveHTTPRequest(route, method, status, 15*time.Millisecond)
			}
		}
	}

	nodes := make([]string, 32)
	for i := range nodes {
		nodes[i] = fmt.Sprintf("pve%d", i+1)
	}
	for _, node := range nodes {
		reg.ObserveCollectorPoll("host", node, 20*time.Millisecond, nil)
	}
	reg.ObserveCollectorPoll("pve", "", 30*time.Millisecond, nil)
	reg.ObserveCollectorPoll("lldp", nodes[0], 5*time.Millisecond, nil)

	for _, op := range []string{metrics.ChangeOpApply, metrics.ChangeOpConfirm, metrics.ChangeOpRollback, metrics.ChangeOpUnattendedRevert} {
		reg.ObserveChangeOutcome(op, true)
		reg.ObserveChangeOutcome(op, false)
	}
	for _, outcome := range []string{"committed", "rolled_back", "failed"} {
		reg.ObserveAwaitingConfirmDuration(outcome, 45*time.Second)
	}

	for _, op := range []string{"select", "insert", "update", "delete", "other", "tx"} {
		reg.ObserveStoreQuery(op, 800*time.Microsecond)
	}

	endpoints := make([]string, 30)
	for i := range endpoints {
		endpoints[i] = fmt.Sprintf("/api/peer/family%d/action", i)
	}
	for _, node := range nodes {
		for _, endpoint := range endpoints {
			reg.ObservePeerCall(node, endpoint, "ok", 8*time.Millisecond)
		}
	}

	return reg
}

func populatedScrapeRouter(t testing.TB) http.Handler {
	t.Helper()
	counters := fakeMetricsCounterService{counters: exporterTestCounters()}
	reg := populatedSelfMetrics()
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: false}, Topology: fakeTopologyService{connCount: 3},
		MetricsCounters: counters, SelfMetrics: reg,
		Collectors:      stubCollectorHealth{sources: []CollectorSourceStatus{{Name: "pve"}, {Name: "host", Node: "pve1"}}},
		Store:           fakeStoreInfo{size: 10 << 20, current: 34, latest: 34},
		MetricsExporter: MetricsExporterConfig{Token: []byte("tok"), BuildVersion: "test"},
	})
}

// TestMetricsScrape_OverheadBudget is AC4's own test: it measures (not
// estimates) a real GET /metrics call against the populated registry above
// and asserts it stays under scrapeOverheadBudget.
func TestMetricsScrape_OverheadBudget(t *testing.T) {
	r := populatedScrapeRouter(t)

	// Warm up (first call pays for any lazy initialization/GC startup
	// noise) before the measured call.
	warmup := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	warmup.Header.Set("Authorization", "Bearer tok")
	r.ServeHTTP(httptest.NewRecorder(), warmup)

	const samples = 20
	var total time.Duration
	var bodyLen int
	for i := 0; i < samples; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()

		start := time.Now()
		r.ServeHTTP(rec, req)
		elapsed := time.Since(start)

		if rec.Code != http.StatusOK {
			t.Fatalf("scrape status = %d, want 200", rec.Code)
		}
		total += elapsed
		bodyLen = rec.Body.Len()
	}
	avg := total / samples

	t.Logf("GET /metrics: %d samples, avg %s, body %d bytes, budget %s", samples, avg, bodyLen, scrapeOverheadBudget)
	if avg > scrapeOverheadBudget {
		t.Errorf("average scrape latency %s exceeds the documented budget %s", avg, scrapeOverheadBudget)
	}
}

// BenchmarkMetricsScrape gives a per-call ns/op and allocs/op figure
// (`go test -bench=BenchmarkMetricsScrape -benchmem ./internal/api/`) for
// this task's report, over the same populated registry.
func BenchmarkMetricsScrape(b *testing.B) {
	r := populatedScrapeRouter(b)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	req.Header.Set("Authorization", "Bearer tok")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("scrape status = %d, want 200", rec.Code)
		}
	}
}
