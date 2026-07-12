// debugpprof.go: an opt-in, off-by-default diagnostic listener for
// runtime.NumGoroutine()/pprof introspection, added for T-607's soak-test
// observability requirement (docs/development.md's Definition of Done asks
// for "measured, not estimated" numbers; the task card explicitly
// anticipates "runtime.NumGoroutine() via a debug endpoint if one already
// exists" — none did, so this is the minimal one).
//
// This is NOT part of docs/api.md's contract, is never mounted on the main
// TLS/8007 listener (no CSP/security-header interaction, no new
// capability/auth surface), and only starts at all when the operator sets
// VNPROX_DEBUG_PPROF_ADDR — a plain-HTTP loopback listener intended for a
// local soak-test run (testdata/genscale/soak), never production. Only
// stdlib (net/http/pprof) — no new dependency.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"runtime"
	"strconv"
)

// maybeStartDebugPprof starts the debug listener if VNPROX_DEBUG_PPROF_ADDR
// is set in the environment, returning a runGroup-compatible actor (nil if
// unset — the caller skips adding it). Exposes pprof's standard routes plus
// a tiny "/debug/goroutines" returning just the current count as plain
// text (runtime.NumGoroutine()), since that single number — not a full
// profile — is all the soak harness samples.
func maybeStartDebugPprof(logger *slog.Logger) func(ctx context.Context) error {
	addr := os.Getenv("VNPROX_DEBUG_PPROF_ADDR")
	if addr == "" {
		return nil
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.HandleFunc("/debug/goroutines", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(strconv.Itoa(runtime.NumGoroutine())))
	})

	srv := &http.Server{Addr: addr, Handler: mux}
	return func(ctx context.Context) error {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}
		logger.Warn("debug: pprof/goroutine-count listener started (soak-test diagnostic only, never production)", "addr", addr)
		errCh := make(chan error, 1)
		go func() { errCh <- srv.Serve(ln) }()
		select {
		case <-ctx.Done():
			_ = srv.Close()
			<-errCh
			return nil
		case err := <-errCh:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		}
	}
}
