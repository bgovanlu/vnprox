// SPDX-License-Identifier: Apache-2.0

// Package perfbudget is T-2506's single source of performance budgets: the
// machine-readable file both measurement sites read, the median-of-N
// aggregation they measure with, the host normalisation that stops a budget
// measured on a 32-core box from reddening a 2-core one, and the headroom
// report every run prints.
//
// # Why the numbers are not simply absolute
//
// T-2505 learned this the expensive way (T-2505-input-02): commit 4968bf3 is
// 89 passed / 0 failed here and 87 / 2 on a GitHub-hosted runner, same
// command, no code difference — this host is 32-core/62 GB, that one is 2-4
// core/~16 GB. A wall-clock number measured here and asserted there is a gate
// that is green on the developer's machine and red on every pipeline run,
// which trains people to ignore it. docs/development.md records what that
// costs: make check's bare `npm audit` failed on every run regardless of the
// diff until an allowlist replaced it.
//
// So every budget declares how it survives a change of machine:
//
//   - "calibrated" — the limit is multiplied by this machine's measured speed
//     relative to the reference host's, using the fixed CPU kernel in
//     calibrate.go. The right choice for CPU-bound Go work.
//   - "cores" — the limit is multiplied by T-2505's own availableParallelism
//     ladder (x2.5 under 4 cores, x1.5 under 8, unchanged above), the
//     normalisation web/playwright.config.ts already applies to its deadlines.
//     The right choice for browser-side work, whose cost is rasterisation and
//     layout rather than anything a Go kernel can time.
//   - "absolute" — no normalisation. Only legal for a report-only budget: an
//     absolute number is a hardware target somebody transcribes, not something
//     a gate may fail a build on. Validate enforces that.
//
// The consequence, stated rather than buried: normalisation only ever loosens.
// Both factors are clamped at a floor of 1.0, so the documented limit is the
// tightest the gate ever gets and the reference host keeps full sensitivity,
// while a slower machine trades sensitivity for not producing false failures.
// A regression small enough to hide inside the factor on a 2-core runner is
// caught on the reference host and not there.
package perfbudget

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RepoRelPath is where the one file lives, relative to the repository root.
// Both this package and web/perf/budgets.ts read exactly this path; a second
// copy anywhere is the failure this card exists to remove.
const RepoRelPath = "perf/budgets.json"

// Direction says which side of the limit is good.
type Direction string

// The two directions a budget can have. Every budget today is a Max (a
// duration ceiling); Min exists because a frame-rate budget is naturally a
// floor, and expressing it as a ceiling on frame time would be one more unit
// conversion for a reader to get wrong.
const (
	// Max means the measurement must not exceed the limit.
	Max Direction = "max"
	// Min means the measurement must not fall below the limit.
	Min Direction = "min"
)

// Scaling says how a budget survives a change of machine. See the package
// comment for why "absolute" is not allowed to gate.
type Scaling string

// The three normalisation modes.
const (
	// Calibrated multiplies the limit by this machine's measured speed
	// relative to the reference host (calibrate.go).
	Calibrated Scaling = "calibrated"
	// Cores multiplies the limit by T-2505's availableParallelism ladder.
	Cores Scaling = "cores"
	// Absolute does not normalise at all. Report-only.
	Absolute Scaling = "absolute"
)

// Enforcement says whether exceeding the budget fails the run.
type Enforcement string

// The two enforcement levels.
const (
	// Gate means exceeding it fails the run, naming the budget.
	Gate Enforcement = "gate"
	// Report means measured and reported with its headroom, but never fails a
	// run. For targets this environment cannot honestly verify — the 20 ms v2
	// frame budget is a GPU-compositing number and the e2e runner is
	// software-rasterised.
	ReportOnly Enforcement = "report"
)

// MinGateSamples is how many measurements a gating budget must take. A gate
// that decides on one sample fails on one scheduling hiccup; T-2506 AC4
// requires the measurement be a median of N runs, and Validate enforces this
// floor so a later budget cannot quietly opt out of it.
const MinGateSamples = 3

