// SPDX-License-Identifier: Apache-2.0

package e2egate

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Verdict is everything the gate decided, in a form a caller can print without
// re-deriving anything.
type Verdict struct {
	// SlowestShard names the shard that set the wall clock.
	//
	// First for field alignment, not for emphasis: govet's fieldalignment wants
	// the shortest pointer-bearing prefix, and one string in front of seven
	// slices is eight bytes cheaper than one behind them.
	SlowestShard string
	// Failures are failing tests no valid, unexpired quarantine covers. Any
	// entry here fails the build.
	Failures []Outcome
	// Tolerated are failing tests a valid, unexpired quarantine covers. They do
	// NOT fail the build; they are reported so a green build still says out
	// loud what it forgave.
	Tolerated []Outcome
	// Expired are quarantine entries whose expiry has passed. Each fails the
	// build, whether or not its test failed on this run — an expiry that only
	// bites when the test happens to fail again is not a deadline.
	Expired []QuarantineEntry
	// Stale are quarantine entries matching no test in the reports. Only
	// populated when Complete is true; a partial run legitimately does not
	// contain every test.
	Stale []QuarantineEntry
	// Invalid are entries that failed Validate. They are not honoured.
	Invalid []Problem
	// ReportErrors are report-level errors from any shard (a webServer that
	// never started, a spec file that failed to import). These carry no test,
	// so nothing else in this struct would notice them.
	ReportErrors []string
	// MissingShards are shards the caller expected and no report arrived for.
	MissingShards []string
	// TotalMS is the sum of every shard's wall clock; SlowestShardMS is the one
	// that actually set the suite's.
	TotalMS        int64
	SlowestShardMS int64
	// Passed and Skipped summarise the run.
	Passed  int
	Skipped int
}

// OK reports whether the build passes.
func (v Verdict) OK() bool {
	return len(v.Failures) == 0 &&
		len(v.Expired) == 0 &&
		len(v.Stale) == 0 &&
		len(v.Invalid) == 0 &&
		len(v.ReportErrors) == 0 &&
		len(v.MissingShards) == 0
}

// EvaluateInput is what Evaluate needs. A struct rather than six positional
// parameters, because four of them are booleans-and-strings that would be easy
// to transpose at a call site.
type EvaluateInput struct {
	Reports    []ShardReport
	Quarantine Quarantine
	Now        time.Time
	// ExpectedShards, when non-empty, is the set of shard names that must have
	// reported. A shard whose Playwright process died before writing a report
	// leaves no failures behind — it leaves nothing at all, which is why this
	// has to be checked explicitly.
	ExpectedShards []string
	// Complete says the reports cover the whole suite, enabling the stale-entry
	// check. False for a single-shard or grep-filtered run.
	Complete bool
}

