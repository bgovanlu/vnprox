package main

// verifycmd_test.go drives `vnproxctl verify` through run() — the real
// argv-to-exit-code path, not the internal function.
//
// That distinction is AC4's explicit requirement and it is not pedantry. The
// mock refusal is a guard at the door of the *command*; a test that called
// verify.DetectMock directly would keep passing after somebody wired the CLI
// to skip it, which is precisely the regression the guard exists to survive.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/pvemock"
	"github.com/bgovanlu/vnprox/internal/verify"
)

// mockPVE starts internal/pvemock over the shipped single-node fixture — the
// exact server every other test in this repository develops against, which is
// the thing an operator is most likely to point verify at by accident.
func mockPVE(t *testing.T) *httptest.Server {
	t.Helper()
	fixture, err := pvemock.LoadFixture(filepath.Join("..", "..", "testdata", "clusters", "single-node.yaml"))
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	ts := httptest.NewServer(pvemock.NewServer(fixture, pvemock.WithLogger(discardLogger())))
	t.Cleanup(ts.Close)
	return ts
}

// TestVerifyRefusesAMockEndpointWithoutTheFlag is AC4.
func TestVerifyRefusesAMockEndpointWithoutTheFlag(t *testing.T) {
	ts := mockPVE(t)
	var stdout, stderr bytes.Buffer

	code := run([]string{"verify", "--suite=hardware", "--pve-url", ts.URL, "--pve-token", "u@pve!t=x"}, &stdout, &stderr)

	if code == ExitSuccess {
		t.Fatal("verify exited 0 against internal/pvemock with no --allow-mock: a mock run would be indistinguishable from hardware validation")
	}
	if code != ExitUsage {
		t.Errorf("exit code = %d, want %d (a refusal is a usage problem, not a cluster failure)", code, ExitUsage)
	}
	msg := stderr.String()
	if !strings.Contains(msg, verify.AllowMockFlag) {
		t.Errorf("the refusal does not name %s:\n%s", verify.AllowMockFlag, msg)
	}
	if !strings.Contains(msg, "refusing") {
		t.Errorf("the refusal does not say it refused:\n%s", msg)
	}
	// A refusal that produced a report anyway would defeat the point.
	if strings.Contains(stdout.String(), "passed") {
		t.Errorf("verify printed a report despite refusing:\n%s", stdout.String())
	}
}

// TestVerifyRunsAgainstAMockWithTheFlagAndStampsTheReport is the other half:
// the escape hatch works, and the resulting artifact says what it is. A mock
// run that produced an unmarked report would be worse than a refusal, because
// it would travel.
func TestVerifyRunsAgainstAMockWithTheFlagAndStampsTheReport(t *testing.T) {
	ts := mockPVE(t)
	var stdout, stderr bytes.Buffer

	code := run([]string{
		"verify", "--suite=hardware", "--allow-mock",
		"--pve-url", ts.URL, "--pve-token", "u@pve!t=x", "-o", "json",
	}, &stdout, &stderr)

	// Exit is expected to be non-zero: against a mock with no vnprox daemon
	// behind it, essentially everything skips, and AC3 says a run that
	// validated nothing is not a success.
	if code == ExitSuccess {
		t.Errorf("a run against a mock with no daemon exited 0; nothing was validated")
	}

	var report verify.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decoding the report from stdout: %v\n%s", err, stdout.String())
	}
	if !report.Environment.Mock {
		t.Error("the report of a --allow-mock run is not flagged as a mock run")
	}
	if report.Environment.MockReason == "" {
		t.Error("the report does not record which signal identified the endpoint as a mock")
	}
	if report.Summary.Passed != 0 {
		t.Errorf("summary claims %d passed against a mock with no daemon", report.Summary.Passed)
	}
	_ = stderr
}

// TestVerifyRefusesWithNoPVEEndpoint closes the one way to dodge the mock
// guard: run with no endpoint at all, skip everything, and file the report.
func TestVerifyRefusesWithNoPVEEndpoint(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"verify", "--suite=hardware", "--config", filepath.Join(t.TempDir(), "absent.toml")}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("exit code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "--pve-url") {
		t.Errorf("the error does not say how to supply an endpoint:\n%s", stderr.String())
	}
}

// TestVerifyUnknownOnlyIDIsAUsageErrorNamingIt is AC6 through the CLI.
func TestVerifyUnknownOnlyIDIsAUsageErrorNamingIt(t *testing.T) {
	ts := mockPVE(t)
	var stdout, stderr bytes.Buffer

	code := run([]string{
		"verify", "--allow-mock", "--only", "lldp.no_such_check",
		"--pve-url", ts.URL, "--pve-token", "u@pve!t=x",
	}, &stdout, &stderr)

	if code != ExitUsage {
		t.Errorf("exit code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "lldp.no_such_check") {
		t.Errorf("the error does not name the unknown id:\n%s", stderr.String())
	}
	if strings.Contains(stdout.String(), "passed") {
		t.Errorf("an unknown --only id produced a report instead of an error:\n%s", stdout.String())
	}
}

// TestVerifyDestructiveSuiteRefusesWithoutIUnderstand: the interlock is
// enforced before anything is constructed, so the refusal happens without a
// daemon or a cluster being touched.
func TestVerifyDestructiveSuiteRefusesWithoutIUnderstand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"verify", "--suite=destructive", "--pve-url", "https://127.0.0.1:1", "--pve-token", "u@pve!t=x"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("exit code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "--i-understand") {
		t.Errorf("the refusal does not name the flag:\n%s", stderr.String())
	}
}

