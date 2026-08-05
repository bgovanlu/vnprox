package config

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/capture"
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
	if cfg.FirewallLog.Path != DefaultFirewallLogPath {
		t.Errorf("FirewallLog.Path = %q, want default %q", cfg.FirewallLog.Path, DefaultFirewallLogPath)
	}
	if cfg.FirewallLog.DevFixtureDir != "" {
		t.Errorf("FirewallLog.DevFixtureDir = %q, want empty (dev-only override) when unset", cfg.FirewallLog.DevFixtureDir)
	}
	// T-1301 [capture] defaults (internal/capture's own conservative caps).
	if cfg.Capture.Root != DefaultCaptureRoot {
		t.Errorf("Capture.Root = %q, want default %q", cfg.Capture.Root, DefaultCaptureRoot)
	}
	if cfg.Capture.MaxPackets != capture.DefaultCaps.MaxPackets || cfg.Capture.MaxBytes != capture.DefaultCaps.MaxBytes {
		t.Errorf("Capture caps = %+v, want capture.DefaultCaps %+v", cfg.Capture, capture.DefaultCaps)
	}
	if cfg.Capture.MaxFilterInstructions != capture.DefaultMaxFilterInstructions {
		t.Errorf("Capture.MaxFilterInstructions = %d, want default %d", cfg.Capture.MaxFilterInstructions, capture.DefaultMaxFilterInstructions)
	}
}

// TestLoad_CaptureOverride covers T-1301's [capture] section: the
// server-enforced cap ceilings and file root all override cleanly.
func TestLoad_CaptureOverride(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())
	toml := `
[server]
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"
[capture]
root = "/data/caps"
max_duration_sec = 30
max_bytes = 1048576
max_packets = 500
retention_hours = 6
max_filter_instructions = 16
`
	cfg, err := Load(writeTemp(t, "capture.toml", toml), discardLogger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Capture.Root != "/data/caps" || cfg.Capture.MaxDurationSec != 30 ||
		cfg.Capture.MaxBytes != 1048576 || cfg.Capture.MaxPackets != 500 ||
		cfg.Capture.RetentionHours != 6 || cfg.Capture.MaxFilterInstructions != 16 {
		t.Errorf("Capture section did not override cleanly: %+v", cfg.Capture)
	}
}

