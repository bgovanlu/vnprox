// SPDX-License-Identifier: Apache-2.0

// hubcmd_mirror.go implements `vnproxctl hub mirror` and `vnproxctl hub pull`
// (T-4009): the air-gapped half of the registry family hubcmd.go's other
// subcommands already cover.
//
//	mirror  fetches a hosted registry's signed index plus every live entry's
//	        artifact, byte-for-byte, into a local directory laid out the same
//	        way `vnproxctl hub index` lays out a registry root — so the SAME
//	        internal/hub.Client + internal/hubreg.Gate combination the daemon
//	        already uses to browse a hosted registry can browse this
//	        directory too (internal/hub's NewLocalDoer/LocalRegistryURL,
//	        T-4009). It refuses to write anything if the fetched index does
//	        not verify: a mirror of an unverifiable catalog is not a mirror
//	        of anything trustworthy.
//
//	pull    fetches one artifact through the real signature-verifying path
//	        (internal/hubreg.Gate over internal/hub.Client) from either a
//	        hosted registry or a mirror directory `mirror` produced, and
//	        writes it to disk. Pointed at a mirror with --registry, it is
//	        the offline half of AC1: verification still runs — Gate.doIndex
//	        still calls hubreg.Verify, and Gate.doArtifact still enforces
//	        the mirrored index's own allowlist/revocations — no network
//	        access is possible at all when --registry names a local
//	        directory, because internal/hub.NewLocalDoer reads the mirrored
//	        files directly and is the only doer ever constructed in that
//	        case (see runHubPull below).
//
// Both commands refuse to run without --signers: an air-gapped operator who
// does not name a trusted signer gets a usage error, never a silently
// unverified fetch (the failure mode T-4009's card names as the one to
// guard hardest).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/hub"
	"github.com/bgovanlu/vnprox/internal/hubreg"
)

// hubNetworkTimeout bounds every network call `hub mirror`/`hub pull` make
// against a hosted registry — generous for a catalog + a handful of small
// artifacts, matching internal/hub.Client's own 15s-per-request default
// (this wraps a whole run, not one request, so it is longer).
const hubNetworkTimeout = 60 * time.Second

