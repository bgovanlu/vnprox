// SPDX-License-Identifier: Apache-2.0

package main

// autorollback_test.go covers the composition root's findings watcher: the
// canary health checker T-2602 defined and left unwired, and the conversion
// feeding change.Service.ObserveFindings.
//
// The negative cases here (a pre-existing finding does not fail a hold; a
// finding on another node does not) are each paired with a control leg over
// the SAME watcher state, so a clean verdict is never reported as evidence
// until we have shown the same watcher can report a dirty one.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/findings"
)

func errFinding(id, node string, refs ...string) findings.Finding {
	return findings.Finding{
		ID: id, Source: findings.SourceHealth, Check: "bridge_no_carrier",
		Severity: findings.SeverityError, Detail: "no carrier",
		Nodes: []string{node}, Refs: refs,
	}
}

// testGuard builds a guard over a clock the test drives, so "first seen
// before/after the hold started" is set rather than slept for.
func testGuard(at *int64) *findingsGuard {
	return newFindingsGuard(func() time.Time { return time.Unix(*at, 0) }, testLogger())
}

// TestFindingsGuard_CheckCanary is the `gate: auto` evidence contract.
func TestFindingsGuard_CheckCanary(t *testing.T) {
	ctx := context.Background()
	const holdStart = 2000

	tests := []struct {
		name         string
		wantFindings []string
		cycles       [][]findings.Finding
		cycleAt      []int64
		wantHealthy  bool
	}{
		{
			// Fail-closed: nothing observed during the hold is not evidence
			// of health, so the gate declines rather than promoting on silence.
			name:        "no cycle during the hold is not clean",
			cycles:      [][]findings.Finding{{}},
			cycleAt:     []int64{holdStart - 100},
			wantHealthy: false,
		},
		{
			name:        "a cycle during the hold with nothing firing is clean",
			cycles:      [][]findings.Finding{{}, {}},
			cycleAt:     []int64{holdStart - 100, holdStart + 10},
			wantHealthy: true,
		},
		{
			// The control for the two negative legs below: the SAME watcher,
			// the SAME hold, an error finding that genuinely appeared during
			// the hold on a canary node.
			name: "control: a new error finding on a canary node is reported",
			cycles: [][]findings.Finding{
				{},
				{errFinding("health:new|bridge:pve1:vmbr0", "pve1", "bridge:pve1:vmbr0")},
			},
			cycleAt:      []int64{holdStart - 100, holdStart + 10},
			wantHealthy:  true,
			wantFindings: []string{"health:new|bridge:pve1:vmbr0"},
		},
		{
			name: "a finding already firing before the hold is not reported",
			cycles: [][]findings.Finding{
				{errFinding("health:old|bridge:pve1:vmbr0", "pve1", "bridge:pve1:vmbr0")},
				{errFinding("health:old|bridge:pve1:vmbr0", "pve1", "bridge:pve1:vmbr0")},
			},
			cycleAt:     []int64{holdStart - 100, holdStart + 10},
			wantHealthy: true,
		},
		{
			name: "a new error finding on a non-canary node is not reported",
			cycles: [][]findings.Finding{
				{},
				{errFinding("health:new|bridge:pve9:vmbr0", "pve9", "bridge:pve9:vmbr0")},
			},
			cycleAt:     []int64{holdStart - 100, holdStart + 10},
			wantHealthy: true,
		},
		{
			name: "a new warning on a canary node is not reported",
			cycles: [][]findings.Finding{
				{},
				{func() findings.Finding {
					f := errFinding("health:warn|bridge:pve1:vmbr0", "pve1", "bridge:pve1:vmbr0")
					f.Severity = findings.SeverityWarning
					return f
				}()},
			},
			cycleAt:     []int64{holdStart - 100, holdStart + 10},
			wantHealthy: true,
		},
		{
			// Attribution through the ref alone, with no node list — the shape
			// several producers actually emit.
			name: "a new error finding attributed only by ref is reported",
			cycles: [][]findings.Finding{
				{},
				{findings.Finding{
					ID: "health:ref|bridge:pve1:vmbr0", Source: findings.SourceHealth,
					Check: "bridge_no_carrier", Severity: findings.SeverityError,
					Refs: []string{"bridge:pve1:vmbr0"},
				}},
			},
			cycleAt:      []int64{holdStart - 100, holdStart + 10},
			wantHealthy:  true,
			wantFindings: []string{"health:ref|bridge:pve1:vmbr0"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			now := int64(0)
			g := testGuard(&now)
			for i, cycle := range tc.cycles {
				now = tc.cycleAt[i]
				g.observe(ctx, cycle)
			}

			verdict, err := g.CheckCanary(ctx, []string{"pve1"}, holdStart)
			if err != nil {
				t.Fatalf("CheckCanary: %v", err)
			}
			if verdict.Healthy != tc.wantHealthy {
				t.Errorf("healthy = %v, want %v (reason %q)", verdict.Healthy, tc.wantHealthy, verdict.Reason)
			}
			if fmt.Sprint(verdict.Findings) != fmt.Sprint(tc.wantFindings) {
				t.Errorf("findings = %v, want %v", verdict.Findings, tc.wantFindings)
			}
			if !verdict.Clean() && verdict.Reason == "" {
				t.Error("an un-clean verdict with no reason: the operator would be told only that something went wrong")
			}
			if wantClean := tc.wantHealthy && len(tc.wantFindings) == 0; verdict.Clean() != wantClean {
				t.Errorf("clean = %v, want %v", verdict.Clean(), wantClean)
			}
		})
	}
}

