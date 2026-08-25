package telemetrycollector

// consent_test.go is T-3710's AC1/AC2, demonstrated by observation rather
// than by reading the code path: a REAL collector (this package's own
// Server, stood up on an httptest server) and the REAL, shipped client
// (internal/telemetry.Submit) talking to it. With consent withheld, the
// assertion is that the collector's request count stays at zero — not that
// Submit returned a particular error, which would only be re-testing
// internal/telemetry's own unit tests. With consent given, the assertion is
// that a request actually arrived AND is readable back out through this
// package's own store.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/telemetry"
	"github.com/bgovanlu/vnprox/internal/verify"
)

// countingHandler wraps h and counts every request that reaches it, so a
// test can assert "the collector received zero requests" — an assertion
// about the network, not about a return value.
type countingHandler struct {
	http.Handler
	n atomic.Int64
}

func (c *countingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.n.Add(1)
	c.Handler.ServeHTTP(w, r)
}

func sampleVerifyReport() verify.Report {
	results := []verify.Result{
		{
			ID:           "drift.config_vs_live",
			MatrixRow:    21,
			Area:         "Drift detection (config-vs-live, node-vs-node)",
			Suite:        verify.SuiteHardware,
			Precondition: "a real PVE node",
			Status:       verify.StatusPass,
			Detail:       "node-alpha matches its staged config",
			Evidence: []verify.Evidence{
				verify.NewEvidence(verify.SourceCommand, "ssh node-alpha ip -j link", "enp3s0 up"),
			},
			DurationMS: 100,
		},
	}
	return verify.Report{
		ReportVersion: verify.CurrentReportVersion,
		GeneratedAt:   time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC),
		Suite:         verify.SuiteHardware,
		Environment: verify.Environment{
			VnproxVersion: "3.0.3",
			PVEVersion:    "pve-manager/9.2.4",
			Kernel:        "6.8.12-4-pve",
			NICModels:     []string{"enp3s0 0x8086:0x1521 pci:v00008086d00001521"},
			Nodes:         []string{"node-alpha"},
			PVEEndpoint:   "https://192.0.2.10:8006",
		},
		Results: results,
		Summary: verify.Summarize(results),
	}
}

// TestConsent_WithheldSendsNothing_GivenSendsOne is the observation-based
// proof T-3710's acceptance criteria demand: a real collector receives
// zero requests while consent is withheld, and exactly one submission,
// readable back out, once it is given.
func TestConsent_WithheldSendsNothing_GivenSendsOne(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(context.Background(), filepath.Join(dir, "telemetry.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	srv := NewServer(store)
	counter := &countingHandler{Handler: srv.Router()}
	ts := httptest.NewServer(counter)
	defer ts.Close()

	snap, err := telemetry.Build(sampleVerifyReport(), testInstallID)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// --- consent withheld ---------------------------------------------
	disabled := telemetry.Destination{Enabled: false, Endpoint: ts.URL + "/v1/submissions"}
	err = telemetry.Submit(context.Background(), disabled, snap)
	if err == nil {
		t.Fatal("Submit with consent withheld returned no error")
	}
	if counter.n.Load() != 0 {
		t.Fatalf("collector received %d requests while consent was withheld, want 0", counter.n.Load())
	}
	if n, cerr := store.Count(context.Background()); cerr != nil || n != 0 {
		t.Fatalf("store has %d rows while consent was withheld, want 0 (err=%v)", n, cerr)
	}

	// Enabled but no endpoint named is the other structurally-off state
	// (T-2503's "vnprox ships no default endpoint"). Same assertion.
	noEndpoint := telemetry.Destination{Enabled: true, Endpoint: ""}
	err = telemetry.Submit(context.Background(), noEndpoint, snap)
	if err == nil {
		t.Fatal("Submit with no endpoint configured returned no error")
	}
	if counter.n.Load() != 0 {
		t.Fatalf("collector received %d requests with no endpoint configured, want 0", counter.n.Load())
	}

	// --- consent given ---------------------------------------------------
	enabled := telemetry.Destination{Enabled: true, Endpoint: ts.URL + "/v1/submissions"}
	if err = telemetry.Submit(context.Background(), enabled, snap); err != nil {
		t.Fatalf("Submit with consent given: %v", err)
	}
	if counter.n.Load() != 1 {
		t.Fatalf("collector received %d requests after consent was given, want exactly 1", counter.n.Load())
	}

	n, err := store.Count(context.Background())
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 1 {
		t.Fatalf("store has %d rows after one consented submission, want 1", n)
	}

	sum, err := store.BuildSummary(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("BuildSummary: %v", err)
	}
	if sum.PVEVersions["pve-manager/9.2.4"] != 1 {
		t.Fatalf("summary does not show the submitted pveVersion: %#v", sum.PVEVersions)
	}
}
