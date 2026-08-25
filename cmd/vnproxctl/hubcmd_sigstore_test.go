package main

// hubcmd_sigstore_test.go covers T-3709's CLI additions to `vnproxctl hub`:
// verify --sigstore-key-bundle (a registry's Sigstore-signed key-custody
// attestation, verified by internal/hubreg/sigstoreverify) and revoke
// --log-entry (deny-listing one such attestation event inside an ordinary,
// still-Ed25519-signed index.json). The positive verify case reuses the
// same ca.VirtualSigstore-based real-material assembly
// internal/hubreg/sigstoreverify's own tests use, for the same "sigstore-go's
// own test double, not a hand-rolled cert" reason those tests give.

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	protocommon "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	protorekor "github.com/sigstore/protobuf-specs/gen/pb-go/rekor/v1"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	sigstoreroot "github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/testing/ca"
	"github.com/sigstore/sigstore-go/pkg/tlog"
)

const (
	cliSigstoreIssuer   = "https://token.actions.githubusercontent.com"
	cliSigstoreIdentity = "https://github.com/bgovanlu/vnprox/.github/workflows/publish-registry.yml@refs/heads/main"
)

// TestHubCLI_VerifySigstoreKeyBundle exercises `hub verify
// --sigstore-key-bundle` end to end against a real signed attestation+bundle
// pair (ca.VirtualSigstore, the same real-material construction
// internal/hubreg/sigstoreverify's own tests use) and against the
// identity-mismatch, revocation, and usage-error cases.
func TestHubCLI_VerifySigstoreKeyBundle(t *testing.T) {
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("NewVirtualSigstore: %v", err)
	}
	dir := t.TempDir()

	attestation := map[string]any{
		"schemaVersion": 1,
		"generatedAt":   1770000000,
		"registryUrl":   "https://registry.example.com/vnprox",
		"indexSigners": []map[string]any{
			{"fingerprint": strings.Repeat("ab", 32), "note": "primary index key"},
		},
	}
	attestRaw, err := json.Marshal(attestation)
	if err != nil {
		t.Fatalf("marshal attestation: %v", err)
	}
	attestPath := filepath.Join(dir, "trusted-signers.json")
	if writeErr := os.WriteFile(attestPath, attestRaw, 0o600); writeErr != nil {
		t.Fatalf("write attestation: %v", writeErr)
	}

	te, err := vs.Sign(cliSigstoreIdentity, cliSigstoreIssuer, attestRaw)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	bundleRaw := cliSigstoreBundleJSON(t, vs, te)
	bundlePath := filepath.Join(dir, "trusted-signers.json.sigstore.json")
	if err := os.WriteFile(bundlePath, bundleRaw, 0o600); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	trustedRootPath := cliWritePinnedTrustedRoot(t, vs)

	t.Run("usage: missing --sigstore-bundle", func(t *testing.T) {
		if code, _, _ := hubRun(t, "hub", "verify", "--sigstore-key-bundle", attestPath); code == ExitSuccess {
			t.Fatal("expected a usage error")
		}
	})

	t.Run("usage: missing identity flags", func(t *testing.T) {
		code, _, errOut := hubRun(t, "hub", "verify", "--sigstore-key-bundle", attestPath, "--sigstore-bundle", bundlePath)
		if code == ExitSuccess {
			t.Fatalf("expected a usage error, stderr=%s", errOut)
		}
	})

	t.Run("good bundle verifies and prints the log entry id and fingerprints", func(t *testing.T) {
		code, out, errOut := hubRun(t, "hub", "verify",
			"--sigstore-key-bundle", attestPath, "--sigstore-bundle", bundlePath,
			"--sigstore-issuer", cliSigstoreIssuer, "--sigstore-identity", cliSigstoreIdentity,
			"--sigstore-trusted-root", trustedRootPath, "-o", "json")
		if code != ExitSuccess {
			t.Fatalf("verify: code=%d stdout=%s stderr=%s", code, out, errOut)
		}
		var resp struct {
			TransparencyLogEntry string `json:"transparencyLogEntry"`
			IndexSigners         []struct {
				Fingerprint string `json:"fingerprint"`
			} `json:"indexSigners"`
		}
		if err := json.Unmarshal([]byte(out), &resp); err != nil {
			t.Fatalf("decode: %v (out=%s)", err, out)
		}
		if resp.TransparencyLogEntry == "" {
			t.Fatal("transparencyLogEntry was not printed")
		}
		if len(resp.IndexSigners) != 1 || resp.IndexSigners[0].Fingerprint != strings.Repeat("ab", 32) {
			t.Fatalf("indexSigners = %+v, want the one attested fingerprint", resp.IndexSigners)
		}
	})

	t.Run("wrong identity refused", func(t *testing.T) {
		code, out, errOut := hubRun(t, "hub", "verify",
			"--sigstore-key-bundle", attestPath, "--sigstore-bundle", bundlePath,
			"--sigstore-issuer", cliSigstoreIssuer, "--sigstore-identity", "https://github.com/someone-else/evil/.github/workflows/x.yml@refs/heads/main",
			"--sigstore-trusted-root", trustedRootPath)
		if code == ExitSuccess {
			t.Fatalf("verify succeeded with the wrong identity, stdout=%s stderr=%s", out, errOut)
		}
	})

	t.Run("revoked log entry refused when checked against an index", func(t *testing.T) {
		// First learn the real log entry id (never guessed).
		code, out, errOut := hubRun(t, "hub", "verify",
			"--sigstore-key-bundle", attestPath, "--sigstore-bundle", bundlePath,
			"--sigstore-issuer", cliSigstoreIssuer, "--sigstore-identity", cliSigstoreIdentity,
			"--sigstore-trusted-root", trustedRootPath, "-o", "json")
		if code != ExitSuccess {
			t.Fatalf("verify: code=%d stdout=%s stderr=%s", code, out, errOut)
		}
		var resp struct {
			TransparencyLogEntry string `json:"transparencyLogEntry"`
		}
		if err := json.Unmarshal([]byte(out), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}

		root := filepath.Join(dir, "registry")
		idxKey, _ := keygen(t, dir, "index.key")
		if revokeCode, _, revokeErrOut := hubRun(t, "hub", "revoke", "--root", root, "--key", idxKey,
			"--log-entry", resp.TransparencyLogEntry, "--reason", "compromised workflow run"); revokeCode != ExitSuccess {
			t.Fatalf("revoke --log-entry: code=%d stderr=%s", revokeCode, revokeErrOut)
		}

		code, _, errOut = hubRun(t, "hub", "verify",
			"--sigstore-key-bundle", attestPath, "--sigstore-bundle", bundlePath,
			"--sigstore-issuer", cliSigstoreIssuer, "--sigstore-identity", cliSigstoreIdentity,
			"--sigstore-trusted-root", trustedRootPath,
			"--check-revoked-against", filepath.Join(root, "index.json"))
		if code == ExitSuccess {
			t.Fatal("verify succeeded against a revoked log entry")
		}
		if !strings.Contains(errOut, "revoked") {
			t.Fatalf("stderr = %q, want it to mention the revocation", errOut)
		}
	})
}

