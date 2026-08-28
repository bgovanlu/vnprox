// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/doctor"
)

// TestDoctorExitsNonZeroOnFailure covers AC4 end to end through the real
// command, not just the report type: a config that does not exist is a failing
// check, and the process must say so in its exit status.
func TestDoctorExitsNonZeroOnFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runDoctor([]string{"--config", filepath.Join(t.TempDir(), "absent.toml")}, &stdout, &stderr)

	if code == ExitSuccess {
		t.Errorf("runDoctor exited %d with a missing config; want non-zero (AC4)", code)
	}
	if !strings.Contains(stdout.String(), "config") {
		t.Errorf("output does not mention the config check:\n%s", stdout.String())
	}
	// The remediation for an absent file must not talk about syntax errors.
	if strings.Contains(stdout.String(), "fix the syntax error") {
		t.Errorf("a missing config was told to fix its syntax:\n%s", stdout.String())
	}
}

// TestDoctorJSONIsWellFormed covers AC3: the JSON is the contract T-1902's
// bundle and CI consume, so it must decode into the documented shape.
func TestDoctorJSONIsWellFormed(t *testing.T) {
	var stdout, stderr bytes.Buffer
	runDoctor([]string{"--config", filepath.Join(t.TempDir(), "absent.toml"), "-o", "json"}, &stdout, &stderr)

	var rep doctor.Report
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
		t.Fatalf("decoding -o json output: %v\n%s", err, stdout.String())
	}
	if len(rep.Results) != len(doctor.AllChecks) {
		t.Errorf("JSON carried %d results; want %d", len(rep.Results), len(doctor.AllChecks))
	}
	if err := rep.Validate(); err != nil {
		t.Errorf("the emitted report does not satisfy its own invariants: %v", err)
	}
	// Every failing or warning result must carry a remediation on the wire too
	// (AC2) — not only in the human rendering.
	for _, res := range rep.Results {
		if (res.Status == doctor.StatusFail || res.Status == doctor.StatusWarn) && res.Remediation == "" {
			t.Errorf("check %q is %s in JSON with no remediation", res.Check, res.Status)
		}
	}
}

// TestDoctorRejectsBadOutputFormat keeps the usage contract shared with every
// other subcommand: a typo is a usage error, not a silent fallback.
func TestDoctorRejectsBadOutputFormat(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runDoctor([]string{"-o", "jsno"}, &stdout, &stderr); code != ExitUsage {
		t.Errorf("runDoctor -o jsno = %d; want ExitUsage (%d)", code, ExitUsage)
	}
}

// TestDoctorSucceedsOnAHealthyEnoughConfig is the control: without it, the
// failure assertions above could be passing because doctor fails on everything.
// A parseable config on a machine with no PVE yields skips and passes, and no
// failure other than pmxcfs — which this asserts is the *only* one, rather than
// asserting a green run this environment cannot produce.
func TestDoctorSucceedsOnAHealthyEnoughConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "vnprox.toml")
	if err := os.WriteFile(cfgPath, []byte("[server]\nlisten = \"0.0.0.0:8007\"\n"), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	runDoctor([]string{"--config", cfgPath, "-o", "json"}, &stdout, &stderr)

	var rep doctor.Report
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
		t.Fatalf("decoding: %v\n%s", err, stdout.String())
	}

	var failed []string
	for _, res := range rep.Results {
		if res.Status == doctor.StatusFail {
			failed = append(failed, res.Check)
		}
	}
	for _, check := range failed {
		// pmxcfs is the one check that legitimately fails on a developer
		// workstation: this is not a Proxmox node.
		if check != doctor.CheckPmxcfs {
			t.Errorf("check %q failed on a parseable config; only pmxcfs should fail off a PVE node", check)
		}
	}
	// Control: the config check itself must have passed, or this test would be
	// satisfied by a run that never got started.
	for _, res := range rep.Results {
		if res.Check == doctor.CheckConfig && res.Status != doctor.StatusPass {
			t.Errorf("config check = %s (%s); the written config is valid", res.Status, res.Detail)
		}
	}
}

// TestSummarizeSSLine covers the parsing behind the port check's "held by"
// message, including the shape ss actually emits.
func TestSummarizeSSLine(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "ss with process and pid",
			in:   `LISTEN 0 4096 127.0.0.1:8007 0.0.0.0:* users:(("vnproxd",pid=1234,fd=7))`,
			want: "vnproxd (pid 1234)",
		},
		{
			name: "ss with process, no pid",
			in:   `LISTEN 0 4096 0.0.0.0:8007 0.0.0.0:* users:(("proxmox-backup",fd=7))`,
			want: "proxmox-backup",
		},
		{
			name: "no users column (not root)",
			in:   `LISTEN 0 4096 0.0.0.0:8007 0.0.0.0:*`,
			want: "LISTEN 0 4096 0.0.0.0:8007 0.0.0.0:*",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := summarizeSSLine(tt.in); got != tt.want {
				t.Errorf("summarizeSSLine(%q) = %q; want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParsePort(t *testing.T) {
	tests := []struct {
		addr string
		want int
		ok   bool
	}{
		{addr: "0.0.0.0:8007", want: 8007, ok: true},
		{addr: ":8007", want: 8007, ok: true},
		{addr: "0.0.0.0", ok: false},
		{addr: "0.0.0.0:0", ok: false},
		{addr: "0.0.0.0:99999", ok: false},
	}
	for _, tt := range tests {
		got, ok := parsePort(tt.addr)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("parsePort(%q) = %d, %v; want %d, %v", tt.addr, got, ok, tt.want, tt.ok)
		}
	}
}
