// SPDX-License-Identifier: Apache-2.0

// remote_changesets.go implements `vnproxctl remote changesets <subcommand>`
// (T-1105): CLI parity with docs/api.md's Changesets section — the same
// stage → validate → diff → apply → confirm/rollback lifecycle the UI
// drives, over the identical HTTP routes (T-205), never a second mutation
// path (CLAUDE.md's core safety guarantee — this file adds no new way to
// touch network state, only a new caller of the existing one).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

// changesetFindingWire mirrors docs/api.md's validation finding shape:
// `{severity, code, message, ref?, fix?}`.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape, not memory packing.
type changesetFindingWire struct {
	Fix      json.RawMessage `json:"fix,omitempty"`
	Severity string          `json:"severity"`
	Code     string          `json:"code"`
	Message  string          `json:"message"`
	Ref      string          `json:"ref,omitempty"`
}

// changesetWire mirrors internal/api's changesetResponse field-for-field
// (docs/api.md's Changesets section) — redefined here rather than imported
// because internal/api's type is unexported and this package's only
// dependency on the wire contract should be the documented JSON shape
// itself, the same "redefined, not imported" precedent status.go's
// healthResponse doc comment sets. Ops/Plan/ApplyLog travel as raw JSON:
// this CLI never needs to construct or mutate an Op locally (create/update
// bodies are passed through verbatim from a user-supplied file — see
// runRemoteChangesetsCreate), so there is no reason to duplicate
// internal/change's discriminated-union Op decoder here.
type changesetWire struct {
	Ops             []json.RawMessage      `json:"ops"`
	Plan            json.RawMessage        `json:"plan,omitempty"`
	ApplyLog        json.RawMessage        `json:"applyLog,omitempty"`
	ConfirmDeadline *int64                 `json:"confirmDeadline,omitempty"`
	ID              string                 `json:"id"`
	Title           string                 `json:"title"`
	Author          string                 `json:"author"`
	Status          string                 `json:"status"`
	Findings        []changesetFindingWire `json:"findings"`
	CreatedAt       int64                  `json:"createdAt"`
	UpdatedAt       int64                  `json:"updatedAt"`
	TouchesMgmtPath bool                   `json:"touchesMgmtPath"`
}

// fileDiffWire mirrors internal/change/ifaces.FileDiff.
type fileDiffWire struct {
	Node    string `json:"node"`
	Path    string `json:"path"`
	Unified string `json:"unified"`
	Changed bool   `json:"changed"`
}

// opSummaryWire mirrors internal/change/ifaces.OpSummary.
type opSummaryWire struct {
	Op      string `json:"op"`
	Target  string `json:"target"`
	Node    string `json:"node"`
	Summary string `json:"summary"`
}

// changesetDiffWire mirrors internal/change/ifaces.ChangesetDiff
// (docs/api.md's `GET /changesets/{id}/diff` response).
type changesetDiffWire struct {
	Files []fileDiffWire  `json:"files"`
	Ops   []opSummaryWire `json:"ops"`
}

func runRemoteChangesets(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "vnproxctl remote changesets: expected a subcommand (list, get, diff, create, validate, apply, confirm, rollback, discard)")
		return ExitUsage
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return runRemoteChangesetsList(rest, stdout, stderr)
	case "get":
		return runRemoteChangesetsGetOrAction(rest, stdout, stderr, "get", "GET", "", nil)
	case "diff":
		return runRemoteChangesetsDiff(rest, stdout, stderr)
	case "create":
		return runRemoteChangesetsCreate(rest, stdout, stderr)
	case "validate":
		return runRemoteChangesetsGetOrAction(rest, stdout, stderr, "validate", "POST", "/validate", nil)
	case "apply":
		return runRemoteChangesetsApply(rest, stdout, stderr)
	case "confirm":
		return runRemoteChangesetsGetOrAction(rest, stdout, stderr, "confirm", "POST", "/confirm", nil)
	case "rollback":
		return runRemoteChangesetsGetOrAction(rest, stdout, stderr, "rollback", "POST", "/rollback", nil)
	case "discard":
		return runRemoteChangesetsDiscard(rest, stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "vnproxctl remote changesets: unknown subcommand %q\n", sub)
		return ExitUsage
	}
}

