// SPDX-License-Identifier: Apache-2.0

// remoteclient.go implements T-1105's HTTP-backed command family's shared
// transport: a thin client over vnproxd's documented /api/v1 surface
// (docs/api.md), authenticated exclusively with a T-1104 bearer token (never
// a PVE username/password — CLAUDE.md's "do not re-litigate decisions" plus
// this task card's own explicit constraint). Every `vnproxctl remote ...`
// and `vnproxctl apply` subcommand is built on remoteClient/remoteFlags
// below; no subcommand talks to net/http directly.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/bgovanlu/vnprox/internal/config"
)

// remoteTokenEnvVar is the environment variable `--token` falls back to
// (T-1105 card: "requiring ... --token <token> / VNPROX_TOKEN").
const remoteTokenEnvVar = "VNPROX_TOKEN"

// apiError mirrors docs/api.md's error envelope
// (`{"error": {"code","message","details"}}`), the shape every 4xx/5xx
// response from vnproxd's /api/v1 routes carries.
//
//nolint:govet // fieldalignment: wire DTO; field order documents the JSON shape, not memory packing (matches internal/api/spec.go's identical precedent).
type apiError struct {
	Details json.RawMessage `json:"details,omitempty"`
	Code    string          `json:"code"`
	Message string          `json:"message"`
}

// apiErrorEnvelope is the top-level `{"error": {...}}` wrapper.
type apiErrorEnvelope struct {
	Error apiError `json:"error"`
}

// remoteClient is a minimal bearer-token HTTP client over one vnproxd's
// /api/v1 base URL. Every method call sets `Authorization: Bearer <token>`
// (docs/api.md's Conventions section: "accepted on every route in this
// document") and never CSRF (bearer requests skip it, same doc).
type remoteClient struct {
	http    *http.Client
	baseURL string // e.g. "https://pve1:8007/api/v1", no trailing slash
	token   string
}

// requestError distinguishes a network-layer failure (could not even talk
// to the daemon) from every other kind of error, so callers can map it to
// ExitNetwork specifically (T-1105's documented exit-code table) instead of
// the generic ExitError bucket.
type requestError struct {
	err error
}

func (e *requestError) Error() string { return e.err.Error() }
func (e *requestError) Unwrap() error { return e.err }

// isNetworkError reports whether err represents a transport-level failure
// (dial/TLS/timeout) as opposed to an HTTP-level error response (which
// apiError above already carries) or a local decode error.
func isNetworkError(err error) bool {
	var re *requestError
	return errors.As(err, &re)
}

// doJSON issues method/path (relative to baseURL) with body marshaled as
// the JSON request payload (nil for no body), decodes a 2xx response body
// into out (nil to discard it), and returns the parsed apiError for any 4xx/
// 5xx response. Exactly one of (apiErr, err) is meaningful on failure: a
// non-nil err means the request/response plumbing itself failed (network,
// malformed JSON); a non-nil apiErr means the daemon answered with a
// well-formed error envelope.
func (c *remoteClient) doJSON(ctx context.Context, method, path string, body, out any) (status int, apiErr *apiError, err error) {
	var reader io.Reader
	if body != nil {
		b, marshalErr := json.Marshal(body)
		if marshalErr != nil {
			return 0, nil, fmt.Errorf("encoding request body: %w", marshalErr)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return 0, nil, fmt.Errorf("building request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, &requestError{err: fmt.Errorf("%s %s: %w", method, path, err)}
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, &requestError{err: fmt.Errorf("reading response body: %w", err)}
	}

	if resp.StatusCode >= 400 {
		var env apiErrorEnvelope
		if len(data) > 0 {
			_ = json.Unmarshal(data, &env) // best-effort; a non-JSON error body still reports the HTTP status
		}
		if env.Error.Code == "" {
			env.Error.Code = "unknown_error"
			env.Error.Message = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return resp.StatusCode, &env.Error, nil
	}

	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, nil, fmt.Errorf("decoding response from %s %s: %w", method, path, err)
		}
	}
	return resp.StatusCode, nil, nil
}

// exitForAPIError maps a daemon error response to T-1105's documented
// exit-code table: 401/403 are always ExitAuth (missing/invalid/revoked
// token, or a token whose scopes don't cover this route); 422 is
// ExitPending (validation_failed — a changeset carries blocking findings);
// everything else falls into the generic ExitError bucket.
func exitForAPIError(status int) int {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ExitAuth
	case http.StatusUnprocessableEntity:
		return ExitPending
	default:
		return ExitError
	}
}

