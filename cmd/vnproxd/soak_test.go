// SPDX-License-Identifier: Apache-2.0

// soak_test.go is T-2504's nightly soak and resource-leak gate: the real
// production runDaemon path, booted in-process against a real in-process
// pvemock server, driven by a seeded churn generator, with goroutines, live
// heap, RSS, open file descriptors, and every SQLite table's row count
// sampled on a fixed interval — and the verdict taken from the *trend* of
// each of those series over the second half of the run, never from its
// absolute value. internal/soak owns the sampling and the arithmetic; this
// file owns the daemon, the churn, and the tolerances.
//
// It is a normal `go test` file with no build tag, deliberately: it is
// vetted, linted, and compiled by `make check` like everything else, and it
// skips instantly unless -soak.duration is set. `make soak` is the way to
// run it.
//
//	make soak                       # the default local run
//	make soak SOAK_DURATION=8h      # the nightly run
//	make soak LEAK=goroutine        # AC1 fixture: must FAIL, naming goroutines
//	make soak LEAK=table            # AC2 fixture: must FAIL, naming the table
//	make soak LEAK=flat             # AC3 fixture: must PASS (high but flat)
//
// The three LEAK modes need the `soakleak` build tag, which `make soak`
// adds for them; see cmd/vnproxd/soakleak.go.
//
// Why the daemon runs in-process rather than as a subprocess: the same
// pattern scale_bench_test.go and secretlog_test.go already use for the
// real runDaemon path, and it makes runtime.NumGoroutine() and
// runtime.ReadMemStats direct reads rather than something that has to be
// scraped out of a debug HTTP endpoint. The cost is that the harness's own
// goroutines and heap are counted too — which is harmless for a trend gate,
// since a constant offset has zero slope.

package main

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/pvemock"
	"github.com/bgovanlu/vnprox/internal/soak"
)

var (
	soakDuration = flag.Duration("soak.duration", 0,
		"run the T-2504 soak/resource-leak gate for this long (0 = skip; `make soak` sets it)")
	soakInterval = flag.Duration("soak.interval", 5*time.Second,
		"soak sampling interval")
	soakChurnInterval = flag.Duration("soak.churn-interval", 2*time.Second,
		"soak churn interval")
	soakSeed = flag.Uint64("soak.seed", 0,
		"churn generator seed (0 = derive one from the clock and log it)")
	soakArtifacts = flag.String("soak.artifacts", "",
		"directory for samples.csv and report.json (default var/soak/<timestamp>)")
	soakFixture = flag.String("soak.fixture", "three-node-vlan.yaml",
		"pvemock fixture under testdata/clusters/ to soak against")
)

// Trend tolerances (units per minute) and absolute-rise floors (units
// across the observed window), applied to the second half of the run. A
// metric fails only when it breaks BOTH — see soak.Policy.MinRise for why
// the second condition exists and what it buys.
//
// Every number here is calibrated against real clean runs of this harness
// on the three-node fixture; docs/performance.md §12 records the measured
// clean-run slopes these were derived from. None is a guess.
const (
	// Clean runs measure |slope| < 0.3 goroutines/min once the resting-min
	// reduction (soak.Goroutines) has removed in-flight handler goroutines.
	// A goroutine leaked per PVE collection cycle is 6/min at dev.toml's 10s
	// interval — twenty times this tolerance.
	tolGoroutinesPerMin = 0.5
	riseGoroutines      = 6
	// Live heap after a forced GC. Clean runs measure under 100 KiB/min and
	// falling. 512 KiB/min is ~250 MiB over an 8-hour nightly: comfortably
	// above the noise, comfortably below anything that survives to morning
	// unnoticed.
	tolHeapBytesPerMin = 512 << 10
	riseHeapBytes      = 8 << 20
	// RSS is the coarse backstop, not the sharp instrument: the Go runtime
	// returns memory to the OS lazily and SQLite's page cache climbs toward
	// its own ceiling, so a clean short run still measures ~1.4 MiB/min
	// while it warms up. The rise floor absorbs that on short runs; the rate
	// is what gates a nightly, where RSS has long since settled.
	tolRSSBytesPerMin = 1 << 20
	riseRSSBytes      = 32 << 20
	// File descriptors are flat outright once warmed up: every listener and
	// DB handle is opened at startup. 0.2/min is "one new fd every five
	// minutes", which is already a bug.
	tolOpenFDsPerMin = 0.2
	riseOpenFDs      = 6
	// Default for any table: 10 rows/min. A table nobody prunes grows far
	// faster than this under churn (the AC2 fixture does 300/min). A table
	// that legitimately fills faster gets its own entry below, with a
	// reason, rather than loosening this one.
	tolTableRowsPerMin = 10
	riseTableRows      = 20
)

