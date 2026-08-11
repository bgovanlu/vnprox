package config

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// gitsyncTOML wraps a [gitsync] section in the minimum surrounding config a
// Load call needs (TLS paths are resolved by validate()).
func gitsyncTOML(t *testing.T, section string) string {
	t.Helper()
	certPath, keyPath := writeTestCert(t, t.TempDir())
	return `
[server]
listen = "127.0.0.1:8007"
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"
` + section
}

// TestLoad_GitSyncOffByDefault is the card's "off by default" requirement at
// the configuration layer: a config file with no [gitsync] section at all
// yields a section that cannot poll anything.
func TestLoad_GitSyncOffByDefault(t *testing.T) {
	cfg, err := Load(writeTemp(t, "dev.toml", gitsyncTOML(t, "")), discardLogger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.GitSync.Enabled {
		t.Error("gitsync is enabled with no [gitsync] section")
	}
	if cfg.GitSync.URL != "" || cfg.GitSync.Path != "" {
		t.Errorf("gitsync has a remote configured by default: %+v", cfg.GitSync)
	}
}

// TestLoad_GitSyncValidation is the table over the section's own gates. The
// asymmetry it pins down: a *disabled* section is never an error however
// incomplete, and an *enabled* one is fatally checked.
func TestLoad_GitSyncValidation(t *testing.T) {
	//nolint:govet // fieldalignment: test table; field order documents each case, not packing.
	tests := []struct {
		name    string
		section string
		wantErr string
		check   func(t *testing.T, g GitSyncConfig)
	}{
		{
			name: "a fully configured section loads",
			section: `
[gitsync]
enabled = true
url = "https://github.com/org/infra"
provider = "github"
ref = "production"
path = "network/cluster.yaml"
poll_interval = "90s"
token_file = "/etc/vnprox/keys/gitsync.token"
require_signed_commits = true
allowed_signers_file = "/etc/vnprox/gitsync-allowed-signers"
`,
			check: func(t *testing.T, g GitSyncConfig) {
				if !g.Enabled || g.Ref != "production" || g.PollInterval != 90*time.Second {
					t.Errorf("resolved section = %+v", g)
				}
				if !g.RequireSignedCommits || g.AllowedSignersFile == "" {
					t.Errorf("signature gate not resolved: %+v", g)
				}
			},
		},
		{
			// T-2702: the push credential is its own key. A section that
			// configures a sync but no push_token_file resolves to a sync
			// that cannot propose — which is the default, and the reason
			// enabling proposals is a separate, explicit act.
			name: "the push credential is separate from the read one",
			section: `
[gitsync]
enabled = true
url = "https://github.com/org/infra"
path = "network/cluster.yaml"
token_file = "/etc/vnprox/keys/gitsync.token"
`,
			check: func(t *testing.T, g GitSyncConfig) {
				if g.TokenFile == "" {
					t.Fatal("control failed: the read token did not resolve, so the assertion below proves nothing")
				}
				if g.PushTokenFile != "" {
					t.Errorf("PushTokenFile = %q for a section that never set one", g.PushTokenFile)
				}
			},
		},
		{
			name: "setting push_token_file resolves it, and only it",
			section: `
[gitsync]
enabled = true
url = "https://github.com/org/infra"
path = "network/cluster.yaml"
token_file = "/etc/vnprox/keys/gitsync.token"
push_token_file = "/etc/vnprox/keys/gitsync-push.token"
`,
			check: func(t *testing.T, g GitSyncConfig) {
				if g.PushTokenFile != "/etc/vnprox/keys/gitsync-push.token" {
					t.Errorf("PushTokenFile = %q", g.PushTokenFile)
				}
				if g.TokenFile == g.PushTokenFile {
					t.Error("the read and push credentials resolved to the same file")
				}
			},
		},
		{
			name: "ref defaults to main",
			section: `
[gitsync]
enabled = true
url = "https://github.com/org/infra"
path = "cluster.yaml"
`,
			check: func(t *testing.T, g GitSyncConfig) {
				if g.Ref != "main" {
					t.Errorf("Ref = %q, want the default main", g.Ref)
				}
				if g.PollInterval != 0 {
					t.Errorf("PollInterval = %v, want 0 so the package default applies", g.PollInterval)
				}
			},
		},
		{
			name: "a disabled but half-filled section is not an error",
			section: `
[gitsync]
enabled = false
require_signed_commits = true
`,
			check: func(t *testing.T, g GitSyncConfig) {
				if g.Enabled {
					t.Error("section reports enabled")
				}
			},
		},
		{
			name: "enabled with no url is fatal",
			section: `
[gitsync]
enabled = true
path = "cluster.yaml"
`,
			wantErr: "gitsync.url is required",
		},
		{
			name: "enabled with no path is fatal",
			section: `
[gitsync]
enabled = true
url = "https://github.com/org/infra"
`,
			wantErr: "gitsync.path is required",
		},
		{
			name: "an unknown provider is fatal",
			section: `
[gitsync]
enabled = true
url = "https://github.com/org/infra"
path = "cluster.yaml"
provider = "svn"
`,
			wantErr: "gitsync.provider",
		},
		{
			name: "requiring signatures with no trust anchors is fatal",
			section: `
[gitsync]
enabled = true
url = "https://github.com/org/infra"
path = "cluster.yaml"
require_signed_commits = true
`,
			wantErr: "gitsync.allowed_signers_file is required",
		},
		{
			name: "a malformed poll interval is fatal, never silently defaulted",
			section: `
[gitsync]
enabled = true
url = "https://github.com/org/infra"
path = "cluster.yaml"
poll_interval = "5 minutes"
`,
			wantErr: "gitsync.poll_interval",
		},
		{
			name: "a non-positive poll interval is fatal",
			section: `
[gitsync]
enabled = true
url = "https://github.com/org/infra"
path = "cluster.yaml"
poll_interval = "0s"
`,
			wantErr: "gitsync.poll_interval must be positive",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(writeTemp(t, "dev.toml", gitsyncTOML(t, tc.section)), discardLogger())
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("Load accepted the section; want an error mentioning %q", tc.wantErr)
				}
				if !errors.Is(err, ErrInvalidConfig) {
					t.Errorf("error %v does not wrap ErrInvalidConfig", err)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error %q does not mention %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			tc.check(t, cfg.GitSync)
		})
	}
}
