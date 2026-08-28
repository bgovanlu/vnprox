// SPDX-License-Identifier: Apache-2.0

// T-2101: the "consumable by an external CI run" half of this package.
//
// Every scenario in this package (lifecycle_test.go, validation_test.go,
// rollback_test.go, specimport_test.go, manifest_test.go) drives its target
// purely over HTTP with a bearer token — harness_test.go's newContractHarness
// normally stands up that target itself, in-process, against
// internal/pvemock. This file adds a second way to reach exactly the same
// scenarios: point them at an already-running, out-of-process vnproxd
// instead.
//
// This is what makes the suite something terraform-provider-vnprox and
// ansible-collection-vnprox (planning/tasks/phase-21.md's T-2101, separate
// repositories this task does not create — see harness_test.go's package
// doc) can actually run in their own CI, against their own pinned vnprox
// version, rather than only ever running inside this repo's `make check`.
//
// # Documented invocation
//
// A downstream repo's CI job:
//
//  1. Checks out this repository at the vnprox version it is pinned to
//     (a tag, e.g. `git clone --branch v3.1.0 --depth 1
//     https://github.com/bgovanlu/vnprox`). This suite lives under
//     internal/ and therefore cannot be imported as a Go module dependency
//     (the Go toolchain refuses cross-module imports of an internal
//     package) — running it means having this source checked out and
//     invoking `go test` against it directly, the same way this repo's own
//     CI does. That is a deliberate consequence of keeping the suite next
//     to the handlers it pins, not an oversight: it is what stops a
//     conformance suite from silently drifting into its own second
//     implementation of the contract it's supposed to be checking.
//
//  2. Starts its own pinned vnproxd against a backend of its choice — the
//     mock-backed `make dev` shape (`go run ./cmd/pvemock --fixture
//     testdata/clusters/single-node.yaml` + `go run ./cmd/vnproxd --config
//     testdata/dev.toml`) for CI, or a real cluster for a hardware
//     validation run. Either way, this suite never starts or manages the
//     target daemon itself in external mode — it only ever talks to one
//     that already exists, over HTTP.
//
//  3. Runs:
//
//     VNPROX_CONFORMANCE_BASE_URL=https://127.0.0.1:8007 \
//     VNPROX_CONFORMANCE_USERNAME=root@pam \
//     VNPROX_CONFORMANCE_PASSWORD=vnprox-mock \
//     VNPROX_CONFORMANCE_INSECURE_SKIP_VERIFY=1 \
//     go test ./internal/apicontract/... -count=1 -v
//
// This package bootstraps everything else itself: it logs in with the
// supplied credentials exactly like a real operator would (docs/api.md's
// `POST /auth/login`), mints two bearer tokens over `POST /tokens` with the
// real CSRF double-submit flow (one {netRead, netWrite}, one {netRead}
// alone — the two scope shapes every scenario in this package needs), and
// only then drives the same lifecycle/validation/rollback/spec-import flows
// every in-process run already covers, asserting the same golden fixtures.
//
// A deliberate handler schema break in this repository — the same kind
// T-1106's report proved with a renamed JSON tag — fails this run exactly
// the way it fails the in-process one, because it is the *same test code*
// against the *same golden fixtures*; only the transport target differs.
// That is this task's contract-break protocol in one sentence: a downstream
// repo's CI runs this exact invocation against its own pinned vnprox
// version, and a break here is a break there, discovered in the downstream
// repo's own CI rather than by a user's `terraform apply` failing silently
// in a way neither side can attribute.
//
// # Environment variables
//
//   - VNPROX_CONFORMANCE_BASE_URL (required to enable external mode) — the
//     target vnproxd's base URL, e.g. "https://127.0.0.1:8007". Unset
//     (the default): every test in this package runs the in-process harness
//     exactly as it always has; nothing below is reachable.
//   - VNPROX_CONFORMANCE_USERNAME / VNPROX_CONFORMANCE_PASSWORD (required
//     once base URL is set) — PVE identity credentials for `POST
//     /auth/login`, the *only* way this suite ever authenticates in
//     external mode (T-1106's card: "no PVE ticket flow exposed to
//     [automation]" describes the routes automation calls once it holds a
//     token — bootstrapping that token in the first place is still an
//     ordinary human/CI login, the same one a real operator performs before
//     minting a token to hand to Terraform).
//   - VNPROX_CONFORMANCE_REALM (optional) — PVE realm; omit it and embed
//     the realm in the username instead ("root@pam"), exactly like
//     `POST /auth/login` itself allows.
//   - VNPROX_CONFORMANCE_FIXTURE (optional, default "single-node") — which
//     of this package's two named fixtures to run; the other is skipped.
//     Must match whatever fixture the target vnproxd was actually started
//     against — this suite cannot detect or change that from outside.
//   - VNPROX_CONFORMANCE_INSECURE_SKIP_VERIFY (optional, "1" to enable) —
//     skip TLS certificate verification. Needed only for
//     testdata/dev.toml's throwaway self-signed dev certificate; a real
//     deployment's certificate should always verify and this must stay
//     unset against one.
package apicontract

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	envConformanceBaseURL  = "VNPROX_CONFORMANCE_BASE_URL"
	envConformanceUsername = "VNPROX_CONFORMANCE_USERNAME"
	envConformancePassword = "VNPROX_CONFORMANCE_PASSWORD"
	envConformanceRealm    = "VNPROX_CONFORMANCE_REALM"
	envConformanceFixture  = "VNPROX_CONFORMANCE_FIXTURE"
	envConformanceInsecure = "VNPROX_CONFORMANCE_INSECURE_SKIP_VERIFY"

	// csrfCookieName/csrfHeaderName mirror internal/auth.CSRFCookieName /
	// CSRFHeaderName verbatim. Reimplemented as literals rather than
	// imported: this package deliberately asserts on the *wire* contract
	// (docs/api.md), not on internal/auth's Go identifiers, and the
	// external-mode bootstrap is itself part of that wire contract — a
	// downstream repo has only docs/api.md to go on, never this constant.
	csrfCookieName = "vnprox_csrf"
	csrfHeaderName = "X-VNPROX-CSRF"
)