// tablesWithAStatedFillRate are the tables whose row count legitimately
// climbs for the whole length of a soak run, each with the reason and the
// measured clean-run rate the tolerance is derived from.
//
// This is an exception list, not an exclusion list, and the distinction is
// the point: every table stays gated, at a rate chosen so that a leak *on
// top of* the legitimate fill still fails. Dropping them from the gate
// instead would leave the largest tables in the store unwatched, which is
// the opposite of what this card is for.
var tablesWithAStatedFillRate = map[string]float64{
	// One row per interface per polled node per host-loop tick (5s),
	// bounded by store.MetricRetention (24h) — a window no soak run
	// reaches, so it is still filling throughout. Clean-run rate on this
	// fixture: single digits per minute; the ceiling is set for the real
	// per-interface rate a live cluster produces.
	soak.TablePrefix + "metric_samples": 2000,
	// One row per finding transition. The churn generator deliberately
	// makes the `orphan_vnet` finding appear and clear; pruned on the same
	// 24h window as metric_samples. Clean-run rate: ~0-10/min.
	soak.TablePrefix + "finding_events": 200,
	// Every mutating request the churn generator makes appends here, and
	// [retention] audit_keep_days is 730 days. Clean-run rate: ~30/min,
	// entirely churn-driven.
	soak.TablePrefix + "audit_log": 200,
	// DELETE /changesets/{id} is a *transition to `discarded`*, not a row
	// delete (internal/change.Service.Discard), so every draft the churn
	// generator creates leaves a row behind by design. Clean-run rate:
	// ~10/min, exactly the generator's own draft rate.
	soak.TablePrefix + "changesets": 120,
	// The latency & loss mesh's probe ring and the WAN prober's, both
	// retention-pruned on windows far longer than a run. Clean-run rate:
	// under 10/min on this fixture.
	soak.TablePrefix + "latency_samples":   200,
	soak.TablePrefix + "wan_probe_samples": 200,
	soak.TablePrefix + "flow_samples":      2000,
}

func soakPolicy() soak.Policy {
	perMetric := map[string]float64{
		soak.MetricGoroutines: tolGoroutinesPerMin,
		soak.MetricHeapBytes:  tolHeapBytesPerMin,
		soak.MetricRSSBytes:   tolRSSBytesPerMin,
		soak.MetricOpenFDs:    tolOpenFDsPerMin,
		soak.TablePrefix:      tolTableRowsPerMin,
	}
	for table, tol := range tablesWithAStatedFillRate {
		perMetric[table] = tol
	}
	return soak.Policy{
		Default:        tolTableRowsPerMin,
		DefaultMinRise: riseTableRows,
		PerMetric:      perMetric,
		MinRise: map[string]float64{
			soak.MetricGoroutines: riseGoroutines,
			soak.MetricHeapBytes:  riseHeapBytes,
			soak.MetricRSSBytes:   riseRSSBytes,
			soak.MetricOpenFDs:    riseOpenFDs,
			soak.TablePrefix:      riseTableRows,
		},
		// Four samples in the second half: at the default 5s interval that
		// is a 40-second run, below which the gate refuses to have an
		// opinion rather than reporting a clean run it did not observe.
		MinWindowSamples: 4,
	}
}

