// SPDX-License-Identifier: Apache-2.0

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

// buildTestRegistry walks the real publish -> index CLI flow (same as
// TestHubCLI_PublishReviewIndexFlow) to produce a genuine, signed registry
// root directory containing one blueprint entry, then serves it exactly as
// static hosting would (index.json + /artifacts/** on disk, nothing else) —
// this is the "local registry fixture" T-4009's task card points at, since
// the hosted registry.vnprox.com is unreachable from this environment.
func buildTestRegistry(t *testing.T, dir, blueprintID string) (root, idxFP string) {
	t.Helper()
	root = filepath.Join(dir, "registry")
	pubKey, _ := keygen(t, dir, blueprintID+"-publisher.key")
	idxKey, idxFP := keygen(t, dir, blueprintID+"-index.key")

	bundlePath := writeBundle(t, dir, blueprintID)
	submission := filepath.Join(dir, blueprintID+".submission.json")
	if code, _, errOut := hubRun(t, "hub", "publish",
		"--artifact", bundlePath, "--type", "blueprint", "--version", "1.0.0",
		"--key", pubKey, "--out", submission); code != ExitSuccess {
		t.Fatalf("publish: code=%d stderr=%s", code, errOut)
	}
	if code, _, errOut := hubRun(t, "hub", "index", "--root", root, "--submission", submission, "--key", idxKey); code != ExitSuccess {
		t.Fatalf("index: code=%d stderr=%s", code, errOut)
	}
	return root, idxFP
}

// serveRegistry stands up plain static hosting over a registry root — no
// service behind it, matching docs/hub-registry.md's "static index.json plus
// an artifact tree" architecture.
func serveRegistry(t *testing.T, root string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.FileServer(http.Dir(root)))
	t.Cleanup(srv.Close)
	return srv
}

