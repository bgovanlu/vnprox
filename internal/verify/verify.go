// SPDX-License-Identifier: Apache-2.0

// Package verify implements `vnproxctl verify` (T-2501): the hardware
// validation checklist, executed.
//
// The problem it exists to solve is stated plainly in docs/status-matrix.md
// §5.3: hardware validation sits at a handful of items out of a hundred-odd
// because validating an item means a human reading a checklist line, doing
// the thing, and writing down what happened. That does not scale, does not
// repeat, and cannot be handed to a user who wants to help. planning/validation/
// took the first step — scripts a human runs and pastes back — and this package
// takes the second: the observation, the verdict, and the evidence all in one
// command that anyone with a cluster can run.
//
// It is `internal/doctor` generalised from "is this daemon healthy" to "does
// this cluster exhibit the behaviour we claim". Four properties are carried
// over deliberately, because each is the difference between a report and a
// decoration:
//
//   - **Every check must be able to fail.** Every system interaction arrives
//     through Deps' interfaces rather than being called directly, so each check
//     has a test driving a deliberately broken fixture through it (AC2). A
//     check with no failing fixture fails the build.
//   - **`skip` is never `pass`.** A check that cannot run says *why* and counts
//     as skipped. A run where everything skips exits non-zero and reports
//     `0 passed` (AC3) — "we did not look" and "we looked and it was fine" are
//     different facts, and a suite that conflates them is worse than no suite,
//     because it is trusted.
//   - **A verdict without evidence is an opinion.** Every pass and every fail
//     carries the command output, API response, or captured state the verdict
//     rests on, enforced by Report.Validate rather than merely asserted. The
//     whole value of this artifact over a ticked checkbox is that a reader can
//     disagree with the verdict and check the working.
//   - **A green run cannot be produced by accident.** The suite refuses to run
//     against a mock endpoint unless --allow-mock is passed (AC4), because a
//     hardware-validation report produced against internal/pvemock says nothing
//     about hardware — and would be indistinguishable from one that did.
//
// What a check reports is an Outcome, not a Result: the ID, matrix row, area
// and hardware precondition are supplied by the registry, so a check
// structurally cannot report under an ID other than the one it is registered
// as. See registry.go.
package verify

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Status is one check's verdict.
type Status string

const (
	// StatusPass — the behaviour was observed, and it was what we claim.
	StatusPass Status = "pass"
	// StatusFail — the behaviour was observed, and it was not what we claim.
	// Drives a non-zero exit.
	StatusFail Status = "fail"
	// StatusSkip — the behaviour could not be observed here, with a reason.
	// Deliberately distinct from pass, and deliberately NOT a success: a run
	// of nothing but skips exits non-zero (AC3).
	StatusSkip Status = "skip"
)

// Suite groups checks by what running them costs the operator.
type Suite string

const (
	// SuiteHardware needs a real PVE node and changes nothing. This is the
	// suite an operator who wants to help can run on a production cluster
	// without a maintenance window.
	SuiteHardware Suite = "hardware"
	// SuiteMultinode needs two or more real nodes. On a smaller cluster its
	// checks skip loudly, naming the node count they saw.
	SuiteMultinode Suite = "multinode"
	// SuiteDestructive injects failures — pulls a node, interrupts an apply,
	// lets a commit-confirm window expire. It requires --i-understand and
	// refuses to run without it.
	SuiteDestructive Suite = "destructive"
)

// AllSuites is every suite name, in increasing order of what it costs to run.
var AllSuites = []Suite{SuiteHardware, SuiteMultinode, SuiteDestructive}

// ValidSuite reports whether s names a suite.
func ValidSuite(s Suite) bool {
	for _, known := range AllSuites {
		if s == known {
			return true
		}
	}
	return false
}