// TestLoad_FirewallLogOverride covers T-505's [firewalllog] section: both
// fields override cleanly, mirroring [safety]'s dev_interfaces_dir
// precedent.
func TestLoad_FirewallLogOverride(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())
	toml := `
[server]
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"

[firewalllog]
path = "/custom/pve-firewall.log"
dev_fixture_dir = "testdata/firewall-logs"
`
	path := writeTemp(t, "fwlog.toml", toml)

	cfg, err := Load(path, discardLogger())
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	if cfg.FirewallLog.Path != "/custom/pve-firewall.log" {
		t.Errorf("FirewallLog.Path = %q, want /custom/pve-firewall.log", cfg.FirewallLog.Path)
	}
	if cfg.FirewallLog.DevFixtureDir != "testdata/firewall-logs" {
		t.Errorf("FirewallLog.DevFixtureDir = %q, want testdata/firewall-logs", cfg.FirewallLog.DevFixtureDir)
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

// TestLoadStorageOnly_SucceedsWithNoTLSCertAtAll is T-607's regression test
// for the bug its own packaging-matrix run found: cmd/vnproxctl's
// snapshots/rollback-now commands (docs/deployment.md's "daemon-
// independent disaster-recovery path") must not require a resolvable PVE
// TLS certificate at all — the same nooverride.toml shape
// TestLoad_NoTLSOverrideFailsCleanlyWhenPVECertAbsent proves the full
// Load() correctly rejects (for vnproxd itself, which does need to bind
// HTTPS) must instead *succeed* through LoadStorageOnly, since a
// storage-only caller never touches TLS.
func TestLoadStorageOnly_SucceedsWithNoTLSCertAtAll(t *testing.T) {
	toml := `
[server]
listen = "127.0.0.1:8007"

[storage]
db_path = "/var/lib/vnprox/vnprox.db"
`
	path := writeTemp(t, "storageonly.toml", toml)

	storageCfg, err := LoadStorageOnly(path, discardLogger())
	if err != nil {
		t.Fatalf("LoadStorageOnly must succeed with no TLS cert/override present at all: %v", err)
	}
	if storageCfg.DBPath != "/var/lib/vnprox/vnprox.db" {
		t.Errorf("DBPath = %q, want the configured value", storageCfg.DBPath)
	}
}

// TestLoadStorageOnly_AppliesDefaultsWhenSectionOmitted mirrors
// TestLoad_DefaultsAppliedWhenSectionsOmitted for the storage-only path.
func TestLoadStorageOnly_AppliesDefaultsWhenSectionOmitted(t *testing.T) {
	path := writeTemp(t, "empty.toml", "")

	storageCfg, err := LoadStorageOnly(path, discardLogger())
	if err != nil {
		t.Fatalf("LoadStorageOnly with an empty file: %v", err)
	}
	if storageCfg.DBPath != DefaultDBPath {
		t.Errorf("DBPath = %q, want default %q", storageCfg.DBPath, DefaultDBPath)
	}
	if storageCfg.SessionKeyFile != DefaultSessionKeyFile {
		t.Errorf("SessionKeyFile = %q, want default %q", storageCfg.SessionKeyFile, DefaultSessionKeyFile)
	}
}

// TestLoadStorageOnly_MissingFile mirrors TestLoad_MissingFile.
func TestLoadStorageOnly_MissingFile(t *testing.T) {
	_, err := LoadStorageOnly("/nonexistent/path.toml", discardLogger())
	if err == nil {
		t.Fatal("expected an error for a nonexistent config file, got nil")
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

// TestLoad_MetricsDefaults covers T-1001's [metrics] section left entirely
// unset: Enabled defaults true (docs/security.md's exporter is opt-out, not
// opt-in, unlike T-1002's flow listeners), KeyFile defaults to
// DefaultMetricsKeyFile, AllowFrom defaults to nil ("allow any source").
func TestLoad_MetricsDefaults(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())
	toml := `
[server]
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"
`
	cfg, err := Load(writeTemp(t, "metrics-default.toml", toml), discardLogger())
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	if !cfg.Metrics.Enabled {
		t.Error("Metrics.Enabled = false, want true (default) when [metrics] is omitted")
	}
	if cfg.Metrics.KeyFile != DefaultMetricsKeyFile {
		t.Errorf("Metrics.KeyFile = %q, want default %q", cfg.Metrics.KeyFile, DefaultMetricsKeyFile)
	}
	if len(cfg.Metrics.AllowFrom) != 0 {
		t.Errorf("Metrics.AllowFrom = %v, want empty (default: allow any source)", cfg.Metrics.AllowFrom)
	}
}

// TestLoad_MetricsOverride covers explicit [metrics] values, including
// disabling the exporter and a multi-entry allow_from CIDR list.
func TestLoad_MetricsOverride(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())
	toml := `
[server]
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"

[metrics]
enabled = false
key_file = "/custom/metrics.key"
allow_from = ["10.0.0.0/8", "192.168.1.5/32"]
`
	cfg, err := Load(writeTemp(t, "metrics-override.toml", toml), discardLogger())
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	if cfg.Metrics.Enabled {
		t.Error("Metrics.Enabled = true, want false (explicit override)")
	}
	if cfg.Metrics.KeyFile != "/custom/metrics.key" {
		t.Errorf("Metrics.KeyFile = %q, want /custom/metrics.key", cfg.Metrics.KeyFile)
	}
	if len(cfg.Metrics.AllowFrom) != 2 {
		t.Fatalf("Metrics.AllowFrom = %v, want 2 entries", cfg.Metrics.AllowFrom)
	}
	if !cfg.Metrics.AllowFrom[0].Contains(mustParseIP(t, "10.1.2.3")) {
		t.Errorf("Metrics.AllowFrom[0] = %v, want it to contain 10.1.2.3", cfg.Metrics.AllowFrom[0])
	}
	if !cfg.Metrics.AllowFrom[1].Contains(mustParseIP(t, "192.168.1.5")) {
		t.Errorf("Metrics.AllowFrom[1] = %v, want it to contain 192.168.1.5", cfg.Metrics.AllowFrom[1])
	}
	if cfg.Metrics.AllowFrom[1].Contains(mustParseIP(t, "192.168.1.6")) {
		t.Errorf("Metrics.AllowFrom[1] = %v, want it to exclude 192.168.1.6 (/32)", cfg.Metrics.AllowFrom[1])
	}
}

// TestLoad_FlowsHostSampleDefaults covers T-1004's [flows] additions:
// both samplers default to disabled, and the shared poll interval defaults
// to internal/flow/hostsample.DefaultHostSampleInterval (10s) when
// host_sample_interval_sec is omitted.
func TestLoad_FlowsHostSampleDefaults(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())
	toml := `
[server]
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"
`
	cfg, err := Load(writeTemp(t, "flows-hostsample-default.toml", toml), discardLogger())
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	if cfg.Flows.ConntrackSamplingEnabled {
		t.Error("Flows.ConntrackSamplingEnabled = true, want false (default, opt-in)")
	}
	if cfg.Flows.EBPFSamplingEnabled {
		t.Error("Flows.EBPFSamplingEnabled = true, want false (default, opt-in)")
	}
	if cfg.Flows.HostSampleIntervalSec != 10 {
		t.Errorf("Flows.HostSampleIntervalSec = %d, want default 10", cfg.Flows.HostSampleIntervalSec)
	}
}