// TestHubMirror_CreatesConsumableLocalMirror is T-4009 AC1's first half: a
// mirror of a real, signed registry lays out index.json plus the blueprint's
// artifact exactly where internal/hub.NewLocalDoer expects to find them, and
// the CLI's own output (both text and JSON) reports what it wrote.
func TestHubMirror_CreatesConsumableLocalMirror(t *testing.T) {
	dir := t.TempDir()
	root, idxFP := buildTestRegistry(t, dir, "mirror-bp")
	srv := serveRegistry(t, root)
	mirrorDir := filepath.Join(dir, "mirror")

	code, out, errOut := hubRun(t, "hub", "mirror", "--registry", srv.URL, "--signers", idxFP, "--out", mirrorDir)
	if code != ExitSuccess {
		t.Fatalf("mirror: code=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "1 artifact(s) written") {
		t.Fatalf("mirror output = %q, want it to report 1 artifact written", out)
	}
	if _, err := os.Stat(filepath.Join(mirrorDir, "index.json")); err != nil {
		t.Fatalf("mirror did not write index.json: %v", err)
	}
	artifactPath := filepath.Join(mirrorDir, "artifacts", "blueprint", "mirror-bp", "1.0.0.json")
	if _, err := os.Stat(artifactPath); err != nil {
		t.Fatalf("mirror did not write the artifact at the expected layout: %v", err)
	}

	code, jsonOut, errOut := hubRun(t, "hub", "mirror", "--registry", srv.URL, "--signers", idxFP, "--out", filepath.Join(dir, "mirror2"), "-o", "json")
	if code != ExitSuccess {
		t.Fatalf("mirror -o json: code=%d stderr=%s", code, errOut)
	}
	assertDocumentedJSON(t, "hub mirror", []byte(jsonOut))
	var resp struct {
		RegistryURL string `json:"registryUrl"`
		Artifacts   int    `json:"artifacts"`
		Live        int    `json:"live"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &resp); err != nil {
		t.Fatalf("decode mirror -o json output: %v", err)
	}
	if resp.Artifacts != 1 || resp.Live != 1 {
		t.Fatalf("mirror -o json = %+v, want artifacts=1 live=1", resp)
	}
	if !strings.HasPrefix(resp.RegistryURL, "file://") {
		t.Fatalf("mirror -o json registryUrl = %q, want a file:// URL", resp.RegistryURL)
	}

	// Refuses without --signers: offline must mean "verify against what was
	// mirrored," never "skip verification" — mirroring is no exception.
	if code, _, _ := hubRun(t, "hub", "mirror", "--registry", srv.URL, "--out", filepath.Join(dir, "mirror3")); code != ExitUsage {
		t.Fatalf("mirror without --signers: code=%d, want ExitUsage", code)
	}
}

// TestHubMirror_OfflineConsumptionByteIdenticalToDirectPull is T-4009 AC1 in
// full: mirror a registry, pull the SAME artifact once directly from the
// (still-running) origin and once from the mirror, then — critically — shut
// the origin server down entirely (closing its listening socket, so any
// stray network call fails immediately rather than hanging) and pull from
// the mirror AGAIN, proving the mirror path never depended on the origin
// being reachable. All three pulls must produce byte-identical output.
func TestHubMirror_OfflineConsumptionByteIdenticalToDirectPull(t *testing.T) {
	dir := t.TempDir()
	root, idxFP := buildTestRegistry(t, dir, "offline-bp")
	srv := serveRegistry(t, root)
	mirrorDir := filepath.Join(dir, "mirror")

	if code, _, errOut := hubRun(t, "hub", "mirror", "--registry", srv.URL, "--signers", idxFP, "--out", mirrorDir); code != ExitSuccess {
		t.Fatalf("mirror: code=%d stderr=%s", code, errOut)
	}

	directOut := filepath.Join(dir, "direct.pull.json")
	code, out, errOut := hubRun(t, "hub", "pull",
		"--registry", srv.URL, "--signers", idxFP,
		"--type", "blueprint", "--id", "offline-bp", "--version", "1.0.0",
		"--out", directOut)
	if code != ExitSuccess {
		t.Fatalf("direct pull: code=%d stderr=%s", code, errOut)
	}
	if strings.Contains(out, "no network access") {
		t.Fatalf("a direct (non-mirror) pull reported 'no network access': %q", out)
	}

	mirrorOut := filepath.Join(dir, "mirror.pull.json")
	code, out, errOut = hubRun(t, "hub", "pull",
		"--registry", mirrorDir, "--signers", idxFP,
		"--type", "blueprint", "--id", "offline-bp", "--version", "1.0.0",
		"--out", mirrorOut)
	if code != ExitSuccess {
		t.Fatalf("mirror pull (server still up): code=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "no network access") {
		t.Fatalf("a mirror pull's output = %q, want it to say no network access", out)
	}

	// Now actually take the network away: close the origin's listener.
	// Any subsequent attempt by the mirror path to dial it would fail loudly
	// (connection refused), not silently succeed.
	srv.Close()

	mirrorOutAfterClose := filepath.Join(dir, "mirror.pull.afterclose.json")
	code, _, errOut = hubRun(t, "hub", "pull",
		"--registry", mirrorDir, "--signers", idxFP,
		"--type", "blueprint", "--id", "offline-bp", "--version", "1.0.0",
		"--out", mirrorOutAfterClose)
	if code != ExitSuccess {
		t.Fatalf("mirror pull after origin shutdown: code=%d stderr=%s (this must succeed with the origin unreachable)", code, errOut)
	}

	direct, err := os.ReadFile(directOut) //nolint:gosec // test-local path
	if err != nil {
		t.Fatalf("read direct pull output: %v", err)
	}
	mirrored, err := os.ReadFile(mirrorOutAfterClose) //nolint:gosec // test-local path
	if err != nil {
		t.Fatalf("read mirror pull output: %v", err)
	}
	if !bytes.Equal(direct, mirrored) {
		t.Fatalf("mirror pull produced different bytes than a direct pull:\ndirect:  %s\nmirror:  %s", direct, mirrored)
	}
	beforeClose, err := os.ReadFile(mirrorOut) //nolint:gosec // test-local path
	if err != nil {
		t.Fatalf("read mirror pull (before close) output: %v", err)
	}
	if !bytes.Equal(direct, beforeClose) {
		t.Fatal("mirror pull (origin still reachable) already differed from a direct pull")
	}
}

// TestHubPull_UntrustedSignerRefused proves verification actually runs
// against a mirrored index, not just a hosted one: a pull against the SAME
// mirror with a signer fingerprint the operator did not configure is
// refused, exactly as it would be against the hosted registry.
func TestHubPull_UntrustedSignerRefused(t *testing.T) {
	dir := t.TempDir()
	root, idxFP := buildTestRegistry(t, dir, "untrusted-bp")
	srv := serveRegistry(t, root)
	mirrorDir := filepath.Join(dir, "mirror")
	if code, _, errOut := hubRun(t, "hub", "mirror", "--registry", srv.URL, "--signers", idxFP, "--out", mirrorDir); code != ExitSuccess {
		t.Fatalf("mirror: code=%d stderr=%s", code, errOut)
	}
	srv.Close()

	_, wrongFP := keygen(t, dir, "wrong.key")
	out := filepath.Join(dir, "should-not-exist.json")
	code, _, errOut := hubRun(t, "hub", "pull",
		"--registry", mirrorDir, "--signers", wrongFP,
		"--type", "blueprint", "--id", "untrusted-bp", "--version", "1.0.0",
		"--out", out)
	if code == ExitSuccess {
		t.Fatal("pull with an untrusted signer fingerprint succeeded — verification did not run against the mirrored index")
	}
	if !strings.Contains(errOut, "untrusted") {
		t.Fatalf("pull stderr = %q, want it to name the untrusted signer", errOut)
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Fatal("pull wrote an artifact despite failing verification")
	}
}

// TestHubPull_TamperedMirrorIndexFailsVerification is T-4009's hardest
// failure mode, made explicit: a mirror directory that has been tampered
// with after mirroring (standing in for anything between "hub mirror wrote
// it" and "hub pull reads it" — a compromised USB stick, a corrupted copy,
// deliberate manipulation) must be REFUSED, not silently accepted because
// there is no network path to double-check it against. Two tamper shapes are
// covered: corrupting the signed bytes (breaks the Ed25519 signature) and
// splicing in an entirely unsigned index (no signature at all).
func TestHubPull_TamperedMirrorIndexFailsVerification(t *testing.T) {
	dir := t.TempDir()
	root, idxFP := buildTestRegistry(t, dir, "tamper-bp")
	srv := serveRegistry(t, root)
	mirrorDir := filepath.Join(dir, "mirror")
	if code, _, errOut := hubRun(t, "hub", "mirror", "--registry", srv.URL, "--signers", idxFP, "--out", mirrorDir); code != ExitSuccess {
		t.Fatalf("mirror: code=%d stderr=%s", code, errOut)
	}
	srv.Close()

	indexPath := filepath.Join(mirrorDir, "index.json")
	original, err := os.ReadFile(indexPath) //nolint:gosec // test-local path
	if err != nil {
		t.Fatalf("read mirrored index: %v", err)
	}

	tests := []struct {
		name    string
		tamper  func([]byte) []byte
		wantErr string
	}{
		{
			name: "flip a byte inside the signed content",
			tamper: func(raw []byte) []byte {
				out := append([]byte(nil), raw...)
				i := bytes.Index(out, []byte("tamper-bp"))
				if i < 0 {
					t.Fatal("test bug: could not find entry id in index bytes to corrupt")
				}
				out[i] = 'X'
				return out
			},
			wantErr: "signature",
		},
		{
			name: "strip the signature entirely",
			tamper: func(raw []byte) []byte {
				var doc map[string]any
				if jerr := json.Unmarshal(raw, &doc); jerr != nil {
					t.Fatalf("unmarshal index for tamper: %v", jerr)
				}
				delete(doc, "signature")
				out, jerr := json.Marshal(doc)
				if jerr != nil {
					t.Fatalf("remarshal tampered index: %v", jerr)
				}
				return out
			},
			wantErr: "not signed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tampered := tt.tamper(original)
			if err := os.WriteFile(indexPath, tampered, 0o644); err != nil { //nolint:gosec // test-local path
				t.Fatalf("write tampered index: %v", err)
			}
			t.Cleanup(func() {
				if err := os.WriteFile(indexPath, original, 0o644); err != nil { //nolint:gosec // test-local path
					t.Fatalf("restore original index: %v", err)
				}
			})

			out := filepath.Join(t.TempDir(), "should-not-exist.json")
			code, _, errOut := hubRun(t, "hub", "pull",
				"--registry", mirrorDir, "--signers", idxFP,
				"--type", "blueprint", "--id", "tamper-bp", "--version", "1.0.0",
				"--out", out)
			if code == ExitSuccess {
				t.Fatalf("pull against a tampered mirror (%s) succeeded — verification was silently skipped offline", tt.name)
			}
			if !strings.Contains(strings.ToLower(errOut), tt.wantErr) {
				t.Errorf("pull stderr = %q, want it to mention %q", errOut, tt.wantErr)
			}
			if _, statErr := os.Stat(out); statErr == nil {
				t.Fatal("pull wrote an artifact despite the tampered index failing verification")
			}
		})
	}
}

// TestHubPull_RevokedEntryRefusedFromMirror proves a revocation published
// into the index before mirroring is still honored purely from the mirrored
// bytes, with the origin unreachable — the offline half of the promise
// docs/hub-registry.md makes about revocations riding inside the signed
// document.
func TestHubPull_RevokedEntryRefusedFromMirror(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "registry")
	pubKey, _ := keygen(t, dir, "revoked-publisher.key")
	idxKey, idxFP := keygen(t, dir, "revoked-index.key")
	bundlePath := writeBundle(t, dir, "revoked-bp")
	submission := filepath.Join(dir, "revoked-bp.submission.json")
	if code, _, errOut := hubRun(t, "hub", "publish",
		"--artifact", bundlePath, "--type", "blueprint", "--version", "1.0.0",
		"--key", pubKey, "--out", submission); code != ExitSuccess {
		t.Fatalf("publish: code=%d stderr=%s", code, errOut)
	}
	if code, _, errOut := hubRun(t, "hub", "index", "--root", root, "--submission", submission, "--key", idxKey); code != ExitSuccess {
		t.Fatalf("index: code=%d stderr=%s", code, errOut)
	}
	if code, _, errOut := hubRun(t, "hub", "revoke", "--root", root, "--key", idxKey,
		"--type", "blueprint", "--id", "revoked-bp", "--version", "1.0.0",
		"--reason", "ships an insecure default"); code != ExitSuccess {
		t.Fatalf("revoke: code=%d stderr=%s", code, errOut)
	}

	srv := serveRegistry(t, root)
	mirrorDir := filepath.Join(dir, "mirror")
	code, out, errOut := hubRun(t, "hub", "mirror", "--registry", srv.URL, "--signers", idxFP, "--out", mirrorDir)
	if code != ExitSuccess {
		t.Fatalf("mirror: code=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "0 artifact(s) written") {
		t.Fatalf("mirror output = %q, want 0 artifacts written (the only entry is revoked)", out)
	}
	srv.Close()

	pullOut := filepath.Join(dir, "should-not-exist.json")
	code, _, errOut = hubRun(t, "hub", "pull",
		"--registry", mirrorDir, "--signers", idxFP,
		"--type", "blueprint", "--id", "revoked-bp", "--version", "1.0.0",
		"--out", pullOut)
	if code == ExitSuccess {
		t.Fatal("pull of a revoked entry from a mirror succeeded")
	}
	if !strings.Contains(errOut, "revoked") {
		t.Fatalf("pull stderr = %q, want it to say the entry is revoked", errOut)
	}
}
