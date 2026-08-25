package hubreg

import "errors"

// Sentinel errors for this package (docs/development.md: "sentinel errors in
// each package's errors.go"). Every one of them is a *refusal*: this package
// has no partial-success mode — an index either verifies whole or yields
// nothing at all.
var (
	// ErrInvalidIndex is returned when index bytes are not a well-formed
	// index document: malformed JSON, unknown fields, trailing data, an
	// oversized body, a duplicate (type,id,version), an entry missing a
	// required field, or an artifact URL that is neither an absolute path nor
	// an absolute http(s) URL.
	ErrInvalidIndex = errors.New("hubreg: invalid registry index")

	// ErrUnsignedIndex is returned when a fetched index carries no signature
	// at all. An unsigned index is refused rather than downgraded to "just
	// don't check": revocation lives inside the signature's coverage, so an
	// unsigned index is exactly the document an attacker would serve to strip
	// a revocation.
	ErrUnsignedIndex = errors.New("hubreg: registry index is not signed")

	// ErrInvalidIndexSignature is returned when an index's signature is
	// malformed or does not verify over the index's own canonical bytes —
	// i.e. the index was corrupted or tampered with after signing.
	ErrInvalidIndexSignature = errors.New("hubreg: registry index signature does not verify")

	// ErrUntrustedIndexSigner is returned when an index's signature verifies
	// but the signing key is not one of the fingerprints the operator
	// configured ([hub] index_signers). An empty trusted set refuses
	// everything — this gate fails closed, never open.
	ErrUntrustedIndexSigner = errors.New("hubreg: registry index is signed by an untrusted key")

	// ErrUnsupportedSchema is returned when an index's schemaVersion is not
	// CurrentIndexSchema (mirrors hub.ErrUnsupportedSchema's own convention:
	// a newer index fails loudly rather than being silently misread).
	ErrUnsupportedSchema = errors.New("hubreg: unsupported registry index schema version")

	// ErrRevoked is returned by Gate when a fetch targets an artifact the
	// signed index revokes. Decided entirely from the already-fetched index —
	// no network call is made to reach this verdict.
	ErrRevoked = errors.New("hubreg: artifact is revoked by the registry index")

	// ErrUnlistedArtifact is returned by Gate when a fetch targets a URL that
	// no entry in the verified index names. The gate is an allowlist: an
	// artifact the signed catalog does not list is never downloaded.
	ErrUnlistedArtifact = errors.New("hubreg: artifact URL is not listed in the registry index")

	// ErrNoVerifiedIndex is returned by Gate when an artifact fetch is
	// attempted before any index has been fetched and verified. Fail-closed:
	// without a verified catalog there is no revocation state to consult, so
	// nothing is downloaded.
	ErrNoVerifiedIndex = errors.New("hubreg: no verified registry index has been fetched")

	// ErrConflict is returned when publishing would change an already-indexed
	// (type,id,version). A published version is immutable: republishing the
	// identical artifact is a no-op (AC4), republishing *different* content
	// under the same version is refused rather than silently overwritten.
	ErrConflict = errors.New("hubreg: a different artifact is already indexed at this type/id/version")

	// ErrInvalidSubmission is returned when a submission is malformed, names
	// an artifact whose identity disagrees with its entry, or carries a
	// signature that does not verify.
	ErrInvalidSubmission = errors.New("hubreg: invalid submission")

	// ErrInvalidSigstoreConfig is returned when Sigstore verification is
	// invoked with no verifier configured (a programming error at the
	// caller, not something a served document could trigger).
	ErrInvalidSigstoreConfig = errors.New("hubreg: sigstore verification is not configured")

	// ErrInvalidSigstoreBundle is returned when a fetched sigstore bundle
	// (SigstoreBundleName) is not well-formed JSON in the sigstore-go bundle
	// shape, is oversized, or carries no transparency-log entry at all.
	ErrInvalidSigstoreBundle = errors.New("hubreg: invalid sigstore bundle")

	// ErrInvalidSigstoreSignature is returned when a sigstore bundle fails
	// sigstore-go's own verification: the Fulcio certificate chain does not
	// validate, the Rekor transparency-log inclusion proof does not check
	// out, the bundle's signed artifact does not match the index bytes
	// fetched alongside it, or the certificate's identity (issuer/SAN) does
	// not match the operator's configured SigstoreIdentity.
	ErrInvalidSigstoreSignature = errors.New("hubreg: sigstore bundle does not verify")

	// ErrUnexpectedSignatureMode is returned when a served index document's
	// shape does not match the signature scheme this installation is
	// configured for ([hub] sig_mode) — e.g. an Ed25519 `signature` field
	// present in an index fetched under sig_mode = "sigstore". Refused
	// outright rather than silently read under the other scheme: which
	// scheme verifies is operator configuration, never inferred from what a
	// server happens to serve, which is what keeps a compromised or
	// misconfigured server from downgrading a Sigstore-pinned installation
	// to the (weaker, long-lived-key) Ed25519 path.
	ErrUnexpectedSignatureMode = errors.New("hubreg: index is not shaped for the configured signature mode")

	// ErrRevokedSigstoreEntry is returned by VerifySigstore when a bundle
	// cryptographically verifies but its own transparency-log entry is named
	// in the index document's revocation deny-list
	// (Revocation.TransparencyLogIndex) — T-3709's answer to "there is no key
	// to rotate": the client learns a signature it can still verify is no
	// longer trusted from the same signed document it already fetched, no
	// second network call.
	ErrRevokedSigstoreEntry = errors.New("hubreg: sigstore transparency-log entry is revoked by the registry index")
)
