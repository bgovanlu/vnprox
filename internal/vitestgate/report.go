// SPDX-License-Identifier: Apache-2.0

// Package vitestgate turns vitest's JSON reporter output into the report
// shape internal/e2egate already knows how to gate, quarantine and trend.
//
// T-3708. cmd/e2egate (T-2505) built the mechanism this package plugs into:
// a quarantine with hard expiries, an append-only run-history log, and a
// flake trend computed FROM that history rather than hand-curated. The unit
// suite (web/, vitest, 2,278 tests as of T-3708) gates every push through
// the pre-push `make ci` hook and had none of it. The incident that exposed
// the gap: web/src/governance/TenantsPanel.test.tsx timed out on a
// `findByRole` under `make ci`'s concurrent load, refused a push, then
// passed 3/3 alone and 295/295 in-suite immediately after (fixed in
// 2cd48367 by raising Testing Library's asyncUtilTimeout). Nothing recorded
// that the test was load-sensitive rather than broken, so the next
// occurrence would have been diagnosed from scratch — exactly the problem
// `cmd/e2egate` already solved for the e2e suite.
//
// Rather than a second gate/quarantine/trend engine, this package supplies
// only what genuinely differs from the e2e suite: parsing vitest's report
// format into internal/e2egate's Outcome/ShardReport shape. Everything
// downstream of that — Quarantine, Validate, Evaluate, RunRecord, Trend,
// TrendReport — is internal/e2egate's own exported code, imported rather
// than copied, because none of it is Playwright-specific: it already
// operates on File+Title-keyed outcomes and knows nothing about shards,
// browsers or specs.
//
// vitest is not sharded the way the e2e suite is: one process runs the
// whole suite and writes one JSON report. So the whole run becomes a single
// e2egate.ShardReport named ShardName — there is no second shard to
// reconcile against, and none of e2egate's MissingShards/ExpectedShards
// machinery is exercised here.
package vitestgate

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bgovanlu/vnprox/internal/e2egate"
)

// ShardName is the e2egate.ShardReport.Shard value every vitest run is
// recorded under. A constant rather than a caller-supplied name because
// there is exactly one process producing exactly one report — nothing here
// picks a name the way an e2e shard does.
const ShardName = "vitest"

// vReport is the subset of vitest's built-in "json" reporter output (Jest's
// own JSON reporter shape, which vitest's replicates for drop-in tooling
// compatibility) that this package reads. As with e2egate's pwReport,
// fields vitest emits and this package does not need are deliberately
// absent: a struct mirroring the whole schema would have to be revised on
// every vitest upgrade, whereas this one only breaks when something it
// genuinely depends on moves.
type vReport struct {
	TestResults []vFileResult `json:"testResults"`
}

// Field order here (and in vAssertion below) groups the pointer-bearing
// fields (strings, slices) ahead of the plain float64s, per golangci-lint's
// fieldalignment check — the same convention internal/e2egate's own
// RunRecord comment explains.
type vFileResult struct {
	// Name is the absolute path to the test file, as vitest reports it.
	Name string `json:"name"`
	// Status is "passed" or "failed" for the file as a whole. Message is
	// only meaningful when Status is "failed" and AssertionResults is
	// empty: the file itself failed to load (a syntax error, a bad
	// import) rather than any test inside it failing.
	Status           string       `json:"status"`
	Message          string       `json:"message"`
	AssertionResults []vAssertion `json:"assertionResults"`
	// StartTime and EndTime are Unix-epoch millisecond timestamps (float
	// because that's how vitest/Jest emit them). Used only to estimate
	// the run's wall clock for reporting; nothing here depends on them
	// for correctness.
	StartTime float64 `json:"startTime"`
	EndTime   float64 `json:"endTime"`
}

type vAssertion struct {
	// AncestorTitles is the describe-block ancestry, outermost first —
	// the same thing e2egate's flatten() builds by hand while walking a
	// Playwright suite tree, except vitest's own reporter already
	// carries it as a flat list.
	AncestorTitles  []string `json:"ancestorTitles"`
	Title           string   `json:"title"`
	Status          string   `json:"status"`
	FailureMessages []string `json:"failureMessages"`
	Duration        float64  `json:"duration"`
}