// TestSoak is the gate. It skips unless -soak.duration is set.
func TestSoak(t *testing.T) {
	if *soakDuration <= 0 {
		t.Skip("soak gate not requested; run `make soak` (sets -soak.duration)")
	}
	if !soak.ProcSamplersAvailable() {
		t.Skip("/proc is unavailable; the RSS and fd samplers are Linux-only by design")
	}

	seed := *soakSeed
	if seed == 0 {
		seed = uint64(time.Now().UnixNano())
	}
	artifactDir := *soakArtifacts
	if artifactDir == "" {
		repoRoot, err := repoRootAbs()
		if err != nil {
			t.Fatalf("resolving repo root: %v", err)
		}
		artifactDir = filepath.Join(repoRoot, "var", "soak", time.Now().UTC().Format("20060102T150405Z"))
	}

	logger := soakLogger()
	logger.Info("soak: starting",
		"duration", soakDuration.String(),
		"sample_interval", soakInterval.String(),
		"churn_interval", soakChurnInterval.String(),
		"seed", seed,
		"fixture", *soakFixture,
		"artifacts", artifactDir,
		"leak_fixture", os.Getenv("VNPROX_SOAK_LEAK"))

	d := bootSoakDaemon(t, logger)
	samplers := soakSamplers(t, d)
	churn := newSoakChurn(d, seed, logger)

	res, runErr := soak.Run(context.Background(), soak.Config{
		Duration:      *soakDuration,
		Interval:      *soakInterval,
		ChurnInterval: *soakChurnInterval,
		Seed:          seed,
		Samplers:      samplers,
		Policy:        soakPolicy(),
		Churn:         churn.tick,
		Logger:        logger,
	})
	if res != nil {
		paths, err := soak.WriteArtifact(artifactDir, res)
		if err != nil {
			t.Errorf("writing the soak artifact: %v", err)
		} else {
			logger.Info("soak: artifact written", "paths", strings.Join(paths, " "))
		}
	}
	if runErr != nil {
		t.Fatalf("soak run: %v", runErr)
	}

	logger.Info("soak: churn summary", "ticks", res.ChurnTicks, "errors", res.ChurnErrors,
		"guests_created", churn.guestsCreated, "guests_destroyed", churn.guestsDestroyed,
		"node_flaps", churn.nodeFlaps, "finding_toggles", churn.findingToggles,
		"draft_cycles", churn.draftCycles, "reads", churn.reads)
	for _, v := range res.Report.Verdicts {
		logger.Info("soak: verdict", "metric", v.Metric, "pass", v.Pass, "reason", v.Reason)
	}
	if err := res.Report.Err(); err != nil {
		t.Fatalf("%v\n\nsample series and verdict: %s", err, artifactDir)
	}
}

func soakLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// soakDaemon is one booted, authenticated daemon plus the mock it polls and
// a read-only handle on its store.
type soakDaemon struct {
	client     *http.Client
	mockState  *pvemock.State
	storeDB    *sql.DB
	daemonDone chan error
	cancel     context.CancelFunc
	base       string
	sessionID  string
	csrfToken  string
	nodes      []string
}

func bootSoakDaemon(t *testing.T, logger *slog.Logger) *soakDaemon {
	t.Helper()
	repoRoot, err := repoRootAbs()
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}
	fixturePath := filepath.Join(repoRoot, "testdata", "clusters", *soakFixture)
	fixture, err := pvemock.LoadFixture(fixturePath)
	if err != nil {
		t.Fatalf("LoadFixture(%s): %v", fixturePath, err)
	}
	mock := pvemock.NewServer(fixture)
	mockSrv := httptest.NewServer(mock)
	t.Cleanup(mockSrv.Close)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving ephemeral port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	dir := t.TempDir()
	cfgPath := rewriteDevConfigWithAPIURL(t, repoRoot, dir, port, mockSrv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	daemonDone := make(chan error, 1)
	go func() { daemonDone <- runDaemon(ctx, daemonOptions{ConfigPath: cfgPath}, logger) }()

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
		t.Fatal("login against the soak fixture did not return session/csrf cookies")
	}

	// A second, read-only connection to the daemon's own store. WAL mode
	// (store.Open) makes this safe alongside the daemon's writers, and
	// query_only makes it impossible for the gate to become a writer by
	// accident.
	storeDB, err := sql.Open("sqlite",
		"file:"+filepath.Join(dir, "vnprox.db")+"?_pragma=query_only(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("opening the store read-only for row-count sampling: %v", err)
	}

	nodes := make([]string, 0, len(fixture.Cluster.Nodes))
	for _, n := range fixture.Cluster.Nodes {
		nodes = append(nodes, n.Name)
	}

	d := &soakDaemon{
		client: client, base: base, sessionID: sessionID, csrfToken: csrfToken,
		mockState: mock.State(),
		storeDB:   storeDB, nodes: nodes, cancel: cancel, daemonDone: daemonDone,
	}
	t.Cleanup(func() {
		_ = storeDB.Close()
		cancel()
		select {
		case <-daemonDone:
		case <-time.After(30 * time.Second):
			t.Error("runDaemon did not return within 30s of context cancellation")
		}
	})

	// Let the collectors land one full cycle before the run starts. The
	// second-half window already discards warm-up, but starting from a cold
	// inventory would spend the first half of a short run on it.
	waitForSoakWarmup(t, d)
	return d
}

