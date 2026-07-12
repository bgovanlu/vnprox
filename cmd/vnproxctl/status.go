package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/peer"
	"github.com/bgovanlu/vnprox/internal/pve"
)

// defaultConfigPath matches vnproxd's own --config default
// (cmd/vnproxd/main.go) and docs/deployment.md's documented location.
const defaultConfigPath = "/etc/vnprox/vnprox.toml"

// healthResponse mirrors internal/api's healthResponse (GET
// /api/v1/health -> {"status":"ok","version":"...","collectors":[...]}).
// Redefined here rather than imported because internal/api does not (and
// should not) export a client-side type for it; this is intentionally the
// only contract vnproxctl depends on for `status`. collectorSourceStatus
// mirrors internal/api.CollectorSourceStatus field-for-field.
type healthResponse struct {
	Status     string                  `json:"status"`
	Version    string                  `json:"version"`
	Collectors []collectorSourceStatus `json:"collectors,omitempty"`
}

type collectorSourceStatus struct {
	LastSuccess         time.Time `json:"last_success,omitempty"`
	LastAttempt         time.Time `json:"last_attempt,omitempty"`
	Name                string    `json:"name"`
	Node                string    `json:"node,omitempty"`
	LastError           string    `json:"last_error,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures,omitempty"`
}

// runStatus implements `vnproxctl status` per docs/deployment.md
// "Troubleshooting quick refs": "vnproxctl status — local daemon, peer
// reachability, PVE API health, collector ages." The local-daemon check
// hits this node's own /api/v1/health (which already reports collector
// staleness, T-104); peer reachability and PVE API health are each probed
// directly by this command against the daemon's own config file, rather
// than routed through the daemon's HTTP API, so `status` still reports
// something useful about the surrounding cluster/PVE reachability even if
// the local daemon itself is down (only the "local daemon" section
// requires it to be up).
func runStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vnproxctl status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfigPath, "path to vnprox.toml, used to find the listen address")
	url := fs.String("url", "", "override the health endpoint URL (skips --config lookup for the local-daemon check only)")
	timeout := fs.Duration("timeout", 5*time.Second, "request timeout (applies to every check: local daemon, PVE API, each peer)")
	insecure := fs.Bool("insecure", true, "skip TLS certificate verification")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Best-effort config load: needed for the PVE/peer checks below and
	// (absent --url) for the local-daemon endpoint too. A load failure
	// only fails the whole command when there is no --url override to
	// fall back on for the local-daemon check.
	cfg, cfgErr := config.Load(*configPath, discardLogger())

	endpoint := *url
	if endpoint == "" {
		if cfgErr != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl status: loading %s: %v\n", *configPath, cfgErr)
			return 1
		}
		derived, err := healthEndpointFromListen(cfg.Server.Listen)
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

	exitCode := 0

	start := time.Now()
	resp, err := client.Get(endpoint)
	latency := time.Since(start)
	var health healthResponse
	switch {
	case err != nil:
		_, _ = fmt.Fprintf(stdout, "Endpoint:            %s\n", endpoint)
		_, _ = fmt.Fprintf(stdout, "Reachable:           no (%v)\n", err)
		exitCode = 1
	default:
		func() {
			defer func() { _ = resp.Body.Close() }()
			decodeErr := json.NewDecoder(resp.Body).Decode(&health)

			_, _ = fmt.Fprintf(stdout, "Endpoint:            %s\n", endpoint)
			_, _ = fmt.Fprintln(stdout, "Reachable:           yes")
			_, _ = fmt.Fprintf(stdout, "HTTP status:         %s\n", resp.Status)
			_, _ = fmt.Fprintf(stdout, "Latency:             %s\n", latency.Round(time.Millisecond))
			if decodeErr != nil {
				_, _ = fmt.Fprintf(stdout, "Health payload:      unparseable (%v)\n", decodeErr)
				exitCode = 1
				return
			}
			_, _ = fmt.Fprintf(stdout, "Daemon status:       %s\n", health.Status)
			_, _ = fmt.Fprintf(stdout, "Daemon version:      %s\n", health.Version)
			if resp.StatusCode != http.StatusOK || health.Status != "ok" {
				exitCode = 1
			}
		}()
	}

	printCollectorAges(stdout, health.Collectors)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	printPVEHealth(ctx, stdout, cfg, cfgErr)
	printPeerReachability(ctx, stdout, cfg, cfgErr, *timeout)

	return exitCode
}

