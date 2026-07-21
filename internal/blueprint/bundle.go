// bundle.go implements T-1107's signed blueprint sharing bundles
// (docs/features/blueprints.md §5): an envelope wrapping an existing
// Blueprint value plus an optional Ed25519 signature, so a blueprint
// authored on one vnprox installation can be exported, handed to another
// admin (file, gist, community repo, ...), and imported there with a clear
// signal of whether — and by whom — it was signed.
//
// This file is pure data shape + cryptography (no filesystem, no store, no
// HTTP): signingkey.go owns the on-disk Ed25519 identity a daemon signs
// with, trust.go owns the on-disk set of signers an installation has chosen
// to trust, and internal/api/blueprint_bundle.go is what composes all three
// into the documented GET .../bundle / POST /blueprints/import routes'
// trust-decision logic. Keeping that orchestration out of this package
// mirrors internal/blueprint's existing "Service is the only seam
// internal/api depends on" boundary (doc.go) without forcing every bundle
// concept through Service too.

package blueprint

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// CurrentBundleVersion is the only bundle wire-format version this package
// understands (mirrors CurrentBlueprintVersion's own doc comment: a future
// format change fails loudly instead of silently misreading an older or
// newer file).
const CurrentBundleVersion = 1

// SignatureAlgEd25519 is the only signature algorithm this package produces
// or verifies (docs/features/blueprints.md §5). A bundle whose
// Signature.Alg names anything else is treated as an invalid signature,
// never silently skipped.
const SignatureAlgEd25519 = "ed25519"

// BundleSignature carries an Ed25519 signature over a Bundle's Blueprint
// field, plus enough key material to verify it standalone.
//
// PublicKey is additive beyond the task card's literal envelope listing
// (`{alg, publicKeyFingerprint, sig}`) — flagged here per CLAUDE.md's "no
// unilateral decisions" rule, with the reasoning: PublicKeyFingerprint
// alone is a one-way SHA-256 digest, not key material, so a verifier that
// has never previously pinned this signer (the exact "untrusted/unknown
// signer" case AC3/AC5 exercise) would have no key to check the signature
// against at all — every real-world bundle-signing scheme this design
// mirrors (git commit signing, minisign, APT release files) ships the
// actual public key alongside the signature for this reason. The
// fingerprint is kept as its own field (rather than derived ad hoc by every
// caller) because it is the trust store's lookup key (trust.go) and the
// wire identifier a UI displays before key material is even parsed.
type BundleSignature struct {
	Alg                  string `json:"alg"`
	PublicKeyFingerprint string `json:"publicKeyFingerprint"`
	// PublicKey is the base64-standard-encoded raw 32-byte Ed25519 public
	// key that produced Sig — see the doc comment above for why this is
	// present despite not being in the task card's literal envelope shape.
	PublicKey string `json:"publicKey"`
	// Sig is the base64-standard-encoded Ed25519 signature itself, over
	// canonicalBlueprintBytes(bundle.Blueprint).
	Sig string `json:"sig"`
}

// Bundle is the sharable envelope `{bundleVersion, blueprint, signature?}`
// (docs/features/blueprints.md §5). Signature is nil for an unsigned bundle
// — POST /blueprints/import's documented default-reject-unless-trusted path
// for that case (docs/api.md's Blueprint bundles section).
//
//nolint:govet // fieldalignment: wire envelope; field order is the JSON shape, not packing.
type Bundle struct {
	BundleVersion int              `json:"bundleVersion"`
	Blueprint     Blueprint        `json:"blueprint"`
	Signature     *BundleSignature `json:"signature,omitempty"`
}

// Sentinel errors for this file — see errors.go's doc comment on the
// package's sentinel-error convention.
var (
	// ErrUnsupportedBundleVersion is returned when a parsed Bundle's
	// BundleVersion isn't CurrentBundleVersion.
	ErrUnsupportedBundleVersion = errors.New("blueprint: unsupported bundle version")
	// ErrInvalidSignature is returned by VerifyBundle when a Signature is
	// present but malformed (bad base64, wrong key/sig length, a
	// fingerprint that doesn't match the embedded public key) or simply
	// does not verify against the embedded public key (tampered content —
	// T-1107 AC5).
	ErrInvalidSignature = errors.New("blueprint: invalid signature")
)

// Fingerprint returns the hex-encoded SHA-256 digest of a raw Ed25519
// public key — the identifier this package's trust store (trust.go) and
// the wire signature both key signers by, so a signer can be named and
// compared without transmitting or re-deriving the full key every time.
func Fingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])
}

// canonicalBlueprintBytes returns the exact byte sequence signed/verified
// for bp: encoding/json's deterministic struct-field-order and
// sorted-map-key marshaling of the Blueprint value alone (never the
// enclosing Bundle envelope, so a signature stays independent of how the
// envelope itself happens to be serialized). Any semantic change to any
// field of bp changes these bytes — the property AC5 ("tampering bundle
// content after signing invalidates the signature") depends on — because
// Go's encoding/json sorts map[string]any keys and Blueprint's own field
// order is fixed by its struct definition, so two calls with equal values
// always produce identical bytes regardless of map-iteration-order
// randomization.
func canonicalBlueprintBytes(bp Blueprint) ([]byte, error) {
	b, err := json.Marshal(bp)
	if err != nil {
		return nil, fmt.Errorf("blueprint: encoding blueprint for signature: %w", err)
	}
	return b, nil
}

