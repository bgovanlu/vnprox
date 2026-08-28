// SPDX-License-Identifier: Apache-2.0

package sigstoreverify

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// CurrentAttestationSchema is the schema version this package produces and
// accepts.
const CurrentAttestationSchema = 1

// MaxAttestationBytes and MaxBundleBytes bound the two documents this
// package reads: a key-custody attestation naming a handful of Ed25519
// fingerprints, and its sibling Sigstore bundle (a Fulcio cert chain plus
// one Rekor entry) — both are a few KB at most; this is generous headroom,
// matching internal/hubreg.MaxIndexBytes' own posture.
const (
	MaxAttestationBytes = 1 << 20
	MaxBundleBytes      = 1 << 20
)

// SignerRecord is one Ed25519 index-signing key a KeyAttestation vouches
// for.
type SignerRecord struct {
	Fingerprint string `json:"fingerprint"`
	PublicKey   string `json:"publicKey,omitempty"`
	Note        string `json:"note,omitempty"`
}

// KeyAttestation is the small, infrequently-published document a registry's
// Sigstore-authenticated CI signs (typically only at key rotation, not on
// every ordinary index publish): a claim about which Ed25519 fingerprint(s)
// currently hold the registry's index-signing key custody. It is
// deliberately a different, much narrower document than
// internal/hubreg.Document (index.json) — nothing in this package ever
// parses or produces an index.json, so there is no wire-shape ambiguity for
// a served document to exploit.
//
//nolint:govet // fieldalignment: wire envelope; field order is the JSON shape.
type KeyAttestation struct {
	SchemaVersion int            `json:"schemaVersion"`
	GeneratedAt   int64          `json:"generatedAt,omitempty"`
	RegistryURL   string         `json:"registryUrl,omitempty"`
	IndexSigners  []SignerRecord `json:"indexSigners"`
}

// Verifier wraps a configured sigstore-go verifier plus the one expected
// certificate identity a key attestation must be signed by. Safe for
// concurrent use (sigstore-go's Verifier holds no mutable state per call).
type Verifier struct {
	v        *verify.Verifier
	identity verify.CertificateIdentity
}

// NewVerifier builds a Verifier from trustedMaterial (the Fulcio/Rekor/CT
// trust roots — root.FetchTrustedRoot() for the public-good instance, or
// root.NewTrustedRootFromPath for a pinned/offline copy — see
// LoadTrustedRoot) and the one identity a signature must carry.
// identity.Empty() is refused (ErrNoIdentity): a verifier with no identity
// criteria would accept a certificate issued to anyone who can mint a
// Fulcio cert, which is not a trust decision at all.
func NewVerifier(trustedMaterial root.TrustedMaterial, identity Identity) (*Verifier, error) {
	if identity.Empty() {
		return nil, ErrNoIdentity
	}
	ci, err := identity.certificateIdentity()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidIdentity, err)
	}
	v, err := verify.NewVerifier(trustedMaterial, verify.WithTransparencyLog(1), verify.WithObserverTimestamps(1))
	if err != nil {
		return nil, fmt.Errorf("sigstoreverify: constructing verifier: %w", err)
	}
	return &Verifier{v: v, identity: ci}, nil
}

