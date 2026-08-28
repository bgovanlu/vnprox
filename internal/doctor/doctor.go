// SPDX-License-Identifier: Apache-2.0

// Package doctor implements `vnproxctl doctor` (T-1904): a preflight and
// self-check that turns "it doesn't work" into a message naming the file,
// port, privilege, or command involved.
//
// Design constraints that shaped this package:
//
//   - **Every check must be able to fail.** A check that only ever passes is
//     decoration. Each one here has a test driving a deliberately broken
//     fixture through it (T-1904 AC1), which is only possible because every
//     system interaction arrives through Env's function fields rather than
//     being called directly.
//   - **Every fail and warn carries a remediation** (AC2). An operator running
//     doctor already knows something is wrong; the value is entirely in what to
//     do next.
//   - **doctor never mutates anything.** It reads files, dials, and stats. It
//     is safe to run against a live daemon, mid-incident, as root, at any time.
//   - **Unknown is not pass.** A probe that cannot run (no PVE client
//     configured, no store to open) reports StatusSkip with the reason. Silently
//     passing an un-run check is how a diagnostic starts lying.
package doctor

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"
)

// Status is one check's verdict.
type Status string

const (
	// StatusPass — checked, and healthy.
	StatusPass Status = "pass"
	// StatusWarn — checked, and degraded but working. Does not affect the exit
	// code; an operator can run indefinitely in this state.
	StatusWarn Status = "warn"
	// StatusFail — checked, and broken. Drives a non-zero exit (AC4).
	StatusFail Status = "fail"
	// StatusSkip — could not be checked, with a reason. Deliberately distinct
	// from pass: "we did not look" and "we looked and it was fine" are
	// different facts, and conflating them is how a green report hides a
	// problem.
	StatusSkip Status = "skip"
)

// Check names. These appear in `-o json` output and in T-1902 support bundles,
// so they are a stable contract once shipped (AC3).
const (
	CheckConfig        = "config"
	CheckKeyFiles      = "key_files"
	CheckPmxcfs        = "pmxcfs"
	CheckPortConflict  = "port_conflict"
	CheckPVEReachable  = "pve_reachable"
	CheckPVEPrivileges = "pve_privileges"
	CheckPeerSecret    = "peer_secret"
	CheckSchemaVersion = "schema_version"
	CheckClockSkew     = "clock_skew"
	CheckDiskHeadroom  = "disk_headroom"
)

// AllChecks is every check name, in report order: local and cheap first, then
// anything that touches the network. An operator reading a truncated report
// should still have seen the checks most likely to explain a broken install.
var AllChecks = []string{
	CheckConfig,
	CheckKeyFiles,
	CheckPmxcfs,
	CheckSchemaVersion,
	CheckDiskHeadroom,
	CheckPortConflict,
	CheckPVEReachable,
	CheckPVEPrivileges,
	CheckPeerSecret,
	CheckClockSkew,
}

// Thresholds that decide warn-vs-fail. Named, because each is a judgement
// call someone will want to argue with.
const (
	// clockSkewWarn is half of internal/peer's ±30s replay window. Past this,
	// peer requests are still accepted but the margin for further drift is
	// gone.
	clockSkewWarn = 15 * time.Second
	// clockSkewFail is the replay window itself: at or beyond it, peer
	// authentication is already failing or about to.
	clockSkewFail = 30 * time.Second
	// diskHeadroomWarnBytes is where snapshot and capture growth starts to be
	// a question rather than a fact.
	diskHeadroomWarnBytes = 2 << 30 // 2 GiB
	// diskHeadroomFailBytes is where a capture or a snapshot can plausibly
	// fill the filesystem — which, on a hypervisor, is an outage caused by the
	// tool meant to prevent one.
	diskHeadroomFailBytes = 512 << 20 // 512 MiB
	// keyFileMaxMode is the most permissive mode a key file may carry.
	keyFileMaxMode fs.FileMode = 0o600
)

// Result is one check's outcome.
//
// Remediation is required for StatusWarn and StatusFail — enforced by
// Report.Validate and asserted by TestEveryFailAndWarnHasRemediation, because
// AC2 is the difference between a diagnostic and a complaint.
type Result struct {
	Check       string `json:"check"`
	Status      Status `json:"status"`
	Detail      string `json:"detail"`
	Remediation string `json:"remediation,omitempty"`
}

