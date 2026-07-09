package main

import (
	"fmt"
	"io"
)

// notYetImplemented prints the documented "available after <task>" message
// for a subcommand whose real implementation lands in a later task, and
// returns a non-zero exit code: the command is recognized (it is not a
// usage error) but does not yet do the thing it's documented to do, and
// scripts calling it should not treat that as success.
func notYetImplemented(stdout io.Writer, command, task, what string) int {
	_, _ = fmt.Fprintf(stdout, "vnproxctl %s: available after %s (%s)\n", command, task, what)
	return 1
}

// runSnapshots implements `vnproxctl snapshots list|restore`. Both are
// stubs pending T-206 (planning/tasks/phase-2.md#T-206), which owns the
// zstd-backed snapshot store and the documented "daemon-independent
// restore" disaster path (direct DB + file write + ifreload, no HTTP API
// involved) — see docs/deployment.md "Troubleshooting quick refs".
func runSnapshots(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "vnproxctl snapshots: expected a subcommand (list, restore <id>)")
		return 2
	}
	switch args[0] {
	case "list":
		return notYetImplemented(stdout, "snapshots list", "T-206", "snapshots, time machine, audit UI")
	case "restore":
		return notYetImplemented(stdout, "snapshots restore", "T-206", "snapshots, time machine, audit UI")
	default:
		_, _ = fmt.Fprintf(stderr, "vnproxctl snapshots: unknown subcommand %q\n", args[0])
		return 2
	}
}

// runRollbackNow implements `vnproxctl rollback-now <changeset-id>`, the
// documented CLI escape hatch to force a rollback when the UI is
// unreachable. It is a stub pending T-206, same as snapshots above.
func runRollbackNow(_ []string, stdout, _ io.Writer) int {
	return notYetImplemented(stdout, "rollback-now", "T-206", "snapshots, time machine, audit UI")
}
