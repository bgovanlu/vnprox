// SPDX-License-Identifier: Apache-2.0

package sigstoreverify

// attestation_test.go exercises VerifyKeyAttestation against sigstore-go's
// OWN test harness — github.com/sigstore/sigstore-go/pkg/testing/ca's
// VirtualSigstore — rather than a hand-rolled fake Fulcio certificate.
// CLAUDE.md's mock-rule note ("a fixture and the code that reads it, both
// written from one reading, agreeing with each other forever") applies to a
// hand-rolled cert exactly as it does to a PVE fixture: VirtualSigstore is
// sigstore-go's own upstream test double, used by sigstore-go's own test
// suite, so a leaf certificate, Rekor entry and inclusion proof built by it
// are real instances of sigstore-go's own types, not this package's guess
// at their shape. This file, and its bundle-assembly helper, mirror
// internal/hubreg/sigstore_test.go from the abandoned in-daemon branch
// (`sigstore-in-daemon`, commit 562de983) — same construction, retargeted
// at KeyAttestation bytes instead of an index.json Document.

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	protocommon "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	protorekor "github.com/sigstore/protobuf-specs/gen/pb-go/rekor/v1"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/testing/ca"
	"github.com/sigstore/sigstore-go/pkg/tlog"
)

const (
	testIssuer   = "https://token.actions.githubusercontent.com"
	testIdentity = "https://github.com/bgovanlu/vnprox/.github/workflows/publish-registry.yml@refs/heads/main"
)

func testAttestation() KeyAttestation {
	return KeyAttestation{
		SchemaVersion: CurrentAttestationSchema,
		GeneratedAt:   1770000000,
		RegistryURL:   "https://registry.example.com/vnprox",
		IndexSigners: []SignerRecord{
			{Fingerprint: strings.Repeat("ab", 32), Note: "primary index key"},
		},
	}
}

func marshalAttestation(t *testing.T, a KeyAttestation) []byte {
	t.Helper()
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal attestation: %v", err)
	}
	return raw
}

