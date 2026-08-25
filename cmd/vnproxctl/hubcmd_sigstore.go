// hubcmd_sigstore.go is `vnproxctl hub verify --sigstore-key-bundle`'s
// implementation: the one place in this binary — and, by design, the one
// place in this repository outside cmd/vnproxctl's own dependency tree and
// internal/hubreg/sigstoreverify — that imports sigstore-go. hubcmd.go's
// runHubVerify dispatches here by flag alone (--sigstore-key-bundle set);
// every other `hub` subcommand is untouched by this file and carries no
// sigstore-go import of its own.
//
// See internal/hubreg/sigstoreverify's package doc for the full account of
// what this verifies (a key-custody attestation, not index.json itself),
// what the daemon still does (unchanged Ed25519 verification of every
// index fetch), and what this design costs relative to the abandoned
// in-daemon design it replaces.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/bgovanlu/vnprox/internal/hubreg"
	"github.com/bgovanlu/vnprox/internal/hubreg/sigstoreverify"
)

// hubVerifySigstoreKeyArgs is runHubVerify's sigstore-mode flags, passed
// through as plain strings so hubcmd.go's flag declarations do not
// themselves need a sigstoreverify import.
type hubVerifySigstoreKeyArgs struct {
	attestationPath     string
	bundlePath          string
	issuer              string
	issuerRegexp        string
	identity            string
	identityRegexp      string
	trustedRootPath     string
	checkRevokedAgainst string
}

// runHubVerifySigstoreKey verifies a Sigstore-signed key-custody attestation
// (Fulcio chain, Rekor inclusion, certificate identity) and prints the
// Ed25519 index-signer fingerprint(s) it vouches for, plus its own
// transparency-log entry id. It never writes daemon config: pinning the
// verified fingerprint(s) into an installation's [hub] index_signers is a
// separate, explicit operator step (docs/hub-registry.md's "Sigstore-backed
// key custody" section) — this command's job ends at "here is what verified
// and here is what it said," the same boundary `hub verify --signers`
// already draws for the Ed25519 path.
func runHubVerifySigstoreKey(args hubVerifySigstoreKeyArgs, stdout, stderr io.Writer, jsonOut bool) int {
	if args.bundlePath == "" {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub verify: --sigstore-key-bundle requires --sigstore-bundle\n")
		return ExitUsage
	}
	if (args.issuer == "" && args.issuerRegexp == "") || (args.identity == "" && args.identityRegexp == "") {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub verify: --sigstore-key-bundle needs --sigstore-issuer(-regexp) and --sigstore-identity(-regexp)\n")
		return ExitUsage
	}

	attestRaw, err := os.ReadFile(args.attestationPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub verify: reading %s: %v\n", args.attestationPath, err)
		return ExitError
	}
	bundleRaw, err := os.ReadFile(args.bundlePath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub verify: reading %s: %v\n", args.bundlePath, err)
		return ExitError
	}

	trustedMaterial, err := sigstoreverify.LoadTrustedRoot(args.trustedRootPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub verify: %v\n", err)
		return ExitError
	}
	verifier, err := sigstoreverify.NewVerifier(trustedMaterial, sigstoreverify.Identity{
		Issuer: args.issuer, IssuerRegexp: args.issuerRegexp,
		SAN: args.identity, SANRegexp: args.identityRegexp,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub verify: %v\n", err)
		return ExitError
	}
	attestation, logEntryID, err := sigstoreverify.VerifyKeyAttestation(attestRaw, bundleRaw, verifier)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub verify: %v\n", err)
		return ExitError
	}

	var revokedReason string
	if args.checkRevokedAgainst != "" {
		idxRaw, rerr := os.ReadFile(args.checkRevokedAgainst)
		if rerr != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl hub verify: reading %s: %v\n", args.checkRevokedAgainst, rerr)
			return ExitError
		}
		var idxDoc hubreg.Document
		if derr := json.Unmarshal(idxRaw, &idxDoc); derr != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl hub verify: parsing %s: %v\n", args.checkRevokedAgainst, derr)
			return ExitError
		}
		if rev, revoked := idxDoc.IsLogEntryRevoked(logEntryID); revoked {
			revokedReason = rev.Reason
		}
	}
	if revokedReason != "" {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub verify: transparency-log entry %s is revoked: %s\n", logEntryID, revokedReason)
		return ExitError
	}

	if jsonOut {
		signers := make([]map[string]any, 0, len(attestation.IndexSigners))
		for _, s := range attestation.IndexSigners {
			signers = append(signers, map[string]any{"fingerprint": s.Fingerprint, "note": s.Note})
		}
		if err := writeJSONOut(stdout, map[string]any{
			"transparencyLogEntry": logEntryID,
			"registryUrl":          attestation.RegistryURL,
			"generatedAt":          attestation.GeneratedAt,
			"indexSigners":         signers,
		}); err != nil {
			return ExitError
		}
		return ExitSuccess
	}

	_, _ = fmt.Fprintf(stdout, "OK: key-custody attestation verified (keyless; transparency-log entry %s)\n", logEntryID)
	if attestation.RegistryURL != "" {
		_, _ = fmt.Fprintf(stdout, "  registry: %s\n", attestation.RegistryURL)
	}
	_, _ = fmt.Fprintf(stdout, "  index signers now attested:\n")
	for _, s := range attestation.IndexSigners {
		if s.Note != "" {
			_, _ = fmt.Fprintf(stdout, "    %s  (%s)\n", s.Fingerprint, s.Note)
		} else {
			_, _ = fmt.Fprintf(stdout, "    %s\n", s.Fingerprint)
		}
	}
	_, _ = fmt.Fprintf(stdout, "Add these to [hub] index_signers in vnprox.toml to complete the pin — vnproxctl does not write daemon config for you.\n")
	return ExitSuccess
}
