// Command vnproxtelemetryd runs T-3710's compatibility-telemetry
// collector: the one genuinely dynamic service T-3707's hosted-service
// decision requires. Every other hosted-service group (the plugin/
// blueprint registry, T-3709) is a static, GitHub-served signed index;
// this accepts the small opt-in payload internal/telemetry's shipped
// client produces, stores it, and answers "which PVE versions are vnprox
// installations actually running against" — without which that question
// stays anecdotal.
//
// Subcommands:
//
//	vnproxtelemetryd [serve]        run the HTTP collector (default)
//	vnproxtelemetryd report         print a summary of what has arrived
//	vnproxtelemetryd retention-run  run one retention pass now and exit
//	vnproxtelemetryd revoke         delete every submission for one install-id
//
// See docs/security.md, "Compatibility telemetry (T-2503)" → "The
// collector (T-3710)", for the complete privacy statement this binary is
// held to, and internal/telemetrycollector's package doc for the mechanism
// behind each promise in it.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/bgovanlu/vnprox/internal/telemetrycollector"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "vnproxtelemetryd:", err) //nolint:errcheck // best-effort final diagnostic on the way to os.Exit
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return runServe(args, stdout, stderr)
	}
	switch args[0] {
	case "serve":
		return runServe(args[1:], stdout, stderr)
	case "report":
		return runReport(args[1:], stdout, stderr)
	case "retention-run":
		return runRetentionOnce(args[1:], stdout, stderr)
	case "revoke":
		return runRevoke(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		printUsage(stdout)
		return nil
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `vnproxtelemetryd - T-3710 compatibility telemetry collector

  vnproxtelemetryd [serve] --db <path> [--addr :8443] [...]  Run the HTTP collector.
  vnproxtelemetryd report --db <path> [--json]                Print what has arrived.
  vnproxtelemetryd retention-run --db <path> [--window 720h]  Run one retention pass now and exit.
  vnproxtelemetryd revoke --db <path> --install-id <ulid>     Delete one install's submissions (offline form
                                                                of DELETE /v1/installs/<id> on a running server).

Every flag has a documented default; run any subcommand with -h to see them.
`)
}

// --- serve -----------------------------------------------------------------

func runServe(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("vnproxtelemetryd serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "vnproxtelemetryd.db", "path to the collector's SQLite database")
	addr := fs.String("addr", ":8443", "address to listen on")
	tlsCert := fs.String("tls-cert", "", "TLS certificate file (optional — leave both TLS flags empty to terminate TLS in a reverse proxy in front of this process)")
	tlsKey := fs.String("tls-key", "", "TLS private key file")
	maxBody := fs.Int64("max-body-bytes", telemetrycollector.DefaultMaxBodyBytes, "hard cap on one submission's request body size")
	perInstallCap := fs.Int("per-install-capacity", telemetrycollector.DefaultPerInstallCapacity, "burst capacity of the per-install-id rate limit")
	perInstallRefill := fs.Duration("per-install-refill", telemetrycollector.DefaultPerInstallRefill, "how often the per-install-id bucket regains one token")
	globalCap := fs.Int("global-capacity", telemetrycollector.DefaultGlobalCapacity, "burst capacity of the service-wide (IP-free) rate limit")
	globalRefill := fs.Duration("global-refill", telemetrycollector.DefaultGlobalRefill, "how often the service-wide bucket regains one token")
	retentionWindow := fs.Duration("retention-window", telemetrycollector.DefaultRetentionWindow, "how long a submission is kept before it is deleted")
	retentionInterval := fs.Duration("retention-interval", time.Hour, "how often the retention loop runs")
	readTimeout := fs.Duration("read-timeout", 10*time.Second, "http.Server ReadTimeout")
	writeTimeout := fs.Duration("write-timeout", 10*time.Second, "http.Server WriteTimeout")
	idleTimeout := fs.Duration("idle-timeout", 60*time.Second, "http.Server IdleTimeout")
	readHeaderTimeout := fs.Duration("read-header-timeout", 5*time.Second, "http.Server ReadHeaderTimeout")
	if err := fs.Parse(args); err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := telemetrycollector.Open(ctx, *dbPath)
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer func() { _ = store.Close() }()

	srv := telemetrycollector.NewServer(store,
		telemetrycollector.WithLogger(logger),
		telemetrycollector.WithMaxBodyBytes(*maxBody),
		telemetrycollector.WithPerInstallRateLimit(*perInstallCap, *perInstallRefill),
		telemetrycollector.WithGlobalRateLimit(*globalCap, *globalRefill),
	)

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           srv.Router(),
		ReadTimeout:       *readTimeout,
		WriteTimeout:      *writeTimeout,
		IdleTimeout:       *idleTimeout,
		ReadHeaderTimeout: *readHeaderTimeout,
	}

	retentionCtx, cancelRetention := context.WithCancel(ctx)
	defer cancelRetention()
	retentionDone := make(chan error, 1)
	go func() {
		retentionDone <- telemetrycollector.RunLoop(retentionCtx, store, *retentionInterval, *retentionWindow, logger)
	}()

	useTLS := *tlsCert != "" && *tlsKey != ""
	if (*tlsCert != "") != (*tlsKey != "") {
		return errors.New("--tls-cert and --tls-key must both be set, or both left empty")
	}
	if !useTLS {
		logger.Warn("vnproxtelemetryd: no --tls-cert/--tls-key given; serving plain HTTP. " +
			"docs/security.md documents telemetry as an https:// endpoint — terminate TLS in a reverse proxy in front of this process, or set both flags.")
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("vnproxtelemetryd listening", "addr", *addr, "db", *dbPath, "tls", useTLS, "retentionWindow", retentionWindow.String())
		if useTLS {
			errCh <- httpServer.ListenAndServeTLS(*tlsCert, *tlsKey)
		} else {
			errCh <- httpServer.ListenAndServe()
		}
	}()

	select {
	case serveErr := <-errCh:
		cancelRetention()
		<-retentionDone
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return fmt.Errorf("serving: %w", serveErr)
		}
		return nil
	case <-ctx.Done():
		logger.Info("vnproxtelemetryd shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		shutdownErr := httpServer.Shutdown(shutdownCtx)
		cancelRetention()
		<-retentionDone
		if shutdownErr != nil {
			return fmt.Errorf("shutting down: %w", shutdownErr)
		}
		_, _ = fmt.Fprintln(stdout, "vnproxtelemetryd: stopped")
		return nil
	}
}

