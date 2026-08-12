// Package e2egate turns the Playwright suite's per-shard JSON reports into a
// single build verdict.
//
// T-2505. The e2e suite runs as N independent shards (web/e2e/shards.ts), each
// its own Playwright process with its own daemons, so there is no longer one
// process that can decide whether the suite passed. Something has to read every
// shard's report and answer three questions the exit codes cannot:
//
//   - Did anything fail that is not quarantined?
//   - Is any quarantine past its expiry? An expiry that only expires when
//     someone remembers to look is not an expiry, so an expired entry fails the
//     build whether or not its test failed.
//   - What is each test's flake rate over the last N runs? Answered from
//     recorded run history, never from a hand-maintained list — a hand-written
//     flake list is a list of what someone noticed.
//
// It is a Go package rather than another TypeScript module in web/e2e/ for one
// reason: the decisions above are the kind of logic that must be table-tested
// with a fixture whose expiry is in the past, and `go test ./...` already runs
// on every `make check`.
package e2egate

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// TitleSeparator joins a spec's describe-block ancestry to its own title, the
// same way Playwright's own reporters render a full title.
const TitleSeparator = " › "

// Status is the outcome of one test as this package models it. Playwright's own
// vocabulary ("expected"/"unexpected"/"flaky"/"skipped" on a test, plus
// "passed"/"failed"/"timedOut"/"interrupted"/"skipped" on each result) is
// collapsed here, because the gate only ever has to distinguish three things.
type Status string

const (
	// StatusPassed — every run of the test reached its expected status.
	StatusPassed Status = "passed"
	// StatusFailed — at least one run did not, including a timeout.
	StatusFailed Status = "failed"
	// StatusSkipped — the test did not run (test.skip, or a shard that did not
	// own it).
	StatusSkipped Status = "skipped"
)

// Outcome is one test's result in one run, flattened out of the report tree.
type Outcome struct {
	// File is the spec path as Playwright reports it, relative to the
	// Playwright rootDir ("e2e/scale.spec.ts").
	File string
	// Title is the full title: describe ancestry and the test's own title,
	// joined by TitleSeparator.
	Title string
	// Shard names the shard whose report this came from, so a failure message
	// can say where to re-run it.
	Shard string
	// Status is the collapsed outcome.
	Status Status
	// DurationMS is the wall time of the longest run of this test.
	DurationMS int64
	// Retries is how many times Playwright re-ran the test after a failure.
	// Non-zero with StatusPassed is Playwright's own "flaky" verdict.
	Retries int
}

// Key identifies a test across runs and across shards.
func (o Outcome) Key() string { return o.File + TitleSeparator + o.Title }

// pwReport is the subset of Playwright's JSON reporter output this package
// reads. Fields Playwright emits and the gate does not need are deliberately
// absent rather than modelled and ignored: a struct that mirrors the whole
// schema would have to be revised on every Playwright upgrade, whereas this one
// only breaks when something it genuinely depends on moves.
type pwReport struct {
	Suites []pwSuite `json:"suites"`
	Errors []pwError `json:"errors"`
	Stats  pwStats   `json:"stats"`
}

type pwError struct {
	Message string `json:"message"`
}

type pwStats struct {
	Expected   int     `json:"expected"`
	Unexpected int     `json:"unexpected"`
	Flaky      int     `json:"flaky"`
	Skipped    int     `json:"skipped"`
	Duration   float64 `json:"duration"`
}

type pwSuite struct {
	Title  string    `json:"title"`
	File   string    `json:"file"`
	Specs  []pwSpec  `json:"specs"`
	Suites []pwSuite `json:"suites"`
}

type pwSpec struct {
	Title string   `json:"title"`
	File  string   `json:"file"`
	Tests []pwTest `json:"tests"`
}

type pwTest struct {
	Status  string     `json:"status"`
	Results []pwResult `json:"results"`
}

type pwResult struct {
	Status   string `json:"status"`
	Duration int64  `json:"duration"`
	Retry    int    `json:"retry"`
}

// ShardReport is one shard's parsed report.
type ShardReport struct {
	Shard    string
	Outcomes []Outcome
	// Errors are report-level errors (a webServer that never came up, a spec
	// file that failed to load). They are not attached to any test, so a shard
	// can report zero failures and still be a failed run.
	Errors []string
	// DurationMS is the shard's own wall clock as Playwright measured it.
	DurationMS int64
}