func runRemoteChangesetsList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl remote changesets list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rf := addRemoteFlags(fs)
	status := fs.String("status", "", "filter by changeset status")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	jsonOut, code, ok := parseOutputFlagOrUsage(rf, "vnproxctl remote changesets list", stderr)
	if !ok {
		return code
	}
	client, code := buildRemoteClient(rf, "vnproxctl remote changesets list", stderr)
	if client == nil {
		return code
	}

	path := "/changesets"
	if *status != "" {
		path += "?status=" + *status
	}
	ctx, cancel := context.WithTimeout(context.Background(), *rf.timeout)
	defer cancel()
	var out []changesetWire
	httpStatus, apiErr, err := client.doJSON(ctx, "GET", path, nil, &out)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl remote changesets list: %v\n", err)
		return exitForErr(err)
	}
	if apiErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl remote changesets list: %s: %s\n", apiErr.Code, apiErr.Message)
		return exitForAPIError(httpStatus)
	}

	if jsonOut {
		if err := writeJSONOut(stdout, out); err != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl remote changesets list: %v\n", err)
			return ExitError
		}
		return ExitSuccess
	}
	if len(out) == 0 {
		_, _ = fmt.Fprintln(stdout, "No changesets.")
		return ExitSuccess
	}
	_, _ = fmt.Fprintf(stdout, "%-26s  %-30s  %-16s  %-5s  %s\n", "ID", "TITLE", "STATUS", "MGMT", "AUTHOR")
	for _, c := range out {
		_, _ = fmt.Fprintf(stdout, "%-26s  %-30s  %-16s  %-5t  %s\n", c.ID, c.Title, c.Status, c.TouchesMgmtPath, c.Author)
	}
	return ExitSuccess
}

// runRemoteChangesetsGetOrAction implements every single-id, no-request-body
// changesets subcommand that just returns a changesetWire (get/validate/
// confirm/rollback): they differ only in HTTP method and URL suffix.
func runRemoteChangesetsGetOrAction(args []string, stdout, stderr io.Writer, subName, method, suffix string, body any) int {
	cmdName := "vnproxctl remote changesets " + subName
	fs := flag.NewFlagSet(cmdName, flag.ContinueOnError)
	fs.SetOutput(stderr)
	rf := addRemoteFlags(fs)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintf(stderr, "%s: expected exactly one changeset id\n", cmdName)
		return ExitUsage
	}
	id := fs.Arg(0)
	jsonOut, code, ok := parseOutputFlagOrUsage(rf, cmdName, stderr)
	if !ok {
		return code
	}
	client, code := buildRemoteClient(rf, cmdName, stderr)
	if client == nil {
		return code
	}

	ctx, cancel := context.WithTimeout(context.Background(), *rf.timeout)
	defer cancel()
	var out changesetWire
	httpStatus, apiErr, err := client.doJSON(ctx, method, "/changesets/"+id+suffix, body, &out)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", cmdName, err)
		return exitForErr(err)
	}
	if apiErr != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %s: %s\n", cmdName, apiErr.Code, apiErr.Message)
		return exitForAPIError(httpStatus)
	}

	if jsonOut {
		if err := writeJSONOut(stdout, out); err != nil {
			_, _ = fmt.Fprintf(stderr, "%s: %v\n", cmdName, err)
			return ExitError
		}
		return ExitSuccess
	}
	printChangesetTable(stdout, out)
	return ExitSuccess
}

