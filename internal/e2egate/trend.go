// SPDX-License-Identifier: Apache-2.0

package e2egate

import (
	"fmt"
	"sort"
	"strings"
)

// FlakeStat is one test's behaviour over the trend window.
type FlakeStat struct {
	File  string
	Title string
	// LastFailureRunID names the most recent run it failed in, for someone who
	// wants the trace.
	LastFailureRunID string
	// Runs is how many complete runs in the window executed this test.
	Runs int
	// Failures is how many of those it failed.
	Failures int
	// Quarantined is set when a quarantine entry currently covers the test, so
	// the list distinguishes "flaky and known" from "flaky and unaddressed".
	Quarantined bool
}

// Rate is the flake rate over the window, 0..1.
func (f FlakeStat) Rate() float64 {
	if f.Runs == 0 {
		return 0
	}
	return float64(f.Failures) / float64(f.Runs)
}

// Flaky reports whether the test both passed and failed inside the window.
//
// A test that failed every run in the window is not flaky, it is broken, and
// calling it flaky is how a real failure gets tolerated. It still appears on
// the list — with a rate of 1.00 — but it is not what this predicate means.
func (f FlakeStat) Flaky() bool { return f.Failures > 0 && f.Failures < f.Runs }

// Trend computes per-test flake rates from run history.
//
// AC6 of T-2505 is that this list is generated from run history rather than
// hand-maintained, which is the whole reason AppendRun exists: a list someone
// curates records what they noticed, and the tests that matter are the ones
// nobody did.
//
// Only complete runs count. A grep-filtered or single-shard run does not
// contain every test, and counting a test's absence as a pass would drive every
// rate towards zero exactly as someone starts running targeted subsets to chase
// a flake.
func Trend(runs []RunRecord, q Quarantine) []FlakeStat {
	quarantined := make(map[string]struct{}, len(q.Entries))
	for _, e := range q.Entries {
		quarantined[e.Key()] = struct{}{}
	}

	byKey := make(map[string]*FlakeStat)
	for _, run := range runs {
		if !run.Complete {
			continue
		}
		for _, t := range run.Tests {
			if t.Status == StatusSkipped {
				continue
			}
			stat, ok := byKey[t.Key()]
			if !ok {
				_, isQ := quarantined[t.Key()]
				stat = &FlakeStat{File: t.File, Title: t.Title, Quarantined: isQ}
				byKey[t.Key()] = stat
			}
			stat.Runs++
			if t.Status == StatusFailed {
				stat.Failures++
				stat.LastFailureRunID = run.RunID
			}
		}
	}

	out := make([]FlakeStat, 0, len(byKey))
	for _, s := range byKey {
		out = append(out, *s)
	}
	// Worst first, then most-run, then alphabetical — a stable order so two
	// runs of the same history print identically.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Rate() != out[j].Rate() {
			return out[i].Rate() > out[j].Rate()
		}
		if out[i].Runs != out[j].Runs {
			return out[i].Runs > out[j].Runs
		}
		return out[i].File+out[i].Title < out[j].File+out[j].Title
	})
	return out
}

// TrendReport renders the tests that failed at least once in the window. A
// table of 89 rows of "0/20" is a table nobody reads, so a clean window prints
// one line saying so.
func TrendReport(stats []FlakeStat, window int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "e2e flake trend over the last %d complete runs (generated from run history):\n", window)

	any := false
	for _, s := range stats {
		if s.Failures == 0 {
			continue
		}
		any = true
		mark := " "
		if s.Quarantined {
			mark = "Q"
		}
		fmt.Fprintf(&b, "  %s %5.1f%%  %2d/%-2d  %s %s%s\n",
			mark, s.Rate()*100, s.Failures, s.Runs, s.File, s.Title, lastFailure(s))
	}
	if !any {
		fmt.Fprintf(&b, "  no test failed in the window (%d tests tracked)\n", len(stats))
	}
	return b.String()
}

func lastFailure(s FlakeStat) string {
	if s.LastFailureRunID == "" {
		return ""
	}
	return "  (last failed in " + s.LastFailureRunID + ")"
}
