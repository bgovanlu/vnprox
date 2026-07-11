package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/pvemock"
)

func writeTempConfig(t *testing.T, listen string) string {
	t.Helper()
	dir := t.TempDir()

	// config.Load's validate() step stats the TLS cert/key paths but never
	// parses their contents, so empty placeholder files are sufficient.
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, []byte("cert"), 0o600); err != nil {
		t.Fatalf("writing fake cert: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("key"), 0o600); err != nil {
		t.Fatalf("writing fake key: %v", err)
	}

	toml := "[server]\nlisten = \"" + listen + "\"\ntls_cert = \"" + certPath + "\"\ntls_key = \"" + keyPath + "\"\n"
	cfgPath := filepath.Join(dir, "vnprox.toml")
	if err := os.WriteFile(cfgPath, []byte(toml), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return cfgPath
}

func TestHealthEndpointFromConfig(t *testing.T) {
	tests := []struct {
		listen string
		want   string
	}{
		{"127.0.0.1:8007", "https://127.0.0.1:8007/api/v1/health"},
		{"0.0.0.0:8007", "https://127.0.0.1:8007/api/v1/health"},
		{":8007", "https://127.0.0.1:8007/api/v1/health"},
	}
	for _, tt := range tests {
		t.Run(tt.listen, func(t *testing.T) {
			cfgPath := writeTempConfig(t, tt.listen)
			got, err := healthEndpointFromConfig(cfgPath)
			if err != nil {
				t.Fatalf("healthEndpointFromConfig: %v", err)
			}
			if got != tt.want {
				t.Errorf("endpoint = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHealthEndpointFromConfig_MissingFileFailsCleanly(t *testing.T) {
	_, err := healthEndpointFromConfig("/no/such/vnprox.toml")
	if err == nil {
		t.Fatal("expected an error for a missing config file")
	}
}

func TestRunStatus_HealthyDaemon(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/health" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(healthResponse{Status: "ok", Version: "1.2.3"})
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{"status", "--url", srv.URL + "/api/v1/health"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stdout: %s, stderr: %s)", code, stdout.String(), stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "Reachable:           yes") {
		t.Errorf("stdout = %q, want it to report reachable", out)
	}
	if !strings.Contains(out, "1.2.3") {
		t.Errorf("stdout = %q, want it to report the daemon version", out)
	}
	if !strings.Contains(out, "Collector ages:") {
		t.Errorf("stdout = %q, want a Collector ages section", out)
	}
	if !strings.Contains(out, "PVE API health:") {
		t.Errorf("stdout = %q, want a PVE API health line", out)
	}
	if !strings.Contains(out, "Peer reachability:") {
		t.Errorf("stdout = %q, want a Peer reachability section", out)
	}
}

func TestRunStatus_UnhealthyDaemon(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(healthResponse{Status: "degraded", Version: "1.2.3"})
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{"status", "--url", srv.URL + "/api/v1/health"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), "degraded") {
		t.Errorf("stdout = %q, want it to report the degraded status", stdout.String())
	}
}

func TestRunStatus_UnreachableDaemon(t *testing.T) {
	var stdout, stderr bytes.Buffer
	// Nothing listens here: an unused loopback port.
	code := run([]string{"status", "--url", "https://127.0.0.1:1/api/v1/health", "--timeout", "200ms"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), "Reachable:           no") {
		t.Errorf("stdout = %q, want it to report unreachable", stdout.String())
	}
}

func TestRunStatus_UsesConfigWhenNoURLGiven(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(healthResponse{Status: "ok", Version: "dev"})
	}))
	defer srv.Close()

	// srv.Listener.Addr() gives us the host:port httptest actually bound.
	listen := srv.Listener.Addr().String()
	cfgPath := writeTempConfig(t, listen)

	var stdout, stderr bytes.Buffer
	code := run([]string{"status", "--config", cfgPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stdout: %s, stderr: %s)", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), listen) {
		t.Errorf("stdout = %q, want it to mention the derived endpoint %s", stdout.String(), listen)
	}
}

func TestRunStatus_BadConfigFailsCleanly(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"status", "--config", "/no/such/vnprox.toml"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "vnproxctl status") {
		t.Errorf("stderr = %q, want an error message", stderr.String())
	}
}

