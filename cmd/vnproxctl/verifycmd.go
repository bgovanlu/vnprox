package main

// `vnproxctl verify` (T-2501): the hardware-validation checklist, executed.
//
// This command is the answer to docs/status-matrix.md §5.3 — a hardware
// validation figure stuck in single digits because validating an item means a
// human reading a checklist line, doing the thing, and writing down what
// happened. `planning/validation/` made that a script somebody runs and pastes
// back; this makes it a command that observes, decides, and signs its own
// evidence. The point is that it can be handed to a user with a cluster we do
// not have.
//
// Three behaviours here are load-bearing rather than incidental:
//
//  1. **It refuses to run against a mock unless told to.** A green run against
//     internal/pvemock is byte-for-byte as convincing as a green run against
//     real Proxmox, and filing one as hardware evidence would raise the
//     validated count in the matrix while validating nothing. `--allow-mock`
//     exists, says what it costs, and stamps the report.
//  2. **A skipped check is not a passing one.** A run in which everything
//     skipped exits non-zero and says `0 passed`, because "we could not look"
//     read as "we looked and it was fine" is precisely how a validation
//     figure becomes fiction.
//  3. **Nothing mutates without `--i-understand`.** The destructive suite's
//     write client is not constructed at all without it, so the interlock is
//     the wiring rather than a rule the checks are trusted to follow.

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/blueprint"
	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/pve"
	"github.com/bgovanlu/vnprox/internal/verify"
)

// verifyRunTimeout bounds a whole suite run. The destructive suite waits out
// a commit-confirm window, so this is generous; a hung PVE call still ends
// the run rather than parking a terminal forever.
const verifyRunTimeout = 20 * time.Minute

