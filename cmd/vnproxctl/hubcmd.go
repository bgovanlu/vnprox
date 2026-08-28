// SPDX-License-Identifier: Apache-2.0

// hubcmd.go implements `vnproxctl hub` (T-2803): the publisher and registry
// tooling for the hosted signed registry internal/hub browses.
//
// It joins the *daemon-independent* command family (`backup`, `doctor`,
// `support-bundle`, ...): every subcommand is pure local file work — read an
// artifact, sign it, fold it into a JSON index, write it back. No daemon, no
// network, no service. That is the whole point of T-2102's static-hosting
// posture carried over to the registry: publishing is `git push` to a
// repository whose CI copies a directory to static hosting, and the only
// secret involved is an Ed25519 key file.
//
// The split between subcommands is the review boundary, not a UX choice:
//
//	publish  runs on the publisher's machine, signs the artifact with the
//	         publisher's key, and produces a submission file. It touches no
//	         index — a publisher cannot index their own artifact.
//	index    runs on the registry's side after a human review, re-verifies
//	         everything the submission claims, writes the artifact into the
//	         published tree, and re-signs index.json with the registry's key.
//	revoke   withdraws an artifact (or everything a compromised key signed)
//	         and re-signs the index.
//	verify   verifies a published index against a pinned signer fingerprint —
//	         the same code path the daemon's gate runs, available to anyone
//	         auditing the hosting. Also (T-3709, --sigstore-key-bundle) verifies
//	         a Sigstore-signed key-custody attestation naming the registry's
//	         current Ed25519 index-signer fingerprint(s) — see hubcmd_sigstore.go
//	         and internal/hubreg/sigstoreverify's package doc for why this is a
//	         SEPARATE document from index.json, not a second way to verify it.
//	keygen   creates an Ed25519 key file (the T-1107 on-disk format).

package main

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/hub"
	"github.com/bgovanlu/vnprox/internal/hubreg"
)

// hubIndexFileName is the index document's name under the registry root — the
// path internal/hub's client derives from [hub] registry_url.
const hubIndexFileName = "index.json"

func runHub(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printHubUsage(stderr)
		return ExitUsage
	}
	switch args[0] {
	case "-h", "--help", "help":
		printHubUsage(stdout)
		return ExitSuccess
	case "publish":
		return runHubPublish(args[1:], stdout, stderr)
	case "index":
		return runHubIndex(args[1:], stdout, stderr)
	case "revoke":
		return runHubRevoke(args[1:], stdout, stderr)
	case "verify":
		return runHubVerify(args[1:], stdout, stderr)
	case "keygen":
		return runHubKeygen(args[1:], stdout, stderr)
	case "mirror":
		return runHubMirror(args[1:], stdout, stderr)
	case "pull":
		return runHubPull(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub: unknown subcommand %q\n\n", args[0])
		printHubUsage(stderr)
		return ExitUsage
	}
}

func printHubUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `vnproxctl hub - publish to, and audit, the signed blueprint/plugin registry

  vnproxctl hub publish --artifact <file> --type blueprint|plugin [--version V]
                        [--key <signing key>] [--publisher P] [--description D]
                        [--out <submission.json>]
        Sign an artifact and write a submission file. Open it as a pull
        request against the registry repository; a reviewer indexes it.

  vnproxctl hub index --root <registry dir> --submission <file> --key <index key>
        Registry side, after review: verify the submission, write its artifact
        into <root>, add one entry, and re-sign <root>/index.json. Idempotent —
        re-running with the same submission changes nothing. Also runs T-3709's
        automated "vetted" hygiene checks and folds the pass/fail verdict into
        the entry before signing (docs/hub-registry.md's "Automated vetting").

  vnproxctl hub revoke --root <registry dir> --key <index key> --reason <why>
                       [--type T --id ID [--version V]] [--signer <fingerprint>]
                       [--log-entry <id>]
        Withdraw an artifact, every version of an id, everything a
        compromised signing key produced, or (T-3709, --log-entry) deny-list
        one Sigstore transparency-log entry — the id "hub verify
        --sigstore-key-bundle" prints for a key-custody attestation you no
        longer trust. Revocations live inside the signed index, so clients
        honour them with no extra network call.

  vnproxctl hub verify --index <index.json> --signers <fp[,fp...]>
        Verify a published index exactly as a client does: signature, signer,
        schema and structure. Prints the catalog and any revocations.

  vnproxctl hub verify --sigstore-key-bundle <attestation.json>
                       --sigstore-bundle <bundle.json>
                       --sigstore-issuer <issuer> --sigstore-identity <SAN>
                       [--sigstore-issuer-regexp R] [--sigstore-identity-regexp R]
                       [--sigstore-trusted-root <file>]
                       [--check-revoked-against <index.json>]
        T-3709: verify a registry's Sigstore-signed key-custody attestation
        (Fulcio certificate chain, Rekor transparency-log inclusion, and
        certificate identity) and print the Ed25519 index-signer
        fingerprint(s) it vouches for, plus its own transparency-log entry
        id (the value --log-entry above takes). This does NOT verify
        index.json itself — that is still --index/--signers above, run by
        the daemon on every fetch. vnproxctl never writes daemon config: an
        operator copies the printed fingerprint(s) into their own
        vnprox.toml's [hub] index_signers by hand, the same explicit step
        Ed25519 key rotation has always required (docs/hub-registry.md's
        "Sigstore-backed key custody" section has the full account,
        including what this is weaker than). Omitting
        --sigstore-trusted-root fetches the Sigstore public-good trusted
        root live via TUF.

  vnproxctl hub keygen --key <path>
        Create an Ed25519 signing key file (0600, never overwritten).

  vnproxctl hub mirror --registry <https://hub...> --signers <fp[,fp...]> --out <dir>
        T-4009: fetch a hosted registry's signed index and every live entry's
        artifact, byte-for-byte, into <dir> — refuses to write anything if
        the index does not verify against --signers. Prints the
        [hub] registry_url line to configure the daemon (or "hub pull
        --registry") to consume <dir> with no further network access.

  vnproxctl hub pull --registry <url-or-mirror-dir> --signers <fp[,fp...]>
                     --type blueprint|plugin --id <id> --version <version>
                     --out <file>
        T-4009: fetch one artifact through the same signature-verifying path
        the daemon uses (index verified against --signers, then the artifact
        checked against that verified index's allowlist/revocations) — from
        a hosted registry, or from a "hub mirror" directory (--registry
        names a plain directory or an explicit file:// path), with zero
        network access in the mirror case.
`)
}

// hubKeyFlag loads an optional signing key file.
func loadHubKey(path string) (ed25519.PrivateKey, error) {
	if path == "" {
		return nil, nil //nolint:nilnil // "no key supplied" is a legitimate, checked state
	}
	key, err := blueprint.LoadSigningKeyFile(path)
	if err != nil {
		return nil, err
	}
	return key, nil
}

