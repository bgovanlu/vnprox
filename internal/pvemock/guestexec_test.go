package pvemock

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// newExecTestServer loads single-node.yaml and programmatically declares an
// AgentExecOutcomes table on guest 100 ("web01"), mirroring
// TestNetworkStaging_FixtureDefaultFailureInjection's "mutate the loaded
// Fixture in Go, never edit the shared YAML" pattern — single-node.yaml is
// widely shared across other tasks' tests, so T-802's own scripted-outcome
// coverage lives here instead (testdata/clusters/sim-lab.yaml carries the
// YAML-declared table this task's card asks for, exercised by
// internal/api's POST /simulate/verify tests).
func newExecTestServer(t *testing.T) *Server {
	t.Helper()
	f, err := LoadFixture(fixturePath(t, "single-node.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	f.Nodes["pve1"].Qemu["100"].AgentExecOutcomes = []AgentExecOutcomeSpec{
		{Proto: "icmp", DstIP: "10.10.0.50", Outcome: "reachable", Detail: "1 packet received"},
		{Proto: "tcp", DstIP: "10.10.0.50", Port: 9999, Outcome: "unreachable", Detail: "Connection refused"},
	}
	return NewServer(f)
}

func execRequestBody(t *testing.T, command []string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string][]string{"command": command})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestGuestAgentExec_ScriptedReachable is AC2: the exec/exec-status
// handlers serve a scripted outcome for a declared tuple.
func TestGuestAgentExec_ScriptedReachable(t *testing.T) {
	srv := newExecTestServer(t)
	ticket, csrf := login(t, srv, "root@pam", "vnprox-mock")

	execReq := authedRequest(t, http.MethodPost, "/api2/json/nodes/pve1/qemu/100/agent/exec", ticket, csrf,
		execRequestBody(t, []string{"ping", "-c", "1", "-W", "5", "10.10.0.50"}))
	execBody := mustStatus(t, srv, execReq, http.StatusOK)
	data, _ := execBody["data"].(map[string]any)
	pid, _ := data["pid"].(float64)
	if pid <= 0 {
		t.Fatalf("exec response = %v, want a positive pid", execBody)
	}

	statusReq := authedRequest(t, http.MethodGet,
		"/api2/json/nodes/pve1/qemu/100/agent/exec-status?pid="+strconv.Itoa(int(pid)), ticket, "", nil)
	statusBody := mustStatus(t, srv, statusReq, http.StatusOK)
	sdata, _ := statusBody["data"].(map[string]any)
	if exited, _ := sdata["exited"].(float64); exited != 1 {
		t.Fatalf("exited = %v, want 1", sdata["exited"])
	}
	if code, _ := sdata["exitcode"].(float64); code != 0 {
		t.Fatalf("exitcode = %v, want 0 (scripted reachable)", sdata["exitcode"])
	}
}

// TestGuestAgentExec_ScriptedUnreachable covers the tcp/refused half of the
// scripted table (AC2), also proving the mock correctly maps "unreachable"
// to exit code 1 (internal/probe.classify's "refused" branch parses the
// detail text back out).
func TestGuestAgentExec_ScriptedUnreachable(t *testing.T) {
	srv := newExecTestServer(t)
	ticket, csrf := login(t, srv, "root@pam", "vnprox-mock")

	execReq := authedRequest(t, http.MethodPost, "/api2/json/nodes/pve1/qemu/100/agent/exec", ticket, csrf,
		execRequestBody(t, []string{"nc", "-z", "-w", "5", "10.10.0.50", "9999"}))
	execBody := mustStatus(t, srv, execReq, http.StatusOK)
	data, _ := execBody["data"].(map[string]any)
	pid, _ := data["pid"].(float64)

	statusReq := authedRequest(t, http.MethodGet,
		"/api2/json/nodes/pve1/qemu/100/agent/exec-status?pid="+strconv.Itoa(int(pid)), ticket, "", nil)
	statusBody := mustStatus(t, srv, statusReq, http.StatusOK)
	sdata, _ := statusBody["data"].(map[string]any)
	if code, _ := sdata["exitcode"].(float64); code != 1 {
		t.Fatalf("exitcode = %v, want 1 (scripted unreachable)", sdata["exitcode"])
	}
	errData, _ := sdata["err-data"].(string)
	if errData == "" {
		t.Error("err-data empty, want the scripted detail text (Connection refused)")
	}
}

