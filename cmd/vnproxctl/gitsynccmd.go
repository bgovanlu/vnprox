// gitsynccmd.go implements `vnproxctl gitsync status` (T-2701): what the
// git-backed spec sync last fetched, what it planned, and why the draft
// changeset it opened exists.
//
//	vnproxctl gitsync status [-o json]
//
// It is a read against the running daemon (`GET /gitsync/status`, bearer
// token, like `vnproxctl policy test` and the `remote` family), because the
// answer lives in the daemon's own poll state — a local re-implementation
// would answer a different question than the one the daemon is acting on.
//
// There is deliberately no `vnproxctl gitsync sync` and no `gitsync apply`.
// A sync draft is an ordinary changeset: it is reviewed and applied through
// the ordinary surfaces, by a human, with the ordinary commit-confirm
// window. Nothing in this file mutates anything.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/bgovanlu/vnprox/internal/gitsync"
)

func runGitSync(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "vnproxctl gitsync: expected a subcommand (status)")
		return ExitUsage
	}
	switch args[0] {
	case "status":
		return runGitSyncStatus(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "vnproxctl gitsync: unknown subcommand %q\n", args[0])
		return ExitUsage
	}
}

func runGitSyncStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl gitsync status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rf := addRemoteFlags(fs)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	jsonOut, code, ok := parseOutputFlagOrUsage(rf, "vnproxctl gitsync status", stderr)
	if !ok {
		return code
	}

	client, code := buildRemoteClient(rf, "vnproxctl gitsync status", stderr)
	if client == nil {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), *rf.timeout)
	defer cancel()

	var status gitsync.Status
	httpStatus, apiErr, err := client.doJSON(ctx, "GET", "/gitsync/status", nil, &status)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl gitsync status: %v\n", err)
		return exitForErr(err)
	}
	if apiErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl gitsync status: %s: %s\n", apiErr.Code, apiErr.Message)
		return exitForAPIError(httpStatus)
	}

	if jsonOut {
		if werr := writeJSONOut(stdout, status); werr != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl gitsync status: %v\n", werr)
			return ExitError
		}
		return ExitSuccess
	}
	printGitSyncStatus(stdout, status)
	return ExitSuccess
}

// printGitSyncStatus renders the table form. It answers the card's three
// questions in order — last fetched sha, last plan, why the draft exists —
// and says plainly, every time, that vnprox did not apply anything.
func printGitSyncStatus(w io.Writer, s gitsync.Status) {
	if !s.Enabled {
		_, _ = fmt.Fprintln(w, "git spec sync: disabled ([gitsync] enabled = false)")
		return
	}
	_, _ = fmt.Fprintf(w, "git spec sync: enabled\n")
	_, _ = fmt.Fprintf(w, "  remote           %s\n", s.Remote)
	_, _ = fmt.Fprintf(w, "  ref / path       %s : %s\n", s.Ref, s.Path)
	_, _ = fmt.Fprintf(w, "  poll interval    %ds\n", s.PollIntervalSeconds)
	_, _ = fmt.Fprintf(w, "  signed commits   %s\n", yesNo(s.RequireSignedCommits))
	_, _ = fmt.Fprintf(w, "  last fetched     %s%s\n", orDash(s.LastFetchedSHA), atSuffix(s.LastFetchAt))
	if s.LastSigner != "" {
		_, _ = fmt.Fprintf(w, "  verified signer  %s\n", s.LastSigner)
	}
	if s.LastError != "" {
		_, _ = fmt.Fprintf(w, "  last error       %s\n", s.LastError)
	}

	_, _ = fmt.Fprintf(w, "\nlast plan: %d op(s)\n", s.PlanOpCount)
	for _, line := range s.Plan {
		_, _ = fmt.Fprintf(w, "  %s\n", line)
	}
	if len(s.NotInSpec) > 0 {
		_, _ = fmt.Fprintf(w, "\npresent live but absent from the spec (reported, never deleted):\n")
		for _, ref := range s.NotInSpec {
			_, _ = fmt.Fprintf(w, "  %s\n", ref)
		}
	}

	_, _ = fmt.Fprintf(w, "\nopen sync changeset: %s\n", orDash(s.OpenChangesetID))
	if s.OpenChangesetReason != "" {
		_, _ = fmt.Fprintf(w, "  %s\n", s.OpenChangesetReason)
	}
	if len(s.Issues) > 0 {
		_, _ = fmt.Fprintf(w, "\nfindings:\n")
		for _, iss := range s.Issues {
			_, _ = fmt.Fprintf(w, "  [%s] %s: %s\n", iss.Severity, iss.Check, iss.Detail)
		}
	}
	_, _ = fmt.Fprintln(w, "\nvnprox stages; it never applies a sync draft. Review and apply it like any other changeset.")
}

func yesNo(b bool) string {
	if b {
		return "required"
	}
	return "not required"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func atSuffix(unix int64) string {
	if unix == 0 {
		return ""
	}
	return " (" + time.Unix(unix, 0).UTC().Format(time.RFC3339) + ")"
}
