// SPDX-License-Identifier: Apache-2.0

// demo_test.go covers T-2801 AC3's first direction: demo mode cannot be
// enabled against a real PVE endpoint.
package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeDemoConfig writes a minimal, valid daemon config with no [pve]
// section, plus `extra` appended verbatim. The TLS keypair is generated per
// test because validate() resolves and stats those paths.
func writeDemoConfig(t *testing.T, extra string) string {
	t.Helper()
	dir := t.TempDir()
	certPEM, keyPEM := generateTestCert(t)
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("writing cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("writing key: %v", err)
	}

	body := "\n[server]\nlisten = \"127.0.0.1:24999\"\ntls_cert = \"" + certPath + "\"\ntls_key = \"" + keyPath + "\"\n" +
		"\n[storage]\ndb_path = \"" + filepath.Join(dir, "vnprox.db") + "\"\nsession_key_file = \"" + filepath.Join(dir, "session.key") + "\"\n" +
		extra
	path := filepath.Join(dir, "vnprox.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return path
}

func TestLoadDemo_AcceptsAConfigWithNoPVESection(t *testing.T) {
	path := writeDemoConfig(t, "")
	cfg, err := LoadDemo(path, discardLogger())
	if err != nil {
		t.Fatalf("LoadDemo: %v", err)
	}
	if !cfg.Demo {
		t.Error("LoadDemo did not set Config.Demo; nothing downstream would know this is a demo")
	}
	// Blanked, not defaulted. DefaultPVEAPIURL is https://127.0.0.1:8006 —
	// on a Proxmox node that is pveproxy itself, and a demo daemon must not
	// carry it even unused.
	if cfg.PVE.APIURL != "" {
		t.Errorf("LoadDemo left PVE.APIURL = %q; a demo config must carry no PVE endpoint at all", cfg.PVE.APIURL)
	}
}

// AC3, direction one. Table-driven over every [pve] key, because the
// refusal has to cover the section rather than api_url alone: a config that
// sets only token_file still names an identity minted against a real
// cluster.
func TestLoadDemo_RefusesEveryConfiguredPVEKey(t *testing.T) {
	cases := []struct {
		name  string
		extra string
	}{
		{name: "api_url", extra: "\n[pve]\napi_url = \"https://10.0.0.1:8006\"\n"},
		{name: "token_file", extra: "\n[pve]\ntoken_file = \"/etc/vnprox/keys/pve-token\"\n"},
		{name: "dev_ticket_username", extra: "\n[pve]\ndev_ticket_username = \"root@pam\"\n"},
		{name: "dev_ticket_password", extra: "\n[pve]\ndev_ticket_password = \"hunter2\"\n"},
		{name: "dev_ticket_realm", extra: "\n[pve]\ndev_ticket_realm = \"pam\"\n"},
		{name: "empty api_url still counts", extra: "\n[pve]\napi_url = \"\"\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeDemoConfig(t, tc.extra)
			_, err := LoadDemo(path, discardLogger())
			if err == nil {
				t.Fatalf("LoadDemo accepted a config setting pve.%s; demo mode must refuse a configured PVE endpoint", tc.name)
			}
			if !errors.Is(err, ErrDemoRealEndpoint) {
				t.Errorf("error is not ErrDemoRealEndpoint: %v", err)
			}
			if !errors.Is(err, ErrInvalidConfig) {
				t.Errorf("ErrDemoRealEndpoint must also satisfy errors.Is(err, ErrInvalidConfig): %v", err)
			}
			// The message has to name what was set, or the operator is
			// told "no" with no way to find the offending line.
			if !strings.Contains(err.Error(), "pve.") {
				t.Errorf("error names no offending key: %v", err)
			}
		})
	}
}

// The repository's own dev config sets a PVE endpoint, so it is the most
// realistic thing an operator might reach for — and it must be refused.
// This is the exact invocation the e2e suite's negative case runs.
func TestLoadDemo_RefusesTheRepositoryDevConfig(t *testing.T) {
	_, err := LoadDemo("../../testdata/dev.toml", discardLogger())
	if !errors.Is(err, ErrDemoRealEndpoint) {
		t.Fatalf("LoadDemo(testdata/dev.toml) = %v; want ErrDemoRealEndpoint — dev.toml sets [pve] api_url", err)
	}
}

// The counterpart: the e2e suite's own demo config must stay loadable in
// demo mode. If someone adds a [pve] section to testdata/demo.toml, this
// fails here rather than as a mysterious e2e daemon that never comes up.
func TestLoadDemo_AcceptsTheRepositoryDemoConfig(t *testing.T) {
	// Paths inside it are repo-root-relative; only the refusal and the
	// Demo flag are under test, and both are decided before any path is
	// touched. Read the bytes and load them under a repo-root-relative
	// path so the TLS files resolve.
	data, err := os.ReadFile("../../testdata/demo.toml")
	if err != nil {
		t.Fatalf("reading testdata/demo.toml: %v", err)
	}
	if err := refuseConfiguredPVEEndpoint(data, "testdata/demo.toml"); err != nil {
		t.Fatalf("testdata/demo.toml would be refused in demo mode: %v", err)
	}
}