func runVerify(args []string, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("verify", flag.ContinueOnError)
	fset.SetOutput(stderr)
	var (
		suite      = fset.String("suite", string(verify.SuiteHardware), "which suite to run: hardware, multinode, or destructive")
		only       = fset.String("only", "", "comma-separated check ids to run instead of a whole suite")
		list       = fset.Bool("list", false, "print every registered check id, its matrix row and its hardware precondition, and exit")
		outPath    = fset.String("out", "", "write the signed report artifact to this path")
		signKey    = fset.String("sign-key", "", "Ed25519 signing key file for the report (default: an ephemeral key — the signature still detects tampering but carries no provenance)")
		allowMock  = fset.Bool("allow-mock", false, "run against an endpoint identified as a mock. The report is stamped as a mock run and is NOT hardware evidence")
		understand = fset.Bool("i-understand", false, "required by --suite=destructive: this run will change real state on this cluster")
		pveURL     = fset.String("pve-url", "", "PVE API base URL (default: [pve] api_url from --config)")
		pveToken   = fset.String("pve-token", "", "PVE API token value (default: read from [pve] token_file in --config)")
		configPath = fset.String("config", defaultConfigPath, "vnprox.toml to read the PVE and daemon endpoints from")
		daemonURL  = fset.String("url", "", "the daemon's /api/v1 base URL (default: derived from --config)")
		token      = fset.String("token", "", "T-1104 bearer token for the daemon (falls back to "+remoteTokenEnvVar+")")
		insecure   = fset.Bool("insecure", true, "skip TLS certificate verification (see docs/deployment.md)")
		outputFmt  = fset.String("o", defaultOutputFormat, outputFlagUsage)
	)
	if err := fset.Parse(args); err != nil {
		return ExitUsage
	}
	asJSON, err := parseOutputFormat(*outputFmt)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl verify: %v\n", err)
		return ExitUsage
	}
	if *list {
		return printVerifyChecks(stdout, asJSON)
	}

	opts := verify.Options{
		Suite:   verify.Suite(*suite),
		Only:    splitCommaList(*only),
		Version: version,
		Logger:  discardLogger(),
	}
	if len(opts.Only) == 0 && !verify.ValidSuite(opts.Suite) {
		_, _ = fmt.Fprintf(stderr, "vnproxctl verify: unknown --suite %q (want hardware, multinode or destructive)\n", *suite)
		return ExitUsage
	}
	if opts.Suite == verify.SuiteDestructive && !*understand {
		_, _ = fmt.Fprintf(stderr, "vnproxctl verify: --suite=destructive changes real state on this cluster (it interrupts applies, expires commit-confirm windows and stops the active daemon). Re-run with --i-understand if that is acceptable here.\n")
		return ExitUsage
	}

	ctx, cancel := context.WithTimeout(context.Background(), verifyRunTimeout)
	defer cancel()

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: *insecure}, //nolint:gosec // opt-out flag; see status.go's identical justification
		},
	}

	// --- the endpoint, and the guard at the door ---------------------------
	endpoint := *pveURL
	if endpoint == "" {
		cfg, cfgErr := config.Load(*configPath, discardLogger())
		if cfgErr != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl verify: no --pve-url given and %s could not be read: %v\n", *configPath, cfgErr)
			return ExitUsage
		}
		endpoint = cfg.PVE.APIURL
	}
	if endpoint == "" {
		// Deliberately fatal rather than "run with no PVE". A run with no PVE
		// endpoint would skip everything, exit non-zero for the right reason
		// by accident, and — worse — be the one way to dodge the mock guard.
		_, _ = fmt.Fprintf(stderr, "vnproxctl verify: no PVE endpoint: pass --pve-url or set [pve] api_url in %s. A hardware-validation run without a cluster to validate is not a thing this command will produce.\n", *configPath)
		return ExitUsage
	}

	verdict, err := verify.DetectMock(ctx, httpClient, endpoint)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl verify: could not reach %s: %v\n", endpoint, err)
		return ExitNetwork
	}
	if verdict.IsMock && !*allowMock {
		// AC4. The message names the flag, and says what passing it costs.
		_, _ = fmt.Fprintf(stderr, "vnproxctl verify: %s\n", verify.MockRefusalMessage(endpoint, verdict))
		return ExitUsage
	}

	// --- probes -------------------------------------------------------------
	deps := verify.Deps{
		Now:     time.Now,
		Consent: verify.Consent{AllowMock: *allowMock, Destructive: *understand},
		Endpoint: verify.Endpoint{
			URL:        endpoint,
			Mock:       verdict.IsMock,
			MockReason: verdict.Reason,
		},
	}
	if cluster, clusterErr := buildPVEProbe(httpClient, endpoint, *pveToken, *configPath); clusterErr == nil {
		deps.Cluster = cluster
		if nodes, nodesErr := cluster.Nodes(ctx); nodesErr == nil {
			deps.Nodes = nodes
		} else {
			_, _ = fmt.Fprintf(stderr, "vnproxctl verify: could not read cluster membership from %s: %v — every multi-node check will skip naming this\n", endpoint, nodesErr)
		}
	} else {
		_, _ = fmt.Fprintf(stderr, "vnproxctl verify: no PVE client (%v) — the checks that need one will skip naming it\n", clusterErr)
	}

	rf := &remoteFlags{
		configPath: configPath,
		url:        daemonURL,
		token:      token,
		timeout:    ptrDuration(30 * time.Second),
		insecure:   insecure,
		output:     ptrString(defaultOutputFormat),
	}
	if client, code := buildRemoteClient(rf, "verify", io.Discard); code == ExitSuccess && client != nil {
		deps.Daemon = daemonProbe{client: client}
		if *understand {
			// The interlock: the write client exists only under consent.
			deps.Mutator = daemonProbe{client: client}
		}
	} else {
		_, _ = fmt.Fprintf(stderr, "vnproxctl verify: no daemon client (no --token/%s, or the daemon URL could not be determined) — the checks that need one will skip naming it\n", remoteTokenEnvVar)
	}

	hostNode := ""
	if len(deps.Nodes) > 0 {
		for _, n := range deps.Nodes {
			if n.Local {
				hostNode = n.Name
				break
			}
		}
	}
	deps.Host = verify.LocalHost{Node: hostNode}

	// --- the run --------------------------------------------------------------
	report, err := verify.Run(ctx, opts, deps)
	if err != nil {
		var unknown *verify.UnknownCheckError
		if errors.As(err, &unknown) {
			// AC6: a bad --only is a usage error naming the id, not a silent
			// empty run.
			_, _ = fmt.Fprintf(stderr, "vnproxctl verify: %v\n", err)
			return ExitUsage
		}
		_, _ = fmt.Fprintf(stderr, "vnproxctl verify: %v\n", err)
		return ExitError
	}

	// T-2503: hand the report to telemetry BEFORE the artifact is written and
	// rendered, so whatever time those take is time the send already had.
	// This call does not wait and nothing below it depends on the result —
	// see startVerifyTelemetry. With [telemetry] enabled = false (the shipped
	// default) it returns having contacted nothing.
	_ = startVerifyTelemetry(*configPath, report)

	if *outPath != "" {
		if code := writeVerifyArtifact(report, *signKey, *outPath, stdout, stderr); code != ExitSuccess {
			return code
		}
	}

	if asJSON {
		if err := writeJSONOut(stdout, report); err != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl verify: %v\n", err)
			return ExitError
		}
	} else {
		_, _ = fmt.Fprint(stdout, report.Render())
	}

	// AC3, at the exit code: a run that validated nothing is not a success,
	// however few failures it had.
	if !report.OK() {
		return ExitError
	}
	return ExitSuccess
}

