package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadRecoveryOnly_WorksWithNoResolvableTLSCert is the whole reason
// this loader exists, and it mirrors T-607's regression test for
// LoadStorageOnly: `vnproxctl backup`/`restore` are disaster-recovery
// commands, and the disasters they exist for include "this node's
// certificate is the broken thing". The full Load would fail here.
func TestLoadRecoveryOnly_WorksWithNoResolvableTLSCert(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vnprox.toml")
	body := "[server]\nlisten = \"10.0.0.5:8009\"\n\n" +
		"[storage]\ndb_path = \"/srv/vnprox/store.db\"\nsession_key_file = \"/srv/secrets/session.key\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	// The control: the full loader genuinely fails on this file, so this
	// test is not just asserting that two loaders agree.
	if _, err := Load(path, discardLogger()); err == nil {
		t.Fatal("control: the full Load succeeded on a host with no PVE certificate — " +
			"LoadRecoveryOnly's reason for existing no longer holds, re-check this test")
	}

	cfg, err := LoadRecoveryOnly(path, discardLogger())
	if err != nil {
		t.Fatalf("LoadRecoveryOnly: %v", err)
	}
	if cfg.DBPath != "/srv/vnprox/store.db" {
		t.Errorf("DBPath = %q", cfg.DBPath)
	}
	if cfg.SessionKeyFile != "/srv/secrets/session.key" {
		t.Errorf("SessionKeyFile = %q", cfg.SessionKeyFile)
	}
	if cfg.Listen != "10.0.0.5:8009" {
		t.Errorf("Listen = %q — restore's live-daemon probe would check the wrong address", cfg.Listen)
	}
}

// TestLoadRecoveryOnly_Defaults: an install that configures nothing must
// still yield the documented paths, or `--include-keys` would silently
// collect nothing on a stock node.
func TestLoadRecoveryOnly_Defaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vnprox.toml")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	cfg, err := LoadRecoveryOnly(path, discardLogger())
	if err != nil {
		t.Fatalf("LoadRecoveryOnly: %v", err)
	}
	for _, tc := range []struct{ got, want, name string }{
		{cfg.DBPath, DefaultDBPath, "db_path"},
		{cfg.SessionKeyFile, DefaultSessionKeyFile, "session_key_file"},
		{cfg.Listen, DefaultListen, "listen"},
		{cfg.PVETokenFile, DefaultPVETokenFile, "pve token_file"},
		{cfg.MetricsKeyFile, DefaultMetricsKeyFile, "metrics key_file"},
		{cfg.BlueprintSigningKeyFile, DefaultBlueprintSigningKeyFile, "blueprint signing_key_file"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
	// OIDC is opt-in and has no default; a phantom path here would make
	// `backup --include-keys` warn about a file that never existed.
	if cfg.OIDCClientSecretFile != "" {
		t.Errorf("OIDCClientSecretFile = %q for a config with no [oidc] section", cfg.OIDCClientSecretFile)
	}
}

// TestLoadRecoveryOnly_HonoursRelocatedPaths: an operator who moved their
// keys must still get them backed up.
func TestLoadRecoveryOnly_HonoursRelocatedPaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vnprox.toml")
	body := "[storage]\nsession_key_file = \"/opt/keys/s.key\"\n\n" +
		"[pve]\ntoken_file = \"/opt/keys/pve\"\n\n" +
		"[metrics]\nkey_file = \"/opt/keys/m.key\"\n\n" +
		"[blueprint]\nsigning_key_file = \"/opt/keys/bp.key\"\n\n" +
		"[oidc]\nclient_secret_file = \"/opt/keys/oidc\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	cfg, err := LoadRecoveryOnly(path, discardLogger())
	if err != nil {
		t.Fatalf("LoadRecoveryOnly: %v", err)
	}
	for _, tc := range []struct{ got, want string }{
		{cfg.SessionKeyFile, "/opt/keys/s.key"},
		{cfg.PVETokenFile, "/opt/keys/pve"},
		{cfg.MetricsKeyFile, "/opt/keys/m.key"},
		{cfg.BlueprintSigningKeyFile, "/opt/keys/bp.key"},
		{cfg.OIDCClientSecretFile, "/opt/keys/oidc"},
	} {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
}

func TestLoadRecoveryOnly_MissingFile(t *testing.T) {
	if _, err := LoadRecoveryOnly("/nonexistent/vnprox.toml", discardLogger()); err == nil {
		t.Fatal("LoadRecoveryOnly on a missing file returned no error")
	}
}
