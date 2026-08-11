package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/hub"
	"github.com/bgovanlu/vnprox/internal/hubreg"
)

// hubRun invokes the CLI exactly as main does and returns (code, stdout, stderr).
func hubRun(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// writeBundle writes an unsigned blueprint bundle file for the publisher to sign.
func writeBundle(t *testing.T, dir, id string) string {
	t.Helper()
	bundle := blueprint.Bundle{
		BundleVersion: blueprint.CurrentBundleVersion,
		Blueprint: blueprint.Blueprint{
			BlueprintVersion: blueprint.CurrentBlueprintVersion,
			ID:               id,
			Name:             "Blueprint " + id,
			Description:      "three-node ceph cluster",
		},
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := filepath.Join(dir, id+".bundle.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// keygen creates a signing key and returns (path, fingerprint).
func keygen(t *testing.T, dir, name string) (string, string) {
	t.Helper()
	path := filepath.Join(dir, name)
	code, out, errOut := hubRun(t, "hub", "keygen", "--key", path, "-o", "json")
	if code != ExitSuccess {
		t.Fatalf("keygen: code=%d stderr=%s", code, errOut)
	}
	var resp struct {
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode keygen output %q: %v", out, err)
	}
	if resp.Fingerprint == "" {
		t.Fatal("keygen reported no fingerprint")
	}
	// Refuses to clobber an existing identity.
	if code, _, _ := hubRun(t, "hub", "keygen", "--key", path); code == ExitSuccess {
		t.Fatal("keygen overwrote an existing key file")
	}
	return path, resp.Fingerprint
}

// TestHubCLI_PublishReviewIndexFlow walks the documented registry process end
// to end with a real artifact: publisher signs and submits, reviewer indexes,
// anyone verifies — then the daemon's own client consumes the result. It also
// covers AC4: the second `hub index` of the same submission changes nothing.
func TestHubCLI_PublishReviewIndexFlow(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "registry")
	pubKey, pubFP := keygen(t, dir, "publisher.key")
	idxKey, idxFP := keygen(t, dir, "index.key")

	bundlePath := writeBundle(t, dir, "ceph-3node")
	submission := filepath.Join(dir, "ceph-3node.submission.json")

	// 1. Publisher side: sign the artifact and produce a submission.
	code, _, errOut := hubRun(t, "hub", "publish",
		"--artifact", bundlePath, "--type", "blueprint", "--version", "1.0.0",
		"--key", pubKey, "--publisher", "acme", "--out", submission)
	if code != ExitSuccess {
		t.Fatalf("publish: code=%d stderr=%s", code, errOut)
	}
	subRaw, err := os.ReadFile(submission) //nolint:gosec // test-local path
	if err != nil {
		t.Fatalf("read submission: %v", err)
	}
	sub, err := hubreg.ParseSubmission(subRaw)
	if err != nil {
		t.Fatalf("the emitted submission must be valid: %v", err)
	}
	if sub.Entry.SignerFingerprint() != pubFP {
		t.Fatalf("submission signer = %q, want %q", sub.Entry.SignerFingerprint(), pubFP)
	}

	// 2. Reviewer side: index it.
	code, _, errOut = hubRun(t, "hub", "index", "--root", root, "--submission", submission, "--key", idxKey)
	if code != ExitSuccess {
		t.Fatalf("index: code=%d stderr=%s", code, errOut)
	}
	indexPath := filepath.Join(root, "index.json")
	firstIndex, err := os.ReadFile(indexPath) //nolint:gosec // test-local path
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	artifactPath := filepath.Join(root, "artifacts", "blueprint", "ceph-3node", "1.0.0.json")
	if _, statErr := os.Stat(artifactPath); statErr != nil {
		t.Fatalf("the artifact was not written into the published tree: %v", statErr)
	}

	// 3. AC4: indexing the same submission again yields one entry and does
	//    not even rewrite the published bytes.
	code, out, errOut := hubRun(t, "hub", "index", "--root", root, "--submission", submission, "--key", idxKey)
	if code != ExitSuccess {
		t.Fatalf("re-index: code=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "Already published") {
		t.Fatalf("re-index output = %q, want an 'Already published' no-op", out)
	}
	secondIndex, err := os.ReadFile(indexPath) //nolint:gosec // test-local path
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if !bytes.Equal(firstIndex, secondIndex) {
		t.Fatal("re-publishing rewrote index.json")
	}
	doc, err := hubreg.Verify(secondIndex, []string{idxFP})
	if err != nil {
		t.Fatalf("published index must verify: %v", err)
	}
	if len(doc.Entries) != 1 {
		t.Fatalf("entries = %d, want 1 after publishing the same artifact twice", len(doc.Entries))
	}

	// 4. Anyone: verify the published index against the pinned signer.
	code, out, errOut = hubRun(t, "hub", "verify", "--index", indexPath, "--signers", idxFP)
	if code != ExitSuccess {
		t.Fatalf("verify: code=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "ceph-3node@1.0.0") {
		t.Fatalf("verify output = %q", out)
	}
	// A different pinned signer is refused.
	if wrongSigner, _, _ := hubRun(t, "hub", "verify", "--index", indexPath, "--signers", pubFP); wrongSigner == ExitSuccess {
		t.Fatal("verify accepted an index signed by a key that is not the pinned signer")
	}

	// 5. Revocation, then verify again: the entry is withdrawn from what a
	//    client is offered, and the revocation is published in the index.
	code, _, errOut = hubRun(t, "hub", "revoke", "--root", root, "--key", idxKey,
		"--type", "blueprint", "--id", "ceph-3node", "--version", "1.0.0",
		"--reason", "ships an insecure default")
	if code != ExitSuccess {
		t.Fatalf("revoke: code=%d stderr=%s", code, errOut)
	}
	revokedRaw, err := os.ReadFile(indexPath) //nolint:gosec // test-local path
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	doc, err = hubreg.Verify(revokedRaw, []string{idxFP})
	if err != nil {
		t.Fatalf("the re-signed index must verify: %v", err)
	}
	if len(doc.Revocations) != 1 || len(doc.HubIndex().Entries) != 0 {
		t.Fatalf("revocations=%d offered=%d, want 1/0", len(doc.Revocations), len(doc.HubIndex().Entries))
	}
	// Revoking the same thing twice is idempotent too.
	code, out, _ = hubRun(t, "hub", "revoke", "--root", root, "--key", idxKey,
		"--type", "blueprint", "--id", "ceph-3node", "--version", "1.0.0",
		"--reason", "ships an insecure default")
	if code != ExitSuccess || !strings.Contains(out, "Already revoked") {
		t.Fatalf("re-revoke: code=%d out=%q", code, out)
	}
}

// TestHubCLI_IndexRefusesConflict: a second, different artifact under an
// already-published version is refused, and the published index is untouched.
func TestHubCLI_IndexRefusesConflict(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "registry")
	keyA, _ := keygen(t, dir, "a.key")
	keyB, _ := keygen(t, dir, "b.key")
	idxKey, idxFP := keygen(t, dir, "index.key")

	bundlePath := writeBundle(t, dir, "bp")
	subA := filepath.Join(dir, "a.json")
	subB := filepath.Join(dir, "b.json")
	if code, _, e := hubRun(t, "hub", "publish", "--artifact", bundlePath, "--type", "blueprint", "--version", "1.0.0", "--key", keyA, "--out", subA); code != ExitSuccess {
		t.Fatalf("publish A: %s", e)
	}
	if code, _, e := hubRun(t, "hub", "publish", "--artifact", bundlePath, "--type", "blueprint", "--version", "1.0.0", "--key", keyB, "--out", subB); code != ExitSuccess {
		t.Fatalf("publish B: %s", e)
	}
	if code, _, e := hubRun(t, "hub", "index", "--root", root, "--submission", subA, "--key", idxKey); code != ExitSuccess {
		t.Fatalf("index A: %s", e)
	}
	before, err := os.ReadFile(filepath.Join(root, "index.json")) //nolint:gosec // test-local path
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	code, _, errOut := hubRun(t, "hub", "index", "--root", root, "--submission", subB, "--key", idxKey)
	if code != ExitPending {
		t.Fatalf("code = %d, want ExitPending for a conflicting republish (stderr=%s)", code, errOut)
	}
	if !strings.Contains(errOut, "already indexed") {
		t.Fatalf("stderr = %q, want a conflict message", errOut)
	}
	after, err := os.ReadFile(filepath.Join(root, "index.json")) //nolint:gosec // test-local path
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("a refused publish rewrote the index")
	}
	if _, err := hubreg.Verify(after, []string{idxFP}); err != nil {
		t.Fatalf("index still verifies: %v", err)
	}
}

// TestHubCLI_PublishRefusesUnsignedByDefault: forgetting --key does not
// quietly publish an unsigned artifact.
func TestHubCLI_PublishRefusesUnsignedByDefault(t *testing.T) {
	dir := t.TempDir()
	bundlePath := writeBundle(t, dir, "bp")
	out := filepath.Join(dir, "sub.json")

	code, _, errOut := hubRun(t, "hub", "publish", "--artifact", bundlePath, "--type", "blueprint", "--version", "1.0.0", "--out", out)
	if code != ExitPending {
		t.Fatalf("code = %d, want ExitPending (stderr=%s)", code, errOut)
	}
	if !strings.Contains(errOut, "unsigned") {
		t.Fatalf("stderr = %q", errOut)
	}
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused publish still wrote a submission file")
	}

	// Deliberately publishing unsigned works and says so.
	code, stdout, errOut := hubRun(t, "hub", "publish", "--artifact", bundlePath, "--type", "blueprint", "--version", "1.0.0", "--allow-unsigned", "--out", out)
	if code != ExitSuccess {
		t.Fatalf("code = %d stderr=%s", code, errOut)
	}
	if !strings.Contains(stdout, "UNSIGNED") {
		t.Fatalf("stdout = %q, want an explicit unsigned warning", stdout)
	}
}

// TestHubCLI_PluginPublishCarriesCapabilityScope: the capability scope an
// operator reviews before install comes from the signed manifest.
func TestHubCLI_PluginPublishCarriesCapabilityScope(t *testing.T) {
	dir := t.TempDir()
	key, fp := keygen(t, dir, "publisher.key")
	art := hub.PluginArtifact{Manifest: hub.PluginManifest{
		ID: "flowtap", Name: "Flow Tap", Version: "0.3.1", APIVersion: "v1",
		Transport: "grpc", Endpoint: "/usr/libexec/vnprox-flowtap",
		ExtensionPoints: []string{"flowIngestor"}, Capabilities: []string{"netRead", "flowWrite"},
	}}
	raw, err := json.Marshal(art)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	artPath := filepath.Join(dir, "flowtap.json")
	if writeErr := os.WriteFile(artPath, raw, 0o600); writeErr != nil {
		t.Fatalf("write: %v", writeErr)
	}
	subPath := filepath.Join(dir, "sub.json")
	code, _, errOut := hubRun(t, "hub", "publish", "--artifact", artPath, "--type", "plugin", "--key", key, "--out", subPath)
	if code != ExitSuccess {
		t.Fatalf("publish: code=%d stderr=%s", code, errOut)
	}
	subRaw, err := os.ReadFile(subPath) //nolint:gosec // test-local path
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	sub, err := hubreg.ParseSubmission(subRaw)
	if err != nil {
		t.Fatalf("ParseSubmission: %v", err)
	}
	if sub.Entry.Version != "0.3.1" {
		t.Fatalf("version = %q, want the manifest's 0.3.1", sub.Entry.Version)
	}
	if strings.Join(sub.Entry.Capabilities, ",") != "netRead,flowWrite" {
		t.Fatalf("capabilities = %v", sub.Entry.Capabilities)
	}
	if sub.Entry.SignerFingerprint() != fp {
		t.Fatalf("signer = %q, want %q", sub.Entry.SignerFingerprint(), fp)
	}
}

func TestHubCLI_Usage(t *testing.T) {
	if code, out, _ := hubRun(t, "hub"); code != ExitUsage || !strings.Contains(out, "") {
		t.Fatalf("bare `hub` code = %d", code)
	}
	if code, _, _ := hubRun(t, "hub", "wat"); code != ExitUsage {
		t.Fatalf("unknown subcommand code = %d, want ExitUsage", code)
	}
	if code, out, _ := hubRun(t, "hub", "--help"); code != ExitSuccess || !strings.Contains(out, "vnproxctl hub publish") {
		t.Fatalf("help code = %d out = %q", code, out)
	}
	if code, _, _ := hubRun(t, "hub", "index", "--root", t.TempDir()); code != ExitUsage {
		t.Fatal("missing required flags must be a usage error")
	}
	if code, _, _ := hubRun(t, "hub", "revoke", "--root", t.TempDir(), "--key", "x", "--reason", "y"); code != ExitUsage {
		t.Fatal("a revocation naming nothing must be a usage error")
	}
}
