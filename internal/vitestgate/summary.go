package vitestgate

import (
	"fmt"
	"strings"
	"time"

	"github.com/bgovanlu/vnprox/internal/e2egate"
)

// Summary and TrendReport render internal/e2egate's Verdict and []FlakeStat
// for a vitest run. The DATA comes entirely from internal/e2egate — Verdict,
// Evaluate, Trend and FlakeStat are reused unmodified, because none of that
// logic is Playwright-specific.
//
// The PRESENTATION is not reused: e2egate.Verdict.Summary() and
// e2egate.TrendReport() hardcode "e2e gate" / "e2e flake trend" and print
// each outcome's Shard in brackets, both of which are correct for a suite
// that runs as N Playwright shards and actively misleading for one that
// doesn't — a vitest failure printed as "[vitest] Title" or a verdict line
// reading "e2e gate: PASS" would read as evidence about the WRONG suite in
// `make ci` output. e2egate's own doc comment says its exact strings are
// asserted by its tests, i.e. they are that package's contract with ITS
// callers; contorting it to also serve two different suite names would be
// the "invent a second string format inside one function" version of
// inventing a second mechanism. So this package renders its own text over
// the same struct fields instead.

// Summary renders a vitest verdict for a terminal, failures first.
func Summary(v e2egate.Verdict) string {
	var b strings.Builder
	for _, o := range v.Failures {
		fmt.Fprintf(&b, "FAIL      %s %s\n", o.File, o.Title)
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
	for _, o := range v.Tolerated {
		fmt.Fprintf(&b, "QUARANTINED %s %s — failed, tolerated\n", o.File, o.Title)
	}
	fmt.Fprintf(&b, "%d passed, %d failed, %d quarantined-failing, %d skipped in %s\n",
		v.Passed, len(v.Failures), len(v.Tolerated), v.Skipped, dur(v.SlowestShardMS))
	if v.OK() {
		b.WriteString("vitestgate: PASS\n")
	} else {
		b.WriteString("vitestgate: FAIL\n")
	}
	return b.String()
}

// TrendReport renders the tests that failed at least once in the window,
// the vitest-suite equivalent of e2egate.TrendReport.
func TrendReport(stats []e2egate.FlakeStat, window int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "vitest flake trend over the last %d complete runs (generated from run history):\n", window)

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
		last := ""
		if s.LastFailureRunID != "" {
			last = "  (last failed in " + s.LastFailureRunID + ")"
		}
		fmt.Fprintf(&b, "  %s %5.1f%%  %2d/%-2d  %s %s%s\n",
			mark, s.Rate()*100, s.Failures, s.Runs, s.File, s.Title, last)
	}
	if !any {
		fmt.Fprintf(&b, "  no test failed in the window (%d tests tracked)\n", len(stats))
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