func runHubPublish(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl hub publish", flag.ContinueOnError)
	fs.SetOutput(stderr)
	artifact := fs.String("artifact", "", "path to the artifact to publish (a signed blueprint bundle or a plugin {manifest,signature} file)")
	kind := fs.String("type", "", `artifact type: "blueprint" or "plugin"`)
	version := fs.String("version", "", "catalog version (required for a blueprint; must match the manifest for a plugin)")
	keyFile := fs.String("key", "", "Ed25519 signing key file to sign the artifact with")
	publisher := fs.String("publisher", "", "publisher name shown in the catalog")
	description := fs.String("description", "", "one-line description shown in the catalog")
	base := fs.String("artifact-base", hubreg.DefaultArtifactBase, "path prefix artifact URLs are derived under")
	allowUnsigned := fs.Bool("allow-unsigned", false, "publish an artifact with no signature at all (an operator must then explicitly trust it to install it)")
	out := fs.String("out", "", "submission file to write (default: stdout)")
	output := fs.String("o", defaultOutputFormat, outputFlagUsage)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	jsonOut, ofErr := parseOutputFormat(*output)
	if ofErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub publish: %v\n", ofErr)
		return ExitUsage
	}
	if *artifact == "" || *kind == "" {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub publish: --artifact and --type are required\n")
		return ExitUsage
	}

	raw, err := os.ReadFile(*artifact)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub publish: reading %s: %v\n", *artifact, err)
		return ExitError
	}
	key, err := loadHubKey(*keyFile)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub publish: %v\n", err)
		return ExitError
	}
	sub, err := hubreg.BuildSubmission(raw, hubreg.SubmissionOptions{
		Type:          hub.EntryType(*kind),
		Version:       *version,
		Publisher:     *publisher,
		Description:   *description,
		ArtifactBase:  *base,
		SigningKey:    key,
		AllowUnsigned: *allowUnsigned,
		SubmittedAt:   time.Now().Unix(),
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub publish: %v\n", err)
		return hubExitFor(err)
	}
	body, err := json.MarshalIndent(sub, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub publish: %v\n", err)
		return ExitError
	}
	body = append(body, '\n')
	if *out == "" {
		if _, werr := stdout.Write(body); werr != nil {
			return ExitError
		}
		return ExitSuccess
	}
	if err := os.WriteFile(*out, body, 0o644); err != nil { //nolint:gosec // a submission is public, reviewable content
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub publish: writing %s: %v\n", *out, err)
		return ExitError
	}
	if jsonOut {
		if err := writeJSONOut(stdout, map[string]any{
			"submission": *out, "type": string(sub.Entry.Type), "id": sub.Entry.ID,
			"version": sub.Entry.Version, "artifactUrl": sub.Entry.ArtifactURL,
			"signerFingerprint": sub.Entry.SignerFingerprint(),
		}); err != nil {
			return ExitError
		}
		return ExitSuccess
	}
	_, _ = fmt.Fprintf(stdout, "Wrote %s\n", *out)
	_, _ = fmt.Fprintf(stdout, "  %s %s@%s -> %s\n", sub.Entry.Type, sub.Entry.ID, sub.Entry.Version, sub.Entry.ArtifactURL)
	if fp := sub.Entry.SignerFingerprint(); fp != "" {
		_, _ = fmt.Fprintf(stdout, "  signed by %s\n", fp)
	} else {
		_, _ = fmt.Fprintf(stdout, "  UNSIGNED — an operator must explicitly trust this to install it\n")
	}
	return ExitSuccess
}

func runHubIndex(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl hub index", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", "", "registry root directory (holds index.json and the artifact tree)")
	submission := fs.String("submission", "", "reviewed submission file to index")
	keyFile := fs.String("key", "", "Ed25519 index signing key file")
	output := fs.String("o", defaultOutputFormat, outputFlagUsage)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	jsonOut, ofErr := parseOutputFormat(*output)
	if ofErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub index: %v\n", ofErr)
		return ExitUsage
	}
	if *root == "" || *submission == "" || *keyFile == "" {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub index: --root, --submission and --key are required\n")
		return ExitUsage
	}

	subRaw, err := os.ReadFile(*submission)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub index: reading %s: %v\n", *submission, err)
		return ExitError
	}
	sub, err := hubreg.ParseSubmission(subRaw)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub index: %v\n", err)
		return hubExitFor(err)
	}
	// T-3709: run the automated "vetted" hygiene checks now, at the one
	// point in the pipeline that holds the full, reviewed artifact bytes,
	// and fold the verdict into the entry BEFORE it is (re-)signed — so it
	// rides inside the same signature as everything else, and a mismatched
	// or forged verdict cannot survive index verification.
	vet := hubreg.AutomatedVetChecks(sub)
	sub.Entry.AutomatedChecksPassed = vet.Passed()
	key, err := blueprint.LoadSigningKeyFile(*keyFile)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub index: %v\n", err)
		return ExitError
	}
	doc, err := readIndexDoc(*root)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub index: %v\n", err)
		return ExitError
	}
	doc, entryChanged, err := hubreg.AddEntry(doc, sub)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub index: %v\n", err)
		return hubExitFor(err)
	}
	artifactPath, fileChanged, err := hubreg.WriteArtifact(*root, sub)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub index: %v\n", err)
		return hubExitFor(err)
	}
	changed := entryChanged || fileChanged
	if err := writeIndexDoc(*root, doc, key, changed); err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub index: %v\n", err)
		return ExitError
	}
	if jsonOut {
		if err := writeJSONOut(stdout, map[string]any{
			"changed": changed, "entries": len(doc.Entries), "artifact": artifactPath,
			"id": sub.Entry.ID, "version": sub.Entry.Version,
			"automatedChecksPassed": sub.Entry.AutomatedChecksPassed, "vettingNotes": vet.Notes,
		}); err != nil {
			return ExitError
		}
		return ExitSuccess
	}
	if !changed {
		_, _ = fmt.Fprintf(stdout, "Already published: %s %s@%s (index unchanged, %d entries)\n", sub.Entry.Type, sub.Entry.ID, sub.Entry.Version, len(doc.Entries))
		return ExitSuccess
	}
	_, _ = fmt.Fprintf(stdout, "Indexed %s %s@%s\n", sub.Entry.Type, sub.Entry.ID, sub.Entry.Version)
	_, _ = fmt.Fprintf(stdout, "  artifact: %s\n", artifactPath)
	_, _ = fmt.Fprintf(stdout, "  index:    %s (%d entries)\n", filepath.Join(*root, hubIndexFileName), len(doc.Entries))
	_, _ = fmt.Fprintf(stdout, "  automated vetting: passed=%v\n", sub.Entry.AutomatedChecksPassed)
	for _, note := range vet.Notes {
		_, _ = fmt.Fprintf(stdout, "    - %s\n", note)
	}
	return ExitSuccess
}

