package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// telemetryTOML wraps a [telemetry] section in the minimum a full Load needs.
func telemetryTOML(t *testing.T, section string) string {
	t.Helper()
	certPath, keyPath := writeTestCert(t, t.TempDir())
	return `
[server]
listen = "127.0.0.1:8007"
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"

[pve]
api_url = "https://127.0.0.1:8006"
` + section
}

// TestTelemetryIsOffWhenTheSectionIsAbsent is T-2503's "off by default", at
// the config layer: a file with no [telemetry] section produces a config
// that is off AND has nowhere to send to. Both halves matter — an endpoint
// baked into the binary would be one parsing bug away from being contacted.
func TestTelemetryIsOffWhenTheSectionIsAbsent(t *testing.T) {
	path := writeTemp(t, "no-telemetry.toml", telemetryTOML(t, ""))
	if _, err := Load(path, discardLogger()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg, err := LoadTelemetryOnly(path, discardLogger())
	if err != nil {
		t.Fatalf("LoadTelemetryOnly: %v", err)
	}
	if cfg.Enabled {
		t.Error("telemetry is enabled with no [telemetry] section")
	}
	if cfg.Endpoint != "" {
		t.Errorf("telemetry endpoint defaults to %q; vnprox must ship no default collector", cfg.Endpoint)
	}
}

func TestTelemetryConfig(t *testing.T) {
	cases := []struct {
		name        string
		section     string
		wantErrSub  string
		wantEnabled bool
	}{
		{
			name:    "explicitly false is off",
			section: "\n[telemetry]\nenabled = false\n",
		},
		{
			name:    "false with an endpoint left behind is still off and still valid",
			section: "\n[telemetry]\nenabled = false\nendpoint = \"https://collector.example/vnprox\"\n",
		},
		{
			name:        "enabled with an https endpoint",
			section:     "\n[telemetry]\nenabled = true\nendpoint = \"https://collector.example/vnprox\"\n",
			wantEnabled: true,
		},
		{
			name:       "enabled with no endpoint is fatal, not quietly off",
			section:    "\n[telemetry]\nenabled = true\n",
			wantErrSub: "vnprox ships no default collector",
		},
		{
			name:       "enabled with a plaintext endpoint is refused",
			section:    "\n[telemetry]\nenabled = true\nendpoint = \"http://collector.example/vnprox\"\n",
			wantErrSub: "must be https",
		},
		{
			name:       "enabled with a nonsense endpoint is refused",
			section:    "\n[telemetry]\nenabled = true\nendpoint = \"collector.example\"\n",
			wantErrSub: "telemetry.endpoint",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTemp(t, "telemetry.toml", telemetryTOML(t, tc.section))
			// The daemon's full Load and the CLI's LoadTelemetryOnly must
			// agree about every one of these files: an opt-in one accepts
			// and the other refuses would tell an operator two different
			// things about the same line they wrote.
			_, daemonErr := Load(path, discardLogger())
			cfg, cliErr := LoadTelemetryOnly(path, discardLogger())

			if tc.wantErrSub != "" {
				for name, err := range map[string]error{"Load": daemonErr, "LoadTelemetryOnly": cliErr} {
					if err == nil {
						t.Fatalf("%s accepted %s", name, tc.name)
					}
					if !errors.Is(err, ErrInvalidConfig) {
						t.Errorf("%s: want ErrInvalidConfig, got %v", name, err)
					}
					if !strings.Contains(err.Error(), tc.wantErrSub) {
						t.Errorf("%s: error does not contain %q: %v", name, tc.wantErrSub, err)
					}
				}
				return
			}
			if daemonErr != nil {
				t.Fatalf("Load: %v", daemonErr)
			}
			if cliErr != nil {
				t.Fatalf("LoadTelemetryOnly: %v", cliErr)
			}
			if cfg.Enabled != tc.wantEnabled {
				t.Errorf("Enabled = %v, want %v", cfg.Enabled, tc.wantEnabled)
			}
		})
	}
}

// TestLoadTelemetryOnlyNeedsNoCertificate is why LoadTelemetryOnly exists:
// `vnproxctl telemetry` is daemon-independent and must work on a host with
// no PVE certificate — a dev box, or the broken node the operator wants to
// report about. The config below would fail the full Load.
func TestLoadTelemetryOnlyNeedsNoCertificate(t *testing.T) {
	toml := `
[server]
listen = "127.0.0.1:8007"

[telemetry]
enabled = true
endpoint = "https://collector.example/vnprox"
`
	path := writeTemp(t, "no-cert.toml", toml)

	if _, err := Load(path, discardLogger()); err == nil {
		t.Fatal("the full Load accepted a config with no resolvable TLS certificate; this test is no longer testing anything")
	}

	cfg, err := LoadTelemetryOnly(path, discardLogger())
	if err != nil {
		t.Fatalf("LoadTelemetryOnly: %v", err)
	}
	if !cfg.Enabled || cfg.Endpoint != "https://collector.example/vnprox" {
		t.Fatalf("LoadTelemetryOnly = %+v", cfg)
	}
}

// TestLoadTelemetryOnlyAppliesTheSameValidation: the CLI must not accept an
// opt-in the daemon would refuse, or an operator would be told two different
// things about the same file.
func TestLoadTelemetryOnlyAppliesTheSameValidation(t *testing.T) {
	path := writeTemp(t, "bad.toml", "[telemetry]\nenabled = true\n")
	if _, err := LoadTelemetryOnly(path, discardLogger()); err == nil {
		t.Fatal("LoadTelemetryOnly accepted enabled = true with no endpoint")
	}
}

// TestShippedConfigDoesNotEnableTelemetry reads the REAL file the .deb
// installs. "Off by default" is not a default in a struct somebody could
// contradict in packaging; it is a property of the bytes that land in
// /etc/vnprox/vnprox.toml.
func TestShippedConfigDoesNotEnableTelemetry(t *testing.T) {
	path := filepath.Join("..", "..", "packaging", "config", "vnprox.toml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the shipped config: %v", err)
	}

	// The section must not be live at all: a `[telemetry]` header with
	// `enabled = false` under it would also be off, but the shipped file
	// should not carry the knob uncommented at all.
	for i, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "[telemetry]") || strings.HasPrefix(trimmed, "enabled = true") {
			t.Errorf("packaging/config/vnprox.toml line %d is live telemetry configuration: %q", i+1, trimmed)
		}
	}

	// And parsing it must yield telemetry off with no endpoint. Parsed with
	// LoadTelemetryOnly because the shipped file points at PVE certificate
	// paths that do not exist in CI.
	dir := t.TempDir()
	copyPath := filepath.Join(dir, "vnprox.toml")
	if writeErr := os.WriteFile(copyPath, raw, 0o600); writeErr != nil {
		t.Fatalf("copying the shipped config: %v", writeErr)
	}
	cfg, err := LoadTelemetryOnly(copyPath, discardLogger())
	if err != nil {
		t.Fatalf("LoadTelemetryOnly on the shipped config: %v", err)
	}
	if cfg.Enabled {
		t.Error("the shipped config enables telemetry")
	}
	if cfg.Endpoint != "" {
		t.Errorf("the shipped config names a collector: %q", cfg.Endpoint)
	}

	// A control: the file this test read really is the packaged one and
	// really does mention telemetry, so the scan above is not passing
	// because it was handed the wrong file.
	if !strings.Contains(string(raw), "telemetry") {
		t.Fatal("the shipped config does not mention telemetry at all; this test is reading the wrong file")
	}
}
