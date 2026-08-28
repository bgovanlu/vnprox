// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"strings"
	"testing"
)

// siemexportTOML wraps a [siemexport] section in the minimum surrounding
// config a Load call needs — the same helper shape gitsyncTOML uses.
func siemexportTOML(t *testing.T, section string) string {
	t.Helper()
	certPath, keyPath := writeTestCert(t, t.TempDir())
	return `
[server]
listen = "127.0.0.1:8007"
tls_cert = "` + certPath + `"
tls_key = "` + keyPath + `"
` + section
}

// TestLoad_SIEMExportOffByDefault is the card's "off by default"
// requirement at the configuration layer: a config file with no
// [siemexport] section at all yields a section that ships nothing.
func TestLoad_SIEMExportOffByDefault(t *testing.T) {
	cfg, err := Load(writeTemp(t, "dev.toml", siemexportTOML(t, "")), discardLogger())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SIEMExport.Enabled {
		t.Error("siemexport is enabled with no [siemexport] section")
	}
	if cfg.SIEMExport.Address != "" || cfg.SIEMExport.Path != "" {
		t.Errorf("siemexport has a destination configured by default: %+v", cfg.SIEMExport)
	}
}

// TestLoad_SIEMExportValidation is the table over the section's own gates:
// a disabled section is never an error however incomplete (mirroring
// resolveGitSyncConfig's asymmetry); an enabled one refuses a
// half-configured destination.
func TestLoad_SIEMExportValidation(t *testing.T) {
	//nolint:govet // fieldalignment: test table; field order documents each case, not packing.
	tests := []struct {
		name    string
		section string
		wantErr string
		check   func(t *testing.T, s SIEMExportConfig)
	}{
		{
			name: "a fully configured syslog section loads",
			section: `
[siemexport]
enabled = true
format = "syslog"
network = "tcp"
address = "siem.example:6514"
buffer_size = 8192
facility = 17
`,
			check: func(t *testing.T, s SIEMExportConfig) {
				if !s.Enabled || s.Format != "syslog" || s.Network != "tcp" || s.Address != "siem.example:6514" {
					t.Fatalf("resolved section = %+v", s)
				}
				if s.BufferSize != 8192 {
					t.Errorf("BufferSize = %d, want 8192", s.BufferSize)
				}
				if s.Facility != 17 {
					t.Errorf("Facility = %d, want 17", s.Facility)
				}
			},
		},
		{
			name: "a fully configured jsonl-to-file section loads",
			section: `
[siemexport]
enabled = true
format = "jsonl"
path = "/var/log/vnprox/audit.jsonl"
`,
			check: func(t *testing.T, s SIEMExportConfig) {
				if !s.Enabled || s.Format != "jsonl" || s.Path != "/var/log/vnprox/audit.jsonl" {
					t.Fatalf("resolved section = %+v", s)
				}
				if s.Network != "" || s.Address != "" {
					t.Errorf("a file destination resolved a network destination too: %+v", s)
				}
			},
		},
		{
			name: "a fully configured jsonl-to-socket section loads",
			section: `
[siemexport]
enabled = true
format = "jsonl"
network = "unix"
address = "/run/vnprox/siem.sock"
`,
			check: func(t *testing.T, s SIEMExportConfig) {
				if s.Network != "unix" || s.Address != "/run/vnprox/siem.sock" || s.Path != "" {
					t.Fatalf("resolved section = %+v", s)
				}
			},
		},
		{
			name: "buffer_size and facility default when unset",
			section: `
[siemexport]
enabled = true
format = "syslog"
network = "udp"
address = "siem.example:514"
`,
			check: func(t *testing.T, s SIEMExportConfig) {
				if s.BufferSize != 4096 {
					t.Errorf("BufferSize = %d, want the default 4096", s.BufferSize)
				}
				if s.Facility != 16 {
					t.Errorf("Facility = %d, want the default 16 (local0)", s.Facility)
				}
			},
		},
		{
			name: "a disabled but half-filled section is not an error",
			section: `
[siemexport]
enabled = false
format = "syslog"
`,
			check: func(t *testing.T, s SIEMExportConfig) {
				if s.Enabled {
					t.Error("section reports enabled")
				}
			},
		},
		{
			name: "enabled with no format is fatal",
			section: `
[siemexport]
enabled = true
address = "siem.example:6514"
`,
			wantErr: "siemexport.format is required",
		},
		{
			name: "an unknown format is fatal",
			section: `
[siemexport]
enabled = true
format = "gelf"
`,
			wantErr: `siemexport.format "gelf" is not one of syslog, jsonl`,
		},
		{
			name: "syslog with no network is fatal",
			section: `
[siemexport]
enabled = true
format = "syslog"
address = "siem.example:6514"
`,
			wantErr: "siemexport.network is required",
		},
		{
			name: "syslog with an unknown network is fatal",
			section: `
[siemexport]
enabled = true
format = "syslog"
network = "unix"
address = "/run/siem.sock"
`,
			wantErr: "siemexport.network",
		},
		{
			name: "syslog with no address is fatal",
			section: `
[siemexport]
enabled = true
format = "syslog"
network = "tcp"
`,
			wantErr: "siemexport.address is required",
		},
		{
			name: "syslog with a path set is fatal (no file destination)",
			section: `
[siemexport]
enabled = true
format = "syslog"
network = "tcp"
address = "siem.example:6514"
path = "/var/log/vnprox/audit.log"
`,
			wantErr: "siemexport.path must not be set",
		},
		{
			name: "jsonl with neither path nor network/address is fatal",
			section: `
[siemexport]
enabled = true
format = "jsonl"
`,
			wantErr: "is required when siemexport.format is \"jsonl\"",
		},
		{
			name: "jsonl with both path and network/address is fatal",
			section: `
[siemexport]
enabled = true
format = "jsonl"
path = "/var/log/vnprox/audit.jsonl"
network = "tcp"
address = "siem.example:9000"
`,
			wantErr: "mutually exclusive",
		},
		{
			name: "jsonl with network but no address is fatal",
			section: `
[siemexport]
enabled = true
format = "jsonl"
network = "tcp"
`,
			wantErr: "siemexport.address is required",
		},
		{
			name: "a non-positive buffer_size is fatal, never silently defaulted",
			section: `
[siemexport]
enabled = true
format = "jsonl"
path = "/var/log/vnprox/audit.jsonl"
buffer_size = -1
`,
			wantErr: "siemexport.buffer_size must be positive",
		},
		{
			name: "an out-of-range facility is fatal",
			section: `
[siemexport]
enabled = true
format = "syslog"
network = "tcp"
address = "siem.example:6514"
facility = 99
`,
			wantErr: "siemexport.facility must be 0-23",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(writeTemp(t, "dev.toml", siemexportTOML(t, tc.section)), discardLogger())
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
			tc.check(t, cfg.SIEMExport)
		})
	}
}
