// sigstore.go is T-3709's second index-trust scheme: full sigstore-go
// verification of the registry index over Fulcio-issued, OIDC-bound
// certificates and Rekor transparency-log inclusion, instead of a
// long-lived Ed25519 index-signing key (index.go / gate.go).
//
// Shape, deliberately parallel to the Ed25519 path rather than replacing it
// (docs/hub-registry.md's "Sigstore-signed registries" section has the full
// operator account):
//
//   - The signed document is still exactly index.go's Document — the same
//     entries, the same Revocations, the same JSON shape a client decodes.
//     What differs is how the *whole byte sequence of index.json* is
//     attested: instead of an embedded `signature` field, a sibling file
//     (SigstoreBundleName, next to index.json on the same static host) is a
//     sigstore-go bundle — Fulcio cert chain, message signature, and a Rekor
//     transparency-log entry with its inclusion proof — over those exact
//     bytes. A Document carrying an embedded `signature` field is refused in
//     this mode (VerifySigstore) rather than silently accepted: which scheme
//     is in force is operator configuration ([hub] sig_mode), never inferred
//     from what a served index happens to contain, so a compromised or
//     misconfigured server cannot downgrade a Sigstore-pinned installation to
//     Ed25519 by serving an Ed25519-shaped index instead (see gate.go's
//     SigstoreGate, which never calls the Ed25519 Verify at all).
//   - Certificate IDENTITY is the trust anchor an operator configures
//     (SigstoreIdentity): the OIDC issuer plus a certificate SAN (typically a
//     GitHub Actions workflow ref), each as an exact string or a regexp,
//     mirroring how index_signers pins Ed25519 fingerprints. A bundle that
//     verifies cryptographically but was issued to any other identity is
//     refused — this is the check a bare "the signature verifies" story
//     would miss, and it is why T-3709 calls identity matching out
//     explicitly rather than treating "verifies" as the whole story.
//   - Revocation under keyless signing has no key to rotate, so it revokes a
//     specific *transparency-log entry* instead of a signer fingerprint: a
//     Document.Revocations record with TransparencyLogIndex set (index.go)
//     names one signing EVENT rather than one artifact or one signer, and
//     rides inside the same Sigstore-signed index.json a client already
//     fetched — the same "no second network call, no OCSP-style live check"
//     property Ed25519 revocation has (see IsLogEntryRevoked, index.go). This
//     is what lets a client that cached (or was served) an
//     old-but-still-cryptographically-valid index+bundle pair learn it is no
//     longer trusted, once the CURRENT index republishes with that old log
//     entry denied — see the package doc comment (doc.go) for the residual
//     this does not close: a replay of the OLD index+bundle pair together,
//     never fetching the new one at all, is a rollback attack no purely
//     static, no-live-lookup scheme fully closes. The Ed25519 scheme has the
//     identical residual for the identical reason, so this is not a
//     regression, but it is worth an operator knowing about (docs/security.md).

package hubreg

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// SigstoreBundleName is the sibling file a Sigstore-signed index.json is
// published next to, on the same static host and under the same path
// prefix. SigstoreGate (gate.go) derives it from the index request URL; it
// is never fetched from a different origin.
const SigstoreBundleName = "index.json.sigstore.json"

// MaxSigstoreBundleBytes bounds the sibling bundle document SigstoreGate
// will read from the network — a Fulcio cert chain plus one Rekor entry is a
// few KB; this is generous headroom, matching MaxIndexBytes' own posture.
const MaxSigstoreBundleBytes = 1 << 20

// SigstoreIdentity is the expected Fulcio certificate identity a
// Sigstore-signed index must carry — the trust anchor an operator
// configures ([hub] sigstore_issuer / sigstore_identity*, docs/hub-registry.md),
// never inferred from the certificate itself. Issuer and SAN each accept an
// exact value, a regexp, or both; sigstore-go's own NewShortCertificateIdentity
// requires at least one of the two forms for each.
//
//nolint:govet // fieldalignment: small config value, field order is documentation order.
type SigstoreIdentity struct {
	Issuer       string
	IssuerRegexp string
	SAN          string
	SANRegexp    string
}

