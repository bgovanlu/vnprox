// Command vnproxctl is the vnprox operator CLI documented in
// docs/deployment.md "Troubleshooting quick refs" and docs/user-guide.md §6:
// a root-only, daemon-independent escape hatch for status checks and
// disaster recovery (snapshot restore, forced rollback) when the web UI is
// unreachable.
//
// This is the T-006 skeleton (planning/tasks/phase-0.md#T-006): `status`
// talks to the local daemon's health endpoint for real; `snapshots` and
// `rollback-now` are documented stubs until T-206
// (planning/tasks/phase-2.md#T-206) lands the store-backed implementation,
// which T-606 (planning/tasks/phase-6.md#T-606) then wires into the final
// packaging.
package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
)

// version is vnproxctl's reported version (--version). Overridden at build
// time the same way as vnproxd: go build -ldflags "-X main.version=1.2.3".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run implements main's logic in a way that's testable without exec'ing a
// subprocess: it returns an exit code instead of calling os.Exit and takes
// explicit stdout/stderr writers, mirroring cmd/vnproxd's mainRun.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "-h", "--help", "help":
		printUsage(stdout)
		return 0
	case "--version":
		if _, err := fmt.Fprintf(stdout, "vnproxctl %s\n", version); err != nil {
			return 1
		}
		return 0
	case "status":
		return runStatus(args[1:], stdout, stderr)
	case "snapshots":
		return runSnapshots(args[1:], stdout, stderr)
	case "rollback-now":
		return runRollbackNow(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "vnproxctl: unknown command %q\n\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `vnproxctl - vnprox operator CLI

Usage:
  vnproxctl status                     Local daemon health/reachability check
  vnproxctl snapshots list             List time-machine snapshots
  vnproxctl snapshots restore <id>     Restore a snapshot (bypasses confirm)
  vnproxctl rollback-now <changeset>   Force rollback of an applied changeset
  vnproxctl --version                  Print the vnproxctl version
  vnproxctl --help                     Show this help

status flags:
  --config <path>   vnprox.toml to read the listen address from (default /etc/vnprox/vnprox.toml)
  --url <url>       health endpoint URL, overriding --config lookup
  --insecure        skip TLS verification (default true; see docs/deployment.md)
  --timeout <dur>   request timeout (default 5s)

See docs/deployment.md "Troubleshooting quick refs" for full semantics.
`)
}

// discardLogger returns a slog.Logger that drops everything, used when
// vnproxctl calls into internal/config purely to read values back out — an
// operator running a CLI status check should not see the daemon's own
// config-load log lines.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
