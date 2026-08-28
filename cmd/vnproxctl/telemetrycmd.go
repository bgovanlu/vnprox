// SPDX-License-Identifier: Apache-2.0

package main

// `vnproxctl telemetry` (T-2503): the opt-in compatibility report, and the
// commands that let an operator see exactly what it is before deciding.
//
// The command family is deliberately local and daemon-independent — it reads
// [telemetry] and [storage] out of vnprox.toml directly, the same way
// `snapshots`/`rollback-now` do, so a machine whose daemon is down or whose
// TLS certificate is the very thing broken can still preview, send or reset.
//
// Four subcommands, and the order they are listed in is the order an
// operator meets them:
//
//	preview   print the exact bytes that would be sent, and send nothing
//	status    is it on, where would it go, what is my install-id
//	send      submit one report, in the foreground, and say what happened
//	reset-id  throw away the correlator
//
// `preview` is the trust surface and is not optional: it prints the same
// buffer `send` posts (internal/telemetry.Snapshot), so the two cannot
// drift into "what we show you" and "what we send".

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"

	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/telemetry"
	"github.com/bgovanlu/vnprox/internal/verify"
)

// telemetryTransport is a test seam. It is nil in every shipped build, which
// means http.DefaultTransport; tests set it to a transport that fails if it
// is called, which is the only way to assert "nothing was sent" without
// trusting the code under test to have read its own config correctly.
var telemetryTransport http.RoundTripper

func runTelemetry(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printTelemetryUsage(stderr)
		return ExitUsage
	}
	switch args[0] {
	case "-h", "--help", "help":
		printTelemetryUsage(stdout)
		return ExitSuccess
	case "preview":
		return runTelemetryPreview(args[1:], stdout, stderr)
	case "status":
		return runTelemetryStatus(args[1:], stdout, stderr)
	case "send":
		return runTelemetrySend(args[1:], stdout, stderr)
	case "reset-id":
		return runTelemetryResetID(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "vnproxctl telemetry: unknown subcommand %q\n", args[0])
		printTelemetryUsage(stderr)
		return ExitUsage
	}
}

func printTelemetryUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `vnproxctl telemetry - opt-in compatibility reporting (off by default)

  vnproxctl telemetry preview --report <file>   Print the exact bytes that would be sent. Sends
                                                nothing and does not need telemetry to be enabled.
  vnproxctl telemetry status                    Whether telemetry is on, where it would go, and
                                                this install's correlator id (if one exists yet).
  vnproxctl telemetry send --report <file>      Submit one report. Refuses unless [telemetry]
                                                enabled = true and an https endpoint is configured.
  vnproxctl telemetry reset-id                  Replace the install-id with a new random ULID. The
                                                old one is not recorded anywhere.

<file> is a report written by `+"`vnproxctl verify --out <file>`"+` (signed) or by
`+"`vnproxctl verify -o json`"+` (plain). See docs/security.md, "Compatibility
telemetry (T-2503)", for the complete list of what is collected.
`)
}

// --- preview ---------------------------------------------------------------

func runTelemetryPreview(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl telemetry preview", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfigPath, "path to vnprox.toml (for storage.db_path)")
	reportPath := fs.String("report", "", "a report written by `vnproxctl verify --out` or `verify -o json`")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if *reportPath == "" {
		_, _ = fmt.Fprintf(stderr, "vnproxctl telemetry preview: --report is required. There is nothing to preview without a `vnproxctl verify` report to reduce.\n")
		return ExitUsage
	}

	snap, code := buildTelemetrySnapshot(*configPath, *reportPath, stdout, stderr, "preview")
	if code != ExitSuccess {
		return code
	}
	// The whole point of this command: the bytes, and nothing wrapped
	// around them.
	if err := snap.Preview(stdout); err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl telemetry preview: %v\n", err)
		return ExitError
	}
	return ExitSuccess
}

// --- status ----------------------------------------------------------------

// telemetryStatus is `telemetry status -o json`'s shape.
type telemetryStatus struct {
	Endpoint  string `json:"endpoint"`
	InstallID string `json:"installId"`
	Enabled   bool   `json:"enabled"`
}

func runTelemetryStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl telemetry status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfigPath, "path to vnprox.toml")
	output := fs.String("o", defaultOutputFormat, outputFlagUsage)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	asJSON, err := parseOutputFormat(*output)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl telemetry status: %v\n", err)
		return ExitUsage
	}

	cfg, err := config.LoadTelemetryOnly(*configPath, discardLogger())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl telemetry status: %v\n", err)
		return ExitUsage
	}

	st := telemetryStatus{Enabled: cfg.Enabled, Endpoint: cfg.Endpoint}
	// Peek, never Ensure: asking whether an id exists must not be the thing
	// that creates one.
	ctx := context.Background()
	if db, dbErr := openStore(ctx, *configPath); dbErr == nil {
		defer func() { _ = db.Close() }()
		if id, idErr := telemetry.PeekInstallID(ctx, db); idErr == nil {
			st.InstallID = id
		}
	}

	if asJSON {
		if err := writeJSONOut(stdout, st); err != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl telemetry status: %v\n", err)
			return ExitError
		}
		return ExitSuccess
	}

	state := "disabled (nothing is sent, no endpoint is contacted)"
	if cfg.Enabled {
		state = "ENABLED"
	}
	_, _ = fmt.Fprintf(stdout, "telemetry:  %s\n", state)
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = "(none configured — vnprox ships no default collector)"
	}
	_, _ = fmt.Fprintf(stdout, "endpoint:   %s\n", endpoint)
	installID := st.InstallID
	if installID == "" {
		installID = "(none yet — one is generated on the first preview or send)"
	}
	_, _ = fmt.Fprintf(stdout, "install-id: %s\n", installID)
	_, _ = fmt.Fprintf(stdout, "\nWhat would be sent is listed in docs/security.md and printed in full by\n`vnproxctl telemetry preview --report <file>`.\n")
	return ExitSuccess
}

// --- send ------------------------------------------------------------------

func runTelemetrySend(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl telemetry send", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfigPath, "path to vnprox.toml")
	reportPath := fs.String("report", "", "a report written by `vnproxctl verify --out` or `verify -o json`")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if *reportPath == "" {
		_, _ = fmt.Fprintf(stderr, "vnproxctl telemetry send: --report is required\n")
		return ExitUsage
	}

	// The opt-in gate comes FIRST, before the report is read, before the
	// store is opened and long before an HTTP client could exist. A disabled
	// install does not merely skip the request; it never gets far enough to
	// build one.
	cfg, err := config.LoadTelemetryOnly(*configPath, discardLogger())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl telemetry send: %v\n", err)
		return ExitUsage
	}
	if !cfg.Enabled {
		_, _ = fmt.Fprintf(stderr, "vnproxctl telemetry send: %v. Set [telemetry] enabled = true and an https endpoint in %s first; `vnproxctl telemetry preview --report %s` shows exactly what that would send.\n",
			telemetry.ErrDisabled, *configPath, *reportPath)
		return ExitUsage
	}
	if cfg.Endpoint == "" {
		_, _ = fmt.Fprintf(stderr, "vnproxctl telemetry send: %v\n", telemetry.ErrNoEndpoint)
		return ExitUsage
	}

	snap, code := buildTelemetrySnapshot(*configPath, *reportPath, stdout, stderr, "send")
	if code != ExitSuccess {
		return code
	}

	dst := telemetry.Destination{
		Enabled:   cfg.Enabled,
		Endpoint:  cfg.Endpoint,
		Transport: telemetryTransport,
	}
	if err := telemetry.Submit(context.Background(), dst, snap); err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl telemetry send: %v\n", err)
		if isTelemetryNetworkError(err) {
			return ExitNetwork
		}
		return ExitError
	}
	_, _ = fmt.Fprintf(stdout, "sent %d bytes to %s\n", len(snap.Bytes()), cfg.Endpoint)
	return ExitSuccess
}

// isTelemetryNetworkError distinguishes "could not reach the collector"
// from "the collector said no", because a CI pipeline retries those
// differently — the same distinction exitcodes.go already draws for the
// daemon-facing commands.
func isTelemetryNetworkError(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var urlErr *url.Error
	return errors.As(err, &urlErr)
}

// --- reset-id --------------------------------------------------------------

