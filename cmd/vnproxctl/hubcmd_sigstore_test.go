package main

// hubcmd_sigstore_test.go covers T-3709's CLI additions to `vnproxctl hub`:
// revoke --log-entry (no --key needed — a sigstore-signed registry has no
// local key to re-sign with) and verify --sigstore-bundle (the sigstore
// counterpart of --signers). The positive verify case reuses the same
// ca.VirtualSigstore-based real-material assembly
// internal/hubreg/sigstore_test.go and cmd/vnproxd/hubwiring_sigstore_test.go
// use, for the same "sigstore-go's own test double, not a hand-rolled cert"
// reason those files give.

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	protocommon "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	protorekor "github.com/sigstore/protobuf-specs/gen/pb-go/rekor/v1"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	sigstoreroot "github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/testing/ca"
	"github.com/sigstore/sigstore-go/pkg/tlog"

	"github.com/bgovanlu/vnprox/internal/hubreg"
)

const (
	cliSigstoreIssuer   = "https://token.actions.githubusercontent.com"
	cliSigstoreIdentity = "https://github.com/bgovanlu/vnprox/.github/workflows/publish-registry.yml@refs/heads/main"
)

// TestHubCLI_RevokeLogEntryNeedsNoKey proves the sigstore revoke path: a
// registry's already-published (ed25519-signed, for this test's setup
// convenience) index.json can be revoked BY LOG ENTRY with no --key at all,
// which is the whole point — a sig_mode=sigstore registry has no local key.
// The write is unsigned (Signature stays absent) and idempotent.
func TestHubCLI_RevokeLogEntryNeedsNoKey(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "registry")
	pubKey, _ := keygen(t, dir, "publisher.key")
	idxKey, _ := keygen(t, dir, "index.key")

	bundlePath := writeBundle(t, dir, "ceph-3node")
	submission := filepath.Join(dir, "ceph-3node.submission.json")
	if code, _, errOut := hubRun(t, "hub", "publish", "--artifact", bundlePath, "--type", "blueprint",
		"--version", "1.0.0", "--key", pubKey, "--out", submission); code != ExitSuccess {
		t.Fatalf("publish: code=%d stderr=%s", code, errOut)
	}
	if code, _, errOut := hubRun(t, "hub", "index", "--root", root, "--submission", submission, "--key", idxKey); code != ExitSuccess {
		t.Fatalf("index: code=%d stderr=%s", code, errOut)
	}

	const logEntryID = "deadbeefcafe:1000"
	code, out, errOut := hubRun(t, "hub", "revoke", "--root", root, "--log-entry", logEntryID, "--reason", "compromised workflow run")
	if code != ExitSuccess {
		t.Fatalf("revoke --log-entry with no --key: code=%d stdout=%s stderr=%s", code, out, errOut)
	}

	raw, err := os.ReadFile(filepath.Join(root, "index.json")) //nolint:gosec // test-local path
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var doc hubreg.Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	if doc.Signature != nil {
		t.Fatalf("Signature = %+v, want nil — an unsigned write must never carry a stale signature", doc.Signature)
	}
	found := false
	for _, r := range doc.Revocations {
		if r.TransparencyLogIndex == logEntryID {
			found = true
		}
	}
	if !found {
		t.Fatalf("Revocations = %+v, want an entry for %s", doc.Revocations, logEntryID)
	}

	// Idempotent: running it again changes nothing (still unsigned, same
	// bytes on disk) — matches AddRevocation's own idempotency.
	before, _ := os.ReadFile(filepath.Join(root, "index.json")) //nolint:gosec // test-local path
	if code, out, errOut := hubRun(t, "hub", "revoke", "--root", root, "--log-entry", logEntryID, "--reason", "compromised workflow run"); code != ExitSuccess {
		t.Fatalf("second revoke: code=%d stdout=%s stderr=%s", code, out, errOut)
	}
	after, _ := os.ReadFile(filepath.Join(root, "index.json")) //nolint:gosec // test-local path
	if string(before) != string(after) {
		t.Fatalf("re-running an already-published log-entry revocation churned the file")
	}
}