// TestRunStatus_CollectorAgesFromHealthPayload is T-606's "collector ages"
// completion: GET /api/v1/health has reported per-source staleness since
// T-104/T-303; this pins that vnproxctl actually renders it (rather than
// the old placeholder line) sorted and formatted per printCollectorAges.
func TestRunStatus_CollectorAgesFromHealthPayload(t *testing.T) {
	lastSuccess := time.Now().Add(-90 * time.Second)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(healthResponse{
			Status: "ok", Version: "1.2.3",
			Collectors: []collectorSourceStatus{
				{Name: "pve", LastSuccess: lastSuccess},
				{Name: "host", Node: "pve2", ConsecutiveFailures: 4, LastError: "dial tcp: connection refused"},
			},
		})
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{"status", "--url", srv.URL + "/api/v1/health"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stdout: %s, stderr: %s)", code, stdout.String(), stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "host (pve2)") {
		t.Errorf("stdout = %q, want a host (pve2) collector line", out)
	}
	if !strings.Contains(out, "4 consecutive failure(s)") || !strings.Contains(out, "dial tcp: connection refused") {
		t.Errorf("stdout = %q, want the host/pve2 failure detail", out)
	}
	if !strings.Contains(out, "pve ") || !strings.Contains(out, "last success") {
		t.Errorf("stdout = %q, want a pve collector line reporting a last-success age", out)
	}
}

// fixtureThreeNode is the shared three-node cluster fixture
// (docs/development.md: "every feature must work against at least
// single-node.yaml and three-node-vlan.yaml"), reused here for
// printPVEHealth/printPeerReachability's integration tests.
const fixtureThreeNode = "../../testdata/clusters/three-node-vlan.yaml"

// writeTempConfigWithPVE extends writeTempConfig with [pve] (dev-ticket-auth
// override — see buildStatusPVEClient's doc comment: the same documented
// dev/test-only escape hatch config.PVEConfig.TicketUsername already
// provides for the collectors, reused here so these tests can run against
// internal/pvemock instead of needing a real PVE node's certificate and
// API-token identity) and [peer] sections.
func writeTempConfigWithPVE(t *testing.T, listen, pveAPIURL, secretPath string) string {
	t.Helper()
	base := writeTempConfig(t, listen)
	data, err := os.ReadFile(base)
	if err != nil {
		t.Fatalf("reading base config: %v", err)
	}
	extra := fmt.Sprintf(
		"\n[pve]\napi_url = %q\ndev_ticket_username = \"root@pam\"\ndev_ticket_password = \"vnprox-mock\"\n\n[peer]\nsecret_path = %q\n",
		pveAPIURL, secretPath)
	if err := os.WriteFile(base, append(data, []byte(extra)...), 0o600); err != nil {
		t.Fatalf("appending pve/peer sections: %v", err)
	}
	return base
}

func TestRunStatus_PVEAPIHealth_Reachable(t *testing.T) {
	fx, err := pvemock.LoadFixture(fixtureThreeNode)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	pveSrv := httptest.NewServer(pvemock.NewServer(fx))
	defer pveSrv.Close()

	daemonSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(healthResponse{Status: "ok", Version: "dev"})
	}))
	defer daemonSrv.Close()

	cfgPath := writeTempConfigWithPVE(t, daemonSrv.Listener.Addr().String(), pveSrv.URL, filepath.Join(t.TempDir(), "cluster.secret"))

	var stdout, stderr bytes.Buffer
	code := run([]string{"status", "--config", cfgPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stdout: %s, stderr: %s)", code, stdout.String(), stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "PVE API health:      reachable") {
		t.Errorf("stdout = %q, want PVE API health reachable", out)
	}
	if !strings.Contains(out, "3 cluster node(s)") {
		t.Errorf("stdout = %q, want a node count of 3 (fixtureThreeNode has pve1/pve2/pve3)", out)
	}
}

func TestRunStatus_PVEAPIHealth_TokenMissing(t *testing.T) {
	daemonSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(healthResponse{Status: "ok", Version: "dev"})
	}))
	defer daemonSrv.Close()

	// No dev_ticket_username here — this hits the production AuthAPIToken
	// branch, whose TokenFile doesn't exist.
	base := writeTempConfig(t, daemonSrv.Listener.Addr().String())
	data, err := os.ReadFile(base)
	if err != nil {
		t.Fatalf("reading base config: %v", err)
	}
	extra := "\n[pve]\napi_url = \"https://127.0.0.1:8006\"\ntoken_file = \"/no/such/pve-token\"\n"
	if err := os.WriteFile(base, append(data, []byte(extra)...), 0o600); err != nil {
		t.Fatalf("appending pve section: %v", err)
	}

	var stdout, stderr bytes.Buffer
	_ = run([]string{"status", "--config", base}, &stdout, &stderr)
	if !strings.Contains(stdout.String(), "PVE API health:      not configured") {
		t.Errorf("stdout = %q, want PVE API health to report 'not configured' for a missing token file", stdout.String())
	}
}

