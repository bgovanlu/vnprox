package config

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func writeTemp(t *testing.T, name, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}
	return path
}

func TestLoad_ValidWithOverrideTLS(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())
	toml := `
[server]
listen = "127.0.0.1:8007"
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"

[pve]
api_url = "https://127.0.0.1:8006"

[safety]
allow_dangerous_ops = false

[collect]
pve_interval = "10s"
host_interval = "5s"
lldp_interval = "30s"
`
	path := writeTemp(t, "dev.toml", toml)

	cfg, err := Load(path, discardLogger())
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	if cfg.Server.Listen != "127.0.0.1:8007" {
		t.Errorf("Listen = %q, want 127.0.0.1:8007", cfg.Server.Listen)
	}
	if cfg.Server.TLSCertPath != certPath || cfg.Server.TLSKeyPath != keyPath {
		t.Errorf("resolved TLS paths = (%q, %q), want (%q, %q)", cfg.Server.TLSCertPath, cfg.Server.TLSKeyPath, certPath, keyPath)
	}
	if cfg.Collect.PVEInterval != 10*time.Second {
		t.Errorf("PVEInterval = %v, want 10s", cfg.Collect.PVEInterval)
	}
	if cfg.Server.ConfirmTimeoutDefault != DefaultConfirmTimeoutDefault {
		t.Errorf("ConfirmTimeoutDefault = %d, want default %d", cfg.Server.ConfirmTimeoutDefault, DefaultConfirmTimeoutDefault)
	}
	if cfg.PVE.TicketUsername != "" || cfg.PVE.TicketPassword != "" || cfg.PVE.TicketRealm != "" {
		t.Errorf("PVE ticket override fields = %+v, want all empty when unset", cfg.PVE)
	}
}

func TestLoad_DevTicketOverride(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())
	toml := `
[server]
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"

[pve]
api_url = "http://127.0.0.1:8006"
dev_ticket_username = "root@pam"
dev_ticket_password = "vnprox-mock"
dev_ticket_realm = "pam"
`
	path := writeTemp(t, "dev-ticket.toml", toml)

	cfg, err := Load(path, discardLogger())
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	if cfg.PVE.TicketUsername != "root@pam" || cfg.PVE.TicketPassword != "vnprox-mock" || cfg.PVE.TicketRealm != "pam" {
		t.Errorf("PVE ticket override fields = %+v, want root@pam/vnprox-mock/pam", cfg.PVE)
	}
	// The documented production fields must still resolve to their
	// defaults: setting the dev override does not disturb them.
	if cfg.PVE.TokenFile != DefaultPVETokenFile {
		t.Errorf("TokenFile = %q, want default %q even with the dev ticket override set", cfg.PVE.TokenFile, DefaultPVETokenFile)
	}
}

func TestLoad_DefaultsAppliedWhenSectionsOmitted(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())
	toml := `
[server]
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"
`
	path := writeTemp(t, "minimal.toml", toml)

	cfg, err := Load(path, discardLogger())
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	if cfg.Server.Listen != DefaultListen {
		t.Errorf("Listen = %q, want default %q", cfg.Server.Listen, DefaultListen)
	}
	if cfg.PVE.APIURL != DefaultPVEAPIURL {
		t.Errorf("APIURL = %q, want default %q", cfg.PVE.APIURL, DefaultPVEAPIURL)
	}
	if cfg.Collect.LLDPInterval != DefaultLLDPInterval {
		t.Errorf("LLDPInterval = %v, want default %v", cfg.Collect.LLDPInterval, DefaultLLDPInterval)
	}
}

func TestLoad_UnknownKeysWarnNotFail(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())
	toml := `
[server]
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"
totally_unknown_key = "surprise"

[made_up_section]
foo = "bar"
`
	path := writeTemp(t, "unknown.toml", toml)

	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	cfg, err := Load(path, logger)
	if err != nil {
		t.Fatalf("Load returned unexpected error for unknown keys: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected a config, got nil")
	}
	out := buf.String()
	if !strings.Contains(out, "totally_unknown_key") || !strings.Contains(out, "made_up_section") {
		t.Errorf("expected warnings mentioning unknown keys, got log output: %s", out)
	}
}

func TestLoad_InvalidListenFailsFast(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())
	toml := `
[server]
listen = "not-a-valid-address"
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"
`
	path := writeTemp(t, "badlisten.toml", toml)

	_, err := Load(path, discardLogger())
	if err == nil {
		t.Fatal("expected an error for invalid listen address, got nil")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("expected ErrInvalidConfig, got: %v", err)
	}
}

