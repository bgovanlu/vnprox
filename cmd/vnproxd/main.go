// SPDX-License-Identifier: Apache-2.0

// Command vnproxd is the vnprox daemon: it loads config, serves the HTTPS
// UI/API (with the embedded frontend and a security-hardened middleware
// stack), and shuts down gracefully on SIGTERM/SIGINT. See
// planning/tasks/phase-0.md#T-002 and docs/architecture.md §2/§9.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

// version is the daemon's reported version (GET /api/v1/health and
// --version). Overridden at build time:
//
//	go build -ldflags "-X main.version=1.2.3"
var version = "dev"

func main() {
	os.Exit(mainRun(os.Args[1:], os.Stdout, os.Stderr))
}

// mainRun implements main's logic in a way that's testable without
// exec'ing a subprocess: it returns an exit code instead of calling
// os.Exit, and takes explicit stdout/stderr writers.
func mainRun(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxd", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "/etc/vnprox/vnprox.toml", "path to the vnprox config file (TOML)")
	showVersion := fs.Bool("version", false, "print the vnproxd version and exit")
	// T-2801: demo mode. A flag and not a config key on purpose — see
	// config.Config.Demo. `--demo` alone needs no configuration at all;
	// `--demo --config X` is for a harness that has to choose the listen
	// port and store path, and X must not configure a PVE endpoint.
	demoMode := fs.Bool("demo", false, "run against the embedded synthetic cluster: no Proxmox VE endpoint, no outbound network, every mutating API a no-op that reports what it would have done")
	demoDir := fs.String("demo-dir", "", "where `--demo` keeps its config, store and throwaway TLS keypair (default: $XDG_STATE_HOME/vnprox-demo). Ignored when --config is given.")
	// T-2802: the hosted read-only demo. Strictly an addition to --demo, not
	// an alternative to it — see the refusal below.
	publicDemo := fs.Bool("public-demo", false, "serve a public, read-only demo: every mutating route is refused at the edge, each visitor gets their own session, and per-visitor resource caps apply. Requires --demo.")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// A public demo is a demo with an edge in front of it. Allowing
	// --public-demo alone would mean an operator could put a read-only
	// façade in front of a daemon that still holds real PVE credentials and
	// believe it safe — the edge refuses writes, but the daemon behind it
	// can still reach the cluster, and that is one misconfigured route away
	// from mattering. Demo mode is what makes "there is nothing real behind
	// this" true; the edge only makes "and you cannot write to it" true.
	if *publicDemo && !*demoMode {
		if _, err := fmt.Fprintln(stderr, "vnproxd: --public-demo requires --demo: a public instance must have nothing real behind it"); err != nil {
			return 1
		}
		return 2
	}

	if *showVersion {
		// Direct stdout write, not a log line: this is CLI --version
		// output (like `go version`), not daemon operational logging.
		if _, err := fmt.Fprintf(stdout, "vnproxd %s\n", version); err != nil {
			return 1
		}
		return 0
	}

	logger := slog.New(slog.NewJSONHandler(stderr, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	opts := daemonOptions{ConfigPath: *configPath, Demo: *demoMode, DemoDir: *demoDir, PublicDemo: *publicDemo}
	if *demoMode && !isFlagSet(fs, "config") {
		// The zero-argument demo form: there is no config file yet, and
		// /etc/vnprox/vnprox.toml (this flag's default) is emphatically not
		// one a demo may read — it is a real node's real configuration.
		opts.ConfigPath = ""
	}

	if err := runDaemon(ctx, opts, logger); err != nil {
		logger.Error("vnproxd exited with error", "error", err)
		return 1
	}
	return 0
}

// daemonOptions is how the daemon was started: which config (if any), and
// whether this is a demo.
type daemonOptions struct {
	ConfigPath string
	DemoDir    string
	Demo       bool
	// PublicDemo (T-2802) puts internal/publicdemo's edge in front of the
	// whole handler. Only ever true alongside Demo.
	PublicDemo bool
}

// isFlagSet reports whether name was given on the command line, as opposed
// to left at its default. flag.FlagSet has no accessor for this; Visit
// walks only the flags actually set.
func isFlagSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}