// A --signer/--id revocation still requires --key: there is no sigstore
// equivalent of naming an artifact or a persistent signer key without
// re-signing, so relaxing --key must stay scoped to --log-entry only.
func TestHubCLI_RevokeWithoutLogEntryStillNeedsKey(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "registry")
	code, _, errOut := hubRun(t, "hub", "revoke", "--root", root, "--signer", "deadbeef", "--reason", "key compromised")
	if code == ExitSuccess {
		t.Fatal("revoke --signer with no --key unexpectedly succeeded")
	}
	if errOut == "" {
		t.Fatal("expected an error naming the missing --key")
	}
}

// TestHubCLI_VerifySigstoreBundle exercises `hub verify --sigstore-bundle`
// end to end against a real signed index+bundle pair (ca.VirtualSigstore,
// exactly as internal/hubreg/sigstore_test.go builds one) and against the
// identity-mismatch and usage-error cases.
func TestHubCLI_VerifySigstoreBundle(t *testing.T) {
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("NewVirtualSigstore: %v", err)
	}
	dir := t.TempDir()
	doc := hubreg.Document{SchemaVersion: hubreg.CurrentIndexSchema}
	indexRaw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	indexPath := filepath.Join(dir, "index.json")
	if writeErr := os.WriteFile(indexPath, indexRaw, 0o600); writeErr != nil {
		t.Fatalf("write index: %v", writeErr)
	}

	te, err := vs.Sign(cliSigstoreIdentity, cliSigstoreIssuer, indexRaw)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	bundleRaw := cliWiringBundleJSON(t, vs, te)
	bundlePath := filepath.Join(dir, "index.json.sigstore.json")
	if err := os.WriteFile(bundlePath, bundleRaw, 0o600); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	trustedRootPath := cliWritePinnedTrustedRoot(t, vs)

	t.Run("usage: neither --signers nor --sigstore-bundle", func(t *testing.T) {
		if code, _, _ := hubRun(t, "hub", "verify", "--index", indexPath); code == ExitSuccess {
			t.Fatal("expected a usage error")
		}
	})

	t.Run("usage: missing identity flags", func(t *testing.T) {
		code, _, errOut := hubRun(t, "hub", "verify", "--index", indexPath, "--sigstore-bundle", bundlePath)
		if code == ExitSuccess {
			t.Fatalf("expected a usage error, stderr=%s", errOut)
		}
	})

	t.Run("good bundle verifies and prints the log entry id", func(t *testing.T) {
		code, out, errOut := hubRun(t, "hub", "verify",
			"--index", indexPath, "--sigstore-bundle", bundlePath,
			"--sigstore-issuer", cliSigstoreIssuer, "--sigstore-identity", cliSigstoreIdentity,
			"--sigstore-trusted-root", trustedRootPath, "-o", "json")
		if code != ExitSuccess {
			t.Fatalf("verify: code=%d stdout=%s stderr=%s", code, out, errOut)
		}
		var resp struct {
			TransparencyLogEntry string `json:"transparencyLogEntry"`
		}
		if err := json.Unmarshal([]byte(out), &resp); err != nil {
			t.Fatalf("decode: %v (out=%s)", err, out)
		}
		if resp.TransparencyLogEntry == "" {
			t.Fatal("transparencyLogEntry was not printed")
		}
	})

	t.Run("wrong identity refused", func(t *testing.T) {
		code, out, errOut := hubRun(t, "hub", "verify",
			"--index", indexPath, "--sigstore-bundle", bundlePath,
			"--sigstore-issuer", cliSigstoreIssuer, "--sigstore-identity", "https://github.com/someone-else/evil/.github/workflows/x.yml@refs/heads/main",
			"--sigstore-trusted-root", trustedRootPath)
		if code == ExitSuccess {
			t.Fatalf("verify succeeded with the wrong identity, stdout=%s stderr=%s", out, errOut)
		}
	})
}

