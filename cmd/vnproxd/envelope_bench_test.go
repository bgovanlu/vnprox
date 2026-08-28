// SPDX-License-Identifier: Apache-2.0

// envelope_bench_test.go is T-4107's real, end-to-end measurement of GET
// /api/v1/topology at the documented scale envelope (50 nodes, 5,000
// guests, 100 VNets — internal/pvemock.EnvelopeProfile) — scale_bench_test.
// go's harness (T-607's real-daemon-in-process pattern) reused unmodified,
// only the fixture and what gets measured differ. Unlike scale_bench_test.
// go, the fixture here is built in-process via pvemock.NewScaleProfile
// rather than loaded from a checked-in YAML file: a 50-node/5,000-guest
// fixture serialized to YAML would be a multi-megabyte file nobody would
// ever hand-review, and the whole point of a generator (rather than a
// hand-authored fixture) is that it doesn't need to be.
//
// Run with: go test ./cmd/vnproxd/ -run 'TestPerfBudgets_APIEnvelope' -v
// or:       go test ./cmd/vnproxd/ -run '^$' -bench 'AtEnvelope' -benchtime=10x
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/perfbudget"
	"github.com/bgovanlu/vnprox/internal/pvemock"
)

// envelopePerfSite is this file's own path, as perf/budgets.json spells it.
const envelopePerfSite = "cmd/vnproxd/envelope_bench_test.go"

// bootEnvelopeDaemon is bootScaleDaemon's T-4107 counterpart: same real-
// daemon-in-process pattern, but against an in-memory pvemock.Fixture built
// by pvemock.NewScaleProfile(pvemock.EnvelopeProfile) instead of a fixture
// loaded from testdata/clusters/.
func bootEnvelopeDaemon(t testing.TB) *scaleDaemon {
	t.Helper()
	repoRoot, err := repoRootAbs()
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}

	fixture, err := pvemock.NewScaleProfile(pvemock.EnvelopeProfile)
	if err != nil {
		t.Fatalf("NewScaleProfile(EnvelopeProfile): %v", err)
	}
	mockSrv := httptest.NewServer(pvemock.NewServer(fixture))
	t.Cleanup(mockSrv.Close)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving ephemeral port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	cfgPath := rewriteDevConfigWithAPIURL(t, repoRoot, t.TempDir(), port, mockSrv.URL)

	logger := testLogger()
	ctx, cancel := context.WithCancel(context.Background())
	daemonDone := make(chan error, 1)
	go func() { daemonDone <- runDaemon(ctx, daemonOptions{ConfigPath: cfgPath}, logger) }()

	client := &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test-only client for the throwaway dev cert
		},
	}
	base := fmt.Sprintf("https://127.0.0.1:%d", port)
	waitForHealth(t, client, base, daemonDone)

	sessionID, csrfToken := doLogin(t, client, base, "root@pam", "vnprox-mock")
	if sessionID == "" || csrfToken == "" {
		t.Fatal("login against the envelope fixture did not return session/csrf cookies")
	}
	d := &scaleDaemon{client: client, base: base, sessionID: sessionID, csrfToken: csrfToken, cancel: cancel, daemonDone: daemonDone}
	// 50 nodes' worth of in-flight collector polls take longer to drain on
	// cancellation than scale-lab's 8 do; see shutdownWithin's doc comment.
	t.Cleanup(func() { d.shutdownWithin(t, 45*time.Second) })

	// 50 nodes' worth of collector polling takes longer than scale-lab's 8,
	// so this waits considerably longer than bootScaleDaemon's 20s before
	// declaring the topology unpopulated, and checks for a count consistent
	// with all 50 nodes' worth of rendered nodes having landed (per-node
	// server-side collapse keeps rendered cardinality well under raw entity
	// count — see internal/topology/envelope_bench_test.go's measured
	// rendered-node count — so this threshold is set below that measured
	// figure with margin, not at the raw 5,000-guest count).
	d.waitForPopulatedTopologyAtLeast(t, sessionID, 500, 90*time.Second)
	return d
}