func runRemoteChangesetsDiff(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl remote changesets diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rf := addRemoteFlags(fs)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, "vnproxctl remote changesets diff: expected exactly one changeset id")
		return ExitUsage
	}
	id := fs.Arg(0)
	jsonOut, code, ok := parseOutputFlagOrUsage(rf, "vnproxctl remote changesets diff", stderr)
	if !ok {
		return code
	}
	client, code := buildRemoteClient(rf, "vnproxctl remote changesets diff", stderr)
	if client == nil {
		return code
	}

	ctx, cancel := context.WithTimeout(context.Background(), *rf.timeout)
	defer cancel()
	var out changesetDiffWire
	httpStatus, apiErr, err := client.doJSON(ctx, "GET", "/changesets/"+id+"/diff", nil, &out)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl remote changesets diff: %v\n", err)
		return exitForErr(err)
	}
	if apiErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl remote changesets diff: %s: %s\n", apiErr.Code, apiErr.Message)
		return exitForAPIError(httpStatus)
	}

	if jsonOut {
		if err := writeJSONOut(stdout, out); err != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl remote changesets diff: %v\n", err)
			return ExitError
		}
		return ExitSuccess
	}
	printChangesetDiff(stdout, out)
	return ExitSuccess
}

// printChangesetDiff renders a changesetDiffWire the way `git diff`/
// `terraform plan` output reads: op summaries first (the human-readable
// intent), then each changed file's unified diff.
func printChangesetDiff(w io.Writer, d changesetDiffWire) {
	if len(d.Ops) == 0 && len(d.Files) == 0 {
		_, _ = fmt.Fprintln(w, "No changes.")
		return
	}
	for _, op := range d.Ops {
		_, _ = fmt.Fprintf(w, "* [%s] %s (%s)\n", op.Node, op.Summary, op.Target)
	}
	for _, f := range d.Files {
		if !f.Changed {
			continue
		}
		_, _ = fmt.Fprintf(w, "\n--- %s:%s ---\n%s\n", f.Node, f.Path, f.Unified)
	}
}

func runRemoteChangesetsCreate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl remote changesets create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rf := addRemoteFlags(fs)
	file := fs.String("f", "-", `path to a JSON file shaped {"title","ops":[Op,...]} (docs/api.md POST /changesets body); "-" reads stdin`)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	jsonOut, code, ok := parseOutputFlagOrUsage(rf, "vnproxctl remote changesets create", stderr)
	if !ok {
		return code
	}

	var raw []byte
	var readErr error
	if *file == "-" {
		raw, readErr = io.ReadAll(os.Stdin)
	} else {
		raw, readErr = os.ReadFile(*file)
	}
	if readErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl remote changesets create: reading %s: %v\n", *file, readErr)
		return ExitUsage
	}
	if !json.Valid(raw) {
		_, _ = fmt.Fprintf(stderr, "vnproxctl remote changesets create: %s does not contain valid JSON\n", *file)
		return ExitUsage
	}

	client, code := buildRemoteClient(rf, "vnproxctl remote changesets create", stderr)
	if client == nil {
		return code
	}

	ctx, cancel := context.WithTimeout(context.Background(), *rf.timeout)
	defer cancel()
	var out changesetWire
	httpStatus, apiErr, err := client.doJSON(ctx, "POST", "/changesets", json.RawMessage(raw), &out)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl remote changesets create: %v\n", err)
		return exitForErr(err)
	}
	if apiErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl remote changesets create: %s: %s\n", apiErr.Code, apiErr.Message)
		return exitForAPIError(httpStatus)
	}

	if jsonOut {
		if err := writeJSONOut(stdout, out); err != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl remote changesets create: %v\n", err)
			return ExitError
		}
		return ExitSuccess
	}
	printChangesetTable(stdout, out)
	return ExitSuccess
}

// applyRequestBody mirrors internal/api's applyRequest
// (`{confirmTimeoutSec, mgmtAck?: {node}}`).
type applyRequestBody struct {
	MgmtAck           *mgmtAckBody `json:"mgmtAck,omitempty"`
	ConfirmTimeoutSec int          `json:"confirmTimeoutSec"`
}

type mgmtAckBody struct {
	Node string `json:"node"`
}

