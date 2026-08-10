package main

// autorollback.go is the composition root's half of T-2603 (and the half of
// T-2602 that card deliberately left unwired): one small watcher that sees
// every findings cycle and answers the two questions the change engine cannot
// answer for itself.
//
//  1. change.CanaryHealthChecker (T-2602's `gate: auto` evidence source).
//     Before this file it was defined, tested and NOT wired, so asking for
//     automatic promotion was refused at validation time — honest, but it made
//     `gate: auto` unusable in production. The verdict is now real evidence:
//     error-severity findings attributable to the canary nodes that were first
//     seen at or after the instant the canary stage started mutating them.
//
//  2. The per-cycle feed into change.Service.ObserveFindings (T-2603's
//     finding-triggered rollback). The change engine owns the decision — which
//     changesets are guarded, what their pre-apply baseline was, what their
//     Impact covers; this file only hands it the stream.
//
// WHY FIRST-SEEN AND NOT "PRESENT NOW". A canary hold is short. Asking "is
// there an error finding on pve1 right now" would fail a hold for a finding
// that was already firing before the apply — the same mistake T-2603's rule 2
// exists to prevent, one layer up. So this tracker keeps the instant each
// stable finding ID was FIRST observed and the auto gate only counts the ones
// that appeared during the hold.
//
// FAIL-CLOSED. A hold that no findings cycle completed inside produced no
// evidence at all, and an unassessable canary is not a proof of safety
// (T-2602's own stance, which resolveHold already applies to a checker
// error). Such a hold is reported un-clean with that reason rather than
// promoted on silence.

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/findings"
)

// findingsGuard tracks per-finding first-seen instants across findings cycles
// and fans each cycle out to the change engine. Safe for concurrent use: the
// findings loop writes it, the canary hold timer reads it.
type findingsGuard struct {
	firstSeen map[string]int64
	now       func() time.Time
	log       *slog.Logger
	// changeSvc is late-bound: this guard is constructed before the change
	// service (it is one of that service's own Config dependencies), and set
	// once immediately after — the same late-binding convention mgmtAdapter
	// and scheduleAdapter already use in server.go.
	changeSvc   *change.Service
	last        []findings.Finding
	lastCycleAt int64
	mu          sync.Mutex
}

func newFindingsGuard(now func() time.Time, log *slog.Logger) *findingsGuard {
	if now == nil {
		now = time.Now
	}
	return &findingsGuard{firstSeen: map[string]int64{}, now: now, log: log}
}

// set late-binds the change service. Called once, right after it is built.
func (g *findingsGuard) set(svc *change.Service) {
	g.mu.Lock()
	g.changeSvc = svc
	g.mu.Unlock()
}

// observe is the findings.Config.OnCycle hook: record first-seen instants for
// this cycle, forget findings that have cleared (so a finding that comes back
// after clearing is genuinely new again), and hand the stream to the change
// engine.
func (g *findingsGuard) observe(ctx context.Context, fs []findings.Finding) {
	at := g.now().Unix()

	g.mu.Lock()
	seen := make(map[string]int64, len(fs))
	for _, f := range fs {
		if prev, ok := g.firstSeen[f.ID]; ok {
			seen[f.ID] = prev
			continue
		}
		seen[f.ID] = at
	}
	g.firstSeen = seen
	g.last = append([]findings.Finding(nil), fs...)
	g.lastCycleAt = at
	svc := g.changeSvc
	g.mu.Unlock()

	if svc == nil {
		return // a cycle that beat the change engine's construction; nothing is armed yet
	}
	svc.ObserveFindings(ctx, toObservedFindings(fs))
}

// toObservedFindings converts the unified stream into the change engine's own
// view of it. The conversion lives here, not in internal/change, because
// internal/findings imports internal/change — the dependency can only run in
// that direction.
func toObservedFindings(fs []findings.Finding) []change.ObservedFinding {
	out := make([]change.ObservedFinding, 0, len(fs))
	for _, f := range fs {
		out = append(out, change.ObservedFinding{
			ID: f.ID, Check: f.Check, Severity: f.Severity, Detail: f.Detail,
			Nodes: f.Nodes, Refs: f.Refs,
		})
	}
	return out
}

// CheckCanary implements change.CanaryHealthChecker: the evidence T-2602's
// `gate: auto` promotes (or refuses to promote) on.
func (g *findingsGuard) CheckCanary(_ context.Context, nodes []string, sinceUnix int64) (change.CanaryVerdict, error) {
	want := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		want[n] = true
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.lastCycleAt == 0 || g.lastCycleAt < sinceUnix {
		return change.CanaryVerdict{
			Healthy: false,
			Reason:  "no findings cycle completed during the canary hold, so there is no evidence to promote on",
		}, nil
	}

	var newErrors []string
	for _, f := range g.last {
		if f.Severity != findings.SeverityError {
			continue
		}
		if first, ok := g.firstSeen[f.ID]; !ok || first < sinceUnix {
			continue // already firing before the canary stage touched anything
		}
		if !attributableToNodes(f, want) {
			continue
		}
		newErrors = append(newErrors, f.ID)
	}
	if len(newErrors) > 0 {
		return change.CanaryVerdict{
			Healthy:  true, // reachable and assessed — but not clean
			Findings: newErrors,
			Reason:   "new error-severity findings appeared on the canary nodes during the hold",
		}, nil
	}
	return change.CanaryVerdict{Healthy: true}, nil
}

// attributableToNodes reports whether f names one of nodes, either directly or
// through a ref whose node component is one of them.
func attributableToNodes(f findings.Finding, want map[string]bool) bool {
	for _, n := range change.FindingNodes(change.ObservedFinding{Nodes: f.Nodes, Refs: f.Refs}) {
		if want[n] {
			return true
		}
	}
	return false
}
