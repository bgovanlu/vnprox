// SPDX-License-Identifier: Apache-2.0

package vitestgate

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/e2egate"
)

func TestParseReport(t *testing.T) {
	tests := []struct {
		check func(t *testing.T, rep e2egate.ShardReport)
		name  string
		root  string
		json  string
	}{
		{
			name: "a passing and a failing assertion in the same file",
			root: "/repo/web",
			json: `{
				"testResults": [
					{
						"name": "/repo/web/src/governance/TenantsPanel.test.tsx",
						"status": "failed",
						"startTime": 1000,
						"endTime": 1500,
						"assertionResults": [
							{
								"ancestorTitles": ["TenantsPanel (T-3002 AC3)"],
								"title": "renders no ref the daemon did not give this session",
								"status": "passed",
								"duration": 168.4
							},
							{
								"ancestorTitles": ["TenantsPanel (T-3002 AC3)"],
								"title": "offers no control that enumerates guests",
								"status": "failed",
								"duration": 1002.9,
								"failureMessages": ["TestingLibraryElementError: Unable to find role=\"button\""]
							}
						]
					}
				]
			}`,
			check: func(t *testing.T, rep e2egate.ShardReport) {
				if rep.Shard != ShardName {
					t.Fatalf("Shard = %q, want %q", rep.Shard, ShardName)
				}
				if len(rep.Outcomes) != 2 {
					t.Fatalf("got %d outcomes, want 2", len(rep.Outcomes))
				}
				want := e2egate.Outcome{
					File:       "src/governance/TenantsPanel.test.tsx",
					Title:      "TenantsPanel (T-3002 AC3)" + e2egate.TitleSeparator + "renders no ref the daemon did not give this session",
					Shard:      ShardName,
					Status:     e2egate.StatusPassed,
					DurationMS: 168,
				}
				var got *e2egate.Outcome
				for i := range rep.Outcomes {
					if strings.Contains(rep.Outcomes[i].Title, "renders no ref") {
						got = &rep.Outcomes[i]
					}
				}
				if got == nil {
					t.Fatalf("outcome for the passing assertion not found in %+v", rep.Outcomes)
				}
				if *got != want {
					t.Errorf("got %+v, want %+v", *got, want)
				}
				var failing *e2egate.Outcome
				for i := range rep.Outcomes {
					if rep.Outcomes[i].Status == e2egate.StatusFailed {
						failing = &rep.Outcomes[i]
					}
				}
				if failing == nil {
					t.Fatal("no failing outcome found")
				}
				if failing.DurationMS != 1003 {
					t.Errorf("failing.DurationMS = %d, want 1003 (rounded from 1002.9)", failing.DurationMS)
				}
				if rep.DurationMS != 500 {
					t.Errorf("ShardReport.DurationMS = %d, want 500 (endTime 1500 - startTime 1000)", rep.DurationMS)
				}
			},
		},
		{
			name: "a skipped and a todo assertion both collapse to skipped",
			root: "/repo/web",
			json: `{
				"testResults": [
					{
						"name": "/repo/web/src/a.test.ts",
						"status": "passed",
						"assertionResults": [
							{"ancestorTitles": [], "title": "skipped one", "status": "skipped"},
							{"ancestorTitles": [], "title": "todo one", "status": "todo"},
							{"ancestorTitles": [], "title": "pending one", "status": "pending"}
						]
					}
				]
			}`,
			check: func(t *testing.T, rep e2egate.ShardReport) {
				if len(rep.Outcomes) != 3 {
					t.Fatalf("got %d outcomes, want 3", len(rep.Outcomes))
				}
				for _, o := range rep.Outcomes {
					if o.Status != e2egate.StatusSkipped {
						t.Errorf("%s: Status = %q, want %q", o.Title, o.Status, e2egate.StatusSkipped)
					}
				}
			},
		},
		{
			name: "an unrecognised assertion status fails closed",
			root: "/repo/web",
			json: `{
				"testResults": [
					{
						"name": "/repo/web/src/a.test.ts",
						"status": "passed",
						"assertionResults": [
							{"ancestorTitles": [], "title": "mystery", "status": "interrupted"}
						]
					}
				]
			}`,
			check: func(t *testing.T, rep e2egate.ShardReport) {
				if len(rep.Outcomes) != 1 || rep.Outcomes[0].Status != e2egate.StatusFailed {
					t.Fatalf("got %+v, want a single StatusFailed outcome", rep.Outcomes)
				}
			},
		},
		{
			name: "a file that failed to collect becomes a report-level error, not an outcome",
			root: "/repo/web",
			json: `{
				"testResults": [
					{
						"name": "/repo/web/src/broken.test.ts",
						"status": "failed",
						"message": "Transform failed with 1 error:\n\nsome parse error detail",
						"assertionResults": []
					}
				]
			}`,
			check: func(t *testing.T, rep e2egate.ShardReport) {
				if len(rep.Outcomes) != 0 {
					t.Fatalf("got %d outcomes, want 0", len(rep.Outcomes))
				}
				if len(rep.Errors) != 1 {
					t.Fatalf("got %d report errors, want 1", len(rep.Errors))
				}
				if !strings.Contains(rep.Errors[0], "src/broken.test.ts") {
					t.Errorf("error %q does not name the file", rep.Errors[0])
				}
				if !strings.Contains(rep.Errors[0], "Transform failed with 1 error:") {
					t.Errorf("error %q does not carry the message's first line", rep.Errors[0])
				}
				if strings.Contains(rep.Errors[0], "parse error detail") {
					t.Errorf("error %q carries more than the first line", rep.Errors[0])
				}
			},
		},
		{
			name: "an empty file (matched but nothing ran) produces neither an outcome nor an error",
			root: "/repo/web",
			json: `{
				"testResults": [
					{
						"name": "/repo/web/src/empty.test.ts",
						"status": "passed",
						"assertionResults": []
					}
				]
			}`,
			check: func(t *testing.T, rep e2egate.ShardReport) {
				if len(rep.Outcomes) != 0 || len(rep.Errors) != 0 {
					t.Fatalf("got outcomes=%+v errors=%+v, want both empty", rep.Outcomes, rep.Errors)
				}
			},
		},
		{
			name: "an empty root leaves the absolute path untouched, slash-normalised",
			root: "",
			json: `{
				"testResults": [
					{
						"name": "/repo/web/src/a.test.ts",
						"status": "passed",
						"assertionResults": [
							{"ancestorTitles": [], "title": "one", "status": "passed"}
						]
					}
				]
			}`,
			check: func(t *testing.T, rep e2egate.ShardReport) {
				if len(rep.Outcomes) != 1 || rep.Outcomes[0].File != "/repo/web/src/a.test.ts" {
					t.Fatalf("got %+v", rep.Outcomes)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rep, err := ParseReport(strings.NewReader(tc.json), tc.root)
			if err != nil {
				t.Fatalf("ParseReport: %v", err)
			}
			tc.check(t, rep)
		})
	}
}

func TestParseReportInvalidJSON(t *testing.T) {
	_, err := ParseReport(strings.NewReader("not json"), "/repo/web")
	if err == nil {
		t.Fatal("want an error for invalid JSON, got nil")
	}
}

// TestParseReportRelativeRootAgainstAbsoluteName is a regression test:
// vitest's report always names files with an absolute path, and
// filepath.Rel errors out (rather than producing a relative path) when
// compared against a relative root — which cmd/vitestgate's own --root
// default ("web") is. Before ParseReport resolved root to an absolute path
// first, this silently fell back to leaving File absolute, which made
// every quarantine entry (keyed on a relative path, e.g.
// "src/governance/TenantsPanel.test.tsx") read as STALE against a live
// report — caught by running cmd/vitestgate against a real vitest run from
// the repo root, not by any table test, which is why this one exists now.
func TestParseReportRelativeRootAgainstAbsoluteName(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	// Simulate running from a repo root with root="web", the same relative
	// value cmd/vitestgate's gate flag defaults to.
	absName := filepath.Join(cwd, "web", "src", "governance", "TenantsPanel.test.tsx")
	report := `{"testResults": [{"name": ` + strconv.Quote(absName) + `, "status": "passed",
		"assertionResults": [{"ancestorTitles": [], "title": "one", "status": "passed"}]}]}`

	rep, err := ParseReport(strings.NewReader(report), "web")
	if err != nil {
		t.Fatalf("ParseReport: %v", err)
	}
	if len(rep.Outcomes) != 1 {
		t.Fatalf("got %d outcomes, want 1", len(rep.Outcomes))
	}
	want := "src/governance/TenantsPanel.test.tsx"
	if rep.Outcomes[0].File != want {
		t.Errorf("File = %q, want %q (relative root did not resolve against the absolute report path)", rep.Outcomes[0].File, want)
	}
}