// VerifyKeyAttestation parses attestationRaw and bundleRaw and fully
// verifies the attestation against v, returning the document and the
// bundle's own transparency-log entry id only if every check passes.
//
// The checks, in order:
//
//  1. attestationRaw strictly parses as a KeyAttestation (unknown fields,
//     trailing data and oversized bodies rejected) and names at least one
//     index signer;
//  2. bundleRaw parses as a sigstore-go bundle;
//  3. the bundle's Fulcio certificate chain, Rekor transparency-log
//     inclusion (proof or, for an older-media-type bundle, inclusion
//     promise — sigstore-go's own verify accepts either, and checks
//     whichever the bundle carries), and observer timestamp all verify
//     against v's trusted material;
//  4. the signing certificate's identity (issuer + SAN) matches v's
//     configured identity — a signature that verifies cryptographically but
//     was issued to a different identity is refused here;
//  5. the bundle's own signed artifact is exactly attestationRaw's bytes
//     (checked by the same call as 3/4 — sigstore-go's policy is
//     "verify + identity + artifact" as one operation);
//  6. the schema version is CurrentAttestationSchema.
//
// This function does NOT consult any revocation deny-list — that decision
// belongs to the caller, against whatever index.json it separately trusts
// (internal/hubreg.Document.IsLogEntryRevoked), keyed by the same log entry
// id this function returns. Decoupling the two removes any need for the
// attestation-signing step to already know its own eventual log index
// before producing bytes to sign, unlike a scheme that revokes by denying
// the signed document itself.
func VerifyKeyAttestation(attestationRaw, bundleRaw []byte, v *Verifier) (KeyAttestation, string, error) {
	if v == nil {
		return KeyAttestation{}, "", fmt.Errorf("sigstoreverify: no verifier configured")
	}
	if len(attestationRaw) > MaxAttestationBytes {
		return KeyAttestation{}, "", fmt.Errorf("%w: attestation is larger than %d bytes", ErrInvalidAttestation, MaxAttestationBytes)
	}
	if len(bundleRaw) > MaxBundleBytes {
		return KeyAttestation{}, "", fmt.Errorf("%w: bundle is larger than %d bytes", ErrInvalidBundle, MaxBundleBytes)
	}

	dec := json.NewDecoder(bytes.NewReader(attestationRaw))
	dec.DisallowUnknownFields()
	var a KeyAttestation
	if err := dec.Decode(&a); err != nil {
		return KeyAttestation{}, "", fmt.Errorf("%w: %w", ErrInvalidAttestation, err)
	}
	if dec.More() {
		return KeyAttestation{}, "", fmt.Errorf("%w: trailing data after the attestation document", ErrInvalidAttestation)
	}
	if len(a.IndexSigners) == 0 {
		return KeyAttestation{}, "", fmt.Errorf("%w: attestation names no index signers", ErrInvalidAttestation)
	}
	for _, s := range a.IndexSigners {
		if s.Fingerprint == "" {
			return KeyAttestation{}, "", fmt.Errorf("%w: an index signer entry has no fingerprint", ErrInvalidAttestation)
		}
	}

	var b bundle.Bundle
	if err := b.UnmarshalJSON(bundleRaw); err != nil {
		return KeyAttestation{}, "", fmt.Errorf("%w: parsing sigstore bundle: %w", ErrInvalidBundle, err)
	}

	policy := verify.NewPolicy(verify.WithArtifact(bytes.NewReader(attestationRaw)), verify.WithCertificateIdentity(v.identity))
	if _, verr := v.v.Verify(&b, policy); verr != nil {
		return KeyAttestation{}, "", fmt.Errorf("%w: %w", ErrInvalidSignature, verr)
	}

	entryID, ok := entryIDOf(&b)
	if !ok {
		return KeyAttestation{}, "", fmt.Errorf("%w: bundle carries no transparency-log entry", ErrInvalidBundle)
	}

	if a.SchemaVersion != CurrentAttestationSchema {
		return KeyAttestation{}, "", fmt.Errorf("%w: got schema %d, want %d", ErrInvalidAttestation, a.SchemaVersion, CurrentAttestationSchema)
	}
	return a, entryID, nil
}

// LogEntryID parses bundleRaw and returns its transparency-log entry id in
// the exact form internal/hubreg.Revocation.TransparencyLogIndex and
// `vnproxctl hub revoke --log-entry` expect — the same value
// VerifyKeyAttestation itself computes internally, exported so an operator
// deciding to revoke an attestation they no longer trust can print the id
// without re-running the full verification. It does NOT verify the bundle
// cryptographically; pair it with VerifyKeyAttestation (or only ever act on
// a bundle you already verified) before treating its return value as
// meaningful.
func LogEntryID(bundleRaw []byte) (string, error) {
	var b bundle.Bundle
	if err := b.UnmarshalJSON(bundleRaw); err != nil {
		return "", fmt.Errorf("%w: parsing sigstore bundle: %w", ErrInvalidBundle, err)
	}
	id, ok := entryIDOf(&b)
	if !ok {
		return "", fmt.Errorf("%w: bundle carries no transparency-log entry", ErrInvalidBundle)
	}
	return id, nil
}

// entryIDOf returns a stable identifier for the bundle's (first)
// transparency-log entry: the log's own key id (hex-encoded — LogKeyID()
// returns the raw digest bytes, which are not valid-UTF8-safe and would be
// silently mangled by encoding/json if embedded un-encoded in a JSON string
// field, breaking round-tripping through
// internal/hubreg.Revocation.TransparencyLogIndex) and that entry's index
// within it, joined. This is NOT necessarily Rekor's canonical entry UUID
// wire format, only a value stable and unique enough to name one logged
// signing event for `vnproxctl hub revoke --log-entry` and
// internal/hubreg.Document.IsLogEntryRevoked to agree on.
func entryIDOf(b *bundle.Bundle) (string, bool) {
	entries, err := b.TlogEntries()
	if err != nil || len(entries) == 0 {
		return "", false
	}
	e := entries[0]
	return fmt.Sprintf("%s:%d", hex.EncodeToString([]byte(e.LogKeyID())), e.LogIndex()), true
}
