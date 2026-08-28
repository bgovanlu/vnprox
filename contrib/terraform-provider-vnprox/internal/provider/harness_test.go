// SPDX-License-Identifier: Apache-2.0

package provider

// harness_test.go builds and runs the REAL cmd/pvemock and cmd/vnproxd
// binaries from the main vnprox module as subprocesses, then bootstraps a
// T-1104 bearer token against the real daemon over HTTP — the same "drive
// the real production stack, never a hand-rolled fake" discipline
// internal/apicontract/harness_test.go uses in the main module (see its doc
// comment there), adapted to a process boundary because this provider is a
// separate Go module by design and cannot import internal/api, internal/
// change, or internal/pvemock directly (client.go's package doc comment;
// README.md's "Module boundary" section). This is exactly what T-4001's
// card asks for: "acceptance-style tests that run against cmd/pvemock + a
// real vnproxd, not against a hand-rolled fake."
//
// Only TestMain pays this cost, and only when TF_ACC is set (the standard
// terraform-plugin-testing convention resource.Test itself also honors) —
// an ordinary `go test ./...` with TF_ACC unset never builds or starts
// either binary.

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// testAccBaseURL/testAccToken are populated by TestMain before any
// acceptance test runs, and read by provider config blocks in
// resource_bridge_test.go / resource_vlan_test.go /
// data_source_topology_test.go / data_source_inventory_test.go via
// testAccProviderConfig().
var (
	testAccBaseURL string
	testAccToken   string
)

// findRepoRoot walks upward from this file's own directory (via
// runtime.Caller, which is stable regardless of the test binary's working
// directory) until it finds the main vnprox module's root — identified by
// the presence of cmd/vnproxd, cmd/pvemock, and testdata/dev.toml, the
// three things this harness needs from it. Failing closed (a hard test
// failure) rather than silently testing against nothing is the point: a
// moved directory should break this loudly.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("harness: runtime.Caller(0) failed — cannot locate this file's own path")
	}
	dir := filepath.Dir(thisFile)
	for range 8 {
		if dirExists(filepath.Join(dir, "cmd", "vnproxd")) &&
			dirExists(filepath.Join(dir, "cmd", "pvemock")) &&
			fileExists(filepath.Join(dir, "testdata", "dev.toml")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("harness: could not find the main vnprox module root walking up from %s (looked for cmd/vnproxd, cmd/pvemock, testdata/dev.toml)", filepath.Dir(thisFile))
	return ""
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// reservePort asks the kernel for a free TCP port by binding and
// immediately releasing it — the same small race
// cmd/vnproxd/devconfig_test.go's TestRunDaemon_DevConfigServesHealth
// accepts for the identical reason.
func reservePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("harness: reserving an ephemeral port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// buildBinary runs `go build -o out <pkg>` with cmd.Dir=repoRoot — building
// from source rather than assuming a pre-built binary exists, so this
// harness works from a clean checkout the same way `go test` always does.
func buildBinary(t *testing.T, repoRoot, out, pkg string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", out, pkg)
	cmd.Dir = repoRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("harness: building %s: %v\n%s", pkg, err, stderr.String())
	}
}

// startProcess starts cmd, streaming its stderr to t.Log (prefixed) so a
// startup failure is visible in `go test -v` output, and registers a
// cleanup that terminates it.
func startProcess(t *testing.T, name string, cmd *exec.Cmd) {
	t.Helper()
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("harness: %s: StderrPipe: %v", name, err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("harness: starting %s: %v", name, err)
	}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := stderr.Read(buf)
			if n > 0 {
				t.Logf("[%s] %s", name, strings.TrimRight(string(buf[:n]), "\n"))
			}
			if rerr != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	})
}

// waitTCPUp polls addr until a TCP connect succeeds or timeout elapses.
// pvemock (internal/pvemock) has no unauthenticated readiness route — every
// route under /api2/json requires either a ticket or a privilege check —
// so a bare connect is this harness's only pre-auth signal that it has
// finished opening its listener.
func waitTCPUp(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("harness: %s never accepted a TCP connection within %s: %v", addr, timeout, lastErr)
}

// waitHealthy polls url (expecting a 200) until timeout, for a daemon that
// needs a moment to open its store/TLS listener after Start returns.
func waitHealthy(t *testing.T, hc *http.Client, url string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := hc.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("harness: %s never became healthy within %s: %v", url, timeout, lastErr)
}

