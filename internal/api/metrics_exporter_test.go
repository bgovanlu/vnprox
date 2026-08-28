// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/drift"
	"github.com/bgovanlu/vnprox/internal/findings"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/metrics"
)

// fakeMetricsCounterService is a minimal MetricsCounterService stand-in for
// router tests.
type fakeMetricsCounterService struct {
	counters []metrics.CounterSnapshot
}

func (f fakeMetricsCounterService) AllCounters() []metrics.CounterSnapshot { return f.counters }

func exporterTestCounters() []metrics.CounterSnapshot {
	return []metrics.CounterSnapshot{
		{
			Ref:      inventory.Ref{Kind: inventory.KindPhysNic, Node: "pve1", ID: "eno1"},
			Counters: metrics.Counters{RxBytes: 100, TxBytes: 200, RxPkts: 3, TxPkts: 4, RxErrs: 5, TxErrs: 6, RxDrop: 7, TxDrop: 8},
			At:       1_700_000_000,
		},
	}
}

func exporterTestRouter(t *testing.T, cfg MetricsExporterConfig) (http.Handler, *fakeFindingsService, *fakeDriftService, *change.Service) {
	t.Helper()
	changesets := newChangesetTestService(t)
	fs := &fakeFindingsService{findings: []findings.Finding{
		{ID: "health:1", Source: findings.SourceHealth, Check: "bond_slave_down", Severity: findings.SeverityError, Detail: "d1", Nodes: []string{"pve1"}},
		{ID: "health:2", Source: findings.SourceHealth, Check: "bridge_no_carrier", Severity: findings.SeverityWarning, Detail: "d2", Nodes: []string{"pve1"}},
		{ID: "health:3", Source: findings.SourceHealth, Check: "trunk_unused_vlans", Severity: findings.SeverityInfo, Detail: "d3", Nodes: []string{"pve1"}},
	}}
	ds := &fakeDriftService{findings: []drift.Finding{
		{ID: "drift:1", Check: "bridge_divergence", Severity: "warning", Detail: "x", Nodes: []string{"pve1"}},
	}}
	counters := fakeMetricsCounterService{counters: exporterTestCounters()}

	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:            fakeAuth{authenticated: false},
		Topology:        fakeTopologyService{},
		Findings:        fs,
		Drift:           ds,
		Changesets:      changesets,
		MetricsCounters: counters,
		MetricsExporter: cfg,
	})
	return r, fs, ds, changesets
}