// SignBundle wraps bp in a Bundle and, when priv is non-nil, signs it with
// priv (Ed25519), embedding priv's public half and its fingerprint in the
// resulting Signature. A nil priv produces an unsigned bundle
// (Signature == nil) — GET /blueprints/{id}/bundle's `?sign=` query
// parameter maps directly to this nil-vs-non-nil choice.
func SignBundle(bp Blueprint, priv ed25519.PrivateKey) (Bundle, error) {
	bundle := Bundle{BundleVersion: CurrentBundleVersion, Blueprint: bp}
	if priv == nil {
		return bundle, nil
	}
	msg, err := canonicalBlueprintBytes(bp)
	if err != nil {
		return Bundle{}, err
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return Bundle{}, fmt.Errorf("blueprint: signing key has no Ed25519 public half")
	}
	sig := ed25519.Sign(priv, msg)
	bundle.Signature = &BundleSignature{
		Alg:                  SignatureAlgEd25519,
		PublicKeyFingerprint: Fingerprint(pub),
		PublicKey:            base64.StdEncoding.EncodeToString(pub),
		Sig:                  base64.StdEncoding.EncodeToString(sig),
	}
	return bundle, nil
}

// VerifyBundle checks b's embedded signature, if any, entirely against key
// material carried in the bundle itself (see BundleSignature's doc comment)
// — it does not consult any trust store, so a true/nil result here means
// only "this bundle's content matches what this claimed key signed," never
// "this installation trusts that key." internal/api/blueprint_bundle.go's
// import handler is what layers a TrustStore lookup on top of this result
// to decide whether an import proceeds without an explicit trust flag.
//
// Returns:
//   - verified=false, fingerprint="", err=nil        : b.Signature is nil (unsigned bundle)
//   - verified=false, fingerprint=<claimed>, err=ErrInvalidSignature : a
//     Signature is present but malformed, internally inconsistent (its
//     PublicKeyFingerprint doesn't match its own PublicKey), or the
//     signature bytes don't verify against that public key over
//     canonicalBlueprintBytes(b.Blueprint) — including the case where
//     b.Blueprint was edited after signing (AC5).
//   - verified=true, fingerprint=<signer>, err=nil   : the signature
//     verifies; the caller still must decide whether fingerprint is
//     trusted.
func VerifyBundle(b Bundle) (verified bool, fingerprint string, err error) {
	if b.Signature == nil {
		return false, "", nil
	}
	msg, err := canonicalBlueprintBytes(b.Blueprint)
	if err != nil {
		return false, b.Signature.PublicKeyFingerprint, err
	}
	return VerifySignature(b.Signature, msg)
}

// VerifySignature checks a BundleSignature entirely against key material
// carried in sig itself (its embedded PublicKey), over an arbitrary signed
// message msg — exactly the check VerifyBundle runs, factored out so any
// other signed artifact (T-1705's hub plugin manifests) verifies through
// this one Ed25519 path rather than reimplementing the crypto. Like
// VerifyBundle it consults no trust store: a true/nil result means only
// "msg matches what this claimed key signed", never "this installation
// trusts that key" — the caller layers a TrustStore lookup on top.
//
// Returns:
//   - verified=false, fingerprint=<claimed>, err=ErrInvalidSignature : sig
//     is malformed, internally inconsistent (its PublicKeyFingerprint does
//     not match its own PublicKey), or the signature bytes do not verify
//     against that public key over msg (including msg being tampered after
//     signing).
//   - verified=true, fingerprint=<signer>, err=nil : the signature
//     verifies; the caller still must decide whether fingerprint is trusted.
func VerifySignature(sig *BundleSignature, msg []byte) (verified bool, fingerprint string, err error) {
	if sig == nil {
		return false, "", fmt.Errorf("%w: nil signature", ErrInvalidSignature)
	}
	if sig.Alg != SignatureAlgEd25519 {
		return false, sig.PublicKeyFingerprint, fmt.Errorf("%w: unsupported alg %q", ErrInvalidSignature, sig.Alg)
	}
	pubBytes, err := base64.StdEncoding.DecodeString(sig.PublicKey)
	if err != nil || len(pubBytes) != ed25519.PublicKeySize {
		return false, sig.PublicKeyFingerprint, fmt.Errorf("%w: malformed public key", ErrInvalidSignature)
	}
	pub := ed25519.PublicKey(pubBytes)
	fp := Fingerprint(pub)
	if fp != sig.PublicKeyFingerprint {
		return false, sig.PublicKeyFingerprint, fmt.Errorf("%w: publicKeyFingerprint does not match embedded publicKey", ErrInvalidSignature)
	}
	sigBytes, err := base64.StdEncoding.DecodeString(sig.Sig)
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return false, fp, fmt.Errorf("%w: malformed signature", ErrInvalidSignature)
	}
	if !ed25519.Verify(pub, msg, sigBytes) {
		return false, fp, fmt.Errorf("%w: signature does not verify", ErrInvalidSignature)
	}
	return true, fp, nil
}
