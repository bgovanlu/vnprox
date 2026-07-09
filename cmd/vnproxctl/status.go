package main

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/bgovanlu/vnprox/internal/config"
)

// defaultConfigPath matches vnproxd's own --config default
// (cmd/vnproxd/main.go) and docs/deployment.md's documented location.
const defaultConfigPath = "/etc/vnprox/vnprox.toml"

// healthResponse mirrors internal/api's healthResponse
// (GET /api/v1/health -> {"status":"ok","version":"..."}). It is redefined
// here rather than imported because internal/api does not (and should not)
// export a client-side type for it; this is intentionally the only contract
// vnproxctl depends on for `status`.
type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// runStatus implements `vnproxctl status`: it hits the local daemon's
// /api/v1/health endpoint and reports reachability, version, and latency.
// Per docs/deployment.md, `vnproxctl status` is also documented to report
// "peer reachability, PVE API health, collector ages" — those land with the
// peer subsystem (T-301) and collectors (T-104) respectively, so this
// skeleton prints them as documented-but-not-yet-available lines rather
// than fabricating data.
func runStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfigPath, "path to vnprox.toml, used to find the listen address")
	url := fs.String("url", "", "override the health endpoint URL (skips --config lookup)")
	timeout := fs.Duration("timeout", 5*time.Second, "request timeout")
	insecure := fs.Bool("insecure", true, "skip TLS certificate verification")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	endpoint := *url
	if endpoint == "" {
		derived, err := healthEndpointFromConfig(*configPath)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl status: %v\n", err)
			return 1
		}
		endpoint = derived
	}

	client := &http.Client{
		Timeout: *timeout,
		Transport: &http.Transport{
			// InsecureSkipVerify is opt-out (--insecure=false), not opt-in:
			// the daemon serves the node's reused PVE certificate (per
			// docs/architecture.md §9), which is issued for the node's real
			// hostname, not for 127.0.0.1/localhost that this local CLI
			// check dials. Trust is instead anchored by running on the same
			// host as the daemon and reading its own config file.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: *insecure}, //nolint:gosec // see comment above; local operator health check, not a network client
		},
	}

	start := time.Now()
	resp, err := client.Get(endpoint)
	latency := time.Since(start)
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "Endpoint:            %s\n", endpoint)
		_, _ = fmt.Fprintf(stdout, "Reachable:           no (%v)\n", err)
		printStatusFooter(stdout)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()

	var health healthResponse
	decodeErr := json.NewDecoder(resp.Body).Decode(&health)

	_, _ = fmt.Fprintf(stdout, "Endpoint:            %s\n", endpoint)
	_, _ = fmt.Fprintln(stdout, "Reachable:           yes")
	_, _ = fmt.Fprintf(stdout, "HTTP status:         %s\n", resp.Status)
	_, _ = fmt.Fprintf(stdout, "Latency:             %s\n", latency.Round(time.Millisecond))
	if decodeErr != nil {
		_, _ = fmt.Fprintf(stdout, "Health payload:      unparseable (%v)\n", decodeErr)
		printStatusFooter(stdout)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "Daemon status:       %s\n", health.Status)
	_, _ = fmt.Fprintf(stdout, "Daemon version:      %s\n", health.Version)
	printStatusFooter(stdout)

	if resp.StatusCode != http.StatusOK || health.Status != "ok" {
		return 1
	}
	return 0
}

func printStatusFooter(stdout io.Writer) {
	_, _ = fmt.Fprintln(stdout, "Peer reachability:   available after T-301 (peer API)")
	_, _ = fmt.Fprintln(stdout, "PVE API health:      available after T-104 (collectors)")
	_, _ = fmt.Fprintln(stdout, "Collector ages:      available after T-104 (collectors)")
}

// healthEndpointFromConfig loads the daemon config to find its listen
// address and builds the https URL for /api/v1/health. It resolves
// wildcard bind addresses (0.0.0.0, ::, "") to 127.0.0.1 since vnproxctl is
// documented to always talk to "the local daemon" (docs/deployment.md).
func healthEndpointFromConfig(path string) (string, error) {
	cfg, err := config.Load(path, discardLogger())
	if err != nil {
		return "", fmt.Errorf("loading %s: %w", path, err)
	}

	host, port, err := net.SplitHostPort(cfg.Server.Listen)
	if err != nil {
		return "", fmt.Errorf("parsing server.listen %q: %w", cfg.Server.Listen, err)
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}

	return fmt.Sprintf("https://%s/api/v1/health", net.JoinHostPort(host, port)), nil
}