// ParseReport reads vitest's JSON reporter output and returns it as a
// single e2egate.ShardReport. root, when non-empty, is stripped from each
// test file's absolute path (via filepath.Rel) so File values read the same
// way Playwright's do in web/e2e/quarantine.json — "src/governance/
// TenantsPanel.test.tsx", not a path tied to one checkout's location on
// disk. Pass the directory vitest itself ran from (normally "web").
func ParseReport(r io.Reader, root string) (e2egate.ShardReport, error) {
	var raw vReport
	dec := json.NewDecoder(r)
	if err := dec.Decode(&raw); err != nil {
		return e2egate.ShardReport{}, fmt.Errorf("decoding vitest report: %w", err)
	}

	out := e2egate.ShardReport{Shard: ShardName}
	var minStart, maxEnd float64
	for _, file := range raw.TestResults {
		rel := relTo(root, file.Name)

		if len(file.AssertionResults) == 0 {
			// No test ran in this file at all. A "failed" status with no
			// assertions is vitest reporting that the FILE itself could
			// not be collected (transform/import error) — the same kind
			// of report-level error e2egate.ShardReport.Errors carries
			// for a Playwright webServer that never started. A "passed"
			// status with no assertions is an empty file and carries no
			// information either way.
			if file.Status == "failed" {
				out.Errors = append(out.Errors, fmt.Sprintf("%s: %s", rel, firstLine(file.Message)))
			}
			continue
		}

		for _, a := range file.AssertionResults {
			title := strings.Join(append(append([]string{}, a.AncestorTitles...), a.Title), e2egate.TitleSeparator)
			var durMS int64
			if a.Duration > 0 {
				durMS = int64(a.Duration + 0.5)
			}
			out.Outcomes = append(out.Outcomes, e2egate.Outcome{
				File:       rel,
				Title:      title,
				Shard:      ShardName,
				Status:     mapStatus(a.Status),
				DurationMS: durMS,
			})
		}

		if file.StartTime > 0 && (minStart == 0 || file.StartTime < minStart) {
			minStart = file.StartTime
		}
		if file.EndTime > maxEnd {
			maxEnd = file.EndTime
		}
	}
	if maxEnd > minStart {
		out.DurationMS = int64(maxEnd - minStart)
	}

	sort.Slice(out.Outcomes, func(i, j int) bool { return out.Outcomes[i].Key() < out.Outcomes[j].Key() })
	return out, nil
}

// mapStatus collapses vitest's per-assertion vocabulary ("passed", "failed",
// "skipped", "pending", "todo") to e2egate's three-way Status.
//
// Fails closed on anything else, the same way e2egate's own Playwright
// collapse() treats a result with no recognised status as a failure rather
// than a pass: an assertion result this package does not recognise must not
// read as a silent green.
func mapStatus(s string) e2egate.Status {
	switch s {
	case "passed":
		return e2egate.StatusPassed
	case "skipped", "pending", "todo":
		return e2egate.StatusSkipped
	default: // "failed", or anything unrecognised
		return e2egate.StatusFailed
	}
}

// relTo makes name relative to root when possible, always with forward
// slashes so a quarantine entry written on one machine matches a report
// generated on another.
//
// root is resolved to an absolute path first. vitest's own report always
// names files with an absolute path, and filepath.Rel refuses to compare an
// absolute path against a relative one (it returns an error, which this
// function would otherwise treat as "can't make it relative" and silently
// fall back to the absolute name) — so a caller passing the ordinary
// relative "web" would get every File left absolute and every quarantine
// entry keyed on a relative path would look STALE, having matched nothing.
func relTo(root, name string) string {
	if root != "" {
		if absRoot, err := filepath.Abs(root); err == nil {
			if rel, relErr := filepath.Rel(absRoot, name); relErr == nil {
				return filepath.ToSlash(rel)
			}
		}
	}
	return filepath.ToSlash(name)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
