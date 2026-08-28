// SPDX-License-Identifier: Apache-2.0

// speccmd.go implements `vnproxctl spec` (T-4005): the CLI on-ramp to
// T-1101's declarative cluster spec and T-1102's pinned-spec routes.
//
// The export/import logic already ships — internal/spec.Export/Import and
// GET /spec, POST /spec/import, GET/POST/DELETE /spec/pin (docs/api.md's
// "Declarative cluster network spec" and "Spec pin" sections) all predate
// this file. What was missing was a door: a config-as-code adopter had to
// hand-curl those routes and had no way to learn they existed from
// `vnproxctl --help`. This file adds no export/import logic of its own —
// every subcommand below is a thin HTTP call over the documented shapes,
// exactly like `vnproxctl remote <subcommand>` and `vnproxctl gitsync
// status`.
//
//	vnproxctl spec export [--out <file>] [-o table|json]
//	vnproxctl spec import <file>          [-o table|json]
//	vnproxctl spec pin    [<file>]        [-o table|json]
//	vnproxctl spec unpin                  [-o table|json]
//
// `import` and `pin` never apply anything (CLAUDE.md's core guarantee: the
// change engine, stage -> validate -> diff -> apply -> confirm/rollback, is
// the sole mutation path). `import` stages an ordinary draft changeset and
// stops — reviewing and applying it is `vnproxctl remote changesets
// apply`/`vnproxctl apply --apply`'s job, not this command's. `pin` only
// ever stores a document for internal/drift's spec_drift check to diff
// against later; reconciling a resulting finding is the normal
// POST /drift/{id}/fix -> review -> apply/confirm flow.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
)

// specExportWire mirrors GET /spec's response (internal/api/spec.go's
// specExportResponse; docs/api.md's "Declarative cluster network spec"
// section): `{specVersion, content}`. Redefined here rather than imported,
// the same "the documented JSON shape is the only dependency" precedent
// changesetWire's doc comment sets.
type specExportWire struct {
	Content     string `json:"content"`
	SpecVersion int    `json:"specVersion"`
}

// specPinWire mirrors GET/POST /spec/pin's response (internal/api/specpin.go's
// specPinResponse; docs/api.md's "Spec pin" section): every field but
// `pinned` is omitted when nothing is pinned.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape, not memory packing.
type specPinWire struct {
	Content  string `json:"content,omitempty"`
	PinnedBy string `json:"pinnedBy,omitempty"`
	PinnedAt int64  `json:"pinnedAt,omitempty"`
	Pinned   bool   `json:"pinned"`
}

// specPinRequestBody is POST /spec/pin's body.
type specPinRequestBody struct {
	Content string `json:"content"`
}

func runSpec(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "vnproxctl spec: expected a subcommand (export, import, pin, unpin)")
		return ExitUsage
	}
	switch args[0] {
	case "export":
		return runSpecExport(args[1:], stdout, stderr)
	case "import":
		return runSpecImport(args[1:], stdout, stderr)
	case "pin":
		return runSpecPin(args[1:], stdout, stderr)
	case "unpin":
		return runSpecUnpin(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "vnproxctl spec: unknown subcommand %q\n", args[0])
		return ExitUsage
	}
}

// --- export -----------------------------------------------------------

// runSpecExport implements `vnproxctl spec export`: GET /spec, written to
// stdout (or --out) as the raw YAML document by default. This is the
// direct missing counterpart to `vnproxctl backup`/`support-bundle`'s
// existing file-output pattern (--out, not a second -o convention — see
// this file's doc comment). `-o json` instead prints the wrapping
// `{specVersion, content}` envelope verbatim, matching the HTTP response
// byte-for-byte.
func runSpecExport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl spec export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rf := addRemoteFlags(fs)
	out := fs.String("out", "", "write the YAML document here instead of stdout (ignored with -o json, which always goes to stdout)")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	jsonOut, code, ok := parseOutputFlagOrUsage(rf, "vnproxctl spec export", stderr)
	if !ok {
		return code
	}
	client, code := buildRemoteClient(rf, "vnproxctl spec export", stderr)
	if client == nil {
		return code
	}

	ctx, cancel := context.WithTimeout(context.Background(), *rf.timeout)
	defer cancel()
	var result specExportWire
	status, apiErr, err := client.doJSON(ctx, "GET", "/spec", nil, &result)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl spec export: %v\n", err)
		return exitForErr(err)
	}
	if apiErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl spec export: %s: %s\n", apiErr.Code, apiErr.Message)
		return exitForAPIError(status)
	}

	if jsonOut {
		if err := writeJSONOut(stdout, result); err != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl spec export: %v\n", err)
			return ExitError
		}
		return ExitSuccess
	}

	if *out != "" {
		if err := os.WriteFile(*out, []byte(result.Content), 0o644); err != nil { //nolint:gosec // an operator-named output path, same as backup/support-bundle's --out
			_, _ = fmt.Fprintf(stderr, "vnproxctl spec export: writing %s: %v\n", *out, err)
			return ExitError
		}
		return ExitSuccess
	}
	if _, err := io.WriteString(stdout, result.Content); err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl spec export: %v\n", err)
		return ExitError
	}
	return ExitSuccess
}

// --- import -----------------------------------------------------------