func runHubMirror(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl hub mirror", flag.ContinueOnError)
	fs.SetOutput(stderr)
	registry := fs.String("registry", "", "hosted registry base URL to mirror (http(s)://...)")
	signers := fs.String("signers", "", "comma-separated trusted index-signer fingerprints (the [hub] index_signers value) — required: a mirror of an index that does not verify is refused")
	out := fs.String("out", "", "directory to write the mirror into (created if absent)")
	output := fs.String("o", defaultOutputFormat, outputFlagUsage)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	jsonOut, ofErr := parseOutputFormat(*output)
	if ofErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub mirror: %v\n", ofErr)
		return ExitUsage
	}
	if *registry == "" || *signers == "" || *out == "" {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub mirror: --registry, --signers and --out are required\n")
		return ExitUsage
	}
	trusted := strings.Split(*signers, ",")

	ctx, cancel := context.WithTimeout(context.Background(), hubNetworkTimeout)
	defer cancel()

	indexURL := strings.TrimRight(*registry, "/") + "/index.json"
	// setupErr, not err: every later block in this function is an idiomatic
	// `if err := ...; err != nil`, and govet's shadow check flags those as
	// shadowing an outer `err`. Naming the setup chain distinctly keeps the
	// inner blocks in the form the rest of this codebase uses.
	rawIndex, setupErr := fetchRawURL(ctx, indexURL)
	if setupErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub mirror: fetching %s: %v\n", indexURL, setupErr)
		return ExitError
	}
	// The trust decision: an index that does not verify against the
	// operator-supplied signers is never mirrored, partially or otherwise —
	// nothing is written to --out before this succeeds.
	doc, setupErr := hubreg.Verify(rawIndex, trusted)
	if setupErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub mirror: refusing to mirror: the registry index does not verify: %v\n", setupErr)
		return ExitError
	}

	client, setupErr := hub.NewClient(*registry)
	if setupErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub mirror: %v\n", setupErr)
		return ExitError
	}

	if err := os.MkdirAll(*out, 0o755); err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub mirror: creating %s: %v\n", *out, err)
		return ExitError
	}

	live := doc.HubIndex().Entries
	var artifactsWritten int
	var warnings []string
	for _, e := range live {
		dest, foreign, werr := mirrorArtifactPath(*out, e.ArtifactURL)
		if werr != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl hub mirror: %s %s@%s: %v\n", e.Type, e.ID, e.Version, werr)
			return ExitError
		}
		if foreign {
			warnings = append(warnings, fmt.Sprintf("%s %s@%s: artifactUrl %q is not the self-hosted absolute-path form — mirrored for archival, but a local-mirror client (internal/hub.NewLocalDoer) cannot fetch it back: only same-registry absolute-path artifact URLs are consumable offline", e.Type, e.ID, e.Version, e.ArtifactURL))
		}
		raw, ferr := client.FetchArtifactRaw(ctx, e)
		if ferr != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl hub mirror: fetching artifact %s %s@%s: %v\n", e.Type, e.ID, e.Version, ferr)
			return ExitError
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl hub mirror: %v\n", err)
			return ExitError
		}
		if err := os.WriteFile(dest, raw, 0o644); err != nil { //nolint:gosec // a mirrored artifact is exactly as public as the hosted one it copies
			_, _ = fmt.Fprintf(stderr, "vnproxctl hub mirror: writing %s: %v\n", dest, err)
			return ExitError
		}
		artifactsWritten++
	}

	// Written last, and only once every artifact succeeded: a partial mirror
	// (some artifacts fetched, an error partway through) must not leave
	// behind an index.json that claims a complete catalog.
	indexPath := filepath.Join(*out, "index.json")
	if err := os.WriteFile(indexPath, rawIndex, 0o644); err != nil { //nolint:gosec // a mirrored index is exactly as public as the hosted one it copies
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub mirror: writing %s: %v\n", indexPath, err)
		return ExitError
	}

	localURL, err := hub.LocalRegistryURL(*out)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub mirror: %v\n", err)
		return ExitError
	}

	if jsonOut {
		if err := writeJSONOut(stdout, map[string]any{
			"registry": *registry, "out": *out, "registryUrl": localURL,
			"entries": len(doc.Entries), "live": len(live), "artifacts": artifactsWritten,
			"revocations": len(doc.Revocations), "signerFingerprint": doc.Signature.PublicKeyFingerprint,
			"warnings": warnings,
		}); err != nil {
			return ExitError
		}
		return ExitSuccess
	}
	_, _ = fmt.Fprintf(stdout, "Mirrored %s -> %s\n", *registry, *out)
	_, _ = fmt.Fprintf(stdout, "  signed by %s, %d entr(ies) (%d live, %d revocation(s)), %d artifact(s) written\n",
		doc.Signature.PublicKeyFingerprint, len(doc.Entries), len(live), len(doc.Revocations), artifactsWritten)
	_, _ = fmt.Fprintf(stdout, "  point [hub] registry_url (or `hub pull --registry`) at:\n")
	_, _ = fmt.Fprintf(stdout, "    %s\n", localURL)
	for _, w := range warnings {
		_, _ = fmt.Fprintf(stdout, "  WARNING: %s\n", w)
	}
	return ExitSuccess
}

// mirrorArtifactPath decides where under outDir an entry's artifact is
// written: the path component of its ArtifactURL, joined onto outDir — the
// same layout `vnproxctl hub index`'s WriteArtifact uses under a registry
// root, and the layout internal/hub.NewLocalDoer expects to read back
// (hub.go's "T-4009: local mirror consumption" doc comment). foreign is true
// when artifactURL was not the self-hosted absolute-path form (still
// mirrored, for archival, but not offline-consumable — see the caller's
// warning).
func mirrorArtifactPath(outDir, artifactURL string) (dest string, foreign bool, err error) {
	u, err := url.Parse(artifactURL)
	if err != nil {
		return "", false, fmt.Errorf("parsing artifactUrl %q: %w", artifactURL, err)
	}
	foreign = !strings.HasPrefix(artifactURL, "/")
	return filepath.Join(outDir, filepath.FromSlash(u.Path)), foreign, nil
}

// fetchRawURL GETs url and returns its body, capped at hubreg.MaxIndexBytes —
// the one piece of raw HTTP `hub mirror` does that internal/hub.Client
// itself has no reason to expose (Client's own Index() always decodes; a
// mirror needs the exact bytes a signature was computed over, unmutated by a
// decode/re-encode round trip).
func fetchRawURL(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry returned %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, hubreg.MaxIndexBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > hubreg.MaxIndexBytes {
		return nil, fmt.Errorf("body exceeds %d bytes", hubreg.MaxIndexBytes)
	}
	return raw, nil
}

