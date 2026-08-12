package e2egate

import (
	"strings"
	"testing"
	"time"
)

func recorded(file, title string, status Status) RecordedTest {
	return RecordedTest{File: file, Title: title, Status: status, DurationMS: 100, Shard: "s1"}
}

func run(id string, complete bool, tests ...RecordedTest) RunRecord {
	return RunRecord{RunID: id, StartedAt: now, Commit: "abc1234", Host: "dev", Complete: complete, Tests: tests}
}

func TestTrend(t *testing.T) {
	tests := []struct {
		check func(t *testing.T, stats []FlakeStat, report string)
		name  string
		runs  []RunRecord
		q     Quarantine
	}{
		{
			name: "a test that fails one run in four has a 25% rate",
			runs: []RunRecord{
				run("r1", true, recorded("e2e/a.spec.ts", "one", StatusPassed)),
				run("r2", true, recorded("e2e/a.spec.ts", "one", StatusFailed)),
				run("r3", true, recorded("e2e/a.spec.ts", "one", StatusPassed)),
				run("r4", true, recorded("e2e/a.spec.ts", "one", StatusPassed)),
			},
			check: func(t *testing.T, stats []FlakeStat, report string) {
				if len(stats) != 1 {
					t.Fatalf("stats = %v, want one", stats)
				}
				if got := stats[0].Rate(); got != 0.25 {
					t.Errorf("Rate() = %v, want 0.25", got)
				}
				if !stats[0].Flaky() {
					t.Error("Flaky() = false, want true")
				}
				if stats[0].LastFailureRunID != "r2" {
					t.Errorf("LastFailureRunID = %q, want r2", stats[0].LastFailureRunID)
				}
				if !strings.Contains(report, "25.0%") || !strings.Contains(report, "(last failed in r2)") {
					t.Errorf("report does not carry the rate and the run:\n%s", report)
				}
			},
		},
		{
			name: "a test that fails every run is not flaky, it is broken",
			runs: []RunRecord{
				run("r1", true, recorded("e2e/a.spec.ts", "one", StatusFailed)),
				run("r2", true, recorded("e2e/a.spec.ts", "one", StatusFailed)),
			},
			check: func(t *testing.T, stats []FlakeStat, _ string) {
				if stats[0].Flaky() {
					t.Error("Flaky() = true for a 2/2 failure, want false")
				}
				if stats[0].Rate() != 1 {
					t.Errorf("Rate() = %v, want 1", stats[0].Rate())
				}
			},
		},
		{
			name: "incomplete runs do not count: a filtered run must not dilute the rate",
			runs: []RunRecord{
				run("r1", true, recorded("e2e/a.spec.ts", "one", StatusFailed)),
				run("r2", false, recorded("e2e/b.spec.ts", "two", StatusPassed)),
				run("r3", false, recorded("e2e/b.spec.ts", "two", StatusPassed)),
			},
			check: func(t *testing.T, stats []FlakeStat, _ string) {
				if len(stats) != 1 {
					t.Fatalf("stats = %v, want only the complete run's test", stats)
				}
				if stats[0].Runs != 1 || stats[0].Failures != 1 {
					t.Errorf("stat = %+v, want 1/1", stats[0])
				}
			},
		},
		{
			name: "skipped runs of a test are not counted as passes",
			runs: []RunRecord{
				run("r1", true, recorded("e2e/a.spec.ts", "one", StatusSkipped)),
				run("r2", true, recorded("e2e/a.spec.ts", "one", StatusFailed)),
			},
			check: func(t *testing.T, stats []FlakeStat, _ string) {
				if stats[0].Runs != 1 {
					t.Errorf("Runs = %d, want 1 (the skip must not count)", stats[0].Runs)
				}
			},
		},
		{
			name: "a quarantined test is marked on the list",
			runs: []RunRecord{run("r1", true, recorded("e2e/a.spec.ts", "one", StatusFailed))},
			q:    Quarantine{Entries: []QuarantineEntry{entry("e2e/a.spec.ts", "one", "2026-08-20")}},
			check: func(t *testing.T, stats []FlakeStat, report string) {
				if !stats[0].Quarantined {
					t.Error("Quarantined = false, want true")
				}
				if !strings.Contains(report, "Q ") {
					t.Errorf("report does not mark the quarantined row:\n%s", report)
				}
			},
		},
		{
			name: "a clean window says so rather than printing 89 zero rows",
			runs: []RunRecord{run("r1", true, recorded("e2e/a.spec.ts", "one", StatusPassed))},
			check: func(t *testing.T, _ []FlakeStat, report string) {
				if !strings.Contains(report, "no test failed in the window (1 tests tracked)") {
					t.Errorf("report = %q", report)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stats := Trend(tc.runs, tc.q)
			tc.check(t, stats, TrendReport(stats, TrendWindow))
		})
	}
}

func TestAppendRunRoundTrips(t *testing.T) {
	dir := t.TempDir()
	for i := range 3 {
		rec := run("r"+string(rune('1'+i)), true, recorded("e2e/a.spec.ts", "one", StatusPassed))
		if err := AppendRun(dir, rec); err != nil {
			t.Fatalf("AppendRun: %v", err)
		}
	}
	runs, err := LoadRuns(dir, 0)
	if err != nil {
		t.Fatalf("LoadRuns: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("LoadRuns = %d runs, want 3", len(runs))
	}
	if runs[2].RunID != "r3" || len(runs[2].Tests) != 1 {
		t.Errorf("last run = %+v, want r3 with one test", runs[2])
	}

	last, err := LoadRuns(dir, 2)
	if err != nil {
		t.Fatalf("LoadRuns(2): %v", err)
	}
	if len(last) != 2 || last[0].RunID != "r2" {
		t.Errorf("LoadRuns(2) = %v, want the last two", last)
	}
}

func TestAppendRunTrimsToMax(t *testing.T) {
	dir := t.TempDir()
	for i := range MaxHistoryRuns + 5 {
		rec := RunRecord{
			RunID:     "run-" + time.Duration(i).String(),
			StartedAt: now,
			Complete:  true,
			Tests:     []RecordedTest{recorded("e2e/a.spec.ts", "one", StatusPassed)},
		}
		if err := AppendRun(dir, rec); err != nil {
			t.Fatalf("AppendRun: %v", err)
		}
	}
	runs, err := LoadRuns(dir, 0)
	if err != nil {
		t.Fatalf("LoadRuns: %v", err)
	}
	if len(runs) != MaxHistoryRuns {
		t.Errorf("LoadRuns = %d runs, want the log trimmed to %d", len(runs), MaxHistoryRuns)
	}
}

func TestLoadRunsOnMissingHistoryIsEmpty(t *testing.T) {
	runs, err := LoadRuns(t.TempDir(), 0)
	if err != nil {
		t.Fatalf("LoadRuns: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("LoadRuns = %v, want empty", runs)
	}
}