// TestPrintPeerStatuses_ReachableAndIncompatible exercises the per-peer
// probe/format logic printPeerReachability delegates to: pve2 is a real
// peer.Server (compatible protocol version), pve3 is a bare stub reporting
// a mismatched protocolVersion (same technique
// internal/change/mixedversion_test.go uses) — proving `vnproxctl status`
// surfaces exactly the version-skew signal docs/architecture.md §5
// documents. Talks to a hand-built *peer.Client/[]peer.Peer directly (no
// config file, PVE discovery, or cluster secret plumbing needed), which
// also sidesteps peer.Client's production HTTPS default (Scheme: "http"
// here, matching the plain-HTTP peer stand-ins) — proven correct on its
// own by internal/peer/client_test.go and exercised end to end through
// vnproxctl's config/discovery plumbing by
// TestRunStatus_PVEAPIHealth_Reachable and _SingleNode above.
func TestPrintPeerStatuses_ReachableAndIncompatible(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "cluster.secret")
	secrets, err := peer.LoadOrGenerateSecret(secretPath, nil)
	if err != nil {
		t.Fatalf("LoadOrGenerateSecret: %v", err)
	}

	compatSrv := peer.NewServer(peer.ServerOptions{Secrets: secrets, Version: "1.4.0"})
	compatRouter := chi.NewRouter()
	compatSrv.MountRoutes(compatRouter)
	compatTS := httptest.NewServer(compatRouter)
	defer compatTS.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/peer/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/api/peer/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"old","protocolVersion":` + fmt.Sprint(peer.ProtocolVersion+1) + `}`))
	})
	incompatTS := httptest.NewServer(mux)
	defer incompatTS.Close()

	peerClient := peer.NewClient(peer.ClientOptions{Secrets: secrets, Scheme: "http"})
	peers := []peer.Peer{
		{Node: "pve2", Addr: compatTS.Listener.Addr().String()},
		{Node: "pve3", Addr: incompatTS.Listener.Addr().String()},
	}

	var stdout bytes.Buffer
	printPeerStatuses(t.Context(), &stdout, peerClient, peers)
	out := stdout.String()
	if !strings.Contains(out, "pve2") || !strings.Contains(out, "reachable") || !strings.Contains(out, "version 1.4.0") {
		t.Errorf("stdout = %q, want a reachable pve2 line with its version", out)
	}
	if !strings.Contains(out, "pve3") || !strings.Contains(out, "INCOMPATIBLE") {
		t.Errorf("stdout = %q, want pve3 flagged INCOMPATIBLE", out)
	}
}

// TestRunStatus_PeerReachability_SingleNode exercises printPeerReachability
// end to end (config -> secret -> PVE discovery) for the common case: a
// single-node install with a cluster secret present but no other cluster
// members.
func TestRunStatus_PeerReachability_SingleNode(t *testing.T) {
	fx, err := pvemock.LoadFixture("../../testdata/clusters/single-node.yaml")
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	pveSrv := httptest.NewServer(pvemock.NewServer(fx))
	defer pveSrv.Close()

	daemonSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(healthResponse{Status: "ok", Version: "dev"})
	}))
	defer daemonSrv.Close()

	secretPath := filepath.Join(t.TempDir(), "cluster.secret")
	if _, err := peer.LoadOrGenerateSecret(secretPath, nil); err != nil {
		t.Fatalf("LoadOrGenerateSecret: %v", err)
	}
	cfgPath := writeTempConfigWithPVE(t, daemonSrv.Listener.Addr().String(), pveSrv.URL, secretPath)

	var stdout, stderr bytes.Buffer
	code := run([]string{"status", "--config", cfgPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stdout: %s, stderr: %s)", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "single-node (no peers)") {
		t.Errorf("stdout = %q, want 'single-node (no peers)'", stdout.String())
	}
}