func runHubRevoke(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl hub revoke", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", "", "registry root directory")
	keyFile := fs.String("key", "", "Ed25519 index signing key file")
	kind := fs.String("type", "", `artifact type: "blueprint" or "plugin"`)
	id := fs.String("id", "", "artifact id to revoke (all versions unless --version is given)")
	version := fs.String("version", "", "single version to revoke")
	signer := fs.String("signer", "", "revoke every artifact signed by this fingerprint (key compromise)")
	logEntry := fs.String("log-entry", "", "deny-list one sigstore transparency-log entry (T-3709; the id `hub verify --sigstore-key-bundle` prints) — the keyless equivalent of --signer, for a key-custody attestation you no longer trust")
	reason := fs.String("reason", "", "why this is revoked (required; published in the index)")
	output := fs.String("o", defaultOutputFormat, outputFlagUsage)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	jsonOut, ofErr := parseOutputFormat(*output)
	if ofErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub revoke: %v\n", ofErr)
		return ExitUsage
	}
	if *root == "" || *keyFile == "" || *reason == "" {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub revoke: --root, --key and --reason are required\n")
		return ExitUsage
	}
	if *id == "" && *signer == "" && *logEntry == "" {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub revoke: name what to revoke: --id (with --type), --signer, or --log-entry\n")
		return ExitUsage
	}

	key, err := blueprint.LoadSigningKeyFile(*keyFile)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub revoke: %v\n", err)
		return ExitError
	}
	doc, err := readIndexDoc(*root)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub revoke: %v\n", err)
		return ExitError
	}
	rev := hubreg.Revocation{
		Type:                 hub.EntryType(*kind),
		ID:                   *id,
		Version:              *version,
		SignerFingerprint:    *signer,
		TransparencyLogIndex: *logEntry,
		Reason:               *reason,
		At:                   time.Now().Unix(),
	}
	doc, changed, err := hubreg.AddRevocation(doc, rev)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub revoke: %v\n", err)
		return hubExitFor(err)
	}
	if err := writeIndexDoc(*root, doc, key, changed); err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub revoke: %v\n", err)
		return ExitError
	}
	withdrawn := len(doc.Entries) - len(doc.HubIndex().Entries)
	if jsonOut {
		if err := writeJSONOut(stdout, map[string]any{
			"changed": changed, "revocations": len(doc.Revocations), "withdrawnEntries": withdrawn,
		}); err != nil {
			return ExitError
		}
		return ExitSuccess
	}
	if !changed {
		_, _ = fmt.Fprintf(stdout, "Already revoked (index unchanged)\n")
		return ExitSuccess
	}
	_, _ = fmt.Fprintf(stdout, "Revoked. %d revocation(s) published; %d catalog entr(ies) now withdrawn.\n", len(doc.Revocations), withdrawn)
	return ExitSuccess
}

