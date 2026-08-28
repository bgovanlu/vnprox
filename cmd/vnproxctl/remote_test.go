// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newFakeVnproxd builds a TLS test server standing in for vnproxd's
// /api/v1 surface. handler sees every request; tests assert on method/path/
// headers/body as needed. --insecure defaults to true on every remote/apply
// command (matching status.go's own convention), so a self-signed TLS test
// server needs no extra client configuration.
func newFakeVnproxd(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func requireBearerToken(t *testing.T, r *http.Request, want string) {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != "Bearer "+want {
		t.Errorf("Authorization = %q, want Bearer %s", got, want)
	}
}

func TestRunRemoteTopology_JSONAndTable(t *testing.T) {
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		requireBearerToken(t, r, "tok")
		if r.URL.Path != "/topology" {
			t.Errorf("path = %q, want /topology", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"nodes":       []map[string]string{{"ref": "bridge:pve1:vmbr0", "kind": "bridge", "node": "pve1"}},
			"edges":       []map[string]string{},
			"generatedAt": 1720512345,
		})
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{"remote", "topology", "--url", srv.URL, "--token", "tok"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "1 node(s), 0 edge(s)") {
		t.Errorf("stdout = %q, want a node/edge summary", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"remote", "topology", "--url", srv.URL, "--token", "tok", "-o", "json"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout.String())
	}
	if decoded["generatedAt"].(float64) != 1720512345 {
		t.Errorf("decoded = %+v, want generatedAt 1720512345", decoded)
	}
	assertDocumentedJSON(t, "remote topology", stdout.Bytes())
}

func TestRunRemoteTopology_NoTokenFailsFastAuthExitCode(t *testing.T) {
	dialed := false
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) { dialed = true })

	t.Setenv("VNPROX_TOKEN", "")
	var stdout, stderr bytes.Buffer
	code := run([]string{"remote", "topology", "--url", srv.URL}, &stdout, &stderr)
	if code != ExitAuth {
		t.Fatalf("exit code = %d, want ExitAuth", code)
	}
	if dialed {
		t.Error("no daemon call should have been attempted without a token")
	}
}

func TestRunRemoteTopology_RevokedTokenGetsAuthExitCode(t *testing.T) {
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"code": "not_authenticated", "message": "token revoked"},
		})
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{"remote", "topology", "--url", srv.URL, "--token", "revoked-token"}, &stdout, &stderr)
	if code != ExitAuth {
		t.Fatalf("exit code = %d, want ExitAuth", code)
	}
	if !strings.Contains(stderr.String(), "not_authenticated") {
		t.Errorf("stderr = %q, want it to mention not_authenticated", stderr.String())
	}
}

func TestRunRemoteFindings_TableAndFilters(t *testing.T) {
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("source"); got != "drift" {
			t.Errorf("source query = %q, want drift", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{"id": "drift:bridge_divergence|vmbr0", "source": "drift", "check": "bridge_divergence", "severity": "error", "detail": "vlan mismatch", "nodes": []string{"pve1"}, "fixable": true},
			},
		})
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{"remote", "findings", "--url", srv.URL, "--token", "tok", "--source", "drift"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "bridge_divergence") {
		t.Errorf("stdout = %q, want the finding's check name", stdout.String())
	}
}

