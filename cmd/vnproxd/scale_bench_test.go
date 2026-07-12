// scale_bench_test.go is T-607's backend API latency harness: real,
// end-to-end measurements of GET /api/v1/topology, POST /api/v1/simulate/
// path, and POST /api/v1/changesets/{id}/validate against the real
// production runDaemon path (the same one secretlog_test.go and
// devconfig_test.go already exercise) driven at the docs/features/
// topology.md §4 scale target via testdata/clusters/scale-lab.yaml (8
// nodes x 6 NICs, 4 bridges/node, 300 guests, 40 VNets — see
// testdata/genscale/main.go for how that fixture was generated).
//
// This intentionally reuses the "real daemon in-process against a real
// mock PVE server in-process" pattern from secretlog_test.go rather than
// inventing a new harness: same runDaemon call, same rewriteDevConfigWithAPIURL
// helper, same login flow. The only difference is the fixture and what gets
// measured.
//
// Run with: go test ./cmd/vnproxd/ -run '^$' -bench 'AtScale' -benchtime=30x
// Numbers from an actual run are transcribed into docs/performance.md; see
// planning/reports/T-607.md for the full methodology and pass/fail table.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/pvemock"
)

// scaleDaemon is one booted, authenticated instance of the real daemon
// against the scale-lab fixture, shared across the three sub-benchmarks in
// BenchmarkAPIAtScale so the (expensive: full collector poll cycle over 8
// nodes/300 guests) daemon boot only happens once per benchmark run.
type scaleDaemon struct {
	client     *http.Client
	cancel     context.CancelFunc
	daemonDone chan error
	base       string
	sessionID  string
	csrfToken  string
}

func bootScaleDaemon(t testing.TB) *scaleDaemon {
	t.Helper()
	repoRoot, err := repoRootAbs()
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}

	fixture, err := pvemock.LoadFixture(repoRoot + "/testdata/clusters/scale-lab.yaml")
	if err != nil {
		t.Fatalf("LoadFixture(scale-lab.yaml): %v", err)
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
	go func() { daemonDone <- runDaemon(ctx, cfgPath, logger) }()

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test-only client for the throwaway dev cert
		},
	}
	base := fmt.Sprintf("https://127.0.0.1:%d", port)
	waitForHealth(t, client, base, daemonDone)

	sessionID, csrfToken := doLogin(t, client, base, "root@pam", "vnprox-mock")
	if sessionID == "" || csrfToken == "" {
		t.Fatal("login against scale-lab fixture did not return session/csrf cookies")
	}
	d := &scaleDaemon{client: client, base: base, sessionID: sessionID, csrfToken: csrfToken, cancel: cancel, daemonDone: daemonDone}
	t.Cleanup(func() { d.shutdown(t) })

	// Give the collectors one full poll cycle to populate the inventory
	// graph before any measurement starts — testdata/dev.toml's
	// [collect] intervals are 10s(PVE)/5s(host)/30s(lldp); the topology
	// endpoint works off whatever's landed so far, but a cold-start
	// measurement would conflate "collector hasn't polled yet" with actual
	// endpoint cost. Poll GET /topology until it reports a populated node
	// list (up to the PVE collector's own interval plus margin).
	d.waitForPopulatedTopology(t, sessionID)
	return d
}

func (d *scaleDaemon) shutdown(t testing.TB) {
	t.Helper()
	d.cancel()
	select {
	case <-d.daemonDone:
	case <-time.After(10 * time.Second):
		t.Error("runDaemon did not return within 10s of context cancellation")
	}
}

func (d *scaleDaemon) newRequest(method, path string, body []byte) *http.Request {
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, d.base+path, reader)
	if err != nil {
		panic(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-VNPROX-CSRF", d.csrfToken)
	return req
}

func (d *scaleDaemon) do(t testing.TB, req *http.Request, sessionID string) *http.Response {
	t.Helper()
	req.AddCookie(&http.Cookie{Name: "vnprox_session", Value: sessionID})
	resp, err := d.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}
	return resp
}