func runHubPull(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl hub pull", flag.ContinueOnError)
	fs.SetOutput(stderr)
	registry := fs.String("registry", "", "registry to pull from: a hosted http(s):// URL, or a `hub mirror` directory (a bare path, or an explicit file:// URL)")
	signers := fs.String("signers", "", "comma-separated trusted index-signer fingerprints — required: verification always runs, online or offline")
	kind := fs.String("type", "", `artifact type: "blueprint" or "plugin"`)
	id := fs.String("id", "", "artifact id to pull")
	version := fs.String("version", "", "artifact version to pull")
	out := fs.String("out", "", "file to write the artifact's raw bytes to")
	output := fs.String("o", defaultOutputFormat, outputFlagUsage)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	jsonOut, ofErr := parseOutputFormat(*output)
	if ofErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub pull: %v\n", ofErr)
		return ExitUsage
	}
	if *registry == "" || *signers == "" || *kind == "" || *id == "" || *version == "" || *out == "" {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub pull: --registry, --signers, --type, --id, --version and --out are all required\n")
		return ExitUsage
	}

	regURL, err := hub.NormalizeRegistryURL(*registry)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub pull: %v\n", err)
		return ExitError
	}
	local := strings.HasPrefix(regURL, "file://")

	// The offline guarantee lives here: for a local mirror, `inner` is a
	// LocalDoer that only ever opens files under the mirror directory — no
	// *http.Client, no DNS resolver, no socket is ever constructed on this
	// path. Gate wraps it unchanged; verification runs exactly as it does
	// against a hosted registry (hubreg.Gate.doIndex -> hubreg.Verify).
	var inner hubreg.Doer
	if local {
		u, perr := url.Parse(regURL)
		if perr != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl hub pull: %v\n", perr)
			return ExitError
		}
		inner = hub.NewLocalDoer(u.Path)
	}
	gate := hubreg.NewGate(inner, strings.Split(*signers, ","))
	client, err := hub.NewClient(regURL, hub.WithHTTPClient(gate), hub.WithCacheTTL(0))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub pull: %v\n", err)
		return ExitError
	}

	ctx, cancel := context.WithTimeout(context.Background(), hubNetworkTimeout)
	defer cancel()

	idx, err := client.Index(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub pull: fetching/verifying index: %v\n", err)
		return ExitError
	}

	entry, found := findEntry(idx.Entries, hub.EntryType(*kind), *id, *version)
	if !found {
		if doc, ok := gate.Document(); ok {
			if rev, revoked := doc.IsRevoked(hub.Entry{Type: hub.EntryType(*kind), ID: *id, Version: *version}); revoked {
				_, _ = fmt.Fprintf(stderr, "vnproxctl hub pull: %s %s@%s is revoked: %s\n", *kind, *id, *version, rev.Reason)
				return ExitError
			}
		}
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub pull: no %s %s@%s in the verified index\n", *kind, *id, *version)
		return ExitError
	}

	raw, err := client.FetchArtifactRaw(ctx, entry)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub pull: fetching artifact: %v\n", err)
		return ExitError
	}
	if err := os.WriteFile(*out, raw, 0o644); err != nil { //nolint:gosec // a pulled artifact is exactly as public as the entry it was fetched from
		_, _ = fmt.Fprintf(stderr, "vnproxctl hub pull: writing %s: %v\n", *out, err)
		return ExitError
	}

	if jsonOut {
		if err := writeJSONOut(stdout, map[string]any{
			"registry": *registry, "local": local, "out": *out,
			"type": string(entry.Type), "id": entry.ID, "version": entry.Version,
			"bytes": len(raw), "signerFingerprint": entry.SignerFingerprint(),
		}); err != nil {
			return ExitError
		}
		return ExitSuccess
	}
	_, _ = fmt.Fprintf(stdout, "Pulled %s %s@%s -> %s (%d bytes)\n", entry.Type, entry.ID, entry.Version, *out, len(raw))
	if local {
		_, _ = fmt.Fprintf(stdout, "  from local mirror %s (no network access)\n", regURL)
	}
	return ExitSuccess
}

// findEntry locates an entry by (type, id, version) in a client-facing index
// — i.e. one that has already passed through Gate/hubreg.Verify, so a
// match here is a verified, non-revoked entry.
func findEntry(entries []hub.Entry, kind hub.EntryType, id, version string) (hub.Entry, bool) {
	for _, e := range entries {
		if e.Type == kind && e.ID == id && e.Version == version {
			return e, true
		}
	}
	return hub.Entry{}, false
}
