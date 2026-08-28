// SPDX-License-Identifier: Apache-2.0

// demo_test.go covers the zero-argument `vnproxd --demo` shape: the config
// and TLS keypair it materializes on first run, and the fact that a second
// run reuses both.
package main

import (
	"crypto/tls"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/demo"
)

func TestSetupDemo_ZeroConfigMaterializesAWorkingDemo(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vnprox-demo")

	cfg, rt, err := setupDemo("", dir, testLogger())
	if err != nil {
		t.Fatalf("setupDemo: %v", err)
	}
	if !cfg.Demo {
		t.Error("setupDemo produced a config that is not marked as a demo")
	}
	if !rt.enabled() {
		t.Error("setupDemo produced a disabled demo runtime")
	}
	if cfg.PVE.APIURL != demo.APIURL {
		t.Errorf("PVE.APIURL = %q, want the unresolvable demo address %q", cfg.PVE.APIURL, demo.APIURL)
	}
	if rt.httpClient() == nil {
		t.Error("the demo runtime has no in-process HTTP client; PVE clients would be built with a real transport")
	}

	// The generated config must itself be loadable in demo mode. If
	// demoConfigTOML ever grew a [pve] section, config.LoadDemo would refuse
	// the daemon's own generated config — which is the check working, and a
	// spectacularly confusing way to find out at run time.
	cfgPath := filepath.Join(dir, "demo.toml")
	body, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("reading the generated config: %v", err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue // the file's own prose explains why there is no [pve]
		}
		if strings.HasPrefix(trimmed, "[pve]") || strings.HasPrefix(trimmed, "api_url") {
			t.Errorf("the generated demo config configures PVE (%q); config.LoadDemo would refuse the daemon's own generated config", trimmed)
		}
	}
	// Belt and braces: it round-trips through the refusal it was written to
	// satisfy.
	if _, err := config.LoadDemo(cfgPath, testLogger()); err != nil {
		t.Errorf("the generated demo config does not load in demo mode: %v", err)
	}

	// The TLS keypair has to actually load — CertProvider does exactly this
	// at startup, and a keypair that only looks right fails the daemon after
	// everything else has already succeeded.
	certPath := filepath.Join(dir, "demo-cert.pem")
	keyPath := filepath.Join(dir, "demo-key.pem")
	if _, err := tls.LoadX509KeyPair(certPath, keyPath); err != nil {
		t.Fatalf("the generated demo keypair does not load: %v", err)
	}
	for _, p := range []string{cfgPath, certPath, keyPath} {
		info, statErr := os.Stat(p)
		if statErr != nil {
			t.Fatalf("stat %s: %v", p, statErr)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s has mode %o, want 0600", p, perm)
		}
	}
}

// A second demo start reuses the first one's certificate and config. A demo
// that regenerated its TLS identity on every start would train its user to
// click through a certificate warning, which is not a habit this product
// should be teaching anyone.
func TestSetupDemo_ReusesItsConfigAndCertificate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vnprox-demo")

	if _, _, err := setupDemo("", dir, testLogger()); err != nil {
		t.Fatalf("first setupDemo: %v", err)
	}
	before := map[string][]byte{}
	for _, name := range []string{"demo.toml", "demo-cert.pem", "demo-key.pem"} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		before[name] = body
	}

	if _, _, err := setupDemo("", dir, testLogger()); err != nil {
		t.Fatalf("second setupDemo: %v", err)
	}
	for name, want := range before {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("re-reading %s: %v", name, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s was regenerated on the second start", name)
		}
	}
}

// AC3, direction one, at the process boundary rather than at config.Load:
// `--demo --config <a config that names a PVE endpoint>` does not start.
func TestSetupDemo_RefusesAConfigWithAPVEEndpoint(t *testing.T) {
	_, _, err := setupDemo("../../testdata/dev.toml", t.TempDir(), testLogger())
	if !errors.Is(err, config.ErrDemoRealEndpoint) {
		t.Fatalf("setupDemo(testdata/dev.toml) = %v; want ErrDemoRealEndpoint", err)
	}
}

// The e2e stack's own testdata/demo.toml is checked in
// internal/config/demo_test.go instead: its paths are repo-root-relative
// (the daemon runs with cwd = repo root under Playwright), so loading it
// from this package's directory would fail on the TLS paths for a reason
// that has nothing to do with demo mode.

// TestResolveCertsRoot_DemoModeAvoidsARealPmxcfs is the regression test for
// the bug this session's own real-hardware run of T-3303 found: a demo
// daemon started on a machine that happens to be a real PVE node (unlike
// every dev/CI machine this was tested on before) scanned that node's REAL
// /etc/pve and leaked real node names into a supposedly fully-synthetic
// public demo's findings feed, because nothing gated certs.Service's Root
// on cfg.Demo. See resolveCertsRoot's doc comment.
func TestResolveCertsRoot_DemoModeAvoidsARealPmxcfs(t *testing.T) {
	cases := []struct {
		name           string
		configuredRoot string
		dbPath         string
		want           string
		demo           bool
	}{
		{
			name:   "demo mode, no explicit root: guaranteed-absent path, not /etc/pve",
			demo:   true,
			dbPath: "/var/lib/vnprox-demo-public/vnprox.db",
			want:   "/var/lib/vnprox-demo-public/no-real-pve-in-demo-mode",
		},
		{
			name:           "demo mode, operator set [certs] root explicitly: honored",
			demo:           true,
			configuredRoot: "/some/operator/chosen/path",
			dbPath:         "/var/lib/vnprox-demo-public/vnprox.db",
			want:           "/some/operator/chosen/path",
		},
		{
			name:   "real daemon, no explicit root: real certs.DefaultRoot behavior preserved (empty means default)",
			demo:   false,
			dbPath: "/var/lib/vnprox/vnprox.db",
			want:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{Demo: tc.demo}
			cfg.Certs.Root = tc.configuredRoot
			cfg.Storage.DBPath = tc.dbPath
			if got := resolveCertsRoot(cfg); got != tc.want {
				t.Errorf("resolveCertsRoot() = %q, want %q", got, tc.want)
			}
		})
	}
}
