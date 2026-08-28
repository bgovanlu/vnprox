// SPDX-License-Identifier: Apache-2.0

// Package sigstoreverify is T-3709's Sigstore-dependent verification code:
// full sigstore-go verification (Fulcio-issued, OIDC-bound certificate
// chain; Rekor transparency-log inclusion; a signed observer timestamp) of
// a small "key attestation" document over a keyless, publicly-auditable
// signing event.
//
// # Why this package exists, and who may import it
//
// An earlier version of this feature (preserved, not merged, on the
// `sigstore-in-daemon` branch — see that branch's own commit message)
// verified the registry's *entire* index.json this way, inside vnproxd
// itself. That was abandoned for one reason: sigstore-go's dependency
// weight. Pulling it in grows the module graph from 64 to roughly 400
// modules — a TUF client, a Certificate Transparency verifier, gRPC and
// OpenTelemetry all arrive transitively — and vnproxd is the **privileged
// daemon that controls host networking**. Growing its supply chain by 6x to
// verify a catalog file is not a trade the owner accepted.
//
// So the split is structural, not a naming convention: this package imports
// sigstore-go directly (pkg/bundle, pkg/root, pkg/verify) and is imported
// ONLY by cmd/vnproxctl (and its own tests). internal/hubreg — the sibling
// package one level up, which cmd/vnproxd DOES import — has no dependency
// on this package or on sigstore-go at all. `go list -deps ./cmd/vnproxd`
// must never contain "sigstore"; that is this split's entire acceptance
// test, and it is checked by cmd/vnproxd's own TestVnproxdDoesNotImportSigstore
// on every `make check`, not left to a reviewer to remember.
//
// # What this verifies, and what it does not
//
// This package does NOT verify the registry index (index.json) itself —
// that is still, unchanged, internal/hubreg's Ed25519 path
// (Sign/Verify/Gate), running inside vnproxd exactly as it did before
// T-3709. What this package verifies is a much smaller, much
// less-frequently-published document: a KeyAttestation, naming the Ed25519
// index-signing key fingerprint(s) a registry currently wants trusted. An
// operator runs `vnproxctl hub verify --sigstore-key-bundle` to check that
// attestation's Fulcio certificate chain, Rekor inclusion, and — the check
// a bare "the signature verifies" story would miss — that the signing
// certificate's IDENTITY (OIDC issuer + certificate SAN) matches what they
// configured, then copies the verified fingerprint(s) into their own
// `vnprox.toml`'s `[hub] index_signers` by hand. Sigstore governs KEY
// CUSTODY (which fingerprint is currently legitimate, attested via a
// public, keyless, Fulcio/Rekor-logged event instead of an unverifiable
// "trust me" side channel); Ed25519 remains the signature actually checked
// on every index fetch, unchanged, inside the daemon.
//
// # Say the honest cost out loud
//
// This is WEAKER than the abandoned in-daemon design, and it is worth
// being precise about why. In-daemon keyless verification checked EVERY
// fetched index against Fulcio/Rekor — there was never a persistent secret
// anywhere for an attacker to steal; forging a signature required forging
// a fresh Fulcio certificate from a live OIDC token, every single time.
// This design still has a long-lived Ed25519 private key that signs every
// ordinary index publish, exactly as it did before Sigstore entered the
// picture at all — Sigstore only attests, at rotation time, which
// fingerprint an operator should trust. If that Ed25519 private key is
// stolen between attestations, an attacker can forge index signatures
// indefinitely, and nothing in this package would ever see it happen: this
// package is never in the request path of an ordinary index fetch. What
// Sigstore buys instead is a materially better rotation/distribution
// story — "here is the new fingerprint" becomes a cryptographically
// checkable, Fulcio/Rekor-logged claim instead of release notes an operator
// has no way to verify — not a removal of key custody as a risk. See
// docs/hub-registry.md's "Sigstore-backed key custody" section and
// docs/security.md's hub section for the full account; do not describe
// this as equivalent to per-fetch keyless verification anywhere else in
// this repository.
//
// # The structural downgrade guarantee
//
// A served registry index can NEVER cause vnproxd to accept a different
// trust scheme than the one an operator configured, because vnproxd has no
// code path that can even parse a Sigstore bundle — internal/hubreg.Gate is
// the only verifier the daemon ever runs, full stop, and it is unchanged in
// behaviour from before T-3709. This is a stronger property than "two gate
// types, selected by config, neither able to reach the other's verifier"
// (the abandoned in-daemon design's own guarantee): there is exactly ONE
// gate type in the daemon, so there is no "other scheme" for a compromised
// or misconfigured registry to downgrade *to* even if it tried. The only
// thing a served index can ever do to the daemon's trust is what it could
// already do before T-3709: fail Ed25519 verification against whatever
// `[hub] index_signers` the operator most recently, explicitly configured.
package sigstoreverify
