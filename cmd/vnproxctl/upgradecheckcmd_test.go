// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bgovanlu/vnprox/internal/doctor"
)

func TestUpgradeCheckRequiresExactlyOneTargetVersion(t *testing.T) {
	tests := [][]string{
		{},
		{"9.2", "8.2"},
	}
	for _, args := range tests {
		var stdout, stderr bytes.Buffer
		if code := runUpgradeCheck(args, &stdout, &stderr); code != ExitUsage {
			t.Errorf("runUpgradeCheck(%v) = %d; want ExitUsage (%d); stderr: %s", args, code, ExitUsage, stderr.String())
		}
	}
}

func TestUpgradeCheckRejectsBadVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runUpgradeCheck([]string{"not-a-version"}, &stdout, &stderr); code != ExitUsage {
		t.Errorf("code = %d; want ExitUsage (%d); stderr: %s", code, ExitUsage, stderr.String())
	}
}

func TestUpgradeCheckRejectsBadOutputFormat(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runUpgradeCheck([]string{"-o", "jsno", "9.2"}, &stdout, &stderr); code != ExitUsage {
		t.Errorf("code = %d; want ExitUsage (%d)", code, ExitUsage)
	}
}

// TestUpgradeCheckBelowEveryEntrysBreaksAtYieldsEmptyReport proves Run's
// filtering reaches all the way through the CLI: asking about a target
// below 9.0 (both catalog entries' BreaksAt) produces zero results.
func TestUpgradeCheckBelowEveryEntrysBreaksAtYieldsEmptyReport(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runUpgradeCheck([]string{"-o", "json", "--pmxcfs", t.TempDir(), "8.2"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("code = %d; want ExitSuccess; stderr: %s", code, stderr.String())
	}

	var report doctor.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decoding -o json output: %v\n%s", err, stdout.String())
	}
	if len(report.Results) != 0 {
		t.Errorf("got %d results for a PVE 8.2 target; want 0", len(report.Results))
	}
}

// TestUpgradeCheckJSONIsWellFormed exercises the full command against a PVE
// 9.2 target with a real (temp) pmxcfs directory, and asserts the emitted
// report satisfies doctor.Report's own invariants — the same contract
// `vnproxctl doctor`'s JSON output already has to meet.
func TestUpgradeCheckJSONIsWellFormed(t *testing.T) {
	pmxcfs := t.TempDir()
	if err := os.MkdirAll(filepath.Join(pmxcfs, "firewall"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pmxcfs, "firewall", "cluster.fw"), []byte("[OPTIONS]\n\nenable: 1\n"), 0o600); err != nil {
		t.Fatalf("writing cluster.fw: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runUpgradeCheck([]string{"-o", "json", "--pmxcfs", pmxcfs, "9.2"}, &stdout, &stderr)
	if code != ExitSuccess && code != ExitError {
		t.Fatalf("unexpected exit code %d; stderr: %s", code, stderr.String())
	}

	var report doctor.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decoding -o json output: %v\n%s", err, stdout.String())
	}
	if err := report.Validate(); err != nil {
		t.Errorf("emitted report fails its own invariants: %v", err)
	}
	if len(report.Results) != 2 {
		t.Errorf("got %d results for a PVE 9.2 target; want 2 (both catalog entries apply)", len(report.Results))
	}
	// Every failing or warning result must carry a remediation on the wire.
	for _, res := range report.Results {
		if (res.Status == doctor.StatusFail || res.Status == doctor.StatusWarn) && res.Remediation == "" {
			t.Errorf("check %q is %s in JSON with no remediation", res.Check, res.Status)
		}
	}
}

// TestUpgradeCheckTableRendering is a smoke test for the human-readable
// path (the default, no -o json) — it must at least name the target
// version and not blow up.
func TestUpgradeCheckTableRendering(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runUpgradeCheck([]string{"--pmxcfs", t.TempDir(), "9.2"}, &stdout, &stderr)
	if code != ExitSuccess && code != ExitError {
		t.Fatalf("unexpected exit code %d; stderr: %s", code, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("9.2")) {
		t.Errorf("table output does not mention the target version:\n%s", stdout.String())
	}
}

func TestParseUpgradeTargetVersion(t *testing.T) {
	tests := []struct {
		in      string
		wantErr bool
		wantMaj int
		wantMin int
	}{
		{"9.2", false, 9, 2},
		{"9.2.4", false, 9, 2},
		{"9", false, 9, 0},
		{"8.2", false, 8, 2},
		{"", true, 0, 0},
		{"nine.two", true, 0, 0},
		{"9.two", true, 0, 0},
	}
	for _, tt := range tests {
		v, err := parseUpgradeTargetVersion(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseUpgradeTargetVersion(%q) error = %v; wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if err != nil {
			continue
		}
		if v.Major != tt.wantMaj || v.Minor != tt.wantMin {
			t.Errorf("parseUpgradeTargetVersion(%q) = %+v; want {%d %d}", tt.in, v, tt.wantMaj, tt.wantMin)
		}
	}
}

// --- readFirewallEnabled / readNftablesEngineActive (unit-level) -----------

func TestReadFirewallEnabled(t *testing.T) {
	// Field order is densest-pointer-first: both strings precede the bools,
	// since govet's fieldalignment measures bytes up to the final pointer.
	tests := []struct {
		name       string
		content    string
		fileExists bool
		wantOK     bool
		wantVal    bool
	}{
		{name: "no pmxcfs dir at all", fileExists: false, wantOK: false},
		{name: "enable: 1", fileExists: true, content: "[OPTIONS]\n\nenable: 1\n", wantOK: true, wantVal: true},
		{name: "enable: 0", fileExists: true, content: "[OPTIONS]\n\nenable: 0\n", wantOK: true, wantVal: false},
		{name: "no explicit enable line", fileExists: true, content: "[OPTIONS]\n\n", wantOK: true, wantVal: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.fileExists {
				if err := os.MkdirAll(filepath.Join(dir, "firewall"), 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(filepath.Join(dir, "firewall", "cluster.fw"), []byte(tt.content), 0o600); err != nil {
					t.Fatalf("write: %v", err)
				}
			}
			gotVal, gotOK := readFirewallEnabled(dir)
			if gotOK != tt.wantOK {
				t.Errorf("ok = %v; want %v", gotOK, tt.wantOK)
			}
			if gotOK && gotVal != tt.wantVal {
				t.Errorf("enabled = %v; want %v", gotVal, tt.wantVal)
			}
		})
	}
}

func TestReadNftablesEngineActive(t *testing.T) {
	t.Run("flag file present -> iptables is effective (not active)", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "force-disable")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		active, ok := readNftablesEngineActive(path)
		if !ok || active {
			t.Errorf("active=%v ok=%v; want active=false ok=true", active, ok)
		}
	})
	t.Run("flag file absent -> nftables is effective (active)", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "does-not-exist")
		active, ok := readNftablesEngineActive(path)
		if !ok || !active {
			t.Errorf("active=%v ok=%v; want active=true ok=true", active, ok)
		}
	})
}