// TestVerifyUnknownSuiteIsAUsageError.
func TestVerifyUnknownSuiteIsAUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"verify", "--suite=everything"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("exit code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "everything") {
		t.Errorf("the error does not name the bad suite:\n%s", stderr.String())
	}
}

// TestVerifyListPrintsEveryCheckWithItsPrecondition: an operator deciding
// whether to help needs to see what the suite will ask of their hardware
// before it asks for it.
func TestVerifyListPrintsEveryCheckWithItsPrecondition(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"verify", "--list"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("verify --list exited %d: %s", code, stderr.String())
	}
	out := stdout.String()
	for _, c := range verify.Checks() {
		if !strings.Contains(out, c.ID) {
			t.Errorf("--list omits check %q", c.ID)
		}
		if !strings.Contains(out, c.Precondition) {
			t.Errorf("--list omits %q's hardware precondition", c.ID)
		}
	}

	// And the JSON form, which is what a script would read.
	var jsonOut bytes.Buffer
	if code := run([]string{"verify", "--list", "-o", "json"}, &jsonOut, &stderr); code != ExitSuccess {
		t.Fatalf("verify --list -o json exited %d", code)
	}
	var rows []struct {
		ID           string `json:"id"`
		Precondition string `json:"precondition"`
		MatrixRow    int    `json:"matrixRow"`
	}
	if err := json.Unmarshal(jsonOut.Bytes(), &rows); err != nil {
		t.Fatalf("decoding --list -o json: %v", err)
	}
	if len(rows) != len(verify.Checks()) {
		t.Errorf("--list -o json returned %d rows, want %d", len(rows), len(verify.Checks()))
	}
	for _, r := range rows {
		if r.MatrixRow <= 0 || r.Precondition == "" {
			t.Errorf("--list -o json row %q is missing its matrix row or precondition", r.ID)
		}
	}
}

// TestVerifyWritesAnArtifactThatVerifies is AC5 through the CLI, including the
// re-read-and-re-verify step the command performs before claiming success.
func TestVerifyWritesAnArtifactThatVerifies(t *testing.T) {
	ts := mockPVE(t)
	out := filepath.Join(t.TempDir(), "report.json")
	var stdout, stderr bytes.Buffer

	run([]string{
		"verify", "--suite=hardware", "--allow-mock", "--out", out,
		"--pve-url", ts.URL, "--pve-token", "u@pve!t=x",
	}, &stdout, &stderr)

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("verify --out wrote nothing to %s: %v\nstderr: %s", out, err, stderr.String())
	}
	report, fingerprint, err := verify.ParseSignedReport(raw)
	if err != nil {
		t.Fatalf("the artifact vnproxctl wrote does not verify: %v", err)
	}
	if fingerprint == "" {
		t.Error("the artifact carries no signer fingerprint")
	}
	if len(report.Results) == 0 {
		t.Error("the artifact carries no results")
	}
	if !strings.Contains(stdout.String(), fingerprint) {
		t.Errorf("the command did not report the fingerprint it signed with:\n%s", stdout.String())
	}

	// The file is 0600: a validation report carries command output from a
	// hypervisor and is not world-readable by default.
	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("the artifact is mode %04o; it carries host command output and should not be group/world readable", perm)
	}

	// And tampering with the file the command wrote is caught, not just
	// tampering with bytes a test built in memory.
	raw[len(raw)/2] ^= 0x20
	if _, _, err := verify.ParseSignedReport(raw); err == nil {
		t.Error("a byte flipped in the written artifact still verified")
	}
}

// TestVerifyIsRegisteredInTheUsage: a command nobody can find is a command
// nobody runs, and this one exists to be handed to a stranger with a cluster.
func TestVerifyIsRegisteredInTheUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--help"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("--help exited %d", code)
	}
	usage := stdout.String()
	for _, want := range []string{"vnproxctl verify", "--allow-mock", "--i-understand", "--suite"} {
		if !strings.Contains(usage, want) {
			t.Errorf("the usage text does not mention %q", want)
		}
	}
}

// TestVerifyUnreachableEndpointIsANetworkError keeps the exit-code table
// honest: "could not dial" is a 5, not a validation failure.
func TestVerifyUnreachableEndpointIsANetworkError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := ts.URL
	ts.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{"verify", "--pve-url", url, "--pve-token", "u@pve!t=x"}, &stdout, &stderr)
	if code != ExitNetwork {
		t.Errorf("exit code = %d, want %d (ExitNetwork)", code, ExitNetwork)
	}
}