// runSpecImport implements `vnproxctl spec import <file>`: POST
// /spec/import, then print the id and status of the draft changeset it
// staged and the notInSpec list — and stop. Never calls
// POST /changesets/{id}/apply; reviewing and applying the draft is
// `vnproxctl remote changesets apply` or `vnproxctl apply --apply`'s job.
func runSpecImport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl spec import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rf := addRemoteFlags(fs)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, "vnproxctl spec import: expected exactly one spec file path (\"-\" for stdin)")
		return ExitUsage
	}
	jsonOut, code, ok := parseOutputFlagOrUsage(rf, "vnproxctl spec import", stderr)
	if !ok {
		return code
	}

	content, err := readFileOrStdin(fs.Arg(0))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl spec import: reading %s: %v\n", fs.Arg(0), err)
		return ExitUsage
	}

	client, code := buildRemoteClient(rf, "vnproxctl spec import", stderr)
	if client == nil {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), *rf.timeout)
	defer cancel()
	var imported specImportWire
	status, apiErr, err := client.doJSON(ctx, "POST", "/spec/import", specImportRequestBody{Content: string(content)}, &imported)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl spec import: %v\n", err)
		return exitForErr(err)
	}
	if apiErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl spec import: %s: %s\n", apiErr.Code, apiErr.Message)
		return exitForAPIError(status)
	}

	if jsonOut {
		if err := writeJSONOut(stdout, imported); err != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl spec import: %v\n", err)
			return ExitError
		}
		return ExitSuccess
	}
	printChangesetTable(stdout, imported.changesetWire)
	if len(imported.NotInSpec) > 0 {
		_, _ = fmt.Fprintln(stdout, "\nNot in spec (present live, reported only — never deleted):")
		for _, ref := range imported.NotInSpec {
			_, _ = fmt.Fprintf(stdout, "  %s\n", ref)
		}
	}
	_, _ = fmt.Fprintf(stdout, "\nStaged as a %s changeset. Nothing was applied — review with `vnproxctl remote changesets diff %s`, then apply with `vnproxctl remote changesets apply %s`.\n",
		imported.Status, imported.ID, imported.ID)
	return ExitSuccess
}

// --- pin / unpin --------------------------------------------------------

// runSpecPin implements `vnproxctl spec pin` (bare: GET /spec/pin, the
// current pin) and `vnproxctl spec pin <file>` (POST /spec/pin, re-pin from
// that file's content — no explicit unpin required first, matching
// docs/api.md's "POST re-pins in place" note).
func runSpecPin(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl spec pin", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rf := addRemoteFlags(fs)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() > 1 {
		_, _ = fmt.Fprintln(stderr, "vnproxctl spec pin: expected at most one spec file path (\"-\" for stdin); omit it to show the current pin")
		return ExitUsage
	}
	jsonOut, code, ok := parseOutputFlagOrUsage(rf, "vnproxctl spec pin", stderr)
	if !ok {
		return code
	}
	client, code := buildRemoteClient(rf, "vnproxctl spec pin", stderr)
	if client == nil {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), *rf.timeout)
	defer cancel()

	var result specPinWire
	var status int
	var apiErr *apiError
	var err error
	if fs.NArg() == 0 {
		status, apiErr, err = client.doJSON(ctx, "GET", "/spec/pin", nil, &result)
	} else {
		content, readErr := readFileOrStdin(fs.Arg(0))
		if readErr != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl spec pin: reading %s: %v\n", fs.Arg(0), readErr)
			return ExitUsage
		}
		status, apiErr, err = client.doJSON(ctx, "POST", "/spec/pin", specPinRequestBody{Content: string(content)}, &result)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl spec pin: %v\n", err)
		return exitForErr(err)
	}
	if apiErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl spec pin: %s: %s\n", apiErr.Code, apiErr.Message)
		return exitForAPIError(status)
	}

	if jsonOut {
		if err := writeJSONOut(stdout, result); err != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl spec pin: %v\n", err)
			return ExitError
		}
		return ExitSuccess
	}
	printSpecPin(stdout, result)
	return ExitSuccess
}

// runSpecUnpin implements `vnproxctl spec unpin`: DELETE /spec/pin.
func runSpecUnpin(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl spec unpin", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rf := addRemoteFlags(fs)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	jsonOut, code, ok := parseOutputFlagOrUsage(rf, "vnproxctl spec unpin", stderr)
	if !ok {
		return code
	}
	client, code := buildRemoteClient(rf, "vnproxctl spec unpin", stderr)
	if client == nil {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), *rf.timeout)
	defer cancel()
	status, apiErr, err := client.doJSON(ctx, "DELETE", "/spec/pin", nil, nil)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl spec unpin: %v\n", err)
		return exitForErr(err)
	}
	if apiErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl spec unpin: %s: %s\n", apiErr.Code, apiErr.Message)
		return exitForAPIError(status)
	}

	result := specPinWire{Pinned: false}
	if jsonOut {
		if err := writeJSONOut(stdout, result); err != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl spec unpin: %v\n", err)
			return ExitError
		}
		return ExitSuccess
	}
	_, _ = fmt.Fprintln(stdout, "Unpinned. Nothing is pinned now — spec_drift has nothing to reconcile against.")
	return ExitSuccess
}

func printSpecPin(w io.Writer, p specPinWire) {
	if !p.Pinned {
		_, _ = fmt.Fprintln(w, "Nothing is pinned.")
		return
	}
	_, _ = fmt.Fprintf(w, "Pinned by:  %s\n", p.PinnedBy)
	_, _ = fmt.Fprintf(w, "Pinned at:  %s\n", strconvItoa64(p.PinnedAt))
	_, _ = fmt.Fprintln(w, "Content:")
	_, _ = fmt.Fprintln(w, p.Content)
}

// readFileOrStdin reads path, or stdin when path is "-" — the same
// convention `vnproxctl remote changesets create -f` uses.
func readFileOrStdin(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path) //nolint:gosec // an operator-supplied path, same as apply's spec-file argument
}