// TestFindingsGuard_ClearedFindingIsNewAgain proves first-seen bookkeeping is
// pruned with the stream: a finding that cleared and came back is genuinely
// new, not forever-old because it once fired.
func TestFindingsGuard_ClearedFindingIsNewAgain(t *testing.T) {
	ctx := context.Background()
	now := int64(1000)
	g := testGuard(&now)
	f := errFinding("health:flap|bridge:pve1:vmbr0", "pve1", "bridge:pve1:vmbr0")

	g.observe(ctx, []findings.Finding{f}) // first seen at 1000
	now = 1100
	g.observe(ctx, nil) // cleared
	now = 1200
	g.observe(ctx, []findings.Finding{f}) // back: first seen at 1200

	verdict, err := g.CheckCanary(ctx, []string{"pve1"}, 1150)
	if err != nil {
		t.Fatalf("CheckCanary: %v", err)
	}
	if len(verdict.Findings) != 1 {
		t.Errorf("findings = %v, want the re-appeared finding reported as new", verdict.Findings)
	}

	// Control: with the hold starting before the re-appearance it is still
	// new relative to that hold; with it starting after, it is not.
	late, err := g.CheckCanary(ctx, []string{"pve1"}, 1300)
	if err != nil {
		t.Fatalf("CheckCanary: %v", err)
	}
	if late.Healthy {
		t.Error("a hold starting after the last cycle must be un-clean: no cycle ran inside it")
	}
}

// TestToObservedFindings pins the conversion the change engine's attribution
// depends on — a dropped Refs/Nodes field would silently make every finding
// unattributable, which reads exactly like "nothing ever triggers".
func TestToObservedFindings(t *testing.T) {
	in := []findings.Finding{errFinding("health:x|bridge:pve1:vmbr0", "pve1", "bridge:pve1:vmbr0")}
	got := toObservedFindings(in)
	if len(got) != 1 {
		t.Fatalf("converted %d findings, want 1", len(got))
	}
	want := change.ObservedFinding{
		ID: "health:x|bridge:pve1:vmbr0", Check: "bridge_no_carrier", Severity: "error",
		Detail: "no carrier", Nodes: []string{"pve1"}, Refs: []string{"bridge:pve1:vmbr0"},
	}
	if fmt.Sprint(got[0]) != fmt.Sprint(want) {
		t.Errorf("converted = %+v, want %+v", got[0], want)
	}
}

// TestFindingsGuard_ObserveBeforeTheChangeEngineExists proves a cycle that
// beats the change engine's construction is dropped rather than panicking —
// the guard is one of that engine's own Config dependencies, so this ordering
// is real, not hypothetical.
func TestFindingsGuard_ObserveBeforeTheChangeEngineExists(t *testing.T) {
	now := int64(1000)
	g := testGuard(&now)
	g.observe(context.Background(), []findings.Finding{errFinding("health:x|bridge:pve1:vmbr0", "pve1")})
	if g.lastCycleAt != 1000 {
		t.Errorf("lastCycleAt = %d, want the cycle to have been recorded anyway", g.lastCycleAt)
	}
}
