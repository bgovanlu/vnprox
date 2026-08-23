package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// run's own gate() calls os.Exit(1) on a failing verdict, the same as
// cmd/e2egate's — which is why neither command has a test exercising that
// path directly (it would kill the test binary). What's tested here is
// everything that returns rather than exits: usage errors, a missing
// report, an empty trend, and a passing gate run end to end.

func TestRunUsageErrors(t *testing.T) {
	tests := []struct {
		name    string
		wantErr string
		args    []string
	}{
		{name: "no subcommand", args: nil, wantErr: "usage: vitestgate"},
		{name: "unknown subcommand", args: []string{"bogus"}, wantErr: `unknown subcommand "bogus"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := run(tc.args, capture(t))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("run(%v) = %v, want an error containing %q", tc.args, err, tc.wantErr)
			}
		})
	}
}

func TestGateMissingReportIsAnError(t *testing.T) {
	dir := t.TempDir()
	err := run([]string{
		"gate",
		"--report", filepath.Join(dir, "does-not-exist.json"),
		"--quarantine", filepath.Join(dir, "quarantine.json"),
		"--history", filepath.Join(dir, "history"),
		"--record=false",
	}, capture(t))
	if err == nil {
		t.Fatal("want an error when the report file does not exist — no report is not a pass")
	}
}

func TestTrendWithNoHistoryIsEmptyNotAnError(t *testing.T) {
	dir := t.TempDir()
	f := capture(t)
	err := run([]string{
		"trend",
		"--history", filepath.Join(dir, "history"),
		"--quarantine", filepath.Join(dir, "quarantine.json"),
	}, f)
	if err != nil {
		t.Fatalf("trend with no history: %v", err)
	}
	out := readBack(t, f)
	if !strings.Contains(out, "no test failed in the window") {
		t.Errorf("trend output = %q, want the clean-window message", out)
	}
}

// TestGatePassingRun exercises gate() end to end against a fixture report
// with no failures, so it returns rather than calling os.Exit(1). It checks
// that a run got recorded (AppendRun happened) and that the trend section
// is present in the output.
func TestGatePassingRun(t *testing.T) {
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "vitest.json")
	report := `{
		"testResults": [
			{
				"name": "` + filepath.Join(dir, "src", "a.test.ts") + `",
				"status": "passed",
				"startTime": 1000,
				"endTime": 1010,
				"assertionResults": [
					{"ancestorTitles": ["a"], "title": "one", "status": "passed", "duration": 5}
				]
			}
		]
	}`
	if err := os.WriteFile(reportPath, []byte(report), 0o600); err != nil {
		t.Fatalf("writing fixture report: %v", err)
	}

	historyDir := filepath.Join(dir, "history")
	f := capture(t)
	err := run([]string{
		"gate",
		"--report", reportPath,
		"--root", dir,
		"--quarantine", filepath.Join(dir, "quarantine.json"), // missing = empty quarantine
		"--history", historyDir,
		"--run-id", "test-run-1",
	}, f)
	if err != nil {
		t.Fatalf("gate on a clean report: %v", err)
	}
	out := readBack(t, f)
	if !strings.Contains(out, "1 passed, 0 failed") {
		t.Errorf("gate output = %q, want a 1-passed summary", out)
	}
	if !strings.Contains(out, "vitestgate: PASS") {
		t.Errorf("gate output = %q, want a PASS verdict line", out)
	}
	if strings.Contains(out, "e2e") {
		t.Errorf("gate output = %q, must not read as e2e-suite output", out)
	}
	if _, statErr := os.Stat(filepath.Join(historyDir, "runs.jsonl")); statErr != nil {
		t.Errorf("expected a run history file to be written: %v", statErr)
	}
}

// capture gives run() a real temp file to write its report to, so the
// output can be read back afterwards. run()'s signature takes an *os.File
// rather than an io.Writer (matching cmd/e2egate's own signature), which a
// plain bytes.Buffer can't satisfy.
func capture(t *testing.T) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "vitestgate-out-*")
	if err != nil {
		t.Fatalf("creating capture file: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// readBack rewinds and reads everything capture() has accumulated so far.
func readBack(t *testing.T, f *os.File) string {
	t.Helper()
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("seeking capture file: %v", err)
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("reading capture file: %v", err)
	}
	return string(data)
}