// Empty reports whether no identity criteria were configured at all — the
// zero value, which must never be treated as "match anything."
func (i SigstoreIdentity) Empty() bool {
	return i.Issuer == "" && i.IssuerRegexp == "" && i.SAN == "" && i.SANRegexp == ""
}

func (i SigstoreIdentity) certificateIdentity() (verify.CertificateIdentity, error) {
	return verify.NewShortCertificateIdentity(i.Issuer, i.IssuerRegexp, i.SAN, i.SANRegexp)
}

// SigstoreVerifier wraps a configured sigstore-go verifier plus the one
// expected certificate identity a registry index must be signed by. Safe for
// concurrent use (sigstore-go's Verifier holds no mutable state per call).
type SigstoreVerifier struct {
	v        *verify.Verifier
	identity verify.CertificateIdentity
}

// NewSigstoreVerifier builds a SigstoreVerifier from trustedMaterial (the
// Fulcio/Rekor/CT trust roots — root.FetchTrustedRoot() for the public-good
// instance, or root.NewTrustedRootFromPath for a pinned/offline copy) and the
// one identity a signature must carry. identity.Empty() is refused: a
// Sigstore verifier with no identity criteria would accept a certificate
// issued to *anyone* who can mint a Fulcio cert, which is not a trust
// decision at all.
func NewSigstoreVerifier(trustedMaterial root.TrustedMaterial, identity SigstoreIdentity) (*SigstoreVerifier, error) {
	if identity.Empty() {
		return nil, fmt.Errorf("hubreg: sigstore verification requires an expected certificate identity (issuer and subject)")
	}
	ci, err := identity.certificateIdentity()
	if err != nil {
		return nil, fmt.Errorf("hubreg: invalid sigstore identity: %w", err)
	}
	v, err := verify.NewVerifier(trustedMaterial, verify.WithTransparencyLog(1), verify.WithObserverTimestamps(1))
	if err != nil {
		return nil, fmt.Errorf("hubreg: constructing sigstore verifier: %w", err)
	}
	return &SigstoreVerifier{v: v, identity: ci}, nil
}

