package sigstoreverify

import "errors"

// Sentinel errors for this package (docs/development.md: "sentinel errors
// in each package's errors.go"). As with internal/hubreg, there is no
// partial-success mode: an attestation either verifies whole or yields
// nothing at all.
var (
	// ErrNoIdentity is returned when a Verifier is constructed with no
	// certificate identity criteria at all — a verifier with no identity
	// would accept a certificate issued to ANYONE who can obtain a Fulcio
	// certificate, which is not a trust decision.
	ErrNoIdentity = errors.New("sigstoreverify: verification requires an expected certificate identity (issuer and subject)")

	// ErrInvalidIdentity is returned when the configured issuer/SAN
	// exact-or-regexp values cannot be built into a sigstore-go certificate
	// identity matcher (e.g. an invalid regexp).
	ErrInvalidIdentity = errors.New("sigstoreverify: invalid certificate identity configuration")

	// ErrInvalidAttestation is returned when attestation bytes are not a
	// well-formed KeyAttestation document: malformed JSON, unknown fields,
	// trailing data, an oversized body, or no index signers listed.
	ErrInvalidAttestation = errors.New("sigstoreverify: invalid key attestation document")

	// ErrInvalidBundle is returned when a sigstore bundle is not well-formed
	// JSON in the sigstore-go bundle shape, is oversized, or carries no
	// transparency-log entry at all.
	ErrInvalidBundle = errors.New("sigstoreverify: invalid sigstore bundle")

	// ErrInvalidSignature is returned when a sigstore bundle fails
	// sigstore-go's own verification: the Fulcio certificate chain does not
	// validate, the Rekor transparency-log inclusion proof (or, for an
	// older-media-type bundle, inclusion promise) does not check out, the
	// bundle's signed artifact does not match the attestation bytes fetched
	// alongside it, or the certificate's identity (issuer/SAN) does not
	// match the configured Identity.
	ErrInvalidSignature = errors.New("sigstoreverify: sigstore bundle does not verify")
)