// TestHubCLI_RevokeLogEntryStillNeedsKey proves --log-entry does NOT relax
// the --key requirement in this design: unlike the abandoned
// `sigstore-in-daemon` branch (where index.json itself was signed keylessly,
// so there was no local key to re-sign a revocation with), this design's
// index.json is always Ed25519-signed by an ordinary reviewer — Sigstore
// only attests which fingerprint is currently trusted, never signs the
// index — so every `hub revoke` mode, --log-entry included, re-signs with
// the same local index key as before.
func TestHubCLI_RevokeLogEntryStillNeedsKey(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "registry")
	code, _, errOut := hubRun(t, "hub", "revoke", "--root", root, "--log-entry", "deadbeef:1000", "--reason", "compromised workflow run")
	if code == ExitSuccess {
		t.Fatal("revoke --log-entry with no --key unexpectedly succeeded")
	}
	if errOut == "" {
		t.Fatal("expected an error naming the missing --key")
	}
}

// cliWritePinnedTrustedRoot and cliSigstoreBundleJSON mirror
// internal/hubreg/sigstoreverify's own test helpers, duplicated here because
// each _test.go file's helpers are package-local and this is package main
// under a different binary (vnproxctl, not the sigstoreverify package
// itself) — see that package's attestation_test.go for the fuller account
// of why each construction step below is needed.
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

func cliSigstoreBundleJSON(t *testing.T, vs *ca.VirtualSigstore, te *ca.TestEntity) []byte {
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
