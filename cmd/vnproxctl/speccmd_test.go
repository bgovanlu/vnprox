// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunSpec_UnknownSubcommandFails(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"spec"}, &stdout, &stderr); code != ExitUsage {
		t.Fatalf("bare `spec` exit code = %d, want ExitUsage", code)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"spec", "delete-everything"}, &stdout, &stderr); code != ExitUsage {
		t.Fatalf("exit code = %d, want ExitUsage — there is no such subcommand", code)
	}
}

// TestRunSpecExport_ByteStableAcrossTwoRuns pins AC1: two consecutive runs
// against unchanged live state write byte-identical output, the CLI-level
// consequence of spec.Export's own byte-stability guarantee.
func TestRunSpecExport_ByteStableAcrossTwoRuns(t *testing.T) {
	const yaml = "specVersion: 1\nbridges: []\n"
	var calls int
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		requireBearerToken(t, r, "tok")
		if r.Method != http.MethodGet || r.URL.Path != "/spec" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(specExportWire{SpecVersion: 1, Content: yaml})
	})

	var first, second bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"spec", "export", "--url", srv.URL, "--token", "tok"}, &first, &stderr); code != ExitSuccess {
		t.Fatalf("run 1 exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	stderr.Reset()
	if code := run([]string{"spec", "export", "--url", srv.URL, "--token", "tok"}, &second, &stderr); code != ExitSuccess {
		t.Fatalf("run 2 exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if first.String() != yaml || second.String() != yaml {
		t.Fatalf("stdout = %q / %q, want the raw YAML content %q both times", first.String(), second.String(), yaml)
	}
	if first.String() != second.String() {
		t.Fatalf("two exports of unchanged state differ: %q vs %q", first.String(), second.String())
	}
	if calls != 2 {
		t.Fatalf("GET /spec called %d times, want 2", calls)
	}
}

func TestRunSpecExport_OutFlagWritesFile(t *testing.T) {
	const yaml = "specVersion: 1\n"
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(specExportWire{SpecVersion: 1, Content: yaml})
	})
	dest := filepath.Join(t.TempDir(), "cluster.yaml")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"spec", "export", "--url", srv.URL, "--token", "tok", "--out", dest}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want nothing written when --out is given", stdout.String())
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading %s: %v", dest, err)
	}
	if string(got) != yaml {
		t.Errorf("file content = %q, want %q", got, yaml)
	}
}

func TestRunSpecExport_JSONMatchesAPIShape(t *testing.T) {
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(specExportWire{SpecVersion: 1, Content: "specVersion: 1\n"})
	})
	var stdout, stderr bytes.Buffer
	if code := run([]string{"spec", "export", "--url", srv.URL, "--token", "tok", "-o", "json"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	var decoded specExportWire
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("stdout not valid JSON: %v (%s)", err, stdout.String())
	}
	if decoded.SpecVersion != 1 || decoded.Content != "specVersion: 1\n" {
		t.Errorf("decoded = %+v, want the daemon's response verbatim", decoded)
	}
	assertDocumentedJSON(t, "spec export", stdout.Bytes())
}

// TestRunSpecImport_StagesDraftAndNeverApplies is this command's core
// safety assertion (CLAUDE.md: the change engine is the sole mutation
// path): `spec import` must call POST /spec/import and nothing else — in
// particular, never POST /changesets/{id}/apply.
func TestRunSpecImport_StagesDraftAndNeverApplies(t *testing.T) {
	var importCalls int
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		requireBearerToken(t, r, "tok")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/spec/import":
			importCalls++
			var body specImportRequestBody
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decoding request body: %v", err)
			}
			if !strings.Contains(body.Content, "specVersion: 1") {
				t.Errorf("request body content = %q, want the file's content", body.Content)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"cs1","title":"Spec import","author":"a","status":"validated","ops":[{}],"findings":[],"createdAt":1,"updatedAt":1,"touchesMgmtPath":false,"notInSpec":["bridge:pve1:vmbr9"]}`))
		default:
			t.Fatalf("unexpected request %s %s — spec import must call nothing but POST /spec/import", r.Method, r.URL.Path)
		}
	})

	specPath := writeSpecFile(t, "specVersion: 1\nbridges: []\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"spec", "import", "--url", srv.URL, "--token", "tok", specPath}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if importCalls != 1 {
		t.Fatalf("POST /spec/import called %d times, want 1", importCalls)
	}
	for _, needle := range []string{"cs1", "validated", "bridge:pve1:vmbr9", "Nothing was applied"} {
		if !strings.Contains(stdout.String(), needle) {
			t.Errorf("stdout missing %q:\n%s", needle, stdout.String())
		}
	}
}

// TestRunSpecImport_OJSON pins the -o json shape against docs/cli-json.md.
func TestRunSpecImport_OJSON(t *testing.T) {
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"cs1","title":"Spec import","author":"a","status":"validated","ops":[{"kind":"bridge.create"}],"findings":[],"createdAt":1,"updatedAt":1,"touchesMgmtPath":false,"notInSpec":["bridge:pve1:vmbr9"]}`))
	})
	specPath := writeSpecFile(t, "specVersion: 1\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"spec", "import", "--url", srv.URL, "--token", "tok", "-o", "json", specPath}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	assertDocumentedJSON(t, "spec import", stdout.Bytes())
}