// printCollectorAges renders GET /api/v1/health's "collectors" field
// (staleness per poll-loop/node source, T-104/T-303) as the "collector
// ages" docs/deployment.md's status line promises. Sorted by (name, node)
// for stable, diffable output.
func printCollectorAges(stdout io.Writer, sources []collectorSourceStatus) {
	_, _ = fmt.Fprintln(stdout, "Collector ages:")
	if len(sources) == 0 {
		_, _ = fmt.Fprintln(stdout, "  (none reported — local daemon unreachable, or its collectors have not completed a first poll yet)")
		return
	}
	sorted := make([]collectorSourceStatus, len(sources))
	copy(sorted, sources)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Name != sorted[j].Name {
			return sorted[i].Name < sorted[j].Name
		}
		return sorted[i].Node < sorted[j].Node
	})
	for _, s := range sorted {
		label := s.Name
		if s.Node != "" {
			label = fmt.Sprintf("%s (%s)", s.Name, s.Node)
		}
		age := "never succeeded"
		if !s.LastSuccess.IsZero() {
			age = fmt.Sprintf("last success %s ago", time.Since(s.LastSuccess).Round(time.Second))
		}
		line := fmt.Sprintf("  %-24s %s", label, age)
		if s.ConsecutiveFailures > 0 {
			line += fmt.Sprintf(", %d consecutive failure(s)", s.ConsecutiveFailures)
			if s.LastError != "" {
				line += fmt.Sprintf(" (%s)", s.LastError)
			}
		}
		_, _ = fmt.Fprintln(stdout, line)
	}
}

// buildStatusPVEClient builds the same PVE API client production's
// collectors use (cmd/vnproxd/collect.go's buildCollectorPVEClient):
// production's documented read-only token identity (vnprox@pve!daemon)
// with this node's PVE certificate pinned, or — only when
// cfg.PVE.TicketUsername is set, the same documented dev/test-only escape
// hatch config.PVEConfig.TicketUsername's doc comment describes for the
// collectors — ticket auth against whatever cfg.PVE.APIURL points at
// (typically plain-HTTP internal/pvemock or a bare stub in tests). Neither
// vnproxctl nor cmd/vnproxd can share this helper directly (distinct `main`
// packages can't import each other); duplicated here deliberately narrowly
// rather than factored into a third package for two five-line branches.
func buildStatusPVEClient(cfg *config.Config) (*pve.Client, error) {
	if cfg.PVE.TicketUsername != "" {
		return pve.New(pve.Config{
			APIURL:   cfg.PVE.APIURL,
			Auth:     pve.AuthTicket,
			Username: cfg.PVE.TicketUsername,
			Password: cfg.PVE.TicketPassword,
			Realm:    cfg.PVE.TicketRealm,
		})
	}
	return pve.New(pve.Config{
		APIURL:    cfg.PVE.APIURL,
		Auth:      pve.AuthAPIToken,
		TokenFile: cfg.PVE.TokenFile,
		TLS:       pve.TLSConfig{CACertFile: config.DefaultPVECertPath},
	})
}

// printPVEHealth probes the PVE API directly (the same config.PVEConfig
// production wiring uses, cmd/vnproxd/collect.go's buildCollectorPVEClient)
// with a single lightweight GET /cluster/status call — cheap, always
// available regardless of PVE privilege level, and doubles as the
// cluster-membership count. Never fails the command: an unconfigured or
// unreachable PVE API is reported as a line, not a fatal error, since an
// operator running `vnproxctl status` to diagnose a broken node is exactly
// who needs to see "PVE API unreachable" rather than the whole command
// aborting.
func printPVEHealth(ctx context.Context, stdout io.Writer, cfg *config.Config, cfgErr error) {
	_, _ = fmt.Fprint(stdout, "PVE API health:      ")
	if cfgErr != nil {
		_, _ = fmt.Fprintf(stdout, "unknown (could not load config: %v)\n", cfgErr)
		return
	}

	client, err := buildStatusPVEClient(cfg)
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "not configured (%v) — see vnprox-setup's PVE token step\n", err)
		return
	}

	start := time.Now()
	nodes, err := client.ClusterStatus(ctx)
	latency := time.Since(start)
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "unreachable (%v)\n", err)
		return
	}
	nodeCount := 0
	for _, n := range nodes {
		if n.Type == "node" {
			nodeCount++
		}
	}
	_, _ = fmt.Fprintf(stdout, "reachable (%s), %d cluster node(s)\n", latency.Round(time.Millisecond), nodeCount)
}