// Evidence is the observation a verdict rests on: what was asked, and what
// came back, verbatim.
//
// It is a required field on every pass and every fail (Report.Validate), and
// that requirement is the point of the whole artifact. A checklist tick says
// "someone says this works". An evidence-carrying result says "here is the
// response; disagree with me if you can read it differently". The second
// survives its author leaving the project; the first does not.
type Evidence struct {
	// Source is the kind of observation: "pve-api", "daemon-api", "command",
	// "file", or "state".
	Source string `json:"source"`
	// Ref is what was asked — the API path, the command line, the file path.
	Ref string `json:"ref"`
	// Output is what came back, verbatim up to MaxEvidenceBytes. A check that
	// observed nothing writes something saying so; empty is not allowed,
	// because an empty evidence field is indistinguishable from a check that
	// forgot to record one.
	Output string `json:"output"`
}

// MaxEvidenceBytes bounds one Evidence.Output. A hardware report is meant to
// be attached to an issue; an unbounded packet-capture dump in it is not.
// Truncation is marked in-band so a reader never mistakes a cut-off body for
// a short one.
const MaxEvidenceBytes = 8192

// NewEvidence builds an Evidence, truncating output to MaxEvidenceBytes.
func NewEvidence(source, ref, output string) Evidence {
	if len(output) > MaxEvidenceBytes {
		output = output[:MaxEvidenceBytes] + fmt.Sprintf("\n... [truncated, %d bytes total]", len(output))
	}
	if strings.TrimSpace(output) == "" {
		// An observation of nothing is still an observation, but it has to
		// say so rather than arriving as an empty string that Validate would
		// reject and a reader would misread.
		output = "(no output)"
	}
	return Evidence{Source: source, Ref: ref, Output: output}
}

// Evidence source constants, so a consumer (T-2503's telemetry reduction)
// can group without matching free text.
const (
	SourcePVEAPI    = "pve-api"
	SourceDaemonAPI = "daemon-api"
	SourceCommand   = "command"
	SourceFile      = "file"
	SourceState     = "state"
)

// Outcome is what a Check's Run returns.
//
// It deliberately carries no ID, matrix row, area or precondition: those come
// from the registry entry the function is attached to, so a check cannot
// report a verdict under a row it is not registered against. Run assembles
// the Result. See registry.go.
type Outcome struct {
	Status   Status
	Detail   string
	Reason   string
	Evidence []Evidence
}

// Pass records an observed, correct behaviour. Evidence is variadic in the
// signature and required by Report.Validate — a compile-time requirement
// would be nicer, but it would make every call site build a slice literal to
// say the same thing the validator says with a message.
func Pass(detail string, ev ...Evidence) Outcome {
	return Outcome{Status: StatusPass, Detail: detail, Evidence: ev}
}

// Fail records an observed, wrong behaviour.
func Fail(detail string, ev ...Evidence) Outcome {
	return Outcome{Status: StatusFail, Detail: detail, Evidence: ev}
}

// Skip records that the behaviour could not be observed here, and why.
//
// The reason is not optional and must not diagnose a cause it did not check:
// doctor learned that on real hardware (docs/status-matrix.md §5.10 — a skip
// that asserted "no PVE credentials configured" on a node whose collectors
// were polling PVE successfully). Say what was not observed and what would
// make it observable.
func Skip(reason string, ev ...Evidence) Outcome {
	return Outcome{Status: StatusSkip, Detail: reason, Reason: reason, Evidence: ev}
}

// Result is one check's outcome, joined to its registry identity.
//
// which is read by humans before it is read by a program.
//
//nolint:govet // fieldalignment: this is the on-disk artifact's field order,
type Result struct {
	// ID is the check's stable identifier. It is also, via MatrixRow, the
	// join key to docs/status-matrix.md §2.
	ID string `json:"id"`
	// MatrixRow is the docs/status-matrix.md §2 row number this check backs.
	MatrixRow int `json:"matrixRow"`
	// Area is that row's feature-area title, copied from the registry. A test
	// fails the build if it stops matching the matrix (AC1).
	Area string `json:"area"`
	// Suite is which suite the check belongs to.
	Suite Suite `json:"suite"`
	// Precondition is the hardware this check needs in order to mean anything
	// (AC7). Required on every check, present on every result, so a reader of
	// a skipped result knows what to go and get.
	Precondition string `json:"precondition"`
	// Status is the verdict.
	Status Status `json:"status"`
	// Detail is the verdict in one line.
	Detail string `json:"detail"`
	// SkipReason is why the check could not run. Required on skip, absent
	// otherwise.
	SkipReason string `json:"skipReason,omitempty"`
	// Evidence is what the verdict rests on. Required on pass and fail.
	Evidence []Evidence `json:"evidence"`
	// DurationMS is how long the check took, for T-2503's reduction and for
	// spotting a check that has quietly become a timeout.
	DurationMS int64 `json:"durationMs"`
}