// waitForPopulatedTopologyAtLeast is waitForPopulatedTopology generalized
// with a configurable node-count threshold and deadline — the envelope
// fixture is large enough that scale_bench_test.go's hardcoded ">50 nodes
// within 20s" doesn't fit either number.
func (d *scaleDaemon) waitForPopulatedTopologyAtLeast(t testing.TB, sessionID string, minNodes int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		req := d.newRequest(http.MethodGet, "/api/v1/topology", nil)
		resp := d.do(t, req, sessionID)
		var body struct {
			Nodes []json.RawMessage `json:"nodes"`
		}
		err := json.NewDecoder(resp.Body).Decode(&body)
		_ = resp.Body.Close()
		if err == nil && len(body.Nodes) >= minNodes {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("topology never populated past %d nodes within %s (last count %d, err %v)", minNodes, timeout, len(body.Nodes), err)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// BenchmarkAPIAtEnvelope is BenchmarkAPIAtScale's T-4107 counterpart:
// GET /api/v1/topology latency against the 50-node/5,000-guest envelope.
func BenchmarkAPIAtEnvelope(b *testing.B) {
	d := bootEnvelopeDaemon(b)
	sessionID := d.sessionID

	samples := make([]time.Duration, 0, b.N)
	for i := 0; i < b.N; i++ {
		req := d.newRequest(http.MethodGet, "/api/v1/topology", nil)
		start := time.Now()
		resp := d.do(b, req, sessionID)
		el := time.Since(start)
		_, _ = readAndClose(resp)
		if resp.StatusCode != http.StatusOK {
			b.Fatalf("GET /topology: status %d", resp.StatusCode)
		}
		samples = append(samples, el)
	}
	reportPercentiles(b, samples)
}

// TestPerfBudgets_APIEnvelope is T-4107's perfbudget gate for GET
// /api/v1/topology at the scale envelope, following internal/collect/
// sim_bench_test.go's TestPerfBudgets_Sim pattern: the (expensive) daemon
// boot happens once, outside the measured samples, and each sample is one
// real HTTP round trip.
func TestPerfBudgets_APIEnvelope(t *testing.T) {
	file, err := perfbudget.LoadRepo()
	if err != nil {
		t.Fatalf("loading performance budgets: %v", err)
	}
	machine, err := perfbudget.Detect(file)
	if err != nil {
		t.Fatalf("calibrating this machine: %v", err)
	}

	d := bootEnvelopeDaemon(t)
	sessionID := d.sessionID

	budget, err := file.ByID("api.topology_at_envelope_ms")
	if err != nil {
		t.Fatalf("%v", err)
	}
	// One discarded request: the first request after the daemon settles
	// pays for connection setup / TLS handshake warm-up that a steady-state
	// client never repeats.
	warm := d.newRequest(http.MethodGet, "/api/v1/topology", nil)
	if resp := d.do(t, warm, sessionID); resp.StatusCode != http.StatusOK {
		_, _ = readAndClose(resp)
		t.Fatalf("warm-up GET /topology: status %d", resp.StatusCode)
	} else {
		_, _ = readAndClose(resp)
	}

	result, err := perfbudget.Measure(budget, machine, func(int) (float64, error) {
		req := d.newRequest(http.MethodGet, "/api/v1/topology", nil)
		start := time.Now()
		resp := d.do(t, req, sessionID)
		el := time.Since(start)
		body, _ := readAndClose(resp)
		if resp.StatusCode != http.StatusOK {
			return 0, fmt.Errorf("GET /topology: status %d body %s", resp.StatusCode, body)
		}
		return float64(el.Microseconds()) / 1000, nil
	})
	if err != nil {
		t.Fatalf("measuring %s: %v", budget.ID, err)
	}

	results := []perfbudget.Result{result}
	t.Logf("\n%s", perfbudget.Report(results, machine))

	if err := perfbudget.Missing(file.ForSite(envelopePerfSite), results); err != nil {
		t.Errorf("%v", err)
	}
	if err := perfbudget.Check(results); err != nil {
		t.Errorf("%v", err)
	}
}