func runHubVerify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl hub verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	indexPath := fs.String("index", "", "path to a published index.json")
	signers := fs.String("signers", "", "comma-separated trusted index-signer fingerprints (the [hub] index_signers value)")
	sigstoreKeyBundle := fs.String("sigstore-key-bundle", "", "path to a Sigstore-signed key-custody attestation document (T-3709; mutually exclusive with --index/--signers)")
	sigstoreBundlePath := fs.String("sigstore-bundle", "", "path to the sibling sigstore bundle for --sigstore-key-bundle")
	sigstoreIssuer := fs.String("sigstore-issuer", "", "expected OIDC issuer (exact)")
	sigstoreIssuerRegexp := fs.String("sigstore-issuer-regexp", "", "expected OIDC issuer (regexp)")
	sigstoreIdentity := fs.String("sigstore-identity", "", "expected certificate subject (exact)")
	sigstoreIdentityRegexp := fs.String("sigstore-identity-regexp", "", "expected certificate subject (regexp)")
	sigstoreTrustedRoot := fs.String("sigstore-trusted-root", "", "pinned sigstore trusted-root file (omit to fetch the public-good root live via TUF)")
	checkRevokedAgainst := fs.String("check-revoked-against", "", "an index.json whose revocation deny-list is checked against the attestation's own transparency-log entry")
	output := fs.String("o", defaultOutputFormat, outputFlagUsage)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	jsonOut, ofErr := parseOutputFormat(*output)
	if ofErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub verify: %v\n", ofErr)
		return ExitUsage
	}
	if *sigstoreKeyBundle != "" {
		return runHubVerifySigstoreKey(hubVerifySigstoreKeyArgs{
			attestationPath:     *sigstoreKeyBundle,
			bundlePath:          *sigstoreBundlePath,
			issuer:              *sigstoreIssuer,
			issuerRegexp:        *sigstoreIssuerRegexp,
			identity:            *sigstoreIdentity,
			identityRegexp:      *sigstoreIdentityRegexp,
			trustedRootPath:     *sigstoreTrustedRoot,
			checkRevokedAgainst: *checkRevokedAgainst,
		}, stdout, stderr, jsonOut)
	}
	if *indexPath == "" || *signers == "" {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub verify: --index and --signers are required (or use --sigstore-key-bundle for the sigstore key-custody path)\n")
		return ExitUsage
	}
	raw, err := os.ReadFile(*indexPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub verify: reading %s: %v\n", *indexPath, err)
		return ExitError
	}
	doc, err := hubreg.Verify(raw, strings.Split(*signers, ","))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub verify: %v\n", err)
		return ExitError
	}
	live := doc.HubIndex().Entries
	if jsonOut {
		if err := writeJSONOut(stdout, map[string]any{
			"signerFingerprint": doc.Signature.PublicKeyFingerprint,
			"generatedAt":       doc.GeneratedAt,
			"entries":           len(doc.Entries),
			"live":              len(live),
			"revocations":       len(doc.Revocations),
		}); err != nil {
			return ExitError
		}
		return ExitSuccess
	}
	_, _ = fmt.Fprintf(stdout, "OK: index signed by %s\n", doc.Signature.PublicKeyFingerprint)
	_, _ = fmt.Fprintf(stdout, "  %d entr(ies), %d offered, %d revocation(s)\n", len(doc.Entries), len(live), len(doc.Revocations))
	for _, e := range live {
		_, _ = fmt.Fprintf(stdout, "  %-9s %s@%s  %s\n", e.Type, e.ID, e.Version, e.ArtifactURL)
	}
	for _, r := range doc.Revocations {
		target := r.ID
		switch {
		case target != "" && r.Version != "":
			target += "@" + r.Version
		case target == "" && r.SignerFingerprint != "":
			target = "signer " + r.SignerFingerprint
		case target == "" && r.TransparencyLogIndex != "":
			target = "log entry " + r.TransparencyLogIndex
		}
		_, _ = fmt.Fprintf(stdout, "  REVOKED   %s: %s\n", target, r.Reason)
	}
	return ExitSuccess
}