func waitForSoakWarmup(t *testing.T, d *soakDaemon) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var body struct {
			Nodes []json.RawMessage `json:"nodes"`
		}
		err := d.getJSON("/api/v1/topology", &body)
		if err == nil && len(body.Nodes) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("topology never populated within 30s (last err %v)", err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func soakSamplers(t *testing.T, d *soakDaemon) []soak.Sampler {
	t.Helper()
	tableSamplers, err := soak.TableSamplers(context.Background(), d.storeDB)
	if err != nil {
		t.Fatalf("building table row-count samplers: %v", err)
	}
	if len(tableSamplers) == 0 {
		t.Fatal("no tables found in the daemon's store; the row-count half of the gate would be silent")
	}
	samplers := []soak.Sampler{
		soak.Goroutines(),
		soak.Heap(true),
		soak.RSS(0),
		soak.OpenFDs(0),
	}
	return append(samplers, tableSamplers...)
}

// --- HTTP helpers ---------------------------------------------------------

func (d *soakDaemon) do(method, path string, body []byte) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
	}
	req, err := http.NewRequest(method, d.base+path, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-VNPROX-CSRF", d.csrfToken)
	req.AddCookie(&http.Cookie{Name: "vnprox_session", Value: d.sessionID})
	resp, err := d.client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("%s %s: reading body: %w", method, path, err)
	}
	return resp.StatusCode, raw, nil
}

func (d *soakDaemon) getJSON(path string, out any) error {
	status, raw, err := d.do(http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("GET %s: status %d: %s", path, status, truncate(raw))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("GET %s: decoding: %w", path, err)
	}
	return nil
}

func truncate(b []byte) string {
	const limit = 200
	if len(b) <= limit {
		return string(b)
	}
	return string(b[:limit]) + "..."
}

// --- churn ----------------------------------------------------------------

type soakGuest struct {
	node string
	vmid string
	kind pvemock.GuestKind
}

// soakChurn is the seeded synthetic-churn generator: guests created and
// destroyed, cluster members flapping offline and back, a finding appearing
// and clearing, plus continuous read traffic and draft-changeset churn
// against the daemon's own API.
//
// Determinism (T-2504 AC4): every decision this type makes comes from its
// own *rand.Rand, seeded from the run's seed and advanced only by tick, so
// tick N always makes the same choices for a given seed. What is *not*
// deterministic is how those ticks interleave with the daemon's own poll
// loops in wall-clock time — no seeded generator can fix that — so the
// artifact records the seed to make the churn sequence reproducible, not to
// promise a byte-identical run.
type soakChurn struct {
	d           *soakDaemon
	rng         *rand.Rand
	logger      *slog.Logger
	offlineNode string
	live        []soakGuest

	nextVMID        int
	guestsCreated   int
	guestsDestroyed int
	nodeFlaps       int
	findingToggles  int
	draftCycles     int
	reads           int
	orphanPresent   bool
}

const (
	// maxSoakGuests bounds the synthetic guest population so churn stays
	// churn: a generator that only ever creates would itself be an
	// unbounded-growth fixture and the gate could not tell the two apart.
	maxSoakGuests = 8
	// soakOrphanVnet is the VNet whose zone does not exist, which makes the
	// `orphan_vnet` health check fire; removing it clears the finding.
	soakOrphanVnet   = "soakorphan"
	soakMissingZone  = "soak-zone-that-never-existed"
	soakFirstVMID    = 9000
	soakDraftEveryN  = 3
	soakFlapEveryN   = 5
	soakFindingEvery = 7
)

func newSoakChurn(d *soakDaemon, seed uint64, logger *slog.Logger) *soakChurn {
	return &soakChurn{
		d:        d,
		rng:      rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15)), //nolint:gosec // churn scheduling, not cryptography
		logger:   logger,
		nextVMID: soakFirstVMID,
	}
}

// tick performs one round of churn. It returns an error only when the
// daemon itself would not answer; individual mock-level operations that
// legitimately do nothing (removing a guest when none exist) are not
// errors. soak.Run treats an occasional error as normal and every tick
// failing as fatal.
func (c *soakChurn) tick(ctx context.Context, tick int) error {
	if err := c.readPath(ctx); err != nil {
		return err
	}
	c.guestChurn()
	if tick%soakFlapEveryN == 0 {
		c.nodeFlap()
	}
	if tick%soakFindingEvery == 0 {
		c.findingToggle()
	}
	if tick%soakDraftEveryN == 0 {
		if err := c.draftCycle(tick); err != nil {
			return err
		}
	}
	return nil
}

// readPath is the load a browser with the app open produces.
func (c *soakChurn) readPath(ctx context.Context) error {
	for _, path := range []string{
		"/api/v1/topology",
		"/api/v1/findings",
		"/api/v1/audit?limit=20",
		"/api/v1/health",
		"/api/v1/changesets",
	} {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := c.d.getJSON(path, nil); err != nil {
			return err
		}
	}
	c.reads++
	return nil
}