// Evaluate applies the quarantine to the shard reports and returns the verdict.
func Evaluate(in EvaluateInput) Verdict {
	var v Verdict

	invalid := Validate(in.Quarantine, in.Now)
	v.Invalid = invalid
	invalidKeys := make(map[string]struct{}, len(invalid))
	for _, p := range invalid {
		invalidKeys[p.Entry.Key()] = struct{}{}
	}

	// Only valid entries are honoured, and an expired one is recorded before it
	// is dropped from the honoured set.
	honoured := make(map[string]QuarantineEntry, len(in.Quarantine.Entries))
	for _, e := range in.Quarantine.Entries {
		if _, bad := invalidKeys[e.Key()]; bad {
			continue
		}
		expired, err := e.Expired(in.Now)
		if err != nil {
			// Validate already recorded this shape; belt and braces.
			v.Invalid = append(v.Invalid, Problem{Entry: e, Reason: err.Error()})
			continue
		}
		if expired {
			v.Expired = append(v.Expired, e)
			continue
		}
		honoured[e.Key()] = e
	}

	seen := make(map[string]struct{})
	reported := make(map[string]struct{}, len(in.Reports))
	for _, rep := range in.Reports {
		reported[rep.Shard] = struct{}{}
		v.ReportErrors = append(v.ReportErrors, prefixAll(rep.Shard, rep.Errors)...)
		v.TotalMS += rep.DurationMS
		if rep.DurationMS > v.SlowestShardMS {
			v.SlowestShardMS = rep.DurationMS
			v.SlowestShard = rep.Shard
		}
		for _, o := range rep.Outcomes {
			seen[o.Key()] = struct{}{}
			switch o.Status {
			case StatusPassed:
				v.Passed++
			case StatusSkipped:
				v.Skipped++
			case StatusFailed:
				if _, ok := honoured[o.Key()]; ok {
					v.Tolerated = append(v.Tolerated, o)
					continue
				}
				v.Failures = append(v.Failures, o)
			}
		}
	}

	for _, want := range in.ExpectedShards {
		if _, ok := reported[want]; !ok {
			v.MissingShards = append(v.MissingShards, want)
		}
	}

	if in.Complete {
		// A quarantine matching nothing is worse than none: it reads as a known
		// problem while covering a test that has been renamed, moved or
		// deleted, so the real test runs unprotected and nobody notices.
		for _, e := range in.Quarantine.Entries {
			if _, bad := invalidKeys[e.Key()]; bad {
				continue
			}
			if _, ok := seen[e.Key()]; !ok {
				v.Stale = append(v.Stale, e)
			}
		}
	}

	sort.Slice(v.Failures, func(i, j int) bool { return v.Failures[i].Key() < v.Failures[j].Key() })
	sort.Slice(v.Tolerated, func(i, j int) bool { return v.Tolerated[i].Key() < v.Tolerated[j].Key() })
	return v
}

func prefixAll(shard string, msgs []string) []string {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, fmt.Sprintf("[%s] %s", shard, firstLine(m)))
	}
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Summary renders the verdict for a terminal, failures first. The exact strings
// are asserted by the package's tests, because "the gate said nothing useful"
// is how a red build becomes a re-run.
func (v Verdict) Summary() string {
	var b strings.Builder
	for _, o := range v.Failures {
		fmt.Fprintf(&b, "FAIL      %s [%s] %s\n", o.File, o.Shard, o.Title)
	}
	for _, e := range v.Expired {
		fmt.Fprintf(&b, "EXPIRED   %s %s — quarantine expired %s (%s); fix it or re-triage it\n",
			e.File, e.Title, e.Expires, e.Ticket)
	}
	for _, e := range v.Stale {
		fmt.Fprintf(&b, "STALE     %s %s — quarantined, but no such test ran (renamed or deleted?)\n", e.File, e.Title)
	}
	for _, p := range v.Invalid {
		fmt.Fprintf(&b, "INVALID   %s %s — %s\n", p.Entry.File, p.Entry.Title, p.Reason)
	}
	for _, e := range v.ReportErrors {
		fmt.Fprintf(&b, "RUNERROR  %s\n", e)
	}
	for _, s := range v.MissingShards {
		fmt.Fprintf(&b, "NOREPORT  shard %s produced no report; treating the run as failed\n", s)
	}
	for _, o := range v.Tolerated {
		fmt.Fprintf(&b, "QUARANTINED %s [%s] %s — failed, tolerated\n", o.File, o.Shard, o.Title)
	}
	fmt.Fprintf(&b, "%d passed, %d failed, %d quarantined-failing, %d skipped; slowest shard %s at %s\n",
		v.Passed, len(v.Failures), len(v.Tolerated), v.Skipped, v.SlowestShard, dur(v.SlowestShardMS))
	if v.OK() {
		b.WriteString("e2e gate: PASS\n")
	} else {
		b.WriteString("e2e gate: FAIL\n")
	}
	return b.String()
}

func dur(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%.1f min", d.Minutes())
}