// --- report ------------------------------------------------------------

func runReport(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("vnproxtelemetryd report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "vnproxtelemetryd.db", "path to the collector's SQLite database")
	asJSON := fs.Bool("json", false, "print the summary as JSON instead of a text report")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	store, err := telemetrycollector.Open(ctx, *dbPath)
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer func() { _ = store.Close() }()

	sum, err := store.BuildSummary(ctx, time.Now())
	if err != nil {
		return fmt.Errorf("building summary: %w", err)
	}

	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(sum); err != nil {
			return fmt.Errorf("encoding summary: %w", err)
		}
		return nil
	}
	printSummaryText(stdout, sum)
	return nil
}

func printSummaryText(w io.Writer, sum telemetrycollector.Summary) {
	_, _ = fmt.Fprintf(w, "submissions:        %d\n", sum.TotalSubmissions)
	_, _ = fmt.Fprintf(w, "distinct installs:  %d\n", sum.DistinctInstalls)
	if sum.TotalSubmissions == 0 {
		_, _ = fmt.Fprintln(w, "(nothing has arrived yet)")
		return
	}
	_, _ = fmt.Fprintf(w, "oldest:             %s\n", sum.OldestReceivedAt.Format(time.RFC3339))
	_, _ = fmt.Fprintf(w, "newest:             %s\n", sum.NewestReceivedAt.Format(time.RFC3339))

	_, _ = fmt.Fprintln(w, "\nPVE versions:")
	for _, k := range sortedKeys(sum.PVEVersions) {
		_, _ = fmt.Fprintf(w, "  %-30s %d\n", k, sum.PVEVersions[k])
	}
	_, _ = fmt.Fprintln(w, "\nvnprox versions:")
	for _, k := range sortedKeys(sum.VnproxVersions) {
		_, _ = fmt.Fprintf(w, "  %-30s %d\n", k, sum.VnproxVersions[k])
	}
	_, _ = fmt.Fprintln(w, "\nsuites:")
	for _, k := range sortedKeys(sum.Suites) {
		_, _ = fmt.Fprintf(w, "  %-30s %d\n", k, sum.Suites[k])
	}
	_, _ = fmt.Fprintln(w, "\nchecks (pass/fail/skip):")
	for _, c := range sum.Checks {
		_, _ = fmt.Fprintf(w, "  %-40s %d/%d/%d\n", c.CheckID, c.Pass, c.Fail, c.Skip)
	}
}

func sortedKeys(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// --- retention-run -------------------------------------------------------

func runRetentionOnce(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("vnproxtelemetryd retention-run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "vnproxtelemetryd.db", "path to the collector's SQLite database")
	window := fs.Duration("window", telemetrycollector.DefaultRetentionWindow, "delete submissions received before now minus this window (shorten this to demonstrate retention without waiting)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	store, err := telemetrycollector.Open(ctx, *dbPath)
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer func() { _ = store.Close() }()

	before, err := store.Count(ctx)
	if err != nil {
		return fmt.Errorf("counting submissions before retention: %w", err)
	}
	deleted, err := telemetrycollector.RunOnce(ctx, store, time.Now(), *window)
	if err != nil {
		return fmt.Errorf("running retention: %w", err)
	}
	after, err := store.Count(ctx)
	if err != nil {
		return fmt.Errorf("counting submissions after retention: %w", err)
	}
	_, _ = fmt.Fprintf(stdout, "retention: window=%s before=%d deleted=%d after=%d\n", window.String(), before, deleted, after)
	return nil
}

// --- revoke ----------------------------------------------------------------

func runRevoke(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("vnproxtelemetryd revoke", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "vnproxtelemetryd.db", "path to the collector's SQLite database")
	installID := fs.String("install-id", "", "the install-id whose submissions should be deleted (see `vnproxctl telemetry status`)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *installID == "" {
		return errors.New("--install-id is required")
	}

	ctx := context.Background()
	store, err := telemetrycollector.Open(ctx, *dbPath)
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer func() { _ = store.Close() }()

	n, err := store.DeleteByInstallID(ctx, *installID)
	if err != nil {
		return fmt.Errorf("revoking install-id %s: %w", *installID, err)
	}
	_, _ = fmt.Fprintf(stdout, "deleted %d submission(s) for install-id %s\n", n, *installID)
	return nil
}