// Budget is one measured quantity with a limit.
//
// String fields lead and the numerics trail: govet's fieldalignment (enabled
// repo-wide via enable-all) wants the pointer-bearing prefix to be as short as
// it can be, and every string here carries a pointer.
type Budget struct {
	// ID is the stable name the gate prints and docs/performance.md keys on.
	ID string `json:"id"`
	// Title is the one-line human name.
	Title string `json:"title"`
	// Site is the repo-relative file that measures it. The card's requirement
	// is that both measurement sites read one file; this is the back-reference
	// that makes an orphaned budget visible.
	Site string `json:"site"`
	// Metric describes exactly what is timed, in enough detail to re-derive it.
	Metric string `json:"metric"`
	// Unit is the unit of Limit and of every sample ("ms", "fps", ...).
	Unit string `json:"unit"`
	// Direction is Max or Min.
	Direction Direction `json:"direction"`
	// Scaling is how the limit is normalised for the measuring machine.
	Scaling Scaling `json:"scaling"`
	// Enforcement is Gate or Report.
	Enforcement Enforcement `json:"enforcement"`
	// Why records where the number came from and what it is protecting.
	Why string `json:"why"`
	// Limit is the budget itself, in Unit, as it holds on the reference host.
	Limit float64 `json:"limit"`
	// Samples is N: how many measurements the site takes before taking the
	// median. AC4's noise control.
	Samples int `json:"samples"`
}

// ReferenceHost is the machine the limits were measured on. A budget without
// one is the thing T-2505-input-02 says is not a budget.
type ReferenceHost struct {
	Name     string `json:"name"`
	CPU      string `json:"cpu"`
	Memory   string `json:"memory"`
	OS       string `json:"os"`
	Measured string `json:"measured"`
	Notes    string `json:"notes"`
	Cores    int    `json:"cores"`
}

// Calibration is the reference timing of calibrate.go's fixed kernel on
// ReferenceHost. Everything "calibrated" is scaled by this machine's kernel
// time divided by ReferenceNS.
type Calibration struct {
	// Workload names the kernel. It must equal CalibrationWorkload; a kernel
	// change against a stale ReferenceNS would silently rescale every budget,
	// so the mismatch is a hard test failure rather than a comment.
	Workload string `json:"workload"`
	Notes    string `json:"notes"`
	// ReferenceNS is the kernel's median wall time on ReferenceHost, in
	// nanoseconds.
	ReferenceNS float64 `json:"reference_ns"`
	// Samples is how many kernel runs that median is taken over.
	Samples int `json:"samples"`
	// MaxFactor clamps the normalisation. A machine that measures 20x slower
	// than the reference is not one whose budgets should stretch 20x; it is one
	// whose measurement is not worth trusting, and the clamp puts that in the
	// report instead of silently passing everything.
	MaxFactor float64 `json:"max_factor"`
}

// File is perf/budgets.json.
type File struct {
	// Field order is govet fieldalignment's, not the JSON document's (which
	// encoding/json does not care about): the shortest pointer-bearing prefix
	// puts the slice's single pointer word in front of the two nested structs'
	// string runs.
	Budgets       []Budget      `json:"budgets"`
	Comment       string        `json:"comment"`
	ReferenceHost ReferenceHost `json:"reference_host"`
	Calibration   Calibration   `json:"calibration"`
}

// Load reads and validates the budgets file at path.
func Load(path string) (File, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // a repo-relative path this repo owns
	if err != nil {
		return File{}, fmt.Errorf("reading budgets %s: %w", path, err)
	}
	var f File
	dec := json.NewDecoder(bytes.NewReader(raw))
	// A misspelled key is a budget that silently keeps its zero value, which is
	// exactly the kind of quiet wrongness a gate must not have.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return File{}, fmt.Errorf("parsing budgets %s: %w", path, err)
	}
	if err := f.Validate(); err != nil {
		return File{}, fmt.Errorf("validating budgets %s: %w", path, err)
	}
	return f, nil
}

// LoadRepo reads the repository's own budgets file, found by walking up from
// the working directory to the go.mod. Its callers are tests in packages at
// varying depths, and a relative "../../perf/budgets.json" in each of them is
// one more chance to write the wrong number of dots.
func LoadRepo() (File, error) {
	root, err := RepoRoot()
	if err != nil {
		return File{}, err
	}
	return Load(filepath.Join(root, RepoRelPath))
}

// RepoRoot walks up from the working directory until it finds the go.mod that
// owns this module.
func RepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("locating repo root: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("locating repo root: no go.mod in any parent directory")
		}
		dir = parent
	}
}

// ByID returns the named budget.
func (f File) ByID(id string) (Budget, error) {
	for _, b := range f.Budgets {
		if b.ID == id {
			return b, nil
		}
	}
	known := make([]string, 0, len(f.Budgets))
	for _, b := range f.Budgets {
		known = append(known, b.ID)
	}
	sort.Strings(known)
	return Budget{}, fmt.Errorf("no budget %q in %s; known budgets: %s", id, RepoRelPath, strings.Join(known, ", "))
}