// guestChurn creates a guest, or destroys one at random once the population
// has reached its bound — so the inventory graph keeps changing shape while
// the number of entities stays flat.
func (c *soakChurn) guestChurn() {
	if len(c.live) >= maxSoakGuests || (len(c.live) > 0 && c.rng.IntN(2) == 0) {
		i := c.rng.IntN(len(c.live))
		g := c.live[i]
		c.live = append(c.live[:i], c.live[i+1:]...)
		if c.d.mockState.RemoveGuest(g.node, g.vmid) {
			c.guestsDestroyed++
		}
		return
	}
	node := c.d.nodes[c.rng.IntN(len(c.d.nodes))]
	kind := pvemock.GuestQemu
	if c.rng.IntN(2) == 0 {
		kind = pvemock.GuestLXC
	}
	vmid := fmt.Sprintf("%d", c.nextVMID)
	c.nextVMID++
	ok := c.d.mockState.SetGuest(node, kind, vmid, pvemock.GuestSpec{
		Name:   "soak-" + vmid,
		Status: "running",
		Config: map[string]string{
			"net0": fmt.Sprintf("virtio=AA:BB:CC:%02X:%02X:%02X,bridge=vmbr0",
				c.rng.IntN(256), c.rng.IntN(256), c.rng.IntN(256)),
		},
	})
	if ok {
		c.live = append(c.live, soakGuest{node: node, vmid: vmid, kind: kind})
		c.guestsCreated++
	}
}

// nodeFlap takes one non-local cluster member offline, or brings the
// currently-offline one back. pve1 (the local node) is never flapped: a
// daemon that cannot see itself is a different scenario from a peer
// disappearing, and not the one this run is measuring.
func (c *soakChurn) nodeFlap() {
	if c.offlineNode != "" {
		c.d.mockState.ClearNodeOnlineOverride(c.offlineNode)
		c.offlineNode = ""
		c.nodeFlaps++
		return
	}
	if len(c.d.nodes) < 2 {
		return
	}
	node := c.d.nodes[1+c.rng.IntN(len(c.d.nodes)-1)]
	if c.d.mockState.SetNodeOnline(node, false) {
		c.offlineNode = node
		c.nodeFlaps++
	}
}

// findingToggle makes the `orphan_vnet` health check appear and clear, by
// adding and removing an SDN VNet whose zone does not exist.
func (c *soakChurn) findingToggle() {
	if c.orphanPresent {
		c.d.mockState.RemoveSDNVnet(soakOrphanVnet)
		c.orphanPresent = false
	} else {
		c.d.mockState.SetSDNVnet(pvemock.SDNVnetSpec{ID: soakOrphanVnet, Zone: soakMissingZone, Tag: 4001})
		c.orphanPresent = true
	}
	c.findingToggles++
}

// draftCycle exercises the change engine's staging and validation path —
// create, validate, discard — without ever applying. Nothing here mutates
// network state: a soak that applied and rolled back real changes would be
// measuring the change engine, which has its own tests, rather than the
// daemon's steady-state resource behaviour.
func (c *soakChurn) draftCycle(tick int) error {
	name := fmt.Sprintf("vmbrsoak%d", tick%256)
	body := []byte(fmt.Sprintf(
		`{"title":"soak churn %d","ops":[{"op":"bridge.create","target":"bridge:pve1:%s","params":{"mtu":1500}}]}`,
		tick, name))
	status, raw, err := c.d.do(http.MethodPost, "/api/v1/changesets", body)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return fmt.Errorf("POST /changesets: status %d: %s", status, truncate(raw))
	}
	var created struct {
		ID string `json:"id"`
	}
	if decErr := json.Unmarshal(raw, &created); decErr != nil || created.ID == "" {
		return fmt.Errorf("decoding created changeset: %w (body %s)", decErr, truncate(raw))
	}

	valStatus, valRaw, err := c.d.do(http.MethodPost, "/api/v1/changesets/"+created.ID+"/validate", nil)
	if err != nil {
		return err
	}
	if valStatus != http.StatusOK {
		return fmt.Errorf("POST /changesets/%s/validate: status %d: %s", created.ID, valStatus, truncate(valRaw))
	}

	status, raw, err = c.d.do(http.MethodDelete, "/api/v1/changesets/"+created.ID, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusNoContent {
		return fmt.Errorf("DELETE /changesets/%s: status %d: %s", created.ID, status, truncate(raw))
	}
	c.draftCycles++
	return nil
}