func runHubKeygen(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl hub keygen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	keyFile := fs.String("key", "", "path to write the new Ed25519 key file to")
	output := fs.String("o", defaultOutputFormat, outputFlagUsage)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	jsonOut, ofErr := parseOutputFormat(*output)
	if ofErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub keygen: %v\n", ofErr)
		return ExitUsage
	}
	if *keyFile == "" {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub keygen: --key is required\n")
		return ExitUsage
	}
	if err := blueprint.GenerateSigningKeyFile(*keyFile); err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub keygen: %v\n", err)
		return ExitError
	}
	key, err := blueprint.LoadSigningKeyFile(*keyFile)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub keygen: %v\n", err)
		return ExitError
	}
	pub, ok := key.Public().(ed25519.PublicKey)
	if !ok {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub keygen: generated key has no Ed25519 public half\n")
		return ExitError
	}
	fp := blueprint.Fingerprint(pub)
	if jsonOut {
		if err := writeJSONOut(stdout, map[string]any{"key": *keyFile, "fingerprint": fp}); err != nil {
			return ExitError
		}
		return ExitSuccess
	}
	_, _ = fmt.Fprintf(stdout, "Wrote %s\n  fingerprint: %s\n", *keyFile, fp)
	return ExitSuccess
}

// readIndexDoc loads root/index.json, or an empty document if there is none
// yet (bootstrapping a registry is just `hub index` into an empty directory).
// The existing index is NOT signature-checked here: the operator holds the key
// they are about to re-sign with, and a corrupt index would be caught by
// `hub verify` and by every client. It IS structurally parsed, so a malformed
// index cannot be silently rewritten into a valid-looking one.
func readIndexDoc(root string) (hubreg.Document, error) {
	path := filepath.Join(root, hubIndexFileName)
	raw, err := os.ReadFile(path) //nolint:gosec // operator-supplied registry root
	if errors.Is(err, os.ErrNotExist) {
		return hubreg.Document{SchemaVersion: hubreg.CurrentIndexSchema}, nil
	}
	if err != nil {
		return hubreg.Document{}, fmt.Errorf("reading %s: %w", path, err)
	}
	var doc hubreg.Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		return hubreg.Document{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if doc.SchemaVersion != hubreg.CurrentIndexSchema {
		return hubreg.Document{}, fmt.Errorf("%s has schema version %d, want %d", path, doc.SchemaVersion, hubreg.CurrentIndexSchema)
	}
	if err := hubreg.Validate(doc); err != nil {
		return hubreg.Document{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return doc, nil
}

// writeIndexDoc re-signs and writes root/index.json. When nothing changed the
// file is left completely alone — a no-op publish must not churn the
// published bytes (and therefore must not churn what clients re-download).
func writeIndexDoc(root string, doc hubreg.Document, key ed25519.PrivateKey, changed bool) error {
	path := filepath.Join(root, hubIndexFileName)
	if !changed {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
	}
	doc.GeneratedAt = time.Now().Unix()
	signed, err := hubreg.Sign(doc, key)
	if err != nil {
		return err
	}
	body, err := json.Marshal(signed)
	if err != nil {
		return fmt.Errorf("encoding index: %w", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("creating registry root %s: %w", root, err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil { //nolint:gosec // a published index is world-readable static hosting content
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// hubExitFor maps a registry error onto the documented exit-code table: a
// refusal a human has to resolve (a conflict, an invalid submission, an index
// that does not verify) is ExitPending — "this would change something / can't
// proceed automatically, go look" — rather than the generic ExitError.
func hubExitFor(err error) int {
	switch {
	case errors.Is(err, hubreg.ErrConflict),
		errors.Is(err, hubreg.ErrInvalidSubmission),
		errors.Is(err, hubreg.ErrInvalidIndex):
		return ExitPending
	default:
		return ExitError
	}
}