// ForSite returns every budget measured by one file, in file order.
func (f File) ForSite(site string) []Budget {
	var out []Budget
	for _, b := range f.Budgets {
		if b.Site == site {
			out = append(out, b)
		}
	}
	return out
}

// Validate rejects a budgets file that could not be gated on honestly.
//
// The two structural rules are the ones T-2506 exists to make impossible to
// forget, and both are consequences of a real incident rather than taste:
//
//   - a gate may not be "absolute" — that is the budget shape that is green on
//     a 32-core box and red on a 2-core runner (T-2505-input-02);
//   - a gate needs at least MinGateSamples measurements — a single-sample gate
//     fails on one scheduling hiccup, which is the failure T-2505-input-01
//     recorded four independent times.
func (f File) Validate() error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if f.Calibration.Workload == "" {
		add("calibration.workload is empty")
	}
	if f.Calibration.ReferenceNS <= 0 {
		add("calibration.reference_ns must be positive, got %g", f.Calibration.ReferenceNS)
	}
	if f.Calibration.Samples < 1 {
		add("calibration.samples must be at least 1, got %d", f.Calibration.Samples)
	}
	if f.Calibration.MaxFactor < 1 {
		add("calibration.max_factor must be at least 1, got %g", f.Calibration.MaxFactor)
	}
	if f.ReferenceHost.Name == "" {
		add("reference_host.name is empty: a budget that cannot say which machine it holds for is not a budget (T-2505-input-02)")
	}
	if f.ReferenceHost.Cores < 1 {
		add("reference_host.cores must be at least 1, got %d", f.ReferenceHost.Cores)
	}
	if len(f.Budgets) == 0 {
		add("no budgets")
	}

	seen := make(map[string]bool, len(f.Budgets))
	for _, b := range f.Budgets {
		switch {
		case b.ID == "":
			add("a budget has no id")
			continue
		case seen[b.ID]:
			add("%s: duplicate id", b.ID)
			continue
		}
		seen[b.ID] = true
		problems = append(problems, validateBudget(b)...)
	}

	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func validateBudget(b Budget) []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if b.Title == "" {
		add("%s: no title", b.ID)
	}
	if b.Site == "" {
		add("%s: no site — a budget nothing measures is a wish", b.ID)
	}
	if b.Metric == "" {
		add("%s: no metric description", b.ID)
	}
	if b.Unit == "" {
		add("%s: no unit", b.ID)
	}
	if len(b.Why) < 20 {
		add("%s: why must say where the number came from (at least 20 characters)", b.ID)
	}
	if b.Limit <= 0 {
		add("%s: limit must be positive, got %g", b.ID, b.Limit)
	}
	if b.Direction != Max && b.Direction != Min {
		add("%s: direction must be %q or %q, got %q", b.ID, Max, Min, b.Direction)
	}
	switch b.Scaling {
	case Calibrated, Cores, Absolute:
	default:
		add("%s: scaling must be %q, %q or %q, got %q", b.ID, Calibrated, Cores, Absolute, b.Scaling)
	}
	switch b.Enforcement {
	case Gate, ReportOnly:
	default:
		add("%s: enforcement must be %q or %q, got %q", b.ID, Gate, ReportOnly, b.Enforcement)
	}
	if b.Samples < 1 {
		add("%s: samples must be at least 1, got %d", b.ID, b.Samples)
	}
	if b.Enforcement == Gate {
		if b.Scaling == Absolute {
			add("%s: an absolute budget may not gate — an unnormalised wall-clock number is green on the reference host and red on a 2-4 core runner (T-2505-input-02); make it %q or %q, or drop it to %q",
				b.ID, Calibrated, Cores, ReportOnly)
		}
		if b.Samples < MinGateSamples {
			add("%s: a gating budget needs at least %d samples so the verdict is a median, got %d (T-2506 AC4)",
				b.ID, MinGateSamples, b.Samples)
		}
	}
	// A browser-side budget cannot be calibrated: the calibration kernel is a
	// Go CPU loop timed in this process, and the browser's cost is
	// rasterisation and layout in another one. web/perf/budgets.ts implements
	// "cores" and "absolute" only, and this is the rule that keeps the file
	// from asking it for something it cannot do.
	if b.Scaling == Calibrated && strings.HasPrefix(b.Site, "web/") {
		add("%s: %s is a browser-side site, which cannot run the Go calibration kernel; use %q", b.ID, b.Site, Cores)
	}
	return problems
}
