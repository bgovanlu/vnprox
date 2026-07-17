package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/collect"
	"github.com/bgovanlu/vnprox/internal/host"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/probe"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/pvemock"
	"github.com/bgovanlu/vnprox/internal/store"
)

// simLabHarness wires a real *pvemock.Server (loaded from sim-lab.yaml, the
// fixture T-504/T-802 share — see that YAML's own doc comment), a real
// *pve.Client talking to it, a fully-ingested *inventory.Graph (via
// internal/collect, the same pipeline production uses — not a hand-built
// graph, so this test exercises the real Guest/GuestNic/FwRuleset shapes
// POST /simulate/verify's resolvers walk), and an in-memory audit repo.
type simLabHarness struct {
	graph  *inventory.Graph
	client *pve.Client
	audit  *store.AuditRepo
}

// newSimLabHarness loads sim-lab.yaml fresh for each caller, optionally
// applying mutate funcs to the loaded Fixture before the mock server is
// built from it (the same "mutate a freshly-loaded Fixture in Go" pattern
// internal/pvemock's own TestNetworkStaging_FixtureDefaultFailureInjection
// uses) — this is how TestSimulateVerify_DivergesWhenUnreachable below
// rescript the identical (proto,dst,port) tuple's outcome per test, since
// each call gets its own independent in-memory Fixture/Server.
func newSimLabHarness(t *testing.T, mutate ...func(*pvemock.Fixture)) *simLabHarness {
	t.Helper()
	fx, err := pvemock.LoadFixture("../../testdata/clusters/sim-lab.yaml")
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	for _, m := range mutate {
		m(fx)
	}
	srv := pvemock.NewServer(fx)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client, err := pve.New(pve.Config{APIURL: ts.URL, Auth: pve.AuthTicket, Username: "root@pam", Password: "vnprox-mock"})
	if err != nil {
		t.Fatalf("pve.New: %v", err)
	}

	graph := inventory.NewGraph()
	c, err := collect.New(collect.Config{
		PVE:   client,
		Host:  host.NewFixtureReader(pvemock.NewFixtureHostReader(srv)),
		Graph: graph,
		// Long intervals: this test drives ingestion with one explicit
		// RefreshNow call, never the background tickers.
		PVEInterval: time.Hour, HostInterval: time.Hour, LLDPInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("collect.New: %v", err)
	}
	if _, refreshErr := c.RefreshNow(context.Background(), inventory.Scope{}); refreshErr != nil {
		t.Fatalf("RefreshNow: %v", refreshErr)
	}

	db, err := store.Open(context.Background(), t.TempDir()+"/vnprox.db")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return &simLabHarness{graph: graph, client: client, audit: store.NewAuditRepo(db)}
}

func (h *simLabHarness) fixedProbeClients() ProbeClientProvider {
	return fixedProbeClientProvider{client: h.client}
}

type fixedProbeClientProvider struct{ client *pve.Client }

func (f fixedProbeClientProvider) ProbeClientFor(context.Context) (probe.PVEExecer, bool) {
	return f.client, true
}

func postSimulateVerify(t *testing.T, graph SimulatorGraph, probeClients ProbeClientProvider, audit simulateVerifyAuditor, auth AuthService, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, Simulator: graph, ProbeClients: probeClients, ProbeAudit: audit,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/simulate/verify", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestSimulateVerify_ConvergesWithAllow is AC3's first half: a tuple
// matching an existing allow Simulate case (vm-a -> vm-c tcp/22 — vm-c's
// own pos-1 ACCEPT rule; its pos-0 DROP only matches tcp/80) with a
// scripted "reachable" outcome (sim-lab.yaml's own declared default) ->
// diverges:false. The dst is a guest-nic (vm-c), so this also exercises
// resolveProbeTargetIP's live guest-agent IP resolution.
func TestSimulateVerify_ConvergesWithAllow(t *testing.T) {
	h := newSimLabHarness(t)
	rec := postSimulateVerify(t, fakeInv{g: h.graph}, h.fixedProbeClients(), h.audit, netReadAuth(),
		`{"src":{"kind":"guest-nic","ref":"guest-nic:pve1:300/net0"},"dst":{"kind":"guest-nic","ref":"guest-nic:pve1:301/net0"},"proto":"tcp","port":22}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Simulated struct {
			Verdict string `json:"verdict"`
		} `json:"simulated"`
		Observed struct {
			Outcome string `json:"outcome"`
		} `json:"observed"`
		Diverges bool `json:"diverges"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, rec.Body.String())
	}
	if got.Simulated.Verdict != "allow" {
		t.Fatalf("simulated.verdict = %q, want allow (body: %s)", got.Simulated.Verdict, rec.Body.String())
	}
	if got.Observed.Outcome != "reachable" {
		t.Fatalf("observed.outcome = %q, want reachable (body: %s)", got.Observed.Outcome, rec.Body.String())
	}
	if got.Diverges {
		t.Errorf("diverges = true, want false (simulated allow + observed reachable agree)")
	}

	entries, err := h.audit.List(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("audit.List: %v", err)
	}
	if len(entries) != 1 || entries[0].Action != "probe.verify" || entries[0].Result != "ok" {
		t.Fatalf("audit entries = %+v, want exactly one probe.verify/ok row", entries)
	}
}

// TestSimulateVerify_DivergesWhenUnreachable is AC3's second half: the
// *identical* tuple (vm-a -> vm-c tcp/22, same allow verdict) rescripted
// with an "unreachable" observed outcome -> diverges:true.
func TestSimulateVerify_DivergesWhenUnreachable(t *testing.T) {
	h := newSimLabHarness(t, func(fx *pvemock.Fixture) {
		fx.Nodes["pve1"].Qemu["300"].AgentExecOutcomes = []pvemock.AgentExecOutcomeSpec{
			{Proto: "tcp", DstIP: "10.20.0.102", Port: 22, Outcome: "unreachable", Detail: "Connection refused"},
		}
	})
	rec := postSimulateVerify(t, fakeInv{g: h.graph}, h.fixedProbeClients(), h.audit, netReadAuth(),
		`{"src":{"kind":"guest-nic","ref":"guest-nic:pve1:300/net0"},"dst":{"kind":"guest-nic","ref":"guest-nic:pve1:301/net0"},"proto":"tcp","port":22}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Simulated struct {
			Verdict string `json:"verdict"`
		} `json:"simulated"`
		Observed struct {
			Outcome string `json:"outcome"`
		} `json:"observed"`
		Diverges bool `json:"diverges"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Simulated.Verdict != "allow" {
		t.Fatalf("simulated.verdict = %q, want allow", got.Simulated.Verdict)
	}
	if got.Observed.Outcome != "unreachable" {
		t.Fatalf("observed.outcome = %q, want unreachable", got.Observed.Outcome)
	}
	if !got.Diverges {
		t.Error("diverges = false, want true (simulated allow + observed unreachable disagree)")
	}
}

// TestSimulateVerify_AgentUnreachableIsCleanNotA5xx is AC5: a qemu guest
// with an unreachable guest agent (vm-d, T-802's sim-lab addition) answers
// 200 with observed.outcome:"error" and execError set — the probe attempt
// itself is the honest answer, never a 5xx — and diverges is false (an
// error outcome never contradicts a simulated verdict).
func TestSimulateVerify_AgentUnreachableIsCleanNotA5xx(t *testing.T) {
	h := newSimLabHarness(t)
	rec := postSimulateVerify(t, fakeInv{g: h.graph}, h.fixedProbeClients(), h.audit, netReadAuth(),
		`{"src":{"kind":"guest-nic","ref":"guest-nic:pve1:303/net0"},"dst":{"kind":"ip","ip":"10.20.0.102"},"proto":"tcp","port":22}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the attempt itself is the answer); body: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Observed struct {
			Outcome   string `json:"outcome"`
			ExecError string `json:"execError"`
		} `json:"observed"`
		Diverges bool `json:"diverges"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Observed.Outcome != "error" {
		t.Fatalf("observed.outcome = %q, want error", got.Observed.Outcome)
	}
	if got.Observed.ExecError == "" {
		t.Error("execError empty, want a description of the agent-unreachable failure")
	}
	if got.Diverges {
		t.Error("diverges = true, want false (an error outcome never diverges)")
	}

	entries, err := h.audit.List(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("audit.List: %v", err)
	}
	if len(entries) != 1 || entries[0].Result != "error" {
		t.Fatalf("audit entries = %+v, want exactly one probe.verify/error row (AC4: failed probe audited too)", entries)
	}
}

// TestSimulateVerify_IPOrExternalSrcIs400 is AC5's first half: only a
// guest-nic can host a live probe.
func TestSimulateVerify_IPOrExternalSrcIs400(t *testing.T) {
	h := newSimLabHarness(t)
	for _, srcBody := range []string{
		`{"kind":"ip","ip":"10.20.0.5"}`,
		`{"kind":"external"}`,
	} {
		rec := postSimulateVerify(t, fakeInv{g: h.graph}, h.fixedProbeClients(), h.audit, netReadAuth(),
			`{"src":`+srcBody+`,"dst":{"kind":"ip","ip":"10.20.0.102"},"proto":"tcp","port":22}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("src=%s: status = %d, want 400 (body: %s)", srcBody, rec.Code, rec.Body.String())
		}
	}
}

// TestSimulateVerify_BadProtoIs400 proves a live probe rejects protocols it
// cannot execute (unlike /simulate/path, which accepts "any").
func TestSimulateVerify_BadProtoIs400(t *testing.T) {
	h := newSimLabHarness(t)
	rec := postSimulateVerify(t, fakeInv{g: h.graph}, h.fixedProbeClients(), h.audit, netReadAuth(),
		`{"src":{"kind":"guest-nic","ref":"guest-nic:pve1:300/net0"},"dst":{"kind":"ip","ip":"10.20.0.102"},"proto":"udp"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// TestSimulateVerify_RouteNotMountedWithoutProbeClients proves the route is
// nil-safe like every other optional Options field — without a
// ProbeClientProvider, only /simulate/path mounts.
func TestSimulateVerify_RouteNotMountedWithoutProbeClients(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: netReadAuth(), Simulator: buildSimGraph(t),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/simulate/verify",
		bytes.NewBufferString(`{"src":{"kind":"guest-nic","ref":"guest-nic:pve1:100/net0"},"dst":{"kind":"external"},"proto":"tcp","port":22}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route not mounted without a ProbeClientProvider)", rec.Code)
	}
}
