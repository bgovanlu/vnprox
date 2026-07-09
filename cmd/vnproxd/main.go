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
	if err := fs.Parse(args); err != nil {
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

	if err := runDaemon(ctx, *configPath, logger); err != nil {
		logger.Error("vnproxd exited with error", "error", err)
		return 1
	}
	return 0
}
