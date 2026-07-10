// Command vnproxctl is the vnprox operator CLI documented in
// docs/deployment.md "Troubleshooting quick refs" and docs/user-guide.md §6:
// a root-only, daemon-independent escape hatch for status checks and
// disaster recovery (snapshot restore, forced rollback) when the web UI is
// unreachable.
//
// `status` talks to the local daemon's health endpoint; `snapshots
// list|restore` and `rollback-now` (T-206) are deliberately daemon-
// independent — direct SQLite reads of the daemon's own store (WAL mode
// permits this alongside a running daemon too), a local
// /etc/network/interfaces write with a timestamped backup, and a direct
// `ifreload -a` exec. No HTTP API is involved anywhere on that path: it
// must work when the daemon is stopped, because it *is* the documented
// recovery path for exactly that situation. T-606
// (planning/tasks/phase-6.md#T-606) wires this binary into the final
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
  vnproxctl snapshots list             List time-machine snapshots (direct DB read; works daemon-down)
  vnproxctl snapshots restore <id>     Restore this node's interfaces file from a snapshot and ifreload
                                       (root only; direct DB + file write, bypasses confirm — the
                                       documented disaster-recovery path, works daemon-down)
  vnproxctl rollback-now <changeset>   Force rollback of an in-flight (awaiting-confirm/applying)
                                       changeset from its pre-apply snapshot (root only, daemon-down)
  vnproxctl --version                  Print the vnproxctl version
  vnproxctl --help                     Show this help

status flags:
  --config <path>   vnprox.toml to read the listen address from (default /etc/vnprox/vnprox.toml)
  --url <url>       health endpoint URL, overriding --config lookup
  --insecure        skip TLS verification (default true; see docs/deployment.md)
  --timeout <dur>   request timeout (default 5s)

snapshots/rollback-now flags:
  --config <path>   vnprox.toml to read storage.db_path from (default /etc/vnprox/vnprox.toml)
  --node <name>     which captured node's file to restore (default: this host's name)
  --limit <n>       snapshots list: maximum rows (default 50)

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
