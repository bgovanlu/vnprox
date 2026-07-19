// Command k8smock runs the mock Kubernetes API server (internal/k8smock)
// standalone, on a fixed address — the Playwright e2e harness's own
// stand-in for a real k8s cluster (web/e2e/k8s-overlay.spec.ts, T-1502),
// mirroring cmd/pvemock's identical shape. internal/k8smock's own
// NewServer wraps its handler in an httptest.Server (random port,
// in-process-only — fine for Go tests, unusable from an external
// Playwright process), so this binary uses NewHandler directly against a
// real net/http.Server on the address the caller names instead.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/bgovanlu/vnprox/internal/k8smock"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "k8smock:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("k8smock", flag.ContinueOnError)
	addr := fs.String("addr", ":8008", "address to listen on")
	fixturePath := fs.String("fixture", "testdata/k8s/cluster-flannel.yaml", "path to a YAML k8smock fixture")
	if err := fs.Parse(args); err != nil {
		return err
	}

	fixture, err := k8smock.LoadFixtureFile(*fixturePath)
	if err != nil {
		return fmt.Errorf("loading fixture %s: %w", *fixturePath, err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	handler, _ := k8smock.NewHandler(fixture)

	httpServer := &http.Server{Addr: *addr, Handler: handler}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("k8smock listening", "addr", *addr, "fixture", *fixturePath)
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serving: %w", err)
		}
		return nil
	case <-ctx.Done():
		logger.Info("shutting down")
		return httpServer.Shutdown(context.Background())
	}
}
