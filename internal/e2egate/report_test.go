package e2egate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pwJSON is a Playwright JSON report in the shape the reporter actually emits:
// a file-level suite whose title equals its file, describe blocks as nested
// suites, and one or more test entries per spec.
const pwJSON = `{
  "suites": [
    {
      "title": "e2e/a.spec.ts",
      "file": "e2e/a.spec.ts",
      "specs": [
        {"title": "top-level test", "file": "e2e/a.spec.ts",
         "tests": [{"status": "expected", "results": [{"status": "passed", "duration": 1200, "retry": 0}]}]}
      ],
      "suites": [
        {
          "title": "a describe block",
          "file": "e2e/a.spec.ts",
          "specs": [
            {"title": "nested test", "file": "e2e/a.spec.ts",
             "tests": [{"status": "unexpected", "results": [{"status": "timedOut", "duration": 120000, "retry": 0}]}]},
            {"title": "skipped test", "file": "e2e/a.spec.ts",
             "tests": [{"status": "skipped", "results": [{"status": "skipped", "duration": 0, "retry": 0}]}]}
          ],
          "suites": []
        }
      ]
    }
  ],
  "errors": [],
  "stats": {"expected": 1, "unexpected": 1, "flaky": 0, "skipped": 1, "duration": 130000}
}`

func TestParseReport(t *testing.T) {
	rep, err := ParseReport("core-a", strings.NewReader(pwJSON))
	if err != nil {
		t.Fatalf("ParseReport: %v", err)
	}
	if rep.DurationMS != 130000 {
		t.Errorf("DurationMS = %d, want 130000", rep.DurationMS)
	}

	got := make(map[string]Outcome, len(rep.Outcomes))
	for _, o := range rep.Outcomes {
		got[o.Title] = o
	}

	tests := []struct {
		title      string
		wantStatus Status
		wantMS     int64
	}{
		{title: "top-level test", wantStatus: StatusPassed, wantMS: 1200},
		{title: "a describe block › nested test", wantStatus: StatusFailed, wantMS: 120000},
		{title: "a describe block › skipped test", wantStatus: StatusSkipped, wantMS: 0},
	}
	for _, tc := range tests {
		o, ok := got[tc.title]
		if !ok {
			t.Errorf("no outcome titled %q; got %v", tc.title, keys(got))
			continue
		}
		if o.Status != tc.wantStatus {
			t.Errorf("%q status = %q, want %q", tc.title, o.Status, tc.wantStatus)
		}
		if o.DurationMS != tc.wantMS {
			t.Errorf("%q duration = %d, want %d", tc.title, o.DurationMS, tc.wantMS)
		}
		if o.File != "e2e/a.spec.ts" {
			t.Errorf("%q file = %q, want e2e/a.spec.ts", tc.title, o.File)
		}
		if o.Shard != "core-a" {
			t.Errorf("%q shard = %q, want core-a", tc.title, o.Shard)
		}
	}
}

// TestParseReportFileTitleIsNotDoubled pins the one thing in flatten that is
// easy to get wrong and impossible to notice: the file-level suite's title IS
// the file path, and folding it into the test title would make every quarantine
// entry name its file twice and match nothing.
func TestParseReportFileTitleIsNotDoubled(t *testing.T) {
	rep, err := ParseReport("s", strings.NewReader(pwJSON))
	if err != nil {
		t.Fatalf("ParseReport: %v", err)
	}
	for _, o := range rep.Outcomes {
		if strings.Contains(o.Title, ".spec.ts") {
			t.Errorf("title %q contains the file path; Outcome.File already carries it", o.Title)
		}
	}
}

// TestCollapseIsPessimistic covers AC3's shape: --repeat-each=2 emits two test
// entries per spec, and one passing repeat must not hide the other.
func TestCollapseIsPessimistic(t *testing.T) {
	const repeated = `{
      "suites": [{"title": "e2e/a.spec.ts", "file": "e2e/a.spec.ts", "specs": [
        {"title": "repeated", "file": "e2e/a.spec.ts", "tests": [
          {"status": "expected",   "results": [{"status": "passed", "duration": 10, "retry": 0}]},
          {"status": "unexpected", "results": [{"status": "failed", "duration": 20, "retry": 0}]}
        ]}
      ], "suites": []}],
      "errors": [], "stats": {"duration": 30}
    }`
	rep, err := ParseReport("s", strings.NewReader(repeated))
	if err != nil {
		t.Fatalf("ParseReport: %v", err)
	}
	if len(rep.Outcomes) != 1 {
		t.Fatalf("Outcomes = %v, want one", rep.Outcomes)
	}
	if rep.Outcomes[0].Status != StatusFailed {
		t.Errorf("status = %q, want %q: one failing repeat fails the spec", rep.Outcomes[0].Status, StatusFailed)
	}
}

func TestParseReportCarriesRunLevelErrors(t *testing.T) {
	const broken = `{"suites": [], "errors": [{"message": "Error: webServer did not start\n at x"}], "stats": {"duration": 5}}`
	rep, err := ParseReport("s", strings.NewReader(broken))
	if err != nil {
		t.Fatalf("ParseReport: %v", err)
	}
	if len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0], "webServer did not start") {
		t.Fatalf("Errors = %v, want the webServer error", rep.Errors)
	}
}

// TestParseReportDirRefusesAnEmptyDirectory is the check that stops a suite
// which never started from reading as a pass — the single most likely way a
// gate like this becomes decorative.
func TestParseReportDirRefusesAnEmptyDirectory(t *testing.T) {
	_, err := ParseReportDir(t.TempDir())
	if err == nil {
		t.Fatal("ParseReportDir on an empty directory returned no error")
	}
	if !strings.Contains(err.Error(), "not a pass") {
		t.Errorf("error = %v, want it to say an empty report set is not a pass", err)
	}
}

func TestParseReportDirNamesShardsAfterFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"core-a", "core-b"} {
		if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(pwJSON), 0o600); err != nil {
			t.Fatalf("writing fixture: %v", err)
		}
	}
	reports, err := ParseReportDir(dir)
	if err != nil {
		t.Fatalf("ParseReportDir: %v", err)
	}
	if len(reports) != 2 || reports[0].Shard != "core-a" || reports[1].Shard != "core-b" {
		t.Fatalf("reports = %v, want core-a and core-b", reports)
	}
}

func keys(m map[string]Outcome) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