// TestLoad_FlowsHostSampleOverride covers explicit [flows] host-sample
// values: both samplers can be independently enabled, and the interval
// overrides cleanly.
func TestLoad_FlowsHostSampleOverride(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())
	toml := `
[server]
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"

[flows]
conntrack_sampling_enabled = true
ebpf_sampling_enabled = true
host_sample_interval_sec = 30
`
	cfg, err := Load(writeTemp(t, "flows-hostsample-override.toml", toml), discardLogger())
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	if !cfg.Flows.ConntrackSamplingEnabled {
		t.Error("Flows.ConntrackSamplingEnabled = false, want true (explicit override)")
	}
	if !cfg.Flows.EBPFSamplingEnabled {
		t.Error("Flows.EBPFSamplingEnabled = false, want true (explicit override)")
	}
	if cfg.Flows.HostSampleIntervalSec != 30 {
		t.Errorf("Flows.HostSampleIntervalSec = %d, want 30", cfg.Flows.HostSampleIntervalSec)
	}
}

// TestLoad_MetricsInvalidCIDRFailsFast covers Load failing fast on a
// malformed allow_from entry rather than starting the daemon with a
// silently-ignored (or worse, silently-permissive) allowlist.
func TestLoad_MetricsInvalidCIDRFailsFast(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())
	toml := `
[server]
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"

[metrics]
allow_from = ["not-a-cidr"]
`
	_, err := Load(writeTemp(t, "metrics-bad-cidr.toml", toml), discardLogger())
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Load error = %v, want ErrInvalidConfig", err)
	}
}

// TestLoad_LatmeshDefaults covers T-1303's [latmesh] section: every
// tunable defaults to internal/latmesh's own documented constants when
// omitted.
func TestLoad_LatmeshDefaults(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())
	toml := `
[server]
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"
`
	cfg, err := Load(writeTemp(t, "latmesh-default.toml", toml), discardLogger())
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	if cfg.Latmesh.ProbeIntervalSec != 10 {
		t.Errorf("Latmesh.ProbeIntervalSec = %d, want default 10", cfg.Latmesh.ProbeIntervalSec)
	}
	if cfg.Latmesh.RetentionMinutes != 60 {
		t.Errorf("Latmesh.RetentionMinutes = %d, want default 60", cfg.Latmesh.RetentionMinutes)
	}
	if cfg.Latmesh.MaxRows != 500_000 {
		t.Errorf("Latmesh.MaxRows = %d, want default 500000", cfg.Latmesh.MaxRows)
	}
}

// TestLoad_LatmeshOverride covers explicit [latmesh] values.
func TestLoad_LatmeshOverride(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())
	toml := `
[server]
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"

[latmesh]
probe_interval_sec = 30
retention_minutes = 120
max_rows = 1000000
`
	cfg, err := Load(writeTemp(t, "latmesh-override.toml", toml), discardLogger())
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	if cfg.Latmesh.ProbeIntervalSec != 30 {
		t.Errorf("Latmesh.ProbeIntervalSec = %d, want 30", cfg.Latmesh.ProbeIntervalSec)
	}
	if cfg.Latmesh.RetentionMinutes != 120 {
		t.Errorf("Latmesh.RetentionMinutes = %d, want 120", cfg.Latmesh.RetentionMinutes)
	}
	if cfg.Latmesh.MaxRows != 1_000_000 {
		t.Errorf("Latmesh.MaxRows = %d, want 1000000", cfg.Latmesh.MaxRows)
	}
}

// TestLoad_MTUProbeDefaults covers T-1306's [mtuprobe] section: the probe
// interval defaults to internal/mtuprobe's own documented constant (300s,
// deliberately coarser than [latmesh]'s 10s default) when omitted.
func TestLoad_MTUProbeDefaults(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())
	toml := `
[server]
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"
`
	cfg, err := Load(writeTemp(t, "mtuprobe-default.toml", toml), discardLogger())
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	if cfg.MTUProbe.ProbeIntervalSec != 300 {
		t.Errorf("MTUProbe.ProbeIntervalSec = %d, want default 300", cfg.MTUProbe.ProbeIntervalSec)
	}
}

// TestLoad_MTUProbeOverride covers an explicit [mtuprobe] value.
func TestLoad_MTUProbeOverride(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())
	toml := `
[server]
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"

[mtuprobe]
probe_interval_sec = 600
`
	cfg, err := Load(writeTemp(t, "mtuprobe-override.toml", toml), discardLogger())
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	if cfg.MTUProbe.ProbeIntervalSec != 600 {
		t.Errorf("MTUProbe.ProbeIntervalSec = %d, want 600", cfg.MTUProbe.ProbeIntervalSec)
	}
}

