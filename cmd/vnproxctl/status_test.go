package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if !strings.Contains(out, "T-301") || !strings.Contains(out, "T-104") {
		t.Errorf("stdout = %q, want it to point at the tasks that complete peer/collector reporting", out)
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
