package main

// hubwiring_sigstore_test.go is T-3709's daemon-side wiring assertion, the
// sigstore-mode sibling of TestNewHubClient_IndexSignersInstallTheGate
// (hubwiring_test.go): setting [hub] sig_mode = "sigstore" must actually put
// hubreg.SigstoreGate on the client the daemon uses — never hubreg.Gate —
// and a missing/wrong certificate identity must refuse to bring the hub up
// at all rather than starting unprotected.
//
// This uses a LOCAL, pinned trusted-root file (sigstoreroot.NewTrustedRoot
// over a real ca.VirtualSigstore's own CA/Rekor/CT material, matching
// [hub] sigstore_trusted_root_file) so the test never performs the live TUF
// fetch newHubSigstoreGate falls back to when that key is unset — this is
// the config combination that avoids the network dependency entirely, and
// this test exercises exactly it.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/hubreg"
)

const (
	sigstoreWiringIssuer   = "https://token.actions.githubusercontent.com"
	sigstoreWiringIdentity = "https://github.com/bgovanlu/vnprox/.github/workflows/publish-registry.yml@refs/heads/main"
)

// writePinnedTrustedRoot builds a real sigstore-go TrustedRoot from vs's own
// CA/Rekor/CT material (root.NewTrustedRoot, not a hand-rolled JSON blob)
// and writes it to a temp file in the exact shape
// root.NewTrustedRootFromPath reads.
func writePinnedTrustedRoot(t *testing.T, vs *ca.VirtualSigstore) string {
	t.Helper()
	tr, err := sigstoreroot.NewTrustedRoot(
		sigstoreroot.TrustedRootMediaType01,
		vs.FulcioCertificateAuthorities(),
		vs.CTLogs(),
		vs.TimestampingAuthorities(),
		rekorLogsWithRawKeyID(t, vs),
	)
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

// rekorLogsWithRawKeyID fixes up ca.VirtualSigstore.RekorLogs()'s own
// TransparencyLog.ID convention (the hex log-id STRING's ASCII bytes) into
// what the production trusted-root JSON schema expects (the RAW digest
// bytes, hex-encoded again on load — root.ParseTransparencyLogs does
// hex.EncodeToString(tlog.GetLogId().GetKeyId()) to recover the map key).
// Verifying directly against an in-memory *ca.VirtualSigstore (as
// internal/hubreg's own tests do) never round-trips through this JSON shape
// and is unaffected; this fix-up is needed only because this wiring test
// specifically exercises the pinned-file path (root.NewTrustedRootFromPath),
// which real Sigstore trusted-root JSON already stores in the raw-bytes
// form — this is a mismatch in the *test double's* convenience convention,
// not evidence of anything wrong in sigstore-go's real schema or in
// internal/hubreg's own production code.
func rekorLogsWithRawKeyID(t *testing.T, vs *ca.VirtualSigstore) map[string]*sigstoreroot.TransparencyLog {
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
	return fixed
}

func TestNewHubClient_SigstoreModeInstallsSigstoreGate(t *testing.T) {
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("NewVirtualSigstore: %v", err)
	}
	trustedRootPath := writePinnedTrustedRoot(t, vs)

	doc := hubreg.Document{SchemaVersion: hubreg.CurrentIndexSchema}
	srv := signedSigstoreIndexServer(t, vs, doc)

	cfg := config.HubConfig{
		RegistryURL:             srv.URL,
		SigMode:                 config.HubSigModeSigstore,
		SigstoreIssuer:          sigstoreWiringIssuer,
		SigstoreSAN:             sigstoreWiringIdentity,
		SigstoreTrustedRootFile: trustedRootPath,
	}

	t.Run("configured: verifies and refuses the wrong identity", func(t *testing.T) {
		c := newHubClient(cfg, discardSlog())
		if c == nil {
			t.Fatal("hub client is nil with a valid sigstore config")
		}
		if _, idxErr := c.Index(context.Background()); idxErr != nil {
			t.Fatalf("Index: %v", idxErr)
		}

		wrongIdentity := cfg
		wrongIdentity.SigstoreSAN = "https://github.com/someone-else/evil/.github/workflows/x.yml@refs/heads/main"
		bad := newHubClient(wrongIdentity, discardSlog())
		if bad == nil {
			t.Fatal("hub client is nil (construction itself should not fail on a mismatched identity)")
		}
		if _, idxErr := bad.Index(context.Background()); idxErr == nil {
			t.Fatal("Index succeeded against a bundle signed for a different identity")
		}
	})

	t.Run("no identity configured: hub refuses to come up at all", func(t *testing.T) {
		noIdentity := cfg
		noIdentity.SigstoreIssuer, noIdentity.SigstoreSAN = "", ""
		if c := newHubClient(noIdentity, discardSlog()); c != nil {
			t.Fatal("hub client must be nil with sig_mode=sigstore and no identity pinned")
		}
	})

	t.Run("index_signers is never consulted in sigstore mode", func(t *testing.T) {
		// Even if an operator left [hub] index_signers set from a previous
		// ed25519 configuration, sig_mode=sigstore must never fall back to
		// the Ed25519 gate — the downgrade-guard property, exercised from
		// the config side rather than from the served-index side
		// (sigstore_test.go's TestVerifySigstore_RefusesEd25519ShapedIndex
		// covers that half).
		withStaleSigners := cfg
		withStaleSigners.IndexSigners = []string{"deadbeef"}
		c := newHubClient(withStaleSigners, discardSlog())
		if c == nil {
			t.Fatal("hub client is nil")
		}
		if _, idxErr := c.Index(context.Background()); idxErr != nil {
			t.Fatalf("Index: %v (sigstore verification should still succeed; stale index_signers must be ignored, not fatal)", idxErr)
		}
	})
}