// ParseReport reads one shard's Playwright JSON report.
func ParseReport(shard string, r io.Reader) (ShardReport, error) {
	var raw pwReport
	dec := json.NewDecoder(r)
	if err := dec.Decode(&raw); err != nil {
		return ShardReport{}, fmt.Errorf("decoding shard %s report: %w", shard, err)
	}

	out := ShardReport{
		Shard:      shard,
		DurationMS: int64(raw.Stats.Duration),
	}
	for _, e := range raw.Errors {
		out.Errors = append(out.Errors, e.Message)
	}
	for _, s := range raw.Suites {
		out.Outcomes = append(out.Outcomes, flatten(shard, s, nil)...)
	}
	sort.Slice(out.Outcomes, func(i, j int) bool {
		return out.Outcomes[i].Key() < out.Outcomes[j].Key()
	})
	return out, nil
}

// flatten walks the suite tree, carrying the describe-block ancestry down.
//
// The file-level suite's title is the file path and must NOT become part of the
// test's title — Outcome carries the file separately, and doubling it would
// make every quarantine entry name the file twice.
func flatten(shard string, s pwSuite, ancestry []string) []Outcome {
	if s.File == "" || s.Title != s.File {
		// A describe block: its title is part of every test title below it.
		if s.Title != "" {
			ancestry = append(append([]string{}, ancestry...), s.Title)
		}
	}

	var out []Outcome
	for _, spec := range s.Specs {
		title := strings.Join(append(append([]string{}, ancestry...), spec.Title), TitleSeparator)
		file := spec.File
		if file == "" {
			file = s.File
		}
		out = append(out, Outcome{
			File:       file,
			Title:      title,
			Shard:      shard,
			Status:     collapse(spec.Tests),
			DurationMS: longestRun(spec.Tests),
			Retries:    retries(spec.Tests),
		})
	}
	for _, child := range s.Suites {
		out = append(out, flatten(shard, child, ancestry)...)
	}
	return out
}

// collapse reduces every run of every projection of a spec to one status.
//
// Pessimistic on purpose: under --repeat-each=2 a spec produces two test
// entries, and a spec that passes once and fails once is a failing spec. That
// is the whole point of AC3's repeat run, and an optimistic collapse would
// silently discard it.
func collapse(tests []pwTest) Status {
	if len(tests) == 0 {
		return StatusSkipped
	}
	seenRun := false
	for _, t := range tests {
		for _, r := range t.Results {
			switch r.Status {
			case "passed":
				seenRun = true
			case "skipped":
				// A skipped run tells us nothing either way.
			case "":
				// A test entry with no status at all is a report we do not
				// understand; treat it as a failure rather than a pass.
				return StatusFailed
			default: // failed, timedOut, interrupted
				return StatusFailed
			}
		}
		// Playwright's own per-test verdict catches the shapes the per-result
		// scan cannot: an "unexpected" pass (test.fail() that did not fail).
		if t.Status == "unexpected" {
			return StatusFailed
		}
	}
	if !seenRun {
		return StatusSkipped
	}
	return StatusPassed
}

func longestRun(tests []pwTest) int64 {
	var max int64
	for _, t := range tests {
		for _, r := range t.Results {
			if r.Duration > max {
				max = r.Duration
			}
		}
	}
	return max
}

func retries(tests []pwTest) int {
	max := 0
	for _, t := range tests {
		for _, r := range t.Results {
			if r.Retry > max {
				max = r.Retry
			}
		}
	}
	return max
}

// ParseReportDir reads every *.json in dir as a shard report, using each file's
// base name (minus .json) as the shard name.
//
// An empty directory is an error, not an empty pass: "no reports" is what a
// suite that never started looks like, and it must not read as a green build.
func ParseReportDir(dir string) ([]ShardReport, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("globbing shard reports in %s: %w", dir, err)
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return nil, fmt.Errorf("no shard reports in %s: the suite produced no report at all, which is not a pass", dir)
	}

	reports := make([]ShardReport, 0, len(matches))
	for _, path := range matches {
		f, openErr := os.Open(path) //nolint:gosec // path comes from a glob of a caller-named directory.
		if openErr != nil {
			return nil, fmt.Errorf("opening shard report %s: %w", path, openErr)
		}
		shard := strings.TrimSuffix(filepath.Base(path), ".json")
		rep, parseErr := ParseReport(shard, f)
		closeErr := f.Close()
		if parseErr != nil {
			return nil, parseErr
		}
		if closeErr != nil {
			return nil, fmt.Errorf("closing shard report %s: %w", path, closeErr)
		}
		reports = append(reports, rep)
	}
	return reports, nil
}