func TestRunSpecImport_StdinDash(t *testing.T) {
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"cs2","title":"Spec import","author":"a","status":"draft","ops":[],"findings":[],"createdAt":1,"updatedAt":1,"touchesMgmtPath":false,"notInSpec":[]}`))
	})
	// Restore real stdin afterwards; swap in a pipe carrying the spec body.
	origStdin := os.Stdin
	defer func() { os.Stdin = origStdin }()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdin = r
	go func() {
		_, _ = w.WriteString("specVersion: 1\n")
		_ = w.Close()
	}()

	var stdout, stderr bytes.Buffer
	code := run([]string{"spec", "import", "--url", srv.URL, "--token", "tok", "-"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "cs2") {
		t.Errorf("stdout = %q, want the staged changeset id", stdout.String())
	}
}

func TestRunSpecPin_BareGetsCurrentPin(t *testing.T) {
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/spec/pin" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(specPinWire{Pinned: true, Content: "specVersion: 1\n", PinnedBy: "root@pam", PinnedAt: 1700000000})
	})
	var stdout, stderr bytes.Buffer
	if code := run([]string{"spec", "pin", "--url", srv.URL, "--token", "tok"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "root@pam") {
		t.Errorf("stdout = %q, want the pinning user", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"spec", "pin", "--url", srv.URL, "--token", "tok", "-o", "json"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("-o json exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	assertDocumentedJSON(t, "spec pin", stdout.Bytes())
}

func TestRunSpecPin_WithFileRePins(t *testing.T) {
	var postCalls int
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/spec/pin" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		postCalls++
		var body specPinRequestBody
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !strings.Contains(body.Content, "specVersion: 1") {
			t.Errorf("posted content = %q", body.Content)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(specPinWire{Pinned: true, Content: body.Content, PinnedBy: "root@pam", PinnedAt: 1700000001})
	})
	specPath := writeSpecFile(t, "specVersion: 1\n")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"spec", "pin", "--url", srv.URL, "--token", "tok", specPath}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if postCalls != 1 {
		t.Fatalf("POST /spec/pin called %d times, want 1", postCalls)
	}
}

func TestRunSpecUnpin_DeletesPin(t *testing.T) {
	var deleteCalls int
	srv := newFakeVnproxd(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/spec/pin" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		deleteCalls++
		w.WriteHeader(http.StatusNoContent)
	})
	var stdout, stderr bytes.Buffer
	if code := run([]string{"spec", "unpin", "--url", srv.URL, "--token", "tok"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if deleteCalls != 1 {
		t.Fatalf("DELETE /spec/pin called %d times, want 1", deleteCalls)
	}
	if !strings.Contains(stdout.String(), "Unpinned") {
		t.Errorf("stdout = %q, want confirmation it unpinned", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"spec", "unpin", "--url", srv.URL, "--token", "tok", "-o", "json"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("json exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	var decoded specPinWire
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("stdout not valid JSON: %v (%s)", err, stdout.String())
	}
	if decoded.Pinned {
		t.Errorf("decoded.Pinned = true, want false after unpin")
	}
	assertDocumentedJSON(t, "spec unpin", stdout.Bytes())
}

func TestRunSpecCommands_NoTokenNeverDials(t *testing.T) {
	dialed := false
	srv := newFakeVnproxd(t, func(http.ResponseWriter, *http.Request) { dialed = true })
	t.Setenv("VNPROX_TOKEN", "")
	specPath := writeSpecFile(t, "specVersion: 1\n")

	for _, args := range [][]string{
		{"spec", "export", "--url", srv.URL},
		{"spec", "import", "--url", srv.URL, specPath},
		{"spec", "pin", "--url", srv.URL},
		{"spec", "unpin", "--url", srv.URL},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != ExitAuth {
			t.Errorf("%v exit code = %d, want ExitAuth", args, code)
		}
	}
	if dialed {
		t.Error("a daemon call was attempted without a token")
	}
}