// externalConfig is this package's external-conformance-mode configuration,
// loaded once per harness from the environment described in this file's
// package doc comment above.
type externalConfig struct {
	baseURL            string
	username           string
	password           string
	realm              string
	fixture            string
	insecureSkipVerify bool
}

// loadExternalConfig reports whether external conformance mode is enabled
// (VNPROX_CONFORMANCE_BASE_URL set) and, if so, the fully-resolved config —
// failing the test loudly and immediately if a required variable is missing,
// rather than letting a later HTTP call fail with a confusing error far from
// the actual cause.
func loadExternalConfig(t *testing.T) (*externalConfig, bool) {
	t.Helper()
	base := os.Getenv(envConformanceBaseURL)
	if base == "" {
		return nil, false
	}
	cfg := &externalConfig{
		baseURL:            strings.TrimSuffix(base, "/"),
		username:           os.Getenv(envConformanceUsername),
		password:           os.Getenv(envConformancePassword),
		realm:              os.Getenv(envConformanceRealm),
		fixture:            os.Getenv(envConformanceFixture),
		insecureSkipVerify: os.Getenv(envConformanceInsecure) == "1",
	}
	if cfg.fixture == "" {
		cfg.fixture = "single-node"
	}
	if cfg.username == "" || cfg.password == "" {
		t.Fatalf("external conformance mode (%s=%q): %s and %s are both required", envConformanceBaseURL, base, envConformanceUsername, envConformancePassword)
	}
	return cfg, true
}

// externalModeActive reports whether external conformance mode is enabled,
// without needing a *testing.T (unlike loadExternalConfig, which fails the
// test loudly on a misconfiguration) — used by golden_test.go's
// redactedChangeset to redact the one field known to legitimately vary
// between this package's LLDP-less in-process harness and a fully-wired
// real daemon. See redactedChangeset's comment for why.
func externalModeActive() bool {
	return os.Getenv(envConformanceBaseURL) != ""
}

// externalFixtureMatches reports whether fixturePath's short name (the
// filename without extension — "single-node", "three-node-vlan") is the one
// cfg selected. Only one fixture can be loaded into a given running daemon
// at a time, so every other named fixture's subtests are skipped rather than
// run against state they don't describe.
func externalFixtureMatches(fixturePath string, cfg *externalConfig) bool {
	name := strings.TrimSuffix(filepath.Base(fixturePath), filepath.Ext(fixturePath))
	return name == cfg.fixture
}