// writeVerifyArtifact signs the report and writes it (AC5).
func writeVerifyArtifact(report verify.Report, signKeyPath, outPath string, stdout, stderr io.Writer) int {
	var (
		priv ed25519.PrivateKey
		err  error
	)
	if signKeyPath != "" {
		priv, err = blueprint.LoadSigningKeyFile(signKeyPath)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl verify: --sign-key: %v\n", err)
			return ExitError
		}
	} else {
		priv, err = verify.EphemeralSigningKey()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "vnproxctl verify: %v\n", err)
			return ExitError
		}
	}

	artifact, err := verify.SignReport(report, priv)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl verify: %v\n", err)
		return ExitError
	}
	if writeErr := os.WriteFile(outPath, artifact, 0o600); writeErr != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl verify: writing %s: %v\n", outPath, writeErr)
		return ExitError
	}
	// Re-read and re-verify what was just written. A signing path that has
	// never been round-tripped is one that fails the first time somebody
	// depends on it, in the place where it is hardest to debug.
	written, err := os.ReadFile(outPath) //nolint:gosec // the path is the operator's own --out
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl verify: re-reading %s: %v\n", outPath, err)
		return ExitError
	}
	if _, fingerprint, err := verify.ParseSignedReport(written); err != nil {
		_, _ = fmt.Fprintf(stderr, "vnproxctl verify: the artifact just written to %s does not verify: %v\n", outPath, err)
		return ExitError
	} else {
		_, _ = fmt.Fprintf(stdout, "wrote %s (%d bytes), signed by %s\n", outPath, len(artifact), fingerprint)
	}
	return ExitSuccess
}

// printVerifyChecks renders the registry, so an operator can see what the
// suite would ask of their hardware before running any of it.
func printVerifyChecks(stdout io.Writer, asJSON bool) int {
	checks := verify.Checks()
	if asJSON {
		type row struct {
			ID           string `json:"id"`
			Area         string `json:"area"`
			Suite        string `json:"suite"`
			Precondition string `json:"precondition"`
			MatrixRow    int    `json:"matrixRow"`
			MinNodes     int    `json:"minNodes"`
		}
		out := make([]row, 0, len(checks))
		for _, c := range checks {
			out = append(out, row{
				ID:           c.ID,
				MatrixRow:    c.MatrixRow,
				Area:         c.Area,
				Suite:        string(c.Suite),
				MinNodes:     c.MinNodes,
				Precondition: c.Precondition,
			})
		}
		if err := writeJSONOut(stdout, out); err != nil {
			return ExitError
		}
		return ExitSuccess
	}
	for _, c := range checks {
		_, _ = fmt.Fprintf(stdout, "%-36s row %-3d %-11s %s\n", c.ID, c.MatrixRow, c.Suite, c.Area)
		_, _ = fmt.Fprintf(stdout, "%-36s needs: %s\n\n", "", c.Precondition)
	}
	_, _ = fmt.Fprintf(stdout, "%d checks registered.\n", len(checks))
	return ExitSuccess
}

// daemonProbe adapts remoteClient to verify's read and write seams.
type daemonProbe struct {
	client *remoteClient
}

func (p daemonProbe) Get(ctx context.Context, path string) (int, []byte, error) {
	return p.do(ctx, http.MethodGet, path, nil)
}

func (p daemonProbe) Post(ctx context.Context, path string, body any) (int, []byte, error) {
	return p.do(ctx, http.MethodPost, path, body)
}

// GetRoot implements verify.RootProbe (T-2902 wave's pwa.servable check,
// checks_pwa.go): a GET against the daemon's ROOT — the SPA surface where
// the manifest, service worker, and CSP header live — rather than the
// /api/v1 base every other probe call uses. Headers are returned because
// the check's whole subject is a response header.
func (p daemonProbe) GetRoot(ctx context.Context, path string) (int, http.Header, []byte, error) {
	base := strings.TrimSuffix(p.client.baseURL, "/api/v1")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("building root request: %w", err)
	}
	resp, err := p.client.http.Do(req)
	if err != nil {
		return 0, nil, nil, &requestError{err: fmt.Errorf("GET %s: %w", path, err)}
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, resp.Header, nil, fmt.Errorf("reading root response body: %w", err)
	}
	return resp.StatusCode, resp.Header, body, nil
}

// do returns the raw body rather than a decoded value, because verify's
// checks assert on the response the daemon actually sent — see DaemonProbe's
// doc comment for why a shared typed client would defeat the point.
func (p daemonProbe) do(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var raw json.RawMessage
	status, apiErr, err := p.client.doJSON(ctx, method, path, body, &raw)
	if err != nil {
		return status, nil, err
	}
	if apiErr != nil {
		encoded, _ := json.Marshal(apiErr)
		return status, encoded, nil
	}
	return status, raw, nil
}

// buildPVEProbe constructs the read-only PVE client the cluster checks use.
func buildPVEProbe(httpClient *http.Client, apiURL, tokenValue, configPath string) (verify.PVEAdapter, error) {
	cfg := pve.Config{
		HTTPClient: httpClient,
		Logger:     discardLogger(),
		APIURL:     apiURL,
		Auth:       pve.AuthAPIToken,
		TokenValue: tokenValue,
	}
	if cfg.TokenValue == "" {
		if loaded, err := config.Load(configPath, discardLogger()); err == nil {
			cfg.TokenFile = loaded.PVE.TokenFile
		}
	}
	if cfg.TokenValue == "" && cfg.TokenFile == "" {
		return verify.PVEAdapter{}, fmt.Errorf("no PVE token: pass --pve-token or set [pve] token_file in %s", configPath)
	}
	client, err := pve.New(cfg)
	if err != nil {
		return verify.PVEAdapter{}, fmt.Errorf("building the PVE client: %w", err)
	}
	return verify.PVEAdapter{Client: client}, nil
}

// splitCommaList parses --only.
func splitCommaList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