func TestMetricsExportRoute_MissingToken401(t *testing.T) {
	r, _, _, _ := exporterTestRouter(t, MetricsExporterConfig{Token: []byte("secret-token"), BuildVersion: "test"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding error envelope: %v", err)
	}
	if body.Error.Code == "" {
		t.Error("expected the standard {\"error\":...} envelope on a missing token")
	}
}

func TestMetricsExportRoute_InvalidToken401(t *testing.T) {
	r, _, _, _ := exporterTestRouter(t, MetricsExporterConfig{Token: []byte("secret-token"), BuildVersion: "test"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body: %s", rec.Code, rec.Body.String())
	}
}

func TestMetricsExportRoute_ValidToken200(t *testing.T) {
	r, _, _, _ := exporterTestRouter(t, MetricsExporterConfig{Token: []byte("secret-token"), BuildVersion: "test"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain prefix", ct)
	}
}

// TestMetricsExportRoute_AllowFromExcludesSource covers AC2: a valid token
// from a source excluded by allow_from is a 403, checked before the token.
func TestMetricsExportRoute_AllowFromExcludesSource(t *testing.T) {
	_, allowed, err := net.ParseCIDR("10.0.0.0/8")
	if err != nil {
		t.Fatalf("parsing test CIDR: %v", err)
	}
	r, _, _, _ := exporterTestRouter(t, MetricsExporterConfig{
		Token: []byte("secret-token"), AllowFrom: []*net.IPNet{allowed}, BuildVersion: "test",
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	req.RemoteAddr = "192.168.1.5:54321"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body: %s", rec.Code, rec.Body.String())
	}
}

// TestMetricsExportRoute_AllowFromPermitsSource covers the companion case:
// a source within allow_from, with a valid token, succeeds.
func TestMetricsExportRoute_AllowFromPermitsSource(t *testing.T) {
	_, allowed, err := net.ParseCIDR("192.168.1.0/24")
	if err != nil {
		t.Fatalf("parsing test CIDR: %v", err)
	}
	r, _, _, _ := exporterTestRouter(t, MetricsExporterConfig{
		Token: []byte("secret-token"), AllowFrom: []*net.IPNet{allowed}, BuildVersion: "test",
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	req.RemoteAddr = "192.168.1.5:54321"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
}

// TestMetricsExportRoute_NoAllowFromAllowsAnySource covers the documented
// default: unset allow_from allows any source.
func TestMetricsExportRoute_NoAllowFromAllowsAnySource(t *testing.T) {
	r, _, _, _ := exporterTestRouter(t, MetricsExporterConfig{Token: []byte("secret-token"), BuildVersion: "test"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	req.RemoteAddr = "203.0.113.9:12345"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
}

// TestMetricsExportRoute_NoSessionCookieRequired covers this route's one
// documented exception to the session-cookie/CSRF convention: Auth is wired
// with authenticated:false above (no session, no cookie at all sent), yet
// the request still succeeds purely on the bearer token — proving the
// route bypasses AuthService.SessionMiddleware entirely.
func TestMetricsExportRoute_NoSessionCookieRequired(t *testing.T) {
	r, _, _, _ := exporterTestRouter(t, MetricsExporterConfig{Token: []byte("secret-token"), BuildVersion: "test"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with no session cookie at all, body: %s", rec.Code, rec.Body.String())
	}
}

// TestMetricsExportRoute_NilTokenSkipsMounting covers the nil-safe
// "MetricsExporterConfig.Token empty -> route not mounted" convention.
func TestMetricsExportRoute_NilTokenSkipsMounting(t *testing.T) {
	r, _, _, _ := exporterTestRouter(t, MetricsExporterConfig{BuildVersion: "test"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("status = 200 with an empty token, want the route to not be mounted (404/other)")
	}
}

// TestMetricsExportRoute_BodyMatchesFixtures covers AC1's body-content
// requirements and AC3 (valid, expfmt-parseable exposition text): iface
// counters (one series per observed NIC, matching the seeded fixture),
// findings-open-by-severity matching the seeded findings, drift-open,
// changesets-by-status (one draft changeset created via the real
// change.Service), and vnprox_build_info.
func TestMetricsExportRoute_BodyMatchesFixtures(t *testing.T) {
	r, _, _, changesets := exporterTestRouter(t, MetricsExporterConfig{Token: []byte("secret-token"), BuildVersion: "v9.9.9"})

	if _, err := changesets.Create(context.Background(), "root@pam", "test draft", nil); err != nil {
		t.Fatalf("seeding a draft changeset: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}

	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(rec.Body.String()))
	if err != nil {
		t.Fatalf("response body is not valid Prometheus exposition text: %v\nbody:\n%s", err, rec.Body.String())
	}

	// vnprox_iface_rx_bytes_total{ref="physnic:pve1:eno1",...} 100
	rxBytes, ok := families["vnprox_iface_rx_bytes_total"]
	if !ok || len(rxBytes.Metric) != 1 {
		t.Fatalf("vnprox_iface_rx_bytes_total = %+v, want exactly one series", rxBytes)
	}
	if got := rxBytes.Metric[0].GetCounter().GetValue(); got != 100 {
		t.Errorf("vnprox_iface_rx_bytes_total value = %v, want 100", got)
	}
	var gotRef, gotNode, gotKind string
	for _, lp := range rxBytes.Metric[0].Label {
		switch lp.GetName() {
		case "ref":
			gotRef = lp.GetValue()
		case "node":
			gotNode = lp.GetValue()
		case "kind":
			gotKind = lp.GetValue()
		}
	}
	if gotRef != "physnic:pve1:eno1" || gotNode != "pve1" || gotKind != "physnic" {
		t.Errorf("iface counter labels = ref:%q node:%q kind:%q, want physnic:pve1:eno1 / pve1 / physnic", gotRef, gotNode, gotKind)
	}

	// vnprox_findings_open{severity="error|warning|info"} matches the three
	// seeded findings, one per severity.
	findingsFam, ok := families["vnprox_findings_open"]
	if !ok {
		t.Fatal("vnprox_findings_open family missing")
	}
	gotBySeverity := map[string]float64{}
	for _, m := range findingsFam.Metric {
		for _, lp := range m.Label {
			if lp.GetName() == "severity" {
				gotBySeverity[lp.GetValue()] = m.GetGauge().GetValue()
			}
		}
	}
	want := map[string]float64{"error": 1, "warning": 1, "info": 1}
	for sev, wantN := range want {
		if gotBySeverity[sev] != wantN {
			t.Errorf("vnprox_findings_open{severity=%q} = %v, want %v", sev, gotBySeverity[sev], wantN)
		}
	}

	driftFam, ok := families["vnprox_drift_open"]
	if !ok || len(driftFam.Metric) != 1 || driftFam.Metric[0].GetGauge().GetValue() != 1 {
		t.Errorf("vnprox_drift_open = %+v, want a single series with value 1", driftFam)
	}

	changesetsFam, ok := families["vnprox_changesets"]
	if !ok {
		t.Fatal("vnprox_changesets family missing")
	}
	gotByStatus := map[string]float64{}
	for _, m := range changesetsFam.Metric {
		for _, lp := range m.Label {
			if lp.GetName() == "status" {
				gotByStatus[lp.GetValue()] = m.GetGauge().GetValue()
			}
		}
	}
	if gotByStatus["draft"] != 1 {
		t.Errorf("vnprox_changesets{status=\"draft\"} = %v, want 1", gotByStatus["draft"])
	}
	for _, st := range []string{"applying", "awaiting_confirm", "committed", "rolled_back", "failed"} {
		if gotByStatus[st] != 0 {
			t.Errorf("vnprox_changesets{status=%q} = %v, want 0", st, gotByStatus[st])
		}
	}

	buildInfo, ok := families["vnprox_build_info"]
	if !ok || len(buildInfo.Metric) != 1 || buildInfo.Metric[0].GetGauge().GetValue() != 1 {
		t.Fatalf("vnprox_build_info = %+v, want a single series with value 1", buildInfo)
	}
	var gotVersion string
	for _, lp := range buildInfo.Metric[0].Label {
		if lp.GetName() == "version" {
			gotVersion = lp.GetValue()
		}
	}
	if gotVersion != "v9.9.9" {
		t.Errorf("vnprox_build_info version label = %q, want v9.9.9", gotVersion)
	}
}

func TestMetricsExportRoute_NilFindingsDriftChangesets_StillServesIfaceCounters(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth:            fakeAuth{authenticated: false},
		Topology:        fakeTopologyService{},
		MetricsCounters: fakeMetricsCounterService{counters: exporterTestCounters()},
		MetricsExporter: MetricsExporterConfig{Token: []byte("secret-token"), BuildVersion: "test"},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(rec.Body.String()))
	if err != nil {
		t.Fatalf("response body is not valid Prometheus exposition text: %v", err)
	}
	if _, ok := families["vnprox_iface_rx_bytes_total"]; !ok {
		t.Error("vnprox_iface_rx_bytes_total missing even with nil findings/drift/changesets deps")
	}
	if fam, ok := families["vnprox_drift_open"]; !ok || fam.Metric[0].GetGauge().GetValue() != 0 {
		t.Errorf("vnprox_drift_open with a nil DriftService = %+v, want a single zero-valued series", fam)
	}
}

// TestMetricsTokenValid_ConstantTime is a light sanity check that the
// comparison helper actually rejects a mismatched token of the same
// length, not just a differently-sized one.
func TestMetricsTokenValid(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	req.Header.Set("Authorization", "Bearer abcdefgh")
	if metricsTokenValid(req, []byte("abcdefgh")) != true {
		t.Error("expected matching token to validate")
	}
	if metricsTokenValid(req, []byte("abcdefgX")) != false {
		t.Error("expected a same-length mismatched token to fail")
	}
	if metricsTokenValid(req, nil) != false {
		t.Error("expected an empty configured token to always fail")
	}

	noAuthReq := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	if metricsTokenValid(noAuthReq, []byte("abcdefgh")) != false {
		t.Error("expected a request with no Authorization header to fail")
	}
}