// rewriteDevConfig is a trimmed copy of cmd/vnproxd/devconfig_test.go's
// helper of the same name (that file lives in package main of the OTHER
// module and cannot be imported here — see this package's module-boundary
// doc comments) — same replacement set, plus [pve] api_url pointed at the
// mock's own ephemeral port.
func rewriteDevConfig(t *testing.T, repoRoot, dir string, vnproxdPort, pvemockPort int) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot, "testdata", "dev.toml"))
	if err != nil {
		t.Fatalf("harness: reading testdata/dev.toml: %v", err)
	}

	replacements := map[string]string{
		"listen":              fmt.Sprintf("127.0.0.1:%d", vnproxdPort),
		"tls_cert":            filepath.Join(repoRoot, "testdata", "certs", "dev-cert.pem"),
		"tls_key":             filepath.Join(repoRoot, "testdata", "certs", "dev-key.pem"),
		"db_path":             filepath.Join(dir, "vnprox.db"),
		"session_key_file":    filepath.Join(dir, "session.key"),
		"protected_path":      filepath.Join(dir, "protected.json"),
		"dev_interfaces_dir":  filepath.Join(dir, "dev-host"),
		"secret_path":         filepath.Join(dir, "cluster.secret"),
		"key_file":            filepath.Join(dir, "metrics.key"),
		"signing_key_file":    filepath.Join(dir, "blueprint-signing.key"),
		"trusted_signers_dir": filepath.Join(dir, "trusted-signers"),
		"api_url":             fmt.Sprintf("http://127.0.0.1:%d", pvemockPort),
	}
	replaced := make(map[string]bool, len(replacements))

	lines := strings.Split(string(raw), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		for key, value := range replacements {
			if strings.HasPrefix(trimmed, key+" ") || strings.HasPrefix(trimmed, key+"=") {
				lines[i] = fmt.Sprintf("%s = %q", key, value)
				replaced[key] = true
			}
		}
	}
	for key := range replacements {
		if !replaced[key] {
			t.Fatalf("harness: testdata/dev.toml has no %q key to rewrite; update this harness to match its current shape", key)
		}
	}

	cfgPath := filepath.Join(dir, "dev.toml")
	if err := os.WriteFile(cfgPath, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatalf("harness: writing rewritten dev config: %v", err)
	}
	return cfgPath
}

// mintBearerToken logs in against the running vnproxd as the fixture's
// root@pam user (testdata/clusters/single-node.yaml's built-in
// root@pam/vnprox-mock, the exact credential dev.toml's own
// dev_ticket_username/password use for the collector's PVE client), then
// mints a netRead+netWrite bearer token via POST /tokens — the ordinary
// "log in once, mint an automation token" ceremony a real operator would
// perform, not a store-level shortcut (this harness has no access to the
// daemon's store: it is a separate module talking pure HTTP, exactly like
// the provider it's testing). This is deliberately the ONLY place in this
// entire module that touches a username/password — the provider itself
// (client.go, provider.go) never does, per this card's instruction to
// reuse vnproxctl's bearer-token mechanism.
func mintBearerToken(t *testing.T, baseURL string, hc *http.Client) string {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("harness: cookiejar.New: %v", err)
	}
	loginClient := &http.Client{Transport: hc.Transport, Timeout: hc.Timeout, Jar: jar}

	loginBody, _ := json.Marshal(map[string]string{
		"username": "root@pam", "password": "vnprox-mock", "realm": "pam",
	})
	loginReq, err := http.NewRequest(http.MethodPost, baseURL+"/auth/login", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatalf("harness: building login request: %v", err)
	}
	loginReq.Header.Set("Content-Type", "application/json")
	loginResp, err := loginClient.Do(loginReq)
	if err != nil {
		t.Fatalf("harness: POST /auth/login: %v", err)
	}
	defer func() { _ = loginResp.Body.Close() }()
	if loginResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(loginResp.Body)
		t.Fatalf("harness: POST /auth/login: HTTP %d: %s", loginResp.StatusCode, body)
	}

	loginURL, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("harness: parsing base URL: %v", err)
	}
	var csrf string
	for _, c := range jar.Cookies(loginURL) {
		if c.Name == "vnprox_csrf" {
			csrf = c.Value
		}
	}
	if csrf == "" {
		t.Fatal("harness: POST /auth/login did not set a vnprox_csrf cookie")
	}

	tokenBody, _ := json.Marshal(map[string]any{
		"name":   "terraform-provider-acceptance-test",
		"scopes": []string{"netRead", "netWrite"},
	})
	tokenReq, err := http.NewRequest(http.MethodPost, baseURL+"/tokens", bytes.NewReader(tokenBody))
	if err != nil {
		t.Fatalf("harness: building token request: %v", err)
	}
	tokenReq.Header.Set("Content-Type", "application/json")
	tokenReq.Header.Set("X-VNPROX-CSRF", csrf)
	tokenResp, err := loginClient.Do(tokenReq)
	if err != nil {
		t.Fatalf("harness: POST /tokens: %v", err)
	}
	defer func() { _ = tokenResp.Body.Close() }()
	if tokenResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(tokenResp.Body)
		t.Fatalf("harness: POST /tokens: HTTP %d: %s", tokenResp.StatusCode, body)
	}

	var out struct {
		Token string `json:"token"`
	}
	if derr := json.NewDecoder(tokenResp.Body).Decode(&out); derr != nil {
		t.Fatalf("harness: decoding POST /tokens response: %v", derr)
	}
	if out.Token == "" {
		t.Fatal("harness: POST /tokens returned an empty token")
	}
	return out.Token
}