// exitForErr maps a non-API-response error (network failure vs. everything
// else) to T-1105's exit-code table.
func exitForErr(err error) int {
	if isNetworkError(err) {
		return ExitNetwork
	}
	return ExitError
}

// remoteFlags bundles the connection/auth/output flags every `remote`/
// `apply` subcommand registers identically, so each subcommand's own
// flag.FlagSet stays a one-line `addRemoteFlags(fs)` call rather than
// repeating eight `fs.String(...)` calls per command.
type remoteFlags struct {
	configPath *string
	url        *string
	token      *string
	timeout    *time.Duration
	output     *string
	insecure   *bool
}

// addRemoteFlags registers the shared flags onto fs and returns the bound
// pointers. name is the subcommand's own name (for flag.FlagSet's usage
// header only — callers still construct their own FlagSet).
func addRemoteFlags(fs *flag.FlagSet) *remoteFlags {
	return &remoteFlags{
		configPath: fs.String("config", defaultConfigPath, "vnprox.toml to read the listen address from, absent --url"),
		url:        fs.String("url", "", "override the daemon's /api/v1 base URL (e.g. https://pve1:8007/api/v1), skipping --config lookup"),
		token:      fs.String("token", "", "T-1104 bearer token (falls back to the "+remoteTokenEnvVar+" environment variable; never a PVE username/password)"),
		timeout:    fs.Duration("timeout", 10*time.Second, "per-request timeout"),
		insecure:   fs.Bool("insecure", true, "skip TLS certificate verification (see docs/deployment.md)"),
		output:     fs.String("o", defaultOutputFormat, outputFlagUsage),
	}
}

// resolveToken applies the --token / VNPROX_TOKEN precedence (T-1105 card:
// "--token <token> / VNPROX_TOKEN"): an explicit flag always wins.
func (rf *remoteFlags) resolveToken() string {
	if *rf.token != "" {
		return *rf.token
	}
	return os.Getenv(remoteTokenEnvVar)
}

// buildRemoteClient resolves the daemon base URL and bearer token and
// constructs a remoteClient — or fails fast with ExitAuth/ExitUsage/
// ExitError *before ever dialing the daemon* when the token is missing or
// the base URL can't be determined (T-1105 acceptance criterion 1: "with no
// token, fails fast with the documented auth exit code and no daemon call
// attempted").
func buildRemoteClient(rf *remoteFlags, cmdName string, stderr io.Writer) (*remoteClient, int) {
	token := rf.resolveToken()
	if token == "" {
		_, _ = fmt.Fprintf(stderr, "%s: no token: pass --token or set %s (T-1104 bearer tokens only — see `vnproxctl remote --help`)\n", cmdName, remoteTokenEnvVar)
		return nil, ExitAuth
	}

	base := *rf.url
	if base == "" {
		cfg, err := config.Load(*rf.configPath, discardLogger())
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "%s: loading %s: %v\n", cmdName, *rf.configPath, err)
			return nil, ExitError
		}
		derived, err := apiBaseFromListen(cfg.Server.Listen)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "%s: %v\n", cmdName, err)
			return nil, ExitError
		}
		base = derived
	}
	if _, err := url.Parse(base); err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: invalid --url %q: %v\n", cmdName, base, err)
		return nil, ExitUsage
	}

	client := &http.Client{
		Timeout: *rf.timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: *rf.insecure}, //nolint:gosec // opt-out flag; see status.go's identical justification
		},
	}
	return &remoteClient{http: client, baseURL: base, token: token}, ExitSuccess
}

// apiBaseFromListen builds the https base URL for vnproxd's /api/v1 root
// from a `server.listen` address, resolving wildcard binds to 127.0.0.1 —
// the same convention healthEndpointFromListen (status.go) uses for
// /api/v1/health, extracted so both share it instead of duplicating the
// wildcard-resolution switch.
func apiBaseFromListen(listen string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("parsing server.listen %q: %w", listen, err)
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return fmt.Sprintf("https://%s/api/v1", net.JoinHostPort(host, port)), nil
}

// parseOutputFlagOrUsage validates rf.output, printing a usage error and
// returning ExitUsage on an unrecognized value.
func parseOutputFlagOrUsage(rf *remoteFlags, cmdName string, stderr io.Writer) (jsonOutput bool, exitCode int, ok bool) {
	jsonOutput, err := parseOutputFormat(*rf.output)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", cmdName, err)
		return false, ExitUsage, false
	}
	return jsonOutput, ExitSuccess, true
}

// strconvItoa64 is a tiny local helper (avoids importing strconv in every
// call site just for one conversion) used by a couple of table renderers
// below for optional int64 fields.
func strconvItoa64(v int64) string { return strconv.FormatInt(v, 10) }