// newExternalContractHarness bootstraps a session and two bearer tokens
// against cfg.baseURL and returns a harness that drives every scenario in
// this package against it, exactly like newContractHarness's in-process
// return value does.
func newExternalContractHarness(t *testing.T, cfg *externalConfig) *contractHarness {
	t.Helper()
	client := newExternalHTTPClient(cfg)
	rw, ro := bootstrapExternalTokens(t, client, cfg)
	return &contractHarness{
		t: t, baseURL: cfg.baseURL, client: client, localNode: "pve1",
		external: true, externalRWToken: rw, externalROToken: ro,
	}
}

func newExternalHTTPClient(cfg *externalConfig) *http.Client {
	jar, _ := cookiejar.New(nil) // nil error only ever returned for a non-nil Options with a broken PublicSuffixList; not used here
	var transport = http.DefaultTransport
	if cfg.insecureSkipVerify {
		transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // opt-in via VNPROX_CONFORMANCE_INSECURE_SKIP_VERIFY, external conformance mode only — see this file's package doc
		}
	}
	return &http.Client{Jar: jar, Transport: transport, Timeout: 30 * time.Second}
}

// bootstrapExternalTokens logs in once, then mints the two bearer tokens
// every scenario in this package needs: one {netRead, netWrite}, one
// {netRead} alone. Both routes (POST /auth/login, POST /tokens) are dialed
// for real here — including POST /tokens itself, which
// planning/reports/T-1106.md's own report flagged as *not* exercised over
// HTTP by the in-process suite (its stubbed login has no session to mint a
// token from). External mode closes that gap: it is the one place in this
// package that can, because it has a real session to work with.
func bootstrapExternalTokens(t *testing.T, client *http.Client, cfg *externalConfig) (rw, ro string) {
	t.Helper()
	externalLogin(t, client, cfg)
	rw = mintExternalToken(t, client, cfg.baseURL, "vnprox-apicontract-rw", []string{"netRead", "netWrite"})
	ro = mintExternalToken(t, client, cfg.baseURL, "vnprox-apicontract-ro", []string{"netRead"})
	return rw, ro
}

func externalLogin(t *testing.T, client *http.Client, cfg *externalConfig) {
	t.Helper()
	body, err := json.Marshal(map[string]string{
		"username": cfg.username,
		"password": cfg.password,
		"realm":    cfg.realm,
	})
	if err != nil {
		t.Fatalf("external conformance: marshaling login request: %v", err)
	}
	resp, err := client.Post(cfg.baseURL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("external conformance: POST /auth/login against %s: %v", cfg.baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("external conformance: POST /auth/login: status %d, want 200 (as %s): %s", resp.StatusCode, cfg.username, b)
	}
}

// externalCSRFToken reads the JS-readable vnprox_csrf cookie login just set,
// per docs/api.md's documented double-submit convention
// ("session cookie ... + X-VNPROX-CSRF header on mutating requests").
func externalCSRFToken(client *http.Client, baseURL string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parsing base URL %q: %w", baseURL, err)
	}
	for _, c := range client.Jar.Cookies(u) {
		if c.Name == csrfCookieName {
			return c.Value, nil
		}
	}
	return "", fmt.Errorf("no %s cookie present after login — was POST /auth/login actually called first?", csrfCookieName)
}

func mintExternalToken(t *testing.T, client *http.Client, baseURL, name string, scopes []string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"name": name, "scopes": scopes})
	if err != nil {
		t.Fatalf("external conformance: marshaling POST /tokens request for %s: %v", name, err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/tokens", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("external conformance: building POST /tokens request for %s: %v", name, err)
	}
	req.Header.Set("Content-Type", "application/json")
	csrf, err := externalCSRFToken(client, baseURL)
	if err != nil {
		t.Fatalf("external conformance: %v", err)
	}
	req.Header.Set(csrfHeaderName, csrf)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("external conformance: POST /tokens (%s): %v", name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("external conformance: POST /tokens (%s, scopes=%v): status %d, want 201: %s", name, scopes, resp.StatusCode, b)
	}
	var out struct {
		Token string `json:"token"`
	}
	if decodeErr := json.NewDecoder(resp.Body).Decode(&out); decodeErr != nil {
		t.Fatalf("external conformance: decoding POST /tokens (%s) response: %v", name, decodeErr)
	}
	if out.Token == "" {
		t.Fatalf("external conformance: POST /tokens (%s) response carried no token field", name)
	}
	return out.Token
}