// Summary is the count of each status, so a consumer (T-1902's bundle, CI)
// can branch without walking the results.
type Summary struct {
	Pass int `json:"pass"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
	Skip int `json:"skip"`
}

// Report is the whole run. The JSON shape here is the schema-stable contract
// of AC3.
type Report struct {
	GeneratedAt time.Time `json:"generatedAt"`
	Version     string    `json:"version"`
	Results     []Result  `json:"results"`
	Summary     Summary   `json:"summary"`
}

// Failed reports whether any check failed — the sole input to the exit code
// (AC4: non-zero iff at least one check fails). Warnings deliberately do not
// count: an install script that aborts on a warning would abort on a
// two-node cluster with one node down for maintenance.
func (r Report) Failed() bool { return r.Summary.Fail > 0 }

// Validate enforces the invariants the report's consumers rely on. Returned as
// an error rather than panicking so a malformed report degrades to a loud
// message rather than taking down the CLI.
func (r Report) Validate() error {
	seen := make(map[string]bool, len(r.Results))
	for _, res := range r.Results {
		if res.Check == "" {
			return fmt.Errorf("result with empty check name: %+v", res)
		}
		if seen[res.Check] {
			return fmt.Errorf("check %q reported twice", res.Check)
		}
		seen[res.Check] = true
		switch res.Status {
		case StatusPass, StatusSkip:
		case StatusWarn, StatusFail:
			if strings.TrimSpace(res.Remediation) == "" {
				return fmt.Errorf("check %q is %s with no remediation", res.Check, res.Status)
			}
		default:
			return fmt.Errorf("check %q has unknown status %q", res.Check, res.Status)
		}
		if strings.TrimSpace(res.Detail) == "" {
			return fmt.Errorf("check %q has no detail", res.Check)
		}
	}
	return nil
}

// summarize counts statuses.
func summarize(results []Result) Summary {
	var s Summary
	for _, r := range results {
		switch r.Status {
		case StatusPass:
			s.Pass++
		case StatusWarn:
			s.Warn++
		case StatusFail:
			s.Fail++
		case StatusSkip:
			s.Skip++
		}
	}
	return s
}

// pass/warn/fail/skip are constructors that keep the checks below readable.
func pass(check, detail string) Result {
	return Result{Check: check, Status: StatusPass, Detail: detail}
}

func warn(check, detail, remediation string) Result {
	return Result{Check: check, Status: StatusWarn, Detail: detail, Remediation: remediation}
}

func fail(check, detail, remediation string) Result {
	return Result{Check: check, Status: StatusFail, Detail: detail, Remediation: remediation}
}

func skip(check, reason string) Result {
	return Result{Check: check, Status: StatusSkip, Detail: reason}
}

// Render writes the human-readable form: one line per check, worst first, so
// the thing to fix is at the top rather than buried under a column of passes.
func (r Report) Render() string {
	order := map[Status]int{StatusFail: 0, StatusWarn: 1, StatusSkip: 2, StatusPass: 3}
	sorted := make([]Result, len(r.Results))
	copy(sorted, r.Results)
	sort.SliceStable(sorted, func(i, j int) bool {
		return order[sorted[i].Status] < order[sorted[j].Status]
	})

	var b strings.Builder
	for _, res := range sorted {
		fmt.Fprintf(&b, "%-6s %-16s %s\n", strings.ToUpper(string(res.Status)), res.Check, res.Detail)
		if res.Remediation != "" {
			fmt.Fprintf(&b, "%-6s %-16s -> %s\n", "", "", res.Remediation)
		}
	}
	fmt.Fprintf(&b, "\n%d passed, %d warned, %d failed, %d skipped\n",
		r.Summary.Pass, r.Summary.Warn, r.Summary.Fail, r.Summary.Skip)
	if r.Summary.Fail == 0 && r.Summary.Warn == 0 && r.Summary.Skip == 0 {
		b.WriteString("\nNo problems found.\n")
	}
	return b.String()
}