// cliWritePinnedTrustedRoot and cliWiringBundleJSON below are
// cmd/vnproxd/hubwiring_sigstore_test.go's helpers, duplicated here because
// each _test.go file's helpers are package-local and this is package main
// under a different binary (vnproxctl, not vnproxd) — see that file's
// comments for why each construction step is needed.
func cliWritePinnedTrustedRoot(t *testing.T, vs *ca.VirtualSigstore) string {
	t.Helper()
	fixed := make(map[string]*sigstoreroot.TransparencyLog, len(vs.RekorLogs()))
	for hexID, tlogInstance := range vs.RekorLogs() {
		raw, err := hex.DecodeString(hexID)
		if err != nil {
			t.Fatalf("decoding rekor log id %q: %v", hexID, err)
		}
		cp := *tlogInstance
		cp.ID = raw
		fixed[hexID] = &cp
	}
	tr, err := sigstoreroot.NewTrustedRoot(sigstoreroot.TrustedRootMediaType01,
		vs.FulcioCertificateAuthorities(), vs.CTLogs(), vs.TimestampingAuthorities(), fixed)
	if err != nil {
		t.Fatalf("NewTrustedRoot: %v", err)
	}
	data, err := tr.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	path := filepath.Join(t.TempDir(), "trusted-root.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writing trusted root: %v", err)
	}
	return path
}

func cliWiringBundleJSON(t *testing.T, vs *ca.VirtualSigstore, te *ca.TestEntity) []byte {
	t.Helper()
	vc, err := te.VerificationContent()
	if err != nil {
		t.Fatalf("VerificationContent: %v", err)
	}
	cert := vc.Certificate()
	sc, err := te.SignatureContent()
	if err != nil {
		t.Fatalf("SignatureContent: %v", err)
	}
	msc := sc.MessageSignatureContent()
	entries, err := te.TlogEntries()
	if err != nil || len(entries) == 0 {
		t.Fatalf("TlogEntries: %v", err)
	}
	tlogProto := entries[0].TransparencyLogEntry()
	if tlogProto.GetKindVersion() == nil {
		tlogProto.KindVersion = &protorekor.KindVersion{Kind: "hashedrekord", Version: "0.0.1"}
	}
	if tlogProto.GetInclusionPromise() == nil {
		set, setErr := vs.RekorSignPayload(tlog.RekorPayload{
			Body:           entries[0].Body(),
			IntegratedTime: entries[0].IntegratedTime().Unix(),
			LogIndex:       entries[0].LogIndex(),
			LogID:          hex.EncodeToString([]byte(entries[0].LogKeyID())),
		})
		if setErr != nil {
			t.Fatalf("RekorSignPayload: %v", setErr)
		}
		tlogProto.InclusionPromise = &protorekor.InclusionPromise{SignedEntryTimestamp: set}
	}
	mediaType, err := bundle.MediaTypeString("0.1")
	if err != nil {
		t.Fatalf("MediaTypeString: %v", err)
	}
	pb := &protobundle.Bundle{
		MediaType: mediaType,
		VerificationMaterial: &protobundle.VerificationMaterial{
			Content:     &protobundle.VerificationMaterial_Certificate{Certificate: &protocommon.X509Certificate{RawBytes: cert.Raw}},
			TlogEntries: []*protorekor.TransparencyLogEntry{tlogProto},
		},
		Content: &protobundle.Bundle_MessageSignature{
			MessageSignature: &protocommon.MessageSignature{
				MessageDigest: &protocommon.HashOutput{
					Algorithm: protocommon.HashAlgorithm(protocommon.HashAlgorithm_value[msc.DigestAlgorithm()]),
					Digest:    msc.Digest(),
				},
				Signature: msc.Signature(),
			},
		},
	}
	b, err := bundle.NewBundle(pb)
	if err != nil {
		t.Fatalf("assembling bundle: %v", err)
	}
	raw, err := b.MarshalJSON()
	if err != nil {
		t.Fatalf("marshaling bundle: %v", err)
	}
	return raw
}