// Summary counts each status. It is part of the signed artifact and is
// checked against the results by Report.Validate, so a report cannot claim a
// pass count its own results do not support.
type Summary struct {
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

// Environment is what the report is *about* — the thing that makes it
// hardware evidence rather than an assertion. A report that cannot say which
// PVE, which kernel and which NICs produced it is not attributable, and an
// unattributable hardware report is a checklist tick with extra steps.
//
// Every field is required to be non-empty by Report.Validate. Where a value
// genuinely could not be read, the collector writes "unknown" explicitly —
// an honest answer, the same convention planning/validation's evidence blobs
// use — rather than leaving a blank a reader would fill in optimistically.
//
// by a human before it is read by a program.
//
//nolint:govet // fieldalignment: on-disk artifact field order, which is read
type Environment struct {
	// VnproxVersion is the vnproxctl binary's version.
	VnproxVersion string `json:"vnproxVersion"`
	// PVEVersion is what the cluster reported, e.g. "pve-manager/9.2.4".
	PVEVersion string `json:"pveVersion"`
	// Kernel is `uname -r` from the node the suite ran against.
	Kernel string `json:"kernel"`
	// NICModels are the physical NICs observed, so a driver-specific result
	// (LACP partner parsing, SR-IOV VF lifecycle) can be read in context.
	NICModels []string `json:"nicModels"`
	// Nodes are the cluster nodes seen, in order.
	Nodes []string `json:"nodes"`
	// PVEEndpoint is the API base the suite ran against.
	PVEEndpoint string `json:"pveEndpoint"`
	// Mock records whether that endpoint was identified as a mock, and how.
	// It is carried into the artifact rather than only checked at the door:
	// a report is passed around long after the run, and "this was produced
	// against a replay server" must travel with it.
	Mock bool `json:"mock"`
	// MockReason names the signal that identified the endpoint as a mock,
	// empty when it was not one.
	MockReason string `json:"mockReason,omitempty"`
}

// Report is one run of one suite.
//
//nolint:govet // fieldalignment: on-disk artifact field order.
type Report struct {
	// ReportVersion is the artifact schema version. T-2503 reduces this
	// format; a consumer that does not recognise the version must say so
	// rather than guess.
	ReportVersion int `json:"reportVersion"`
	// GeneratedAt is when the run finished, UTC.
	GeneratedAt time.Time `json:"generatedAt"`
	// Suite is which suite ran, empty when the run was an explicit --only
	// selection instead.
	Suite Suite `json:"suite,omitempty"`
	// Selection is the --only ids, when the run was one.
	//
	// It is recorded rather than flattened into Suite because the two mean
	// different things to a reader: a suite run covers everything that suite
	// claims, and a selection covers exactly what somebody asked for. A
	// consumer that treats a three-check selection as a hardware-suite pass
	// would be overstating it by twenty checks.
	Selection []string `json:"selection,omitempty"`
	// Environment attributes the run to real hardware.
	Environment Environment `json:"environment"`
	// Results is one entry per check that was selected, in registry order.
	Results []Result `json:"results"`
	// Summary counts them.
	Summary Summary `json:"summary"`
}

// CurrentReportVersion is the artifact schema version this build writes.
const CurrentReportVersion = 1

// OK reports whether the run counts as a successful validation.
//
// It is deliberately NOT "no failures". A run in which every check skipped
// has no failures and has validated nothing; treating that as success is
// exactly the conflation AC3 forbids, and it is the failure mode that would
// let a CI job go green on a machine with no cluster attached. Success
// requires at least one check to have actually observed something.
func (r Report) OK() bool { return r.Summary.Failed == 0 && r.Summary.Passed > 0 }

// Validate enforces the invariants every consumer of this artifact relies on.
//
// It is the structural half of AC2 and AC3: a check that returns a pass with
// no evidence, or a skip with no reason, produces a malformed report that the
// CLI refuses to print, rather than a plausible-looking line nobody reads
// twice. Returned as an error rather than panicking so the failure mode is a
// loud message, not a crash mid-run on someone's production cluster.
func (r Report) Validate() error {
	if r.ReportVersion == 0 {
		return fmt.Errorf("report has no reportVersion")
	}
	if r.GeneratedAt.IsZero() {
		return fmt.Errorf("report has no generatedAt timestamp")
	}
	// Exactly one of the two: a report that names neither cannot say what it
	// covered, and one that names both is ambiguous about it.
	switch {
	case len(r.Selection) > 0 && r.Suite != "":
		return fmt.Errorf("report names both suite %q and an explicit selection of %d check(s)", r.Suite, len(r.Selection))
	case len(r.Selection) == 0 && !ValidSuite(r.Suite):
		return fmt.Errorf("report names unknown suite %q and no explicit selection", r.Suite)
	}
	if err := r.Environment.validate(); err != nil {
		return fmt.Errorf("report environment: %w", err)
	}

	seen := make(map[string]bool, len(r.Results))
	for _, res := range r.Results {
		if err := res.validate(); err != nil {
			return err
		}
		if seen[res.ID] {
			return fmt.Errorf("check %q reported twice", res.ID)
		}
		seen[res.ID] = true
	}

	if got := Summarize(r.Results); got != r.Summary {
		return fmt.Errorf("summary %+v does not match the results it summarises %+v", r.Summary, got)
	}
	return nil
}

func (e Environment) validate() error {
	required := map[string]string{
		"vnproxVersion": e.VnproxVersion,
		"pveVersion":    e.PVEVersion,
		"kernel":        e.Kernel,
		"pveEndpoint":   e.PVEEndpoint,
	}
	// Sorted so the message is the same on every run; map iteration order
	// would otherwise make a build failure look intermittent.
	names := make([]string, 0, len(required))
	for name := range required {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if strings.TrimSpace(required[name]) == "" {
			return fmt.Errorf("%s is empty; write \"unknown\" rather than leaving it blank", name)
		}
	}
	if e.Mock && strings.TrimSpace(e.MockReason) == "" {
		return fmt.Errorf("environment is flagged as a mock with no mockReason naming the signal")
	}
	if !e.Mock && strings.TrimSpace(e.MockReason) != "" {
		return fmt.Errorf("environment carries mockReason %q but is not flagged as a mock", e.MockReason)
	}
	return nil
}

func (res Result) validate() error {
	if strings.TrimSpace(res.ID) == "" {
		return fmt.Errorf("result with empty check id: %+v", res)
	}
	if res.MatrixRow <= 0 {
		return fmt.Errorf("check %q names no status-matrix.md row", res.ID)
	}
	if strings.TrimSpace(res.Area) == "" {
		return fmt.Errorf("check %q names no feature area", res.ID)
	}
	if !ValidSuite(res.Suite) {
		return fmt.Errorf("check %q names unknown suite %q", res.ID, res.Suite)
	}
	if strings.TrimSpace(res.Precondition) == "" {
		return fmt.Errorf("check %q states no hardware precondition", res.ID)
	}
	if strings.TrimSpace(res.Detail) == "" {
		return fmt.Errorf("check %q has no detail", res.ID)
	}

	switch res.Status {
	case StatusPass, StatusFail:
		if res.SkipReason != "" {
			return fmt.Errorf("check %q is %s but carries a skipReason", res.ID, res.Status)
		}
		if len(res.Evidence) == 0 {
			return fmt.Errorf("check %q is %s with no evidence: a verdict nobody can check is an opinion", res.ID, res.Status)
		}
		for i, ev := range res.Evidence {
			if err := ev.validate(); err != nil {
				return fmt.Errorf("check %q evidence[%d]: %w", res.ID, i, err)
			}
		}
	case StatusSkip:
		if strings.TrimSpace(res.SkipReason) == "" {
			return fmt.Errorf("check %q is skipped with no reason: an unexplained skip is indistinguishable from a check nobody wired up", res.ID)
		}
		// Evidence is allowed on a skip (the node count that made a multinode
		// check inapplicable, say) but is not required — there was, by
		// definition, no observation to carry.
		for i, ev := range res.Evidence {
			if err := ev.validate(); err != nil {
				return fmt.Errorf("check %q evidence[%d]: %w", res.ID, i, err)
			}
		}
	default:
		return fmt.Errorf("check %q has unknown status %q", res.ID, res.Status)
	}
	return nil
}

func (e Evidence) validate() error {
	if strings.TrimSpace(e.Source) == "" {
		return fmt.Errorf("evidence has no source")
	}
	if strings.TrimSpace(e.Ref) == "" {
		return fmt.Errorf("evidence from %s names nothing it observed", e.Source)
	}
	if strings.TrimSpace(e.Output) == "" {
		return fmt.Errorf("evidence from %s for %q carries no output", e.Source, e.Ref)
	}
	return nil
}

// Summarize counts statuses. Exported because Report.Validate compares a
// report's own summary against it, and a consumer re-deriving the counts
// should use the same function rather than a second implementation that can
// disagree.
func Summarize(results []Result) Summary {
	var s Summary
	for _, r := range results {
		switch r.Status {
		case StatusPass:
			s.Passed++
		case StatusFail:
			s.Failed++
		case StatusSkip:
			s.Skipped++
		}
	}
	return s
}

// Render writes the human-readable form: failures first, then skips, then
// passes, because the operator reading this in a terminal wants the thing to
// look at, not a column of ticks.
func (r Report) Render() string {
	order := map[Status]int{StatusFail: 0, StatusSkip: 1, StatusPass: 2}
	sorted := make([]Result, len(r.Results))
	copy(sorted, r.Results)
	sort.SliceStable(sorted, func(i, j int) bool {
		return order[sorted[i].Status] < order[sorted[j].Status]
	})

	var b strings.Builder
	fmt.Fprintf(&b, "vnprox %s · PVE %s · kernel %s\n", r.Environment.VnproxVersion, r.Environment.PVEVersion, r.Environment.Kernel)
	scope := "suite " + string(r.Suite)
	if len(r.Selection) > 0 {
		scope = fmt.Sprintf("selection of %d check(s)", len(r.Selection))
	}
	fmt.Fprintf(&b, "%s · endpoint %s\n", scope, r.Environment.PVEEndpoint)
	if r.Environment.Mock {
		fmt.Fprintf(&b, "MOCK ENDPOINT (%s) — this run is not hardware evidence\n", r.Environment.MockReason)
	}
	if len(r.Environment.NICModels) > 0 {
		fmt.Fprintf(&b, "NICs: %s\n", strings.Join(r.Environment.NICModels, ", "))
	}
	b.WriteString("\n")

	for _, res := range sorted {
		fmt.Fprintf(&b, "%-4s %-34s %s\n", strings.ToUpper(string(res.Status)), res.ID, res.Detail)
		if res.Status == StatusSkip {
			fmt.Fprintf(&b, "%-4s %-34s needs: %s\n", "", "", res.Precondition)
		}
	}

	fmt.Fprintf(&b, "\n%d passed, %d failed, %d skipped\n", r.Summary.Passed, r.Summary.Failed, r.Summary.Skipped)
	switch {
	case r.Summary.Failed > 0:
		b.WriteString("\nThis cluster does not behave the way the matrix claims. See the failing checks above.\n")
	case r.Summary.Passed == 0:
		// AC3 in the rendering, not only in the exit code: a wall of skips
		// with a "0 failed" footer reads as success to a tired operator.
		b.WriteString("\nNothing was validated: every check skipped. A skipped check is not a passing one.\n")
	default:
		b.WriteString("\nEvery check that could run, passed.\n")
	}
	return b.String()
}