func runTelemetryResetID(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl telemetry reset-id", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfigPath, "path to vnprox.toml (for storage.db_path)")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	ctx := context.Background()
	db, err := openStore(ctx, *configPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl telemetry reset-id: %v\n", err)
		return ExitError
	}
	defer func() { _ = db.Close() }()

	id, err := telemetry.ResetInstallID(ctx, db)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl telemetry reset-id: %v\n", err)
		return ExitError
	}
	// The previous id is deliberately not printed, logged or audited: an
	// operator resetting their correlator is asking for it to be gone.
	_, _ = fmt.Fprintf(stdout, "install-id is now %s\nThe previous id was deleted and recorded nowhere.\n", id)
	return ExitSuccess
}

// --- shared ----------------------------------------------------------------

// buildTelemetrySnapshot is the one path both preview and send take to get
// bytes. Having a single one is what makes "preview prints what send posts"
// true of the COMMANDS, not just of the package.
func buildTelemetrySnapshot(configPath, reportPath string, stdout, stderr io.Writer, verb string) (*telemetry.Snapshot, int) {
	rep, err := loadVerifyReport(reportPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl telemetry %s: %v\n", verb, err)
		return nil, ExitUsage
	}

	ctx := context.Background()
	db, err := openStore(ctx, configPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl telemetry %s: %v\n", verb, err)
		return nil, ExitError
	}
	defer func() { _ = db.Close() }()

	installID, created, err := telemetry.EnsureInstallID(ctx, db)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl telemetry %s: %v\n", verb, err)
		return nil, ExitError
	}
	if created {
		// Something now exists on this machine that did not before. Say so
		// on stderr, so it is visible without polluting the bytes stdout
		// carries.
		_, _ = fmt.Fprintf(stderr, "vnproxctl telemetry %s: generated a new install-id (%s); reset it any time with `vnproxctl telemetry reset-id`\n", verb, installID)
	}

	snap, err := telemetry.Build(rep, installID)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl telemetry %s: %v\n", verb, err)
		return nil, ExitError
	}
	return snap, ExitSuccess
}

// loadVerifyReport reads either artifact `vnproxctl verify` can produce: the
// signed one from --out (preferred — its signature is checked) or the plain
// JSON from -o json.
func loadVerifyReport(path string) (verify.Report, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the path is the operator's own --report
	if err != nil {
		return verify.Report{}, fmt.Errorf("reading %s: %w", path, err)
	}
	if rep, _, signedErr := verify.ParseSignedReport(raw); signedErr == nil {
		return rep, nil
	}
	var rep verify.Report
	if err := json.Unmarshal(raw, &rep); err != nil {
		return verify.Report{}, fmt.Errorf("%s is neither a signed report artifact nor a `verify -o json` report: %w", path, err)
	}
	if err := rep.Validate(); err != nil {
		return verify.Report{}, fmt.Errorf("%s is not a valid verify report: %w", path, err)
	}
	return rep, nil
}

// startVerifyTelemetry is `vnproxctl verify`'s hook, and every line of it is
// about something not happening.
//
// It returns nil — having read nothing but the config — when telemetry is
// off, which is the shipped state. When it is on, it starts the send in the
// background and returns immediately: the returned channel is for tests and
// nobody in the command path waits on it, because AC5 is that a send never
// blocks or delays a verify run. A collector that accepts the connection and
// hangs forever costs the operator nothing.
//
// It logs nowhere, deliberately: the goroutine can outlive the command, and
// writing to the command's own stderr from it would be a data race. The
// diagnosable path is the foreground `vnproxctl telemetry send`.
func startVerifyTelemetry(configPath string, rep verify.Report) <-chan error {
	cfg, err := config.LoadTelemetryOnly(configPath, discardLogger())
	if err != nil || !cfg.Enabled || cfg.Endpoint == "" {
		return nil
	}

	ctx := context.Background()
	db, err := openStore(ctx, configPath)
	if err != nil {
		return nil
	}
	installID, _, err := telemetry.EnsureInstallID(ctx, db)
	closeErr := db.Close()
	if err != nil || closeErr != nil {
		return nil
	}

	snap, err := telemetry.Build(rep, installID)
	if err != nil {
		// A mock run, or a payload the guard refused. Either way this run
		// is not sent, and the verify command it is attached to is not
		// affected in any way.
		return nil
	}
	return telemetry.Start(ctx, telemetry.Destination{
		Enabled:   cfg.Enabled,
		Endpoint:  cfg.Endpoint,
		Transport: telemetryTransport,
	}, snap, nil)
}
