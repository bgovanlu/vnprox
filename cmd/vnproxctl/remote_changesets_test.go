// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRemoteChangesetsList_JSONMatchesAPIShape(t *testing.T) {
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/changesets" {
			t.Errorf("method/path = %s %s, want GET /changesets", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("status"); got != "draft" {
			t.Errorf("status query = %q, want draft", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"cs1","title":"add vlan","author":"root@pam","status":"draft","ops":[],"findings":[],"createdAt":1,"updatedAt":1,"touchesMgmtPath":false}]`))
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{"remote", "changesets", "list", "--url", srv.URL, "--token", "tok", "--status", "draft", "-o", "json"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	var out []changesetWire
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout not valid JSON: %v (%s)", err, stdout.String())
	}
	if len(out) != 1 || out[0].ID != "cs1" || out[0].Status != "draft" {
		t.Errorf("decoded = %+v, want one draft changeset cs1", out)
	}
	assertDocumentedJSON(t, "remote changesets list", stdout.Bytes())
}

func TestRunRemoteChangesetsGet_NotFound(t *testing.T) {
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "not_found", "message": "no such changeset"}})
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{"remote", "changesets", "get", "--url", srv.URL, "--token", "tok", "cs-missing"}, &stdout, &stderr)
	if code != ExitError {
		t.Fatalf("exit code = %d, want ExitError", code)
	}
	if !strings.Contains(stderr.String(), "not_found") {
		t.Errorf("stderr = %q, want it to mention not_found", stderr.String())
	}
}

func TestRunRemoteChangesetsDiff_RendersOpsAndFiles(t *testing.T) {
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/changesets/cs1/diff" {
			t.Errorf("path = %q, want /changesets/cs1/diff", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"files":[{"node":"pve1","path":"/etc/network/interfaces","unified":"--- a\n+++ b\n","changed":true}],"ops":[{"op":"bridge.create","target":"bridge:pve1:vmbr1","node":"pve1","summary":"Create bridge vmbr1 with ports eno2"}]}`))
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{"remote", "changesets", "diff", "--url", srv.URL, "--token", "tok", "cs1"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Create bridge vmbr1") {
		t.Errorf("stdout = %q, want the op summary", stdout.String())
	}
	if !strings.Contains(stdout.String(), "--- a") {
		t.Errorf("stdout = %q, want the unified file diff", stdout.String())
	}

	stdout.Reset()
	code = run([]string{"remote", "changesets", "diff", "--url", srv.URL, "--token", "tok", "-o", "json", "cs1"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("-o json exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	assertDocumentedJSON(t, "remote changesets diff", stdout.Bytes())
}

func TestRunRemoteChangesetsCreate_FromFile(t *testing.T) {
	var gotBody []byte
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/changesets" {
			t.Errorf("method/path = %s %s, want POST /changesets", r.Method, r.URL.Path)
		}
		var err error
		gotBody, err = readAll(r)
		if err != nil {
			t.Fatalf("reading body: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"cs2","title":"from file","author":"root@pam","status":"draft","ops":[],"findings":[],"createdAt":1,"updatedAt":1,"touchesMgmtPath":false}`))
	})

	dir := t.TempDir()
	specFile := filepath.Join(dir, "body.json")
	body := `{"title":"from file","ops":[]}`
	if err := os.WriteFile(specFile, []byte(body), 0o600); err != nil {
		t.Fatalf("writing body file: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"remote", "changesets", "create", "--url", srv.URL, "--token", "tok", "-f", specFile, "-o", "json"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if strings.TrimSpace(string(gotBody)) != body {
		t.Errorf("request body = %q, want %q (passthrough, not re-encoded)", gotBody, body)
	}
	if !strings.Contains(stdout.String(), "cs2") {
		t.Errorf("stdout = %q, want the created changeset id", stdout.String())
	}
	assertDocumentedJSON(t, "changeset", stdout.Bytes())
}

func TestRunRemoteChangesetsCreate_InvalidJSONIsUsageError(t *testing.T) {
	dir := t.TempDir()
	specFile := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(specFile, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("writing body file: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"remote", "changesets", "create", "--url", "https://example.invalid", "--token", "tok", "-f", specFile}, &stdout, &stderr)
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want ExitUsage", code)
	}
}

func TestRunRemoteChangesetsApply_SendsConfirmTimeoutAndMgmtAck(t *testing.T) {
	var gotBody applyRequestBody
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/changesets/cs1/apply" {
			t.Errorf("path = %q, want /changesets/cs1/apply", r.URL.Path)
		}
		data, _ := readAll(r)
		if err := json.Unmarshal(data, &gotBody); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"cs1","title":"t","author":"a","status":"applying","ops":[],"findings":[],"createdAt":1,"updatedAt":1,"touchesMgmtPath":true}`))
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"remote", "changesets", "apply",
		"--url", srv.URL, "--token", "tok",
		"--confirm-timeout-sec", "180", "--mgmt-ack-node", "pve1",
		"-o", "json", "cs1",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if gotBody.ConfirmTimeoutSec != 180 {
		t.Errorf("ConfirmTimeoutSec = %d, want 180", gotBody.ConfirmTimeoutSec)
	}
	if gotBody.MgmtAck == nil || gotBody.MgmtAck.Node != "pve1" {
		t.Errorf("MgmtAck = %+v, want node pve1", gotBody.MgmtAck)
	}
	// `apply`/`get`/`validate`/`confirm`/`rollback` all return the same
	// changeset shape (docs/api.md), documented once in docs/cli-json.md
	// under "changeset" rather than five times over.
	assertDocumentedJSON(t, "changeset", stdout.Bytes())
}

func TestRunRemoteChangesetsDiscard_Success(t *testing.T) {
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/changesets/cs1" {
			t.Errorf("method/path = %s %s, want DELETE /changesets/cs1", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{"remote", "changesets", "discard", "--url", srv.URL, "--token", "tok", "cs1"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Discarded changeset cs1") {
		t.Errorf("stdout = %q, want a discard confirmation", stdout.String())
	}

	stdout.Reset()
	if code := run([]string{"remote", "changesets", "discard", "--url", srv.URL, "--token", "tok", "-o", "json", "cs1"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("-o json exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	assertDocumentedJSON(t, "remote changesets discard", stdout.Bytes())
}

func TestRunRemoteChangesetsValidate_BlockingFindingsIsExitPending(t *testing.T) {
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"code": "validation_failed", "message": "changeset has blocking validation errors"},
		})
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{"remote", "changesets", "validate", "--url", srv.URL, "--token", "tok", "cs1"}, &stdout, &stderr)
	if code != ExitPending {
		t.Fatalf("exit code = %d, want ExitPending", code)
	}
}

// readAll reads and closes the full request body (small test helper).
func readAll(r *http.Request) ([]byte, error) {
	defer func() { _ = r.Body.Close() }()
	return io.ReadAll(r.Body)
}
