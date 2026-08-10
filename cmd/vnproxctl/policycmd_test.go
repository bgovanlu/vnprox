package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePolicyFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing policy file: %v", err)
	}
	return path
}

func TestRunPolicy_UnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runPolicy([]string{"frobnicate"}, &stdout, &stderr); code != ExitUsage {
		t.Errorf("exit = %d, want ExitUsage", code)
	}
	if code := runPolicy(nil, &stdout, &stderr); code != ExitUsage {
		t.Errorf("exit with no subcommand = %d, want ExitUsage", code)
	}
}

func TestRunPolicyLint_Valid(t *testing.T) {
	path := writePolicyFile(t, `version: 1
rules:
  - id: no-vmbr9
    description: vmbr9 is managed out of band
    severity: deny
    match:
      - {field: target.id, op: eq, value: vmbr9}
`)
	var stdout, stderr bytes.Buffer
	if code := runPolicyLint([]string{"--policy=" + path}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("exit = %d, want success; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "no-vmbr9") {
		t.Errorf("stdout = %q, want it to list the rule", stdout.String())
	}
}

// TestRunPolicyLint_MalformedNamesFileRuleAndField is acceptance criterion 5
// from the operator's side: the message they get from the CLI is the same
// one the daemon would refuse to start with, and it names all three things.
func TestRunPolicyLint_MalformedNamesFileRuleAndField(t *testing.T) {
	path := writePolicyFile(t, `version: 1
rules:
  - id: broken-rule
    description: d
    severity: deny
    match:
      - {field: target.nonsense, op: eq, value: x}
`)
	var stdout, stderr bytes.Buffer
	if code := runPolicyLint([]string{"--policy=" + path}, &stdout, &stderr); code != ExitUsage {
		t.Fatalf("exit = %d, want ExitUsage", code)
	}
	msg := stderr.String()
	for _, want := range []string{path, "broken-rule", "rules[0].match[0].field"} {
		if !strings.Contains(msg, want) {
			t.Errorf("stderr = %q, want it to name %q", msg, want)
		}
	}
}

func TestRunPolicyLint_RequiresPolicyFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runPolicyLint(nil, &stdout, &stderr); code != ExitUsage {
		t.Errorf("exit = %d, want ExitUsage", code)
	}
}

// TestRunPolicyExamples_PrintsAValidDocument closes the loop between the
// shipped examples and the linter: what `policy examples` prints must be
// something `policy lint` accepts.
func TestRunPolicyExamples_PrintsAValidDocument(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runPolicyExamples(nil, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("exit = %d, want success; stderr: %s", code, stderr.String())
	}
	path := writePolicyFile(t, stdout.String())

	var lintOut, lintErr bytes.Buffer
	if code := runPolicyLint([]string{"--policy=" + path}, &lintOut, &lintErr); code != ExitSuccess {
		t.Fatalf("the shipped examples do not lint: exit %d, stderr: %s", code, lintErr.String())
	}
}

func TestRunPolicyTest_RequiresChangeset(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runPolicyTest([]string{"--policy=/nonexistent.yaml"}, &stdout, &stderr); code != ExitUsage {
		t.Errorf("exit = %d, want ExitUsage when --changeset is missing", code)
	}
}

// TestRunPolicyTest_MalformedPolicyFailsBeforeAnyDaemonCall: the file is
// parsed locally first, so a typo costs a round trip to nothing.
func TestRunPolicyTest_MalformedPolicyFailsBeforeAnyDaemonCall(t *testing.T) {
	path := writePolicyFile(t, "version: 1\nrules:\n  - id: x\n")
	var stdout, stderr bytes.Buffer
	code := runPolicyTest([]string{"--policy=" + path, "--changeset=01ABC", "--url=https://127.0.0.1:1/api/v1", "--token=t"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Fatalf("exit = %d, want ExitUsage, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), path) {
		t.Errorf("stderr = %q, want it to name the policy file", stderr.String())
	}
}

func TestPolicyTestExitCode(t *testing.T) {
	cases := []struct {
		name   string
		result policyResultWire
		want   int
	}{
		{"clean", policyResultWire{}, ExitSuccess},
		{"warn only", policyResultWire{Findings: []policyFindingWire{{Severity: "warning", Code: "policy.violation"}}}, ExitSuccess},
		{"deny", policyResultWire{Findings: []policyFindingWire{{Severity: "error", Code: "policy.violation"}}}, ExitPending},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := policyTestExitCode(tc.result); got != tc.want {
				t.Errorf("exit = %d, want %d", got, tc.want)
			}
		})
	}
}