// TestLoad_WanDefaults covers T-1405's [wan] section: every tunable
// defaults to internal/wan's own documented constants when omitted.
func TestLoad_WanDefaults(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())
	toml := `
[server]
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"
`
	cfg, err := Load(writeTemp(t, "wan-default.toml", toml), discardLogger())
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	if cfg.Wan.ProbeIntervalSec != 10 {
		t.Errorf("Wan.ProbeIntervalSec = %d, want default 10", cfg.Wan.ProbeIntervalSec)
	}
	if cfg.Wan.RetentionMinutes != 60 {
		t.Errorf("Wan.RetentionMinutes = %d, want default 60", cfg.Wan.RetentionMinutes)
	}
	if cfg.Wan.MaxRows != 500_000 {
		t.Errorf("Wan.MaxRows = %d, want default 500000", cfg.Wan.MaxRows)
	}
	if cfg.Wan.LossWarnPct != 0 {
		t.Errorf("Wan.LossWarnPct = %v, want 0 (unset -> internal/wan.DefaultLossWarnPct applies downstream)", cfg.Wan.LossWarnPct)
	}
}

// TestLoad_WanOverride covers explicit [wan] values.
func TestLoad_WanOverride(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())
	toml := `
[server]
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"

[wan]
probe_interval_sec = 30
retention_minutes = 120
max_rows = 1000000
loss_warn_pct = 15
`
	cfg, err := Load(writeTemp(t, "wan-override.toml", toml), discardLogger())
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	if cfg.Wan.ProbeIntervalSec != 30 {
		t.Errorf("Wan.ProbeIntervalSec = %d, want 30", cfg.Wan.ProbeIntervalSec)
	}
	if cfg.Wan.RetentionMinutes != 120 {
		t.Errorf("Wan.RetentionMinutes = %d, want 120", cfg.Wan.RetentionMinutes)
	}
	if cfg.Wan.MaxRows != 1_000_000 {
		t.Errorf("Wan.MaxRows = %d, want 1000000", cfg.Wan.MaxRows)
	}
	if cfg.Wan.LossWarnPct != 15 {
		t.Errorf("Wan.LossWarnPct = %v, want 15", cfg.Wan.LossWarnPct)
	}
}

// TestLoad_ChangesetsDefaults is T-2003's zero-config guarantee: an omitted
// [changesets] section leaves approval_required off (byte-identical apply
// behavior to every pre-T-2003 deployment) and self-approval permitted
// (matching the implicit single-admin workflow already true when there is
// no approval gate at all).
func TestLoad_ChangesetsDefaults(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())
	toml := `
[server]
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"
`
	cfg, err := Load(writeTemp(t, "changesets-default.toml", toml), discardLogger())
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	if cfg.Changesets.ApprovalRequired {
		t.Error("Changesets.ApprovalRequired = true, want false (default) when [changesets] is omitted")
	}
	if !cfg.Changesets.AllowSelfApproval {
		t.Error("Changesets.AllowSelfApproval = false, want true (default) when [changesets] is omitted")
	}
	if len(cfg.Changesets.Approvers) != 0 {
		t.Errorf("Changesets.Approvers = %v, want empty (default: anyone with netWrite)", cfg.Changesets.Approvers)
	}
}

// TestLoad_ChangesetsOverride covers an explicit [changesets] section:
// approval required, self-approval forbidden, an explicit approvers list.
func TestLoad_ChangesetsOverride(t *testing.T) {
	certPath, keyPath := writeTestCert(t, t.TempDir())
	toml := `
[server]
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"

[changesets]
approval_required = true
allow_self_approval = false
approvers = ["alice@pve", "bob@pve"]
`
	cfg, err := Load(writeTemp(t, "changesets-override.toml", toml), discardLogger())
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	if !cfg.Changesets.ApprovalRequired {
		t.Error("Changesets.ApprovalRequired = false, want true (explicit override)")
	}
	if cfg.Changesets.AllowSelfApproval {
		t.Error("Changesets.AllowSelfApproval = true, want false (explicit override)")
	}
	want := []string{"alice@pve", "bob@pve"}
	if len(cfg.Changesets.Approvers) != len(want) || cfg.Changesets.Approvers[0] != want[0] || cfg.Changesets.Approvers[1] != want[1] {
		t.Errorf("Changesets.Approvers = %v, want %v", cfg.Changesets.Approvers, want)
	}
}

func mustParseIP(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s)
	if ip == nil {
		t.Fatalf("net.ParseIP(%q) returned nil", s)
	}
	return ip
}