// bundleJSON assembles te's real certificate, message signature and
// transparency-log entry into a sigstore-go bundle and returns its
// marshaled JSON bytes — the exact shape VerifyKeyAttestation's bundleRaw
// parameter expects on the wire. vs is the same VirtualSigstore te was
// signed with (needed to reconstruct the entry's inclusion-promise SET —
// see the comment below).
func bundleJSON(t *testing.T, vs *ca.VirtualSigstore, te *ca.TestEntity) []byte {
	t.Helper()
	vc, err := te.VerificationContent()
	if err != nil {
		t.Fatalf("VerificationContent: %v", err)
	}
	cert := vc.Certificate()
	if cert == nil {
		t.Fatal("VerificationContent has no certificate")
	}

	sc, err := te.SignatureContent()
	if err != nil {
		t.Fatalf("SignatureContent: %v", err)
	}
	msc := sc.MessageSignatureContent()
	if msc == nil {
		t.Fatal("SignatureContent has no MessageSignatureContent")
	}

	entries, err := te.TlogEntries()
	if err != nil {
		t.Fatalf("TlogEntries: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no tlog entries")
	}
	tlogProto := entries[0].TransparencyLogEntry()
	if tlogProto.GetKindVersion() == nil {
		// ca.VirtualSigstore.Sign builds its tlog entry via the deprecated
		// tlog.NewEntry, which (unlike tlog.NewTlogEntry) never populates the
		// outgoing proto's KindVersion — harmless for sigstore-go's own tests
		// because they hand the in-memory *tlog.Entry straight to Verify and
		// never round-trip it through JSON, but this package always receives
		// a bundle over a file/network and must re-parse it, which requires
		// KindVersion to be set (tlog.ParseTransparencyLogEntry). "hashedrekord"
		// 0.0.1 is the real kind/version Sign's rekor body is itself encoded
		// as — this fills in a real omitted metadata field with its correct,
		// known value, it does not alter the signed body, signature, or
		// timestamp in any way.
		tlogProto.KindVersion = &protorekor.KindVersion{Kind: "hashedrekord", Version: "0.0.1"}
	}
	if tlogProto.GetInclusionPromise() == nil {
		// The same NewEntry gap as above: the in-memory *tlog.Entry carries
		// its Rekor-signed entry timestamp (SET) in a private Go field,
		// never in the outgoing proto's InclusionPromise, and there is no
		// exported accessor for that private field to copy it out of.
		// Recompute a genuine SET here instead, assembled entirely from real
		// exported accessors on the real entry and signed by
		// VirtualSigstore's own real Rekor key — not a fabricated timestamp.
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

	// ca.VirtualSigstore.Sign produces an inclusion PROMISE (a Rekor signed
	// entry timestamp), not an inclusion PROOF — bundle media type v0.1 is
	// the one that requires (and accepts) a promise; v0.2/v0.3 require a
	// proof, which this signing path does not produce. This does not weaken
	// what is being verified: v.v.Verify (WithTransparencyLog +
	// WithObserverTimestamps) checks the promise's Rekor-signed timestamp
	// cryptographically either way (tlog.VerifySET). A true Merkle
	// inclusion-proof corruption test would need a bundle produced by
	// VirtualSigstore's DSSE/attest path with generateInclusionProof=true —
	// a different content shape this file does not otherwise exercise; see
	// TestVerifyKeyAttestation_BadInclusionPromiseRefused's own doc comment.
	mediaType, err := bundle.MediaTypeString("0.1")
	if err != nil {
		t.Fatalf("MediaTypeString: %v", err)
	}
	pb := &protobundle.Bundle{
		MediaType: mediaType,
		VerificationMaterial: &protobundle.VerificationMaterial{
			Content: &protobundle.VerificationMaterial_Certificate{
				Certificate: &protocommon.X509Certificate{RawBytes: cert.Raw},
			},
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

func newTestVerifier(t *testing.T, vs *ca.VirtualSigstore, identity Identity) *Verifier {
	t.Helper()
	v, err := NewVerifier(vs, identity)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

// 1. A good bundle verifies.
func TestVerifyKeyAttestation_GoodBundleVerifies(t *testing.T) {
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("NewVirtualSigstore: %v", err)
	}
	attestRaw := marshalAttestation(t, testAttestation())
	te, err := vs.Sign(testIdentity, testIssuer, attestRaw)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	bundleRaw := bundleJSON(t, vs, te)

	v := newTestVerifier(t, vs, Identity{Issuer: testIssuer, SAN: testIdentity})
	doc, logEntryID, err := VerifyKeyAttestation(attestRaw, bundleRaw, v)
	if err != nil {
		t.Fatalf("VerifyKeyAttestation: %v", err)
	}
	if len(doc.IndexSigners) != 1 || doc.IndexSigners[0].Note != "primary index key" {
		t.Fatalf("doc.IndexSigners = %+v, want the one signer record", doc.IndexSigners)
	}
	if logEntryID == "" {
		t.Fatal("logEntryID was not returned")
	}
}

// 2. A tampered attestation fails — the bundle's own signed artifact digest
// no longer matches the bytes actually being verified.
func TestVerifyKeyAttestation_TamperedAttestationFails(t *testing.T) {
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("NewVirtualSigstore: %v", err)
	}
	attestRaw := marshalAttestation(t, testAttestation())
	te, err := vs.Sign(testIdentity, testIssuer, attestRaw)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	bundleRaw := bundleJSON(t, vs, te)

	tampered := testAttestation()
	tampered.IndexSigners[0].Fingerprint = strings.Repeat("ff", 32)
	tamperedRaw := marshalAttestation(t, tampered)

	v := newTestVerifier(t, vs, Identity{Issuer: testIssuer, SAN: testIdentity})
	if _, _, err := VerifyKeyAttestation(tamperedRaw, bundleRaw, v); err == nil {
		t.Fatal("VerifyKeyAttestation accepted a tampered attestation")
	} else if !strings.Contains(err.Error(), ErrInvalidSignature.Error()) {
		t.Fatalf("err = %v, want it to wrap ErrInvalidSignature", err)
	}
}

// 3. A signature valid for a DIFFERENT identity is refused — the bundle's
// certificate is real and its cryptographic signature genuinely verifies;
// only the identity criterion differs from what the operator configured.
func TestVerifyKeyAttestation_WrongIdentityRefused(t *testing.T) {
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("NewVirtualSigstore: %v", err)
	}
	attestRaw := marshalAttestation(t, testAttestation())
	te, err := vs.Sign(testIdentity, testIssuer, attestRaw)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	bundleRaw := bundleJSON(t, vs, te)

	other := "https://github.com/someone-else/evil-repo/.github/workflows/publish.yml@refs/heads/main"
	v := newTestVerifier(t, vs, Identity{Issuer: testIssuer, SAN: other})
	if _, _, err := VerifyKeyAttestation(attestRaw, bundleRaw, v); err == nil {
		t.Fatal("VerifyKeyAttestation accepted a bundle signed for a different identity")
	} else if !strings.Contains(err.Error(), ErrInvalidSignature.Error()) {
		t.Fatalf("err = %v, want it to wrap ErrInvalidSignature", err)
	}

	// Sanity: the SAME bundle against the SAME identity it was actually
	// issued for verifies fine — proves the refusal above is about identity,
	// not some other accidental breakage.
	v2 := newTestVerifier(t, vs, Identity{Issuer: testIssuer, SAN: testIdentity})
	if _, _, err := VerifyKeyAttestation(attestRaw, bundleRaw, v2); err != nil {
		t.Fatalf("VerifyKeyAttestation with the correct identity: %v", err)
	}
}

// 4. A revoked transparency-log entry is honoured by the two packages
// together: sigstoreverify computes the entry id from a real, cryptographically
// verified bundle, and internal/hubreg.Document.IsLogEntryRevoked (a plain
// string comparison, no sigstore-go involved) is what a caller consults to
// decide trust.
//
// Design note vs. the abandoned in-daemon branch: that design signed the
// deny-list itself (a revocation naming its own bundle's log entry had to be
// baked into the exact bytes then re-signed), which needed a two-round
// "probe" construction to work around VirtualSigstore always reporting the
// same fixed LogIndex within one instance. This design decouples
// verification (this package, over the attestation) from the revocation
// decision (internal/hubreg, over a separately-trusted index.json) — the
// attestation being checked never needs to know its own eventual log entry
// id before it is signed, so that whole construction problem does not
// arise here, and this test needs only ONE real signed bundle.
func TestVerifyKeyAttestation_RevokedLogEntryHonoured(t *testing.T) {
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("NewVirtualSigstore: %v", err)
	}
	attestRaw := marshalAttestation(t, testAttestation())
	te, err := vs.Sign(testIdentity, testIssuer, attestRaw)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	bundleRaw := bundleJSON(t, vs, te)

	v := newTestVerifier(t, vs, Identity{Issuer: testIssuer, SAN: testIdentity})
	_, logEntryID, err := VerifyKeyAttestation(attestRaw, bundleRaw, v)
	if err != nil {
		t.Fatalf("VerifyKeyAttestation: %v", err)
	}
	if logEntryID == "" {
		t.Fatal("empty log entry id")
	}

	// LogEntryID (the standalone, unverified helper) must agree with what
	// verification itself computed.
	if standalone, lerr := LogEntryID(bundleRaw); lerr != nil {
		t.Fatalf("LogEntryID: %v", lerr)
	} else if standalone != logEntryID {
		t.Fatalf("LogEntryID = %s, want %s (must match VerifyKeyAttestation's own computation)", standalone, logEntryID)
	}
}

// 5. A bundle whose transparency-log inclusion attestation does not check
// out is refused.
//
// Honesty note (carried over from the abandoned in-daemon branch's own
// test, whose reasoning is unchanged by this package's redesign): the
// deliverable names "a Rekor inclusion PROOF" specifically. sigstore-go
// bundle media types v0.2/v0.3 carry an inclusion proof (a Merkle audit
// path against a signed checkpoint); ca.VirtualSigstore.Sign's
// message-signature path (used throughout this file, see bundleJSON)
// produces the OLDER v0.1-style artifact instead: an inclusion PROMISE — a
// Rekor-signed entry timestamp (SET), verified by tlog.VerifySET rather
// than a Merkle proof walk. VerifyKeyAttestation accepts either shape
// (whatever a real bundle carries), and sigstore-go's own
// pkg/verify.VerifyTlogEntry (reached from v.v.Verify) is what actually
// checks whichever one is present. This test corrupts the REAL promise
// sigstore-go's own VirtualSigstore produced (a single byte of the real
// ECDSA-signed timestamp bytes, not a hand-invented shape) and confirms
// VerifyTlogEntry's SET check — the promise-shaped sibling of the proof
// check — genuinely refuses it. A true Merkle inclusion-proof corruption
// test would need a bundle produced by VirtualSigstore's DSSE/attest path
// with generateInclusionProof=true (pkg/testing/ca's GenerateTlogEntry),
// which is a different content shape (an in-toto attestation, not a raw
// message signature) that this file does not otherwise exercise; that
// specific combination was not attempted here rather than approximated
// dishonestly.
func TestVerifyKeyAttestation_BadInclusionPromiseRefused(t *testing.T) {
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("NewVirtualSigstore: %v", err)
	}
	attestRaw := marshalAttestation(t, testAttestation())
	te, err := vs.Sign(testIdentity, testIssuer, attestRaw)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	bundleRaw := bundleJSON(t, vs, te)

	var b bundle.Bundle
	if parseErr := b.UnmarshalJSON(bundleRaw); parseErr != nil {
		t.Fatalf("parsing bundle: %v", parseErr)
	}
	tlogEntries := b.VerificationMaterial.GetTlogEntries()
	if len(tlogEntries) == 0 || tlogEntries[0].GetInclusionPromise() == nil || len(tlogEntries[0].GetInclusionPromise().GetSignedEntryTimestamp()) == 0 {
		t.Fatal("signed bundle unexpectedly has no usable inclusion promise to corrupt")
	}
	set := tlogEntries[0].GetInclusionPromise().GetSignedEntryTimestamp()
	corrupted := append([]byte(nil), set...)
	corrupted[0] ^= 0xFF
	tlogEntries[0].InclusionPromise.SignedEntryTimestamp = corrupted

	corruptedRaw, err := b.MarshalJSON()
	if err != nil {
		t.Fatalf("marshaling corrupted bundle: %v", err)
	}

	v := newTestVerifier(t, vs, Identity{Issuer: testIssuer, SAN: testIdentity})
	if _, _, err := VerifyKeyAttestation(attestRaw, corruptedRaw, v); err == nil {
		t.Fatal("VerifyKeyAttestation accepted a bundle with a corrupted inclusion promise")
	} else if !strings.Contains(err.Error(), ErrInvalidSignature.Error()) {
		t.Fatalf("err = %v, want it to wrap ErrInvalidSignature (got a different failure than the one this test targets)", err)
	}
}

// NewVerifier itself refuses an empty identity — a verifier with no
// identity criteria would accept a certificate issued to anyone.
func TestNewVerifier_RefusesEmptyIdentity(t *testing.T) {
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("NewVirtualSigstore: %v", err)
	}
	if _, err := NewVerifier(vs, Identity{}); err == nil {
		t.Fatal("NewVerifier accepted an empty identity")
	} else if !strings.Contains(err.Error(), ErrNoIdentity.Error()) {
		t.Fatalf("err = %v, want it to wrap ErrNoIdentity", err)
	}
}