func runRemoteChangesetsApply(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl remote changesets apply", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rf := addRemoteFlags(fs)
	confirmTimeoutSec := fs.Int("confirm-timeout-sec", 120, "commit-confirm window, seconds (docs/api.md: floor of 180s on a management-path changeset)")
	mgmtAckNode := fs.String("mgmt-ack-node", "", "typed management-path acknowledgement node name (required by the daemon when the changeset touches a management path)")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, "vnproxctl remote changesets apply: expected exactly one changeset id")
		return ExitUsage
	}
	id := fs.Arg(0)
	jsonOut, code, ok := parseOutputFlagOrUsage(rf, "vnproxctl remote changesets apply", stderr)
	if !ok {
		return code
	}
	client, code := buildRemoteClient(rf, "vnproxctl remote changesets apply", stderr)
	if client == nil {
		return code
	}

	body := applyRequestBody{ConfirmTimeoutSec: *confirmTimeoutSec}
	if *mgmtAckNode != "" {
		body.MgmtAck = &mgmtAckBody{Node: *mgmtAckNode}
	}

	ctx, cancel := context.WithTimeout(context.Background(), *rf.timeout)
	defer cancel()
	var out changesetWire
	httpStatus, apiErr, err := client.doJSON(ctx, "POST", "/changesets/"+id+"/apply", body, &out)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl remote changesets apply: %v\n", err)
		return exitForErr(err)
	}
	if apiErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl remote changesets apply: %s: %s\n", apiErr.Code, apiErr.Message)
		return exitForAPIError(httpStatus)
	}

	if jsonOut {
		if err := writeJSONOut(stdout, out); err != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl remote changesets apply: %v\n", err)
			return ExitError
		}
		return ExitSuccess
	}
	printChangesetTable(stdout, out)
	return ExitSuccess
}

func runRemoteChangesetsDiscard(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl remote changesets discard", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rf := addRemoteFlags(fs)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, "vnproxctl remote changesets discard: expected exactly one changeset id")
		return ExitUsage
	}
	id := fs.Arg(0)
	jsonOut, code, ok := parseOutputFlagOrUsage(rf, "vnproxctl remote changesets discard", stderr)
	if !ok {
		return code
	}
	client, code := buildRemoteClient(rf, "vnproxctl remote changesets discard", stderr)
	if client == nil {
		return code
	}

	ctx, cancel := context.WithTimeout(context.Background(), *rf.timeout)
	defer cancel()
	httpStatus, apiErr, err := client.doJSON(ctx, "DELETE", "/changesets/"+id, nil, nil)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl remote changesets discard: %v\n", err)
		return exitForErr(err)
	}
	if apiErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl remote changesets discard: %s: %s\n", apiErr.Code, apiErr.Message)
		return exitForAPIError(httpStatus)
	}

	if jsonOut {
		if err := writeJSONOut(stdout, map[string]any{"id": id, "discarded": true}); err != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl remote changesets discard: %v\n", err)
			return ExitError
		}
		return ExitSuccess
	}
	_, _ = fmt.Fprintf(stdout, "Discarded changeset %s.\n", id)
	return ExitSuccess
}

// printChangesetTable renders one changeset's key fields plus its findings
// (the review-screen data an operator needs at a glance).
func printChangesetTable(w io.Writer, c changesetWire) {
	_, _ = fmt.Fprintf(w, "ID:               %s\n", c.ID)
	_, _ = fmt.Fprintf(w, "Title:            %s\n", c.Title)
	_, _ = fmt.Fprintf(w, "Author:           %s\n", c.Author)
	_, _ = fmt.Fprintf(w, "Status:           %s\n", c.Status)
	_, _ = fmt.Fprintf(w, "Touches mgmt path: %t\n", c.TouchesMgmtPath)
	_, _ = fmt.Fprintf(w, "Ops:              %d\n", len(c.Ops))
	if c.ConfirmDeadline != nil {
		_, _ = fmt.Fprintf(w, "Confirm deadline: %s\n", strconvItoa64(*c.ConfirmDeadline))
	}
	if len(c.Findings) == 0 {
		_, _ = fmt.Fprintln(w, "Findings:         none")
		return
	}
	_, _ = fmt.Fprintln(w, "Findings:")
	for _, f := range c.Findings {
		_, _ = fmt.Fprintf(w, "  [%s] %s: %s\n", f.Severity, f.Code, f.Message)
	}
}