func (d *scaleDaemon) waitForPopulatedTopology(t testing.TB, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		req := d.newRequest(http.MethodGet, "/api/v1/topology", nil)
		resp := d.do(t, req, sessionID)
		var body struct {
			Nodes []json.RawMessage `json:"nodes"`
		}
		err := json.NewDecoder(resp.Body).Decode(&body)
		_ = resp.Body.Close()
		if err == nil && len(body.Nodes) > 50 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("topology never populated past 50 nodes within 20s (last count %d, err %v)", len(body.Nodes), err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// percentiles reports p50/p95/p99/max of a set of durations, sorted
// in-place, via b.ReportMetric — the same "measure real samples, report
// percentiles" approach internal/inventory's TestSnapshotP99 uses, applied
// at the HTTP-endpoint level instead of the in-process Snapshot() level.
func reportPercentiles(b *testing.B, samples []time.Duration) {
	b.Helper()
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	n := len(samples)
	if n == 0 {
		return
	}
	p50 := samples[n*50/100]
	p95 := samples[min(n-1, n*95/100)]
	p99 := samples[min(n-1, n*99/100)]
	p100Max := samples[n-1]
	b.ReportMetric(float64(p50.Microseconds())/1000, "p50-ms")
	b.ReportMetric(float64(p95.Microseconds())/1000, "p95-ms")
	b.ReportMetric(float64(p99.Microseconds())/1000, "p99-ms")
	b.ReportMetric(float64(p100Max.Microseconds())/1000, "max-ms")
}

// BenchmarkAPIAtScale boots one real daemon against scale-lab.yaml and runs
// the three latency measurements T-607's task card names (topology,
// simulate, validate) as sub-benchmarks sharing that one boot.
func BenchmarkAPIAtScale(b *testing.B) {
	d := bootScaleDaemon(b)
	sessionID := d.sessionID

	b.Run("GetTopology", func(b *testing.B) {
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
	})

	b.Run("SimulatePath", func(b *testing.B) {
		// Cross-node path: pve1's first guest to pve8's first guest, over
		// tcp/22 — a real east-west path traversal through the full
		// scale-lab inventory (8 nodes, 300 guests, 40 VNets), not a
		// same-host shortcut.
		payload := []byte(`{"src":{"kind":"guest-nic","ref":"guest-nic:pve1:100/net0"},"dst":{"kind":"guest-nic","ref":"guest-nic:pve8:399/net0"},"proto":"tcp","port":22}`)
		samples := make([]time.Duration, 0, b.N)
		for i := 0; i < b.N; i++ {
			req := d.newRequest(http.MethodPost, "/api/v1/simulate/path", payload)
			start := time.Now()
			resp := d.do(b, req, sessionID)
			el := time.Since(start)
			body, _ := readAndClose(resp)
			if resp.StatusCode != http.StatusOK {
				b.Fatalf("POST /simulate/path: status %d body %s", resp.StatusCode, body)
			}
			samples = append(samples, el)
		}
		reportPercentiles(b, samples)
	})

	b.Run("ChangesetValidate", func(b *testing.B) {
		createBody := []byte(`{"title":"scale-bench","ops":[{"op":"bridge.create","target":"bridge:pve1:vmbrbench","params":{"mtu":1500,"comments":"scale bench probe"}}]}`)
		req := d.newRequest(http.MethodPost, "/api/v1/changesets", createBody)
		resp := d.do(b, req, sessionID)
		var created struct {
			ID string `json:"id"`
		}
		body, _ := readAndClose(resp)
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
			b.Fatalf("POST /changesets: status %d body %s", resp.StatusCode, body)
		}
		if err := json.Unmarshal(body, &created); err != nil || created.ID == "" {
			b.Fatalf("decoding created changeset: %v (body %s)", err, body)
		}

		samples := make([]time.Duration, 0, b.N)
		for i := 0; i < b.N; i++ {
			req := d.newRequest(http.MethodPost, "/api/v1/changesets/"+created.ID+"/validate", nil)
			start := time.Now()
			resp := d.do(b, req, sessionID)
			el := time.Since(start)
			body, _ := readAndClose(resp)
			if resp.StatusCode != http.StatusOK {
				b.Fatalf("POST /changesets/%s/validate: status %d body %s", created.ID, resp.StatusCode, body)
			}
			samples = append(samples, el)
		}
		reportPercentiles(b, samples)
	})
}

func readAndClose(resp *http.Response) ([]byte, error) {
	defer func() { _ = resp.Body.Close() }()
	buf := new(bytes.Buffer)
	_, err := buf.ReadFrom(resp.Body)
	return buf.Bytes(), err
}