// printPeerReachability discovers cluster peers via PVE's own
// /cluster/status (docs/architecture.md §5) and probes each with the peer
// API's own GET /api/peer/health plus GET /api/peer/version (surfacing a
// version-skew mismatch directly, docs/architecture.md §5's "upgrade
// prompt" case), reusing the exact cluster secret + client construction
// production wiring uses (cmd/vnproxd/server.go's coordPeerClient). Never
// fails the command.
func printPeerReachability(ctx context.Context, stdout io.Writer, cfg *config.Config, cfgErr error, timeout time.Duration) {
	_, _ = fmt.Fprintln(stdout, "Peer reachability:")
	if cfgErr != nil {
		_, _ = fmt.Fprintf(stdout, "  unknown (could not load config: %v)\n", cfgErr)
		return
	}

	if _, err := os.Stat(cfg.Peer.SecretPath); err != nil {
		_, _ = fmt.Fprintf(stdout, "  cluster secret not found at %s (run vnprox-setup, or this is a single-node install with no /etc/pve mount)\n", cfg.Peer.SecretPath)
		return
	}
	secrets, err := peer.LoadOrGenerateSecret(cfg.Peer.SecretPath, discardLogger())
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "  could not load cluster secret at %s: %v\n", cfg.Peer.SecretPath, err)
		return
	}

	pveClient, err := buildStatusPVEClient(cfg)
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "  cannot discover peers without a working PVE API client: %v\n", err)
		return
	}

	// Peer traffic shares this node's own configured vnprox port cluster-
	// wide (docs/architecture.md §9/§5, docs/deployment.md's same-port
	// enforcement) — pulled from cfg rather than left at peer.DefaultPort,
	// so a cluster running on the PBS-conflict fallback port (8008) is
	// still probed correctly.
	port := peer.DefaultPort
	if _, portStr, splitErr := net.SplitHostPort(cfg.Server.Listen); splitErr == nil {
		if p, convErr := strconv.Atoi(portStr); convErr == nil && p > 0 {
			port = p
		}
	}

	peerClient := peer.NewClient(peer.ClientOptions{
		ClusterStatus:  pveClient,
		Secrets:        secrets,
		Port:           port,
		RequestTimeout: timeout,
	})
	peers, err := peerClient.Peers(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "  could not discover cluster peers: %v\n", err)
		return
	}
	if len(peers) == 0 {
		_, _ = fmt.Fprintln(stdout, "  single-node (no peers)")
		return
	}
	printPeerStatuses(ctx, stdout, peerClient, peers)
}

// printPeerStatuses probes each discovered peer with GET /api/peer/health
// then GET /api/peer/version (surfacing a version-skew mismatch directly,
// docs/architecture.md §5's "upgrade prompt" case) and renders one line
// per peer, sorted by node name for stable, diffable output. Split out from
// printPeerReachability so it's independently testable against a
// hand-built *peer.Client/[]peer.Peer, without needing config/PVE/secret
// discovery or real TLS peer servers in a test.
func printPeerStatuses(ctx context.Context, stdout io.Writer, peerClient *peer.Client, peers []peer.Peer) {
	sort.Slice(peers, func(i, j int) bool { return peers[i].Node < peers[j].Node })
	for _, p := range peers {
		start := time.Now()
		healthErr := peerClient.Health(ctx, p)
		latency := time.Since(start)
		if healthErr != nil {
			_, _ = fmt.Fprintf(stdout, "  %-16s %-22s unreachable (%v)\n", p.Node, p.Addr, healthErr)
			continue
		}
		v, verErr := peerClient.Version(ctx, p)
		if verErr != nil {
			_, _ = fmt.Fprintf(stdout, "  %-16s %-22s reachable (%s), version unknown (%v)\n", p.Node, p.Addr, latency.Round(time.Millisecond), verErr)
			continue
		}
		compatNote := ""
		if v.ProtocolVersion != peer.ProtocolVersion {
			compatNote = fmt.Sprintf(" -- INCOMPATIBLE protocol (local %d, peer %d): upgrade this peer", peer.ProtocolVersion, v.ProtocolVersion)
		}
		_, _ = fmt.Fprintf(stdout, "  %-16s %-22s reachable (%s), version %s, protocol %d%s\n",
			p.Node, p.Addr, latency.Round(time.Millisecond), v.Version, v.ProtocolVersion, compatNote)
	}
}

// healthEndpointFromConfig loads the daemon config to find its listen
// address and builds the https URL for /api/v1/health. Kept as its own
// function (rather than folding into runStatus) because status_test.go
// exercises it directly.
func healthEndpointFromConfig(path string) (string, error) {
	cfg, err := config.Load(path, discardLogger())
	if err != nil {
		return "", fmt.Errorf("loading %s: %w", path, err)
	}
	return healthEndpointFromListen(cfg.Server.Listen)
}

// healthEndpointFromListen resolves wildcard bind addresses (0.0.0.0, ::,
// "") to 127.0.0.1 since vnproxctl is documented to always talk to "the
// local daemon" (docs/deployment.md).
func healthEndpointFromListen(listen string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("parsing server.listen %q: %w", listen, err)
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return fmt.Sprintf("https://%s/api/v1/health", net.JoinHostPort(host, port)), nil
}