func TestLoad_InvalidListenPortFailsFast(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())
	toml := `
[server]
listen = "127.0.0.1:notaport"
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"
`
	path := writeTemp(t, "badport.toml", toml)

	_, err := Load(path, discardLogger())
	if err == nil {
		t.Fatal("expected an error for invalid listen port, got nil")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("expected ErrInvalidConfig, got: %v", err)
	}
}

func TestLoad_MissingExplicitCertFailsFast(t *testing.T) {
	toml := `
[server]
listen = "127.0.0.1:8007"
tls_cert = "/nonexistent/path/cert.pem"
tls_key = "/nonexistent/path/key.pem"
`
	path := writeTemp(t, "missingcert.toml", toml)

	_, err := Load(path, discardLogger())
	if err == nil {
		t.Fatal("expected an error for missing explicit cert path, got nil")
	}
	if !errors.Is(err, ErrTLSCertMissing) {
		t.Errorf("expected ErrTLSCertMissing, got: %v", err)
	}
}

func TestLoad_OneSidedTLSOverrideFailsFast(t *testing.T) {
	certPath, _ := writeTestCert(t, t.TempDir())
	toml := `
[server]
listen = "127.0.0.1:8007"
tls_cert = "` + certPath + `"
`
	path := writeTemp(t, "onesided.toml", toml)

	_, err := Load(path, discardLogger())
	if err == nil {
		t.Fatal("expected an error when only tls_cert is set, got nil")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("expected ErrInvalidConfig, got: %v", err)
	}
}

func TestLoad_NoTLSOverrideFailsCleanlyWhenPVECertAbsent(t *testing.T) {
	// In this sandbox (and in any non-PVE dev environment) there is no real
	// Proxmox install, so the default PVE cert paths never exist. Loading a
	// config with no override must fail with a clear, specific error rather
	// than silently serving plaintext or panicking.
	toml := `
[server]
listen = "127.0.0.1:8007"
`
	path := writeTemp(t, "nooverride.toml", toml)

	_, err := Load(path, discardLogger())
	if err == nil {
		t.Fatal("expected an error when no TLS override is given and the PVE cert is absent, got nil")
	}
	if !errors.Is(err, ErrTLSCertMissing) {
		t.Errorf("expected ErrTLSCertMissing, got: %v", err)
	}
}

func TestLoad_InvalidCollectDurationFailsFast(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())
	toml := `
[server]
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"

[collect]
pve_interval = "not-a-duration"
`
	path := writeTemp(t, "badduration.toml", toml)

	_, err := Load(path, discardLogger())
	if err == nil {
		t.Fatal("expected an error for invalid duration, got nil")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("expected ErrInvalidConfig, got: %v", err)
	}
}

func TestLoad_InvalidPVEAPIURLFailsFast(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())
	toml := `
[server]
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"

[pve]
api_url = "://not a url"
`
	path := writeTemp(t, "badurl.toml", toml)

	_, err := Load(path, discardLogger())
	if err == nil {
		t.Fatal("expected an error for invalid pve.api_url, got nil")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("expected ErrInvalidConfig, got: %v", err)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "does-not-exist.toml"), discardLogger())
	if err == nil {
		t.Fatal("expected an error for a missing config file, got nil")
	}
}

// TestLoad_ProtectedPath covers the [safety] protected_path seam added for
// audit-phase-2 F-13: default is the pmxcfs path, and a dev config can
// point it somewhere writable.
func TestLoad_ProtectedPath(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())
	base := `
[server]
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"
`

	t.Run("defaults to the pmxcfs path", func(t *testing.T) {
		cfg, err := Load(writeTemp(t, "default.toml", base), discardLogger())
		if err != nil {
			t.Fatalf("Load returned unexpected error: %v", err)
		}
		if cfg.Safety.ProtectedPath != DefaultProtectedPath {
			t.Errorf("ProtectedPath = %q, want default %q", cfg.Safety.ProtectedPath, DefaultProtectedPath)
		}
	})

	t.Run("override is honored", func(t *testing.T) {
		toml := base + `
[safety]
protected_path = "var/dev-protected.json"
`
		cfg, err := Load(writeTemp(t, "override.toml", toml), discardLogger())
		if err != nil {
			t.Fatalf("Load returned unexpected error: %v", err)
		}
		if cfg.Safety.ProtectedPath != "var/dev-protected.json" {
			t.Errorf("ProtectedPath = %q, want the overridden dev path", cfg.Safety.ProtectedPath)
		}
	})
}