// TestRunRemoteFindings_OJSON pins the -o json shape against docs/cli-json.md.
func TestRunRemoteFindings_OJSON(t *testing.T) {
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{"id": "drift:bridge_divergence|vmbr0", "source": "drift", "check": "bridge_divergence", "severity": "error", "detail": "vlan mismatch", "nodes": []string{"pve1"}, "refs": []string{"bridge:pve1:vmbr0"}, "fixable": true},
			},
		})
	})
	var stdout, stderr bytes.Buffer
	code := run([]string{"remote", "findings", "--url", srv.URL, "--token", "tok", "-o", "json"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	assertDocumentedJSON(t, "remote findings", stdout.Bytes())
}

// TestRunRemoteDrift_OJSON pins the -o json shape against docs/cli-json.md.
// A non-empty sample is required: `remote drift` emits a bare array, and an
// empty one carries no fields to check against the documented (required)
// ones.
func TestRunRemoteDrift_OJSON(t *testing.T) {
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": "drift:bridge_divergence|vmbr0", "check": "bridge_divergence", "severity": "error", "detail": "vlan mismatch", "nodes": []string{"pve1"}, "refs": []string{"bridge:pve1:vmbr0"}, "fixable": true},
		})
	})
	var stdout, stderr bytes.Buffer
	code := run([]string{"remote", "drift", "--url", srv.URL, "--token", "tok", "-o", "json"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	assertDocumentedJSON(t, "remote drift", stdout.Bytes())
}

func TestRunRemoteDrift_EmptyIsCleanExit(t *testing.T) {
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{"remote", "drift", "--url", srv.URL, "--token", "tok"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "No drift findings.") {
		t.Errorf("stdout = %q, want the no-findings message", stdout.String())
	}
}

func TestRunRemoteAudit_Pagination(t *testing.T) {
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("limit"); got != "10" {
			t.Errorf("limit query = %q, want 10", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{"id": "a1", "at": 1720512345, "username": "root@pam", "action": "changeset.apply", "changesetId": "cs1", "result": "ok"},
			},
			"nextCursor": "opaque-cursor",
		})
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{"remote", "audit", "--url", srv.URL, "--token", "tok", "--limit", "10"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "changeset.apply") {
		t.Errorf("stdout = %q, want the action name", stdout.String())
	}
	if !strings.Contains(stdout.String(), "opaque-cursor") {
		t.Errorf("stdout = %q, want the nextCursor hint", stdout.String())
	}
}

// TestRunRemoteAudit_OJSON pins the -o json shape against docs/cli-json.md.
func TestRunRemoteAudit_OJSON(t *testing.T) {
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{"id": "a1", "at": 1720512345, "username": "root@pam", "action": "changeset.apply", "target": "bridge:pve1:vmbr0", "changesetId": "cs1", "result": "ok", "detail": map[string]any{"note": "x"}},
			},
			"nextCursor":  "opaque-cursor",
			"partial":     true,
			"failedNodes": []string{"pve2"},
		})
	})
	var stdout, stderr bytes.Buffer
	code := run([]string{"remote", "audit", "--url", srv.URL, "--token", "tok", "-o", "json"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	assertDocumentedJSON(t, "remote audit", stdout.Bytes())
}

func TestRunRemote_UnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"remote", "not-a-real-subcommand"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want ExitUsage", code)
	}
}

func TestRunRemote_NoSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"remote"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want ExitUsage", code)
	}
}

// TestEveryRemoteCommandSupportsOJSON is T-1105 acceptance criterion 4's
// table test: every new command's flag.FlagSet has `-o` registered (proven
// by triggering its usage output via -h and checking the auto-generated
// flag list mentions it), covering the whole new command family in one
// table rather than one assertion per command.
func TestEveryRemoteCommandSupportsOJSON(t *testing.T) {
	commands := [][]string{
		{"remote", "topology"},
		{"remote", "changesets", "list"},
		{"remote", "changesets", "get"},
		{"remote", "changesets", "diff"},
		{"remote", "changesets", "create"},
		{"remote", "changesets", "validate"},
		{"remote", "changesets", "apply"},
		{"remote", "changesets", "confirm"},
		{"remote", "changesets", "rollback"},
		{"remote", "changesets", "discard"},
		{"remote", "findings"},
		{"remote", "drift"},
		{"remote", "audit"},
		{"apply"},
	}
	for _, cmd := range commands {
		t.Run(strings.Join(cmd, "_"), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := append(append([]string{}, cmd...), "-h")
			_ = run(args, &stdout, &stderr)
			if !strings.Contains(stderr.String(), "-o string") {
				t.Errorf("%v -h stderr = %q, want it to document the -o flag", cmd, stderr.String())
			}
		})
	}
}

// TestEveryLegacyCommandSupportsOJSON is the same acceptance criterion
// applied to the retrofitted pre-existing commands.
func TestEveryLegacyCommandSupportsOJSON(t *testing.T) {
	commands := [][]string{
		{"status"},
		{"snapshots", "list"},
		{"snapshots", "restore"},
		{"rollback-now"},
	}
	for _, cmd := range commands {
		t.Run(strings.Join(cmd, "_"), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := append(append([]string{}, cmd...), "-h")
			_ = run(args, &stdout, &stderr)
			if !strings.Contains(stderr.String(), "-o string") {
				t.Errorf("%v -h stderr = %q, want it to document the -o flag", cmd, stderr.String())
			}
		})
	}
}