func TestNewHubClient_SigstoreModeBadTrustedRootFileDisablesHub(t *testing.T) {
	cfg := config.HubConfig{
		RegistryURL:             "https://registry.example.com/vnprox",
		SigMode:                 config.HubSigModeSigstore,
		SigstoreIssuer:          sigstoreWiringIssuer,
		SigstoreSAN:             sigstoreWiringIdentity,
		SigstoreTrustedRootFile: filepath.Join(t.TempDir(), "does-not-exist.json"),
	}
	if c := newHubClient(cfg, discardSlog()); c != nil {
		t.Fatal("hub client must be nil when the pinned trusted root file cannot be read")
	}
}

// signedSigstoreIndexServer serves a real Sigstore-signed index.json plus
// its sibling bundle, built with the same real-material assembly
// internal/hubreg's own sigstore_test.go uses (ca.VirtualSigstore.Sign, then
// hand-assembling the wire bundle from that signed entity's real, exported
// accessors) — reimplemented minimally here since this test lives in
// package main and cannot import hubreg's unexported _test.go helpers. See
// internal/hubreg/sigstore_test.go's bundleJSON for the fuller account of
// why each of the three fix-ups below (KindVersion, InclusionPromise, media
// type) is needed.
func signedSigstoreIndexServer(t *testing.T, vs *ca.VirtualSigstore, doc hubreg.Document) *httptest.Server {
	t.Helper()
	indexRaw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal document: %v", err)
	}
	te, err := vs.Sign(sigstoreWiringIdentity, sigstoreWiringIssuer, indexRaw)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	bundleRaw := wiringBundleJSON(t, vs, te)

	mux := http.NewServeMux()
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(indexRaw) })
	mux.HandleFunc("/"+hubreg.SigstoreBundleName, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(bundleRaw) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// wiringBundleJSON is internal/hubreg/sigstore_test.go's bundleJSON,
// duplicated for package main's own wiring test (see that function's
// comments for why each step is needed).
func wiringBundleJSON(t *testing.T, vs *ca.VirtualSigstore, te *ca.TestEntity) []byte {
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