// TestGuestAgentExec_UnscriptedTupleDefaultsToError proves an
// undeclared (proto,dst,port) tuple resolves to the honest "no scripted
// outcome" error rather than a guessed default.
func TestGuestAgentExec_UnscriptedTupleDefaultsToError(t *testing.T) {
	srv := newExecTestServer(t)
	ticket, csrf := login(t, srv, "root@pam", "vnprox-mock")

	execReq := authedRequest(t, http.MethodPost, "/api2/json/nodes/pve1/qemu/100/agent/exec", ticket, csrf,
		execRequestBody(t, []string{"ping", "-c", "1", "-W", "5", "192.0.2.99"}))
	execBody := mustStatus(t, srv, execReq, http.StatusOK)
	data, _ := execBody["data"].(map[string]any)
	pid, _ := data["pid"].(float64)

	statusReq := authedRequest(t, http.MethodGet,
		"/api2/json/nodes/pve1/qemu/100/agent/exec-status?pid="+strconv.Itoa(int(pid)), ticket, "", nil)
	statusBody := mustStatus(t, srv, statusReq, http.StatusOK)
	sdata, _ := statusBody["data"].(map[string]any)
	if code, _ := sdata["exitcode"].(float64); code != 2 {
		t.Fatalf("exitcode = %v, want 2 (unscripted -> error)", sdata["exitcode"])
	}
}

// TestGuestAgentExec_AgentUnreachable is AC5's mock-side precondition: a
// guest with no reachable QEMU guest agent fails the exec call itself
// (real PVE's 500), never a fabricated pid.
func TestGuestAgentExec_AgentUnreachable(t *testing.T) {
	f, err := LoadFixture(fixturePath(t, "single-node.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	f.Nodes["pve1"].Qemu["100"].AgentUnreachable = true
	srv := NewServer(f)
	ticket, csrf := login(t, srv, "root@pam", "vnprox-mock")

	execReq := authedRequest(t, http.MethodPost, "/api2/json/nodes/pve1/qemu/100/agent/exec", ticket, csrf,
		execRequestBody(t, []string{"ping", "-c", "1", "-W", "5", "10.10.0.50"}))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, execReq)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (agent unreachable); body=%s", rec.Code, rec.Body.String())
	}
}

// TestGuestAgentExecStatus_UnknownPid404 proves polling a pid this server
// never issued 404s rather than a zero-value success.
func TestGuestAgentExecStatus_UnknownPid404(t *testing.T) {
	srv := newExecTestServer(t)
	ticket, _ := login(t, srv, "root@pam", "vnprox-mock")
	req := authedRequest(t, http.MethodGet, "/api2/json/nodes/pve1/qemu/100/agent/exec-status?pid=999999", ticket, "", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// TestGuestAgentExec_UnrecognizedCommand400 proves the mock does not
// silently accept an arbitrary command it cannot map back to a
// (proto,dst,port) tuple (see parseProbeCommand's doc comment).
func TestGuestAgentExec_UnrecognizedCommand400(t *testing.T) {
	srv := newExecTestServer(t)
	ticket, csrf := login(t, srv, "root@pam", "vnprox-mock")
	execReq := authedRequest(t, http.MethodPost, "/api2/json/nodes/pve1/qemu/100/agent/exec", ticket, csrf,
		execRequestBody(t, []string{"traceroute", "10.10.0.50"}))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, execReq)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
