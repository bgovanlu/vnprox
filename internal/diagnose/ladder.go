// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// Request is one ladder run's input — the same fields POST /diagnose's
// body carries (docs/api.md's Diagnosis section). TargetRef is an
// inventory Ref string ("kind:node:id") naming the guest/edge to diagnose;
// its meaning (which steps apply, how each resolves a concrete src/dst) is
// entirely up to the registered Steps — this package treats it as an
// opaque string.
type Request struct {
	TargetRef         string
	EscalateToCapture bool
}

// Outcome is what one registered Step.Run returns for one Request — the
// Ladder wraps it into a StepResult (stamping Name/RanAt) and folds its
// FindingIDs/SuggestedFixRef into the overall Verdict.
//
// A step reports Eligible: false to short-circuit itself for a target it
// doesn't apply to (StatusSkipped, never StatusError, and SkipReason is
// required) — e.g. no guest-interior step for a bare bridge target
// (T-1307's card, AC1). Err set (regardless of Eligible) always yields
// StatusError and takes priority over every other field — reserved for a
// genuine ladder-level failure, not an honest "could not attempt this"
// result (which is Eligible: true with a Summary saying so, StatusRan).
type Outcome struct {
	Err             error
	Detail          any
	SkipReason      string
	Summary         string
	SuggestedFixRef string
	FindingIDs      []string
	Eligible        bool
}

// StepFunc runs one ladder step for req. Implementations live in
// internal/api/diagnose.go, each a thin wrapper composing one of this
// phase's existing surfaces — this package never reimplements any of them,
// only sequences and reports their outcomes. A StepFunc must not block
// indefinitely; it should respect ctx cancellation the same way any other
// request-scoped call in this codebase does.
type StepFunc func(ctx context.Context, req Request) Outcome

// Step names one registered ladder step. Name must be stable — it is part
// of the documented ladder-result contract (docs/api.md's Diagnosis
// section) and T-1701's MCP surface keys off it.
type Step struct {
	Run  StepFunc
	Name string
}

// Clock is the ladder's injected time source (tests use a fixed clock; the
// daemon wires time.Now via NewLadder's nil default).
type Clock func() time.Time

// Ladder is a registration table of steps, run in order for every target —
// deliberately not a hardcoded sequence: appending a Step here (or at
// construction via NewLadder) is how a future card (e.g. Phase 14's
// WireGuard/edge diagnostics) extends the ladder without touching any of
// this package's own code, per T-1307's card.
type Ladder struct {
	clock Clock
	steps []Step
}

// NewLadder builds a Ladder from steps, run in the given order. A nil
// clock defaults to time.Now (the production case); tests inject a fixed
// clock for deterministic RanAt assertions.
func NewLadder(steps []Step, clock Clock) *Ladder {
	if clock == nil {
		clock = time.Now
	}
	return &Ladder{steps: append([]Step(nil), steps...), clock: clock}
}

// Run executes every registered step in order against req, never
// short-circuiting the whole ladder on one step's skip/error (T-1307's
// AC1/AC3: every OTHER eligible step still runs and the ladder still
// returns a verdict) — folding the per-step outcomes into one Result.
func (l *Ladder) Run(ctx context.Context, req Request) Result {
	res := Result{Target: req.TargetRef, Steps: make([]StepResult, 0, len(l.steps))}
	var findingIDs []string
	var fixRef string
	ran, errored := 0, 0

	for _, step := range l.steps {
		out := step.Run(ctx, req)
		sr := StepResult{Name: step.Name, RanAt: l.clock().Unix()}
		switch {
		case out.Err != nil:
			sr.Status = StatusError
			sr.Summary = out.Err.Error()
			errored++
		case !out.Eligible:
			sr.Status = StatusSkipped
			sr.Summary = out.SkipReason
		default:
			sr.Status = StatusRan
			sr.Summary = out.Summary
			sr.Detail = out.Detail
			ran++
			findingIDs = append(findingIDs, out.FindingIDs...)
			if fixRef == "" && out.SuggestedFixRef != "" {
				fixRef = out.SuggestedFixRef
			}
		}
		res.Steps = append(res.Steps, sr)
	}

	res.Verdict = computeVerdict(len(l.steps), ran, errored, sortedUnique(findingIDs), fixRef)
	return res
}

// computeVerdict derives Verdict.Summary/Confidence from how many steps
// actually ran and whether any related findings surfaced. This is a
// deliberately simple, disclosed heuristic (see T-1307's completion
// report) — a human reads Steps for the real detail; Verdict is a
// one-line orientation, not a scored diagnosis.
func computeVerdict(total, ran, errored int, findingIDs []string, fixRef string) Verdict {
	v := Verdict{LinkedFindingIDs: findingIDs, SuggestedFixRef: fixRef}
	switch {
	case ran == 0:
		v.Confidence = ConfidenceNone
		v.Summary = "no ladder step could run for this target"
	case errored > 0:
		v.Confidence = ConfidenceLow
		v.Summary = fmt.Sprintf("%d of %d step(s) ran; %d failed unexpectedly — see step detail", ran, total, errored)
	case len(findingIDs) > 0:
		v.Confidence = ConfidenceHigh
		v.Summary = fmt.Sprintf("%d of %d step(s) ran; %d related finding(s) surfaced", ran, total, len(findingIDs))
	default:
		v.Confidence = ConfidenceMedium
		v.Summary = fmt.Sprintf("%d of %d step(s) ran; no related findings surfaced", ran, total)
	}
	return v
}

// sortedUnique returns a sorted copy of ss with duplicates and empty
// strings removed (mirrors internal/findings' own helper of the same
// name) — Verdict.LinkedFindingIDs must never contain a duplicate id, and
// a deterministic order keeps the golden/schema test stable. Always
// non-nil (an empty result still serializes as `[]`, never JSON `null`).
func sortedUnique(ss []string) []string {
	seen := make(map[string]bool, len(ss))
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
