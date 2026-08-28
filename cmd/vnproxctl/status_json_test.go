// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRunStatus_JSONOutput(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(healthResponse{Status: "ok", Version: "1.2.3"})
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{"status", "--url", srv.URL + "/api/v1/health", "-o", "json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stdout: %s, stderr: %s)", code, stdout.String(), stderr.String())
	}
	var out statusJSONResult
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout not valid JSON: %v (%s)", err, stdout.String())
	}
	if !out.Reachable || out.Daemon.Status != "ok" || out.Daemon.Version != "1.2.3" {
		t.Errorf("decoded = %+v, want reachable ok/1.2.3", out)
	}
}

func TestRunStatus_JSONOutput_UnhealthyDaemonExitsOne(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(healthResponse{Status: "degraded", Version: "1.2.3"})
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{"status", "--url", srv.URL + "/api/v1/health", "-o", "json"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	var out statusJSONResult
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout not valid JSON: %v (%s)", err, stdout.String())
	}
	if out.Daemon.Status != "degraded" {
		t.Errorf("decoded.Daemon.Status = %q, want degraded", out.Daemon.Status)
	}
}

func TestRunStatus_InvalidOutputFlagIsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"status", "-o", "yaml"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}
