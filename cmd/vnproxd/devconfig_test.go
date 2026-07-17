package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// rewriteDevConfig loads testdata/dev.toml and rewrites the values that are
// host-specific for a test run — the listen port (to an ephemeral one), the
// repo-root-relative TLS cert/key paths (to absolute), the [storage] paths,
// T-301's [peer] secret_path, and T-1001's [metrics] key_file (into dir) —
// leaving everything else (PVE mock settings, collect intervals, safety
// flags) exactly as the checked-in file says. It fails the test if any
// expected key is missing, so a reshaped dev.toml breaks this test loudly
// instead of silently testing something else.
func rewriteDevConfig(t testing.TB, repoRoot, dir string, port int) string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(repoRoot, "testdata", "dev.toml"))
	if err != nil {
		t.Fatalf("reading testdata/dev.toml: %v", err)
	}

	replacements := map[string]string{
		"listen":           fmt.Sprintf("127.0.0.1:%d", port),
		"tls_cert":         filepath.Join(repoRoot, "testdata", "certs", "dev-cert.pem"),
		"tls_key":          filepath.Join(repoRoot, "testdata", "certs", "dev-key.pem"),
		"db_path":          filepath.Join(dir, "vnprox.db"),
		"session_key_file": filepath.Join(dir, "session.key"),
		// The [safety] dev paths are repo-root-relative in dev.toml; left
		// unrewritten they'd resolve against this test's cwd and leak a
		// cmd/vnproxd/var/ directory into the tree.
		"protected_path":     filepath.Join(dir, "protected.json"),
		"dev_interfaces_dir": filepath.Join(dir, "dev-host"),
		"secret_path":        filepath.Join(dir, "cluster.secret"),
		"key_file":           filepath.Join(dir, "metrics.key"),
	}
	replaced := make(map[string]bool, len(replacements))

	lines := strings.Split(string(raw), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		for key, value := range replacements {
			if strings.HasPrefix(trimmed, key+" ") || strings.HasPrefix(trimmed, key+"=") {
				lines[i] = fmt.Sprintf("%s = %q", key, value)
				replaced[key] = true
			}
		}
	}
	for key := range replacements {
		if !replaced[key] {
			t.Fatalf("testdata/dev.toml has no %q key to rewrite; update this test to match its current shape", key)
		}
	}

	cfgPath := filepath.Join(dir, "dev.toml")
	if err := os.WriteFile(cfgPath, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatalf("writing rewritten dev config: %v", err)
	}
	return cfgPath
}

// TestRunDaemon_DevConfigServesHealth is the acceptance-criterion-1 test
// referenced by testdata/dev.toml's header comment (audit phase-0 F-08): the
// full runDaemon path — config load, TLS, store open + migrations, auth,
// change engine, run group including the metric_samples prune actor (F-01)
// — brought up against the checked-in dev config (ephemeral port, temp
// storage), must serve GET /api/v1/health -> {"status":"ok"} and then shut
// down cleanly on context cancellation.
//
// The mock PVE server dev.toml points at is deliberately not started:
// collectors must degrade to logged poll failures without affecting daemon
// health, exactly as documented on setupCollect.
func TestRunDaemon_DevConfigServesHealth(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}

	// Reserve an ephemeral port for the daemon. Closing before handing it
	// to runDaemon is a small race, but the kernel won't re-issue the port
	// to another ephemeral bind this quickly in practice.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving ephemeral port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	cfgPath := rewriteDevConfig(t, repoRoot, t.TempDir(), port)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	daemonDone := make(chan error, 1)
	go func() { daemonDone <- runDaemon(ctx, cfgPath, testLogger()) }()

	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test-only client for the throwaway dev cert
		},
	}
	healthURL := fmt.Sprintf("https://127.0.0.1:%d/api/v1/health", port)

	var health struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case err := <-daemonDone:
			t.Fatalf("daemon exited before serving health: %v", err)
		default:
		}
		resp, err := client.Get(healthURL)
		if err == nil {
			decodeErr := json.NewDecoder(resp.Body).Decode(&health)
			_ = resp.Body.Close()
			if decodeErr != nil {
				t.Fatalf("decoding health response: %v", decodeErr)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET /api/v1/health status = %d, want 200", resp.StatusCode)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon never served %s: last error: %v", healthURL, err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	if health.Status != "ok" {
		t.Errorf("health status = %q, want %q", health.Status, "ok")
	}

	cancel()
	select {
	case err := <-daemonDone:
		if err != nil {
			t.Fatalf("runDaemon returned error on shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runDaemon did not return within 5s of context cancellation")
	}
}