// setupAcceptanceStack builds and starts pvemock + vnproxd, mints a bearer
// token, and populates testAccBaseURL/testAccToken. Called once per test
// (not once per package) deliberately — acceptance tests in this package
// are few and each gets an isolated daemon+store, trading a few seconds of
// startup per test for zero cross-test state coupling, matching this
// card's "table-driven per resource type" instruction more simply than a
// shared TestMain-managed singleton would.
func setupAcceptanceStack(t *testing.T) {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("acceptance test: set TF_ACC=1 to run (see README.md's \"Running the acceptance tests\" section)")
	}

	repoRoot := findRepoRoot(t)
	dir := t.TempDir()

	pvemockBin := filepath.Join(dir, "pvemock")
	vnproxdBin := filepath.Join(dir, "vnproxd")
	buildBinary(t, repoRoot, pvemockBin, "./cmd/pvemock")
	buildBinary(t, repoRoot, vnproxdBin, "./cmd/vnproxd")

	pvemockPort := reservePort(t)
	pvemockCmd := exec.Command(pvemockBin,
		"--addr", fmt.Sprintf(":%d", pvemockPort),
		"--fixture", filepath.Join(repoRoot, "testdata", "clusters", "single-node.yaml"),
	)
	startProcess(t, "pvemock", pvemockCmd)
	waitTCPUp(t, fmt.Sprintf("127.0.0.1:%d", pvemockPort), 10*time.Second)

	vnproxdPort := reservePort(t)
	cfgPath := rewriteDevConfig(t, repoRoot, dir, vnproxdPort, pvemockPort)
	vnproxdCmd := exec.Command(vnproxdBin, "--config", cfgPath)
	startProcess(t, "vnproxd", vnproxdCmd)

	insecureClient := insecureAcceptanceHTTPClient()
	baseURL := fmt.Sprintf("https://127.0.0.1:%d/api/v1", vnproxdPort)
	waitHealthy(t, insecureClient, baseURL+"/health", 15*time.Second)

	testAccBaseURL = baseURL
	testAccToken = mintBearerToken(t, baseURL, insecureClient)
}

// insecureAcceptanceHTTPClient returns a fresh http.Client that skips TLS
// verification — every acceptance-stack daemon this harness starts serves
// testdata/certs' throwaway self-signed dev cert (see provider.go's
// insecure attribute doc comment for why the provider itself defaults the
// other way).
func insecureAcceptanceHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // throwaway dev cert, test-only
		},
	}
}

// testAccProviderConfig renders the provider config block every acceptance
// test's Terraform config starts with — base_url/token from the stack
// setupAcceptanceStack just started, insecure=true because the daemon is
// serving testdata/certs' throwaway self-signed dev cert (the same
// dev-only exception cmd/vnproxctl's --insecure flag documents, never a
// production default — see provider.go's insecure attribute doc comment).
func testAccProviderConfig() string {
	return fmt.Sprintf(`
provider "vnprox" {
  base_url = %q
  token    = %q
  insecure = true
}
`, testAccBaseURL, testAccToken)
}
