// SPDX-License-Identifier: Apache-2.0

// siemexport_integration_test.go is T-4012's acceptance-criterion-1
// end-to-end proof: "with the sink enabled, every audit row ... appears
// in the export stream within a bounded delay, verified against a test
// syslog/JSONL receiver". internal/siemexport's own tests already cover
// Exporter/Sink correctness in isolation (far-end-down, buffer-full,
// reconnect, redaction, drop-notification); this file is the one test
// proving the composition-root wiring itself — config parse ->
// setupSIEMExport -> wireAuditAppendedEvents's second consumer -> a real
// audit row landing in a real file — because that wiring (not the
// package's own logic) is what a future refactor is most likely to
// silently break.
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

// appendSIEMExportSection appends a [siemexport] section writing JSONL to
// path onto the config file at cfgPath — the same "rewriteDevConfig, then
// append/rewrite one more section" pattern rewriteDevConfigWithAPIURL
// uses for [pve] api_url.
func appendSIEMExportSection(t testing.TB, cfgPath, jsonlPath string) {
	t.Helper()
	f, err := os.OpenFile(cfgPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("opening %s to append [siemexport]: %v", cfgPath, err)
	}
	defer func() { _ = f.Close() }()
	section := fmt.Sprintf("\n[siemexport]\nenabled = true\nformat = \"jsonl\"\npath = %q\n", jsonlPath)
	if _, err := f.WriteString(section); err != nil {
		t.Fatalf("appending [siemexport] section: %v", err)
	}
}

// TestSIEMExport_EnabledEndToEnd_AuditRowReachesJSONLFile drives a real
// runDaemon (config load through a real HTTPS listener, exactly the
// production path) with [siemexport] enabled and format="jsonl", triggers
// a failed login (a deterministic, credential-free way to produce an
// audit row — no PVE mock round trip needed), and asserts the row appears
// in the exported JSONL file within a bounded delay with the attempted
// password nowhere in it (this task's redaction requirement, exercised
// end to end rather than only at internal/siemexport's own unit level).
func TestSIEMExport_EnabledEndToEnd_AuditRowReachesJSONLFile(t *testing.T) {
	repoRoot, err := repoRootAbs()
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving ephemeral port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	dir := t.TempDir()
	cfgPath := rewriteDevConfig(t, repoRoot, dir, port)
	jsonlPath := filepath.Join(dir, "siem-export.jsonl")
	appendSIEMExportSection(t, cfgPath, jsonlPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	daemonDone := make(chan error, 1)
	go func() { daemonDone <- runDaemon(ctx, daemonOptions{ConfigPath: cfgPath}, testLogger()) }()

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test-only client for the throwaway dev cert
		},
	}
	base := fmt.Sprintf("https://127.0.0.1:%d", port)
	waitForHealth(t, client, base, daemonDone)

	const wrongPassword = "definitely-wrong-password-siemexport-test" //nolint:gosec // fixture credential, not a real secret
	doLogin(t, client, base, "root@pam", wrongPassword)

	// Bounded-delay poll, per this task's acceptance criterion 1's own
	// wording — not an arbitrary sleep.
	deadline := time.Now().Add(10 * time.Second)
	var lines []string
	for time.Now().Before(deadline) {
		data, readErr := os.ReadFile(jsonlPath)
		if readErr == nil {
			lines = strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) > 0 && lines[0] != "" {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	cancel()
	select {
	case <-daemonDone:
	case <-time.After(5 * time.Second):
		t.Fatal("runDaemon did not return within 5s of context cancellation")
	}

	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("%s never received an exported audit row within the deadline", jsonlPath)
	}

	var foundLogin bool
	for _, line := range lines {
		if line == "" {
			continue
		}
		var rec map[string]any
		if jsonErr := json.Unmarshal([]byte(line), &rec); jsonErr != nil {
			t.Fatalf("exported line is not valid JSON: %v (%q)", jsonErr, line)
		}
		// The exporter carries BOTH audit rows and findings — that is the
		// whole point of T-4012 — so a "finding" record here is correct
		// output, not a failure. An earlier revision asserted every line
		// was an audit row and went red the moment the findings engine
		// completed a cycle before the login landed, which is a race on
		// ordering rather than a defect. What this test actually requires
		// is that the audit row arrives at all (foundLogin, below) and
		// that redaction holds across every record of either kind.
		kind, _ := rec["kind"].(string)
		if kind != "audit" && kind != "finding" {
			t.Fatalf("exported record kind = %q, want \"audit\" or \"finding\"", kind)
		}
		if kind == "audit" {
			if action, _ := rec["action"].(string); strings.Contains(strings.ToLower(action), "login") {
				foundLogin = true
			}
		}
		// The redaction requirement, checked against every field of every
		// exported line, not just the ones this test expects to be
		// affected — a stray leak into an unexpected field is exactly the
		// failure mode redaction exists to prevent.
		blob, _ := json.Marshal(rec)
		if strings.Contains(string(blob), wrongPassword) {
			t.Errorf("exported record contains the attempted password verbatim: %s", blob)
		}
	}
	if !foundLogin {
		t.Fatalf("no exported record's action mentions \"login\"; export lines: %v", lines)
	}
}