// VerifySigstore parses indexRaw and bundleRaw and fully verifies the index
// against sv, returning the document only if every check passes — the same
// "verifies whole or yields nothing" contract Verify (Ed25519) makes.
//
// The checks, in order:
//
//  1. indexRaw strictly parses as a Document (unknown fields, trailing data
//     and oversized bodies rejected — the same posture as Verify);
//  2. the document must carry NO embedded Ed25519 `signature` — an index
//     shaped for the other scheme is refused, not silently accepted, so a
//     server cannot downgrade a Sigstore-pinned installation (the mode
//     itself is chosen by the caller's own configuration, never by what the
//     server serves — see gate.go's SigstoreGate, which is wired in place of
//     Gate rather than alongside it);
//  3. bundleRaw parses as a sigstore-go bundle;
//  4. the bundle's Fulcio certificate chain, Rekor transparency-log
//     inclusion proof, and observer timestamp all verify against sv's
//     trusted material;
//  5. the signing certificate's identity (issuer + SAN) matches sv's
//     configured identity — a signature that verifies cryptographically but
//     was issued to a different identity is refused here;
//  6. the bundle's own signed artifact is exactly indexRaw's bytes (checked
//     by the same call as 4/5 — sigstore-go's policy is "verify + identity +
//     artifact" as one operation);
//  7. the bundle's transparency-log entry is not named in the document's own
//     revocation deny-list (IsLogEntryRevoked) — decided entirely from the
//     document already in hand, no second network call;
//  8. the schema version and document structure are valid (Validate).
func VerifySigstore(indexRaw, bundleRaw []byte, sv *SigstoreVerifier) (Document, error) {
	if sv == nil {
		return Document{}, fmt.Errorf("%w: no sigstore verifier configured", ErrInvalidSigstoreConfig)
	}
	if len(indexRaw) > MaxIndexBytes {
		return Document{}, fmt.Errorf("%w: index is larger than %d bytes", ErrInvalidIndex, MaxIndexBytes)
	}
	if len(bundleRaw) > MaxSigstoreBundleBytes {
		return Document{}, fmt.Errorf("%w: bundle is larger than %d bytes", ErrInvalidSigstoreBundle, MaxSigstoreBundleBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(indexRaw))
	dec.DisallowUnknownFields()
	var d Document
	if err := dec.Decode(&d); err != nil {
		return Document{}, fmt.Errorf("%w: %w", ErrInvalidIndex, err)
	}
	if dec.More() {
		return Document{}, fmt.Errorf("%w: trailing data after the index document", ErrInvalidIndex)
	}
	if d.Signature != nil {
		return Document{}, fmt.Errorf("%w: index carries an Ed25519 signature block, but this installation is configured for sig_mode = \"sigstore\"", ErrUnexpectedSignatureMode)
	}

	var b bundle.Bundle
	if err := b.UnmarshalJSON(bundleRaw); err != nil {
		return Document{}, fmt.Errorf("%w: parsing sigstore bundle: %w", ErrInvalidSigstoreBundle, err)
	}

	policy := verify.NewPolicy(verify.WithArtifact(bytes.NewReader(indexRaw)), verify.WithCertificateIdentity(sv.identity))
	if _, verr := sv.v.Verify(&b, policy); verr != nil {
		return Document{}, fmt.Errorf("%w: %w", ErrInvalidSigstoreSignature, verr)
	}

	logEntryID, ok := sigstoreLogEntryID(&b)
	if !ok {
		return Document{}, fmt.Errorf("%w: bundle carries no transparency-log entry", ErrInvalidSigstoreBundle)
	}
	if rev, revoked := d.IsLogEntryRevoked(logEntryID); revoked {
		return Document{}, fmt.Errorf("%w: transparency log entry %s: %s", ErrRevokedSigstoreEntry, logEntryID, rev.Reason)
	}

	if d.SchemaVersion != CurrentIndexSchema {
		return Document{}, fmt.Errorf("%w: got %d, want %d", ErrUnsupportedSchema, d.SchemaVersion, CurrentIndexSchema)
	}
	if err := Validate(d); err != nil {
		return Document{}, err
	}
	return d, nil
}

// SigstoreLogEntryID parses bundleRaw (the SigstoreBundleName wire bytes)
// and returns its transparency-log entry id in the exact form
// Revocation.TransparencyLogIndex/`vnproxctl hub revoke --log-entry` expect
// — the same value VerifySigstore itself computes internally, exported so
// `vnproxctl hub verify` can print it for an operator to revoke by. It does
// NOT verify the bundle cryptographically; pair it with Verify/VerifySigstore
// (or trust only a bundle you already verified) before acting on the id it
// returns.
func SigstoreLogEntryID(bundleRaw []byte) (string, error) {
	var b bundle.Bundle
	if err := b.UnmarshalJSON(bundleRaw); err != nil {
		return "", fmt.Errorf("%w: parsing sigstore bundle: %w", ErrInvalidSigstoreBundle, err)
	}
	id, ok := sigstoreLogEntryID(&b)
	if !ok {
		return "", fmt.Errorf("%w: bundle carries no transparency-log entry", ErrInvalidSigstoreBundle)
	}
	return id, nil
}

// sigstoreLogEntryID returns a stable identifier for the bundle's (first)
// transparency-log entry: the log's own key id (hex-encoded — LogKeyID()
// returns the raw digest bytes, which are not valid-UTF8-safe and would be
// silently mangled by encoding/json if embedded in a JSON string field
// un-encoded, breaking round-tripping through Revocation.TransparencyLogIndex)
// and that entry's index within it, joined. This is this package's own
// deny-list key (index.go's Revocation.TransparencyLogIndex) — it is NOT
// necessarily Rekor's canonical entry UUID wire format, only a value stable
// and unique enough to name one logged signing event for `vnproxctl hub
// revoke --log-entry` and IsLogEntryRevoked to agree on.
func sigstoreLogEntryID(b *bundle.Bundle) (string, bool) {
	entries, err := b.TlogEntries()
	if err != nil || len(entries) == 0 {
		return "", false
	}
	e := entries[0]
	return fmt.Sprintf("%s:%d", hex.EncodeToString([]byte(e.LogKeyID())), e.LogIndex()), true
}
