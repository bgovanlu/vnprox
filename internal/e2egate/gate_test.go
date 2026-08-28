// SPDX-License-Identifier: Apache-2.0

package e2egate

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// now is the fixed "today" every table below is written against, so a test that
// passes in August still passes in December.
var now = time.Date(2026, 8, 12, 14, 0, 0, 0, time.UTC)

func failing(file, title string) Outcome {
	return Outcome{File: file, Title: title, Shard: "s1", Status: StatusFailed, DurationMS: 1000}
}

func passing(file, title string) Outcome {
	return Outcome{File: file, Title: title, Shard: "s1", Status: StatusPassed, DurationMS: 1000}
}

func entry(file, title, expires string) QuarantineEntry {
	return QuarantineEntry{
		File:    file,
		Title:   title,
		Reason:  "times out in-suite and passes alone; under bisection, see the card",
		Ticket:  "T-2505",
		Expires: expires,
	}
}

func report(shard string, outcomes ...Outcome) ShardReport {
	for i := range outcomes {
		outcomes[i].Shard = shard
	}
	return ShardReport{Shard: shard, Outcomes: outcomes, DurationMS: 60_000}
}

func TestEvaluate(t *testing.T) {
	tests := []struct {
		check func(t *testing.T, v Verdict)
		name  string
		// wantIn are substrings the summary must contain, so a passing test
		// also proves the operator is told *why*.
		wantIn []string
		in     EvaluateInput
		// wantOK is the build verdict.
		wantOK bool
	}{
		{
			name: "clean run passes",
			in: EvaluateInput{
				Reports: []ShardReport{report("s1", passing("e2e/a.spec.ts", "one"))},
				Now:     now,
			},
			wantOK: true,
			wantIn: []string{"1 passed, 0 failed", "e2e gate: PASS"},
		},
		{
			name: "an unquarantined failure fails the build and names the spec",
			in: EvaluateInput{
				Reports: []ShardReport{report("s1", failing("e2e/a.spec.ts", "one"))},
				Now:     now,
			},
			wantOK: false,
			wantIn: []string{"FAIL      e2e/a.spec.ts [s1] one", "e2e gate: FAIL"},
		},
		{
			name: "AC5 first half: a live quarantine tolerates its test's failure",
			in: EvaluateInput{
				Reports: []ShardReport{report("s1", failing("e2e/a.spec.ts", "one"))},
				Quarantine: Quarantine{Entries: []QuarantineEntry{
					entry("e2e/a.spec.ts", "one", "2026-08-20"),
				}},
				Now: now,
			},
			wantOK: true,
			wantIn: []string{"QUARANTINED e2e/a.spec.ts [s1] one — failed, tolerated", "e2e gate: PASS"},
			check: func(t *testing.T, v Verdict) {
				if len(v.Failures) != 0 {
					t.Errorf("Failures = %v, want none", v.Failures)
				}
				if len(v.Tolerated) != 1 {
					t.Errorf("Tolerated = %v, want one", v.Tolerated)
				}
			},
		},
		{
			name: "a quarantine expiring today still tolerates: the expiry day is inclusive",
			in: EvaluateInput{
				Reports: []ShardReport{report("s1", failing("e2e/a.spec.ts", "one"))},
				Quarantine: Quarantine{Entries: []QuarantineEntry{
					entry("e2e/a.spec.ts", "one", "2026-08-12"),
				}},
				Now: now,
			},
			wantOK: true,
		},
		{
			name: "AC5 second half: an expired quarantine fails the build even though the test PASSED",
			in: EvaluateInput{
				Reports: []ShardReport{report("s1", passing("e2e/a.spec.ts", "one"))},
				Quarantine: Quarantine{Entries: []QuarantineEntry{
					entry("e2e/a.spec.ts", "one", "2026-08-11"),
				}},
				Now: now,
			},
			wantOK: false,
			wantIn: []string{"EXPIRED   e2e/a.spec.ts one — quarantine expired 2026-08-11 (T-2505)"},
		},
		{
			name: "an expired quarantine does not tolerate its test's failure either",
			in: EvaluateInput{
				Reports: []ShardReport{report("s1", failing("e2e/a.spec.ts", "one"))},
				Quarantine: Quarantine{Entries: []QuarantineEntry{
					entry("e2e/a.spec.ts", "one", "2026-08-11"),
				}},
				Now: now,
			},
			wantOK: false,
			wantIn: []string{"FAIL      e2e/a.spec.ts [s1] one", "EXPIRED   e2e/a.spec.ts one"},
		},
		{
			name: "a quarantine matching no test in a complete run is stale and fails",
			in: EvaluateInput{
				Reports: []ShardReport{report("s1", passing("e2e/a.spec.ts", "one"))},
				Quarantine: Quarantine{Entries: []QuarantineEntry{
					entry("e2e/a.spec.ts", "renamed away", "2026-08-20"),
				}},
				Now:      now,
				Complete: true,
			},
			wantOK: false,
			wantIn: []string{"STALE     e2e/a.spec.ts renamed away"},
		},
		{
			name: "the same entry in a partial run is not stale: the shard simply did not own it",
			in: EvaluateInput{
				Reports: []ShardReport{report("s1", passing("e2e/a.spec.ts", "one"))},
				Quarantine: Quarantine{Entries: []QuarantineEntry{
					entry("e2e/b.spec.ts", "elsewhere", "2026-08-20"),
				}},
				Now:      now,
				Complete: false,
			},
			wantOK: true,
		},
		{
			name: "a malformed quarantine is not honoured, so the failure it names still fails",
			in: EvaluateInput{
				Reports: []ShardReport{report("s1", failing("e2e/a.spec.ts", "one"))},
				Quarantine: Quarantine{Entries: []QuarantineEntry{
					{File: "e2e/a.spec.ts", Title: "one", Reason: "flaky", Ticket: "T-1", Expires: "2026-08-20"},
				}},
				Now: now,
			},
			wantOK: false,
			wantIn: []string{"INVALID", "FAIL      e2e/a.spec.ts [s1] one"},
		},
		{
			name: "a shard that produced no report fails the build",
			in: EvaluateInput{
				Reports:        []ShardReport{report("s1", passing("e2e/a.spec.ts", "one"))},
				Now:            now,
				ExpectedShards: []string{"s1", "s2"},
			},
			wantOK: false,
			wantIn: []string{"NOREPORT  shard s2 produced no report"},
		},
		{
			name: "a report-level error fails the build even with no failing test",
			in: EvaluateInput{
				Reports: []ShardReport{{
					Shard:      "s1",
					Outcomes:   []Outcome{passing("e2e/a.spec.ts", "one")},
					Errors:     []string{"Error: webServer did not start\n  at foo"},
					DurationMS: 1000,
				}},
				Now: now,
			},
			wantOK: false,
			wantIn: []string{"RUNERROR  [s1] Error: webServer did not start"},
		},
		{
			name: "the slowest shard sets the reported wall clock, not the sum",
			in: EvaluateInput{
				Reports: []ShardReport{
					{Shard: "s1", Outcomes: []Outcome{passing("e2e/a.spec.ts", "one")}, DurationMS: 120_000},
					{Shard: "s2", Outcomes: []Outcome{passing("e2e/b.spec.ts", "two")}, DurationMS: 300_000},
				},
				Now: now,
			},
			wantOK: true,
			wantIn: []string{"slowest shard s2 at 5.0 min"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := Evaluate(tc.in)
			if v.OK() != tc.wantOK {
				t.Errorf("OK() = %v, want %v\nsummary:\n%s", v.OK(), tc.wantOK, v.Summary())
			}
			summary := v.Summary()
			for _, want := range tc.wantIn {
				if !strings.Contains(summary, want) {
					t.Errorf("summary does not contain %q:\n%s", want, summary)
				}
			}
			if tc.check != nil {
				tc.check(t, v)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		entry   QuarantineEntry
		wantErr string
	}{
		{
			name:  "a well-formed entry has no problems",
			entry: entry("e2e/a.spec.ts", "one", "2026-08-20"),
		},
		{
			name:    "a reason too short to be a reason",
			entry:   QuarantineEntry{File: "e2e/a.spec.ts", Title: "one", Reason: "flaky", Ticket: "T-1", Expires: "2026-08-20"},
			wantErr: "reason is shorter than 20 characters",
		},
		{
			name:    "no ticket",
			entry:   QuarantineEntry{File: "e2e/a.spec.ts", Title: "one", Reason: strings.Repeat("x", 30), Expires: "2026-08-20"},
			wantErr: "no ticket",
		},
		{
			name:    "an expiry that is not a date",
			entry:   QuarantineEntry{File: "e2e/a.spec.ts", Title: "one", Reason: strings.Repeat("x", 30), Ticket: "T-1", Expires: "soon"},
			wantErr: `expiry "soon" is not a 2006-01-02 date`,
		},
		{
			name:    "an expiry beyond the cap is a disabled test",
			entry:   QuarantineEntry{File: "e2e/a.spec.ts", Title: "one", Reason: strings.Repeat("x", 30), Ticket: "T-1", Expires: "2099-01-01"},
			wantErr: "that is a disabled test, not a quarantine",
		},
		{
			name:    "no file",
			entry:   QuarantineEntry{Title: "one", Reason: strings.Repeat("x", 30), Ticket: "T-1", Expires: "2026-08-20"},
			wantErr: "no file",
		},
		{
			name:    "no title",
			entry:   QuarantineEntry{File: "e2e/a.spec.ts", Reason: strings.Repeat("x", 30), Ticket: "T-1", Expires: "2026-08-20"},
			wantErr: "no title",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			problems := Validate(Quarantine{Entries: []QuarantineEntry{tc.entry}}, now)
			if tc.wantErr == "" {
				if len(problems) != 0 {
					t.Fatalf("Validate() = %v, want none", problems)
				}
				return
			}
			if len(problems) == 0 {
				t.Fatalf("Validate() found no problem, want one containing %q", tc.wantErr)
			}
			if !strings.Contains(problems[0].Reason, tc.wantErr) {
				t.Errorf("Validate() reason = %q, want it to contain %q", problems[0].Reason, tc.wantErr)
			}
		})
	}
}

func TestValidateRejectsDuplicates(t *testing.T) {
	q := Quarantine{Entries: []QuarantineEntry{
		entry("e2e/a.spec.ts", "one", "2026-08-20"),
		entry("e2e/a.spec.ts", "one", "2026-08-21"),
	}}
	problems := Validate(q, now)
	if len(problems) != 1 || !strings.Contains(problems[0].Reason, "duplicate") {
		t.Fatalf("Validate() = %v, want one duplicate problem", problems)
	}
}

// TestRepoQuarantineIsValid is the check that keeps the shipped file honest.
// Every rule above applies to web/e2e/quarantine.json itself, evaluated against
// the real clock — so an entry whose expiry has quietly passed turns `make
// check` red without waiting for anyone to run the e2e suite.
func TestRepoQuarantineIsValid(t *testing.T) {
	path := filepath.Join("..", "..", "web", "e2e", "quarantine.json")
	q, err := LoadQuarantine(path)
	if err != nil {
		t.Fatalf("loading %s: %v", path, err)
	}
	for _, p := range Validate(q, time.Now()) {
		t.Errorf("%s: %s %s — %s", path, p.Entry.File, p.Entry.Title, p.Reason)
	}
	for _, e := range q.Entries {
		expired, expErr := e.Expired(time.Now())
		if expErr != nil {
			t.Errorf("%s: %v", path, expErr)
			continue
		}
		if expired {
			t.Errorf("%s: quarantine for %s %s expired on %s (%s) — fix the test or re-triage the entry",
				path, e.File, e.Title, e.Expires, e.Ticket)
		}
	}
}
