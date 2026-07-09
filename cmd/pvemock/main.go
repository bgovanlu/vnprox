// Command pvemock runs the mock PVE API server (internal/pvemock) standalone,
// for local development and manual curl exploration. This is the entry
// point the Makefile's `mockpve` target (T-004) invokes; see
// internal/pvemock/README.md for the full curl walkthrough.
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

	"github.com/bgovanlu/vnprox/internal/pvemock"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "pvemock:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("pvemock", flag.ContinueOnError)
	addr := fs.String("addr", ":8006", "address to listen on")
	fixturePath := fs.String("fixture", "testdata/clusters/single-node.yaml", "path to a YAML cluster fixture")
	if err := fs.Parse(args); err != nil {
		return err
	}

	fixture, err := pvemock.LoadFixture(*fixturePath)
	if err != nil {
		return fmt.Errorf("loading fixture %s: %w", *fixturePath, err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	srv := pvemock.NewServer(fixture, pvemock.WithLogger(logger))

	httpServer := &http.Server{Addr: *addr, Handler: srv}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("pvemock listening", "addr", *addr, "fixture", *fixturePath)
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
