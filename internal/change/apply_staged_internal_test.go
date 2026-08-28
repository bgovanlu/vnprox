// SPDX-License-Identifier: Apache-2.0

package change

// Unit tests for T-2602's pure staging helpers — the arithmetic and set
// operations every acceptance property in apply_canary_test.go rests on.
// They are in-package because the helpers are deliberately unexported: they
// are implementation detail of the staged apply, not API.

import (
	"fmt"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/store"
)

// TestClampHold pins the one line acceptance criterion 5 rests on: a hold
// deadline may never fall after the commit-confirm deadline. This is a
// belt-and-braces guard — validateApplyStrategy already refuses a hold that
// is not strictly shorter than the window — but the whole point of AC5 is
// that a stalled canary cannot outlive the window, so the arithmetic that
// enforces it is asserted directly rather than only through the paths that
// happen to reach it today.
func TestClampHold(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tests := []struct {
		name            string
		hold            time.Duration
		confirmDeadline int64
		want            int64
	}{
		{
			name: "a hold well inside the window keeps its own deadline",
			hold: 30 * time.Second, confirmDeadline: now.Unix() + 120, want: now.Unix() + 30,
		},
		{
			name: "a hold ending exactly at the window keeps its own deadline",
			hold: 120 * time.Second, confirmDeadline: now.Unix() + 120, want: now.Unix() + 120,
		},
		{
			name: "a hold that would outlast the window is cut back to it",
			hold: 300 * time.Second, confirmDeadline: now.Unix() + 120, want: now.Unix() + 120,
		},
		{
			name: "a window that has already closed yields an immediately-expired hold",
			hold: 30 * time.Second, confirmDeadline: now.Unix() - 5, want: now.Unix() - 5,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := clampHold(now, tc.hold, tc.confirmDeadline)
			if got != tc.want {
				t.Errorf("clampHold(%v, %v) = %d, want %d", tc.hold, tc.confirmDeadline, got, tc.want)
			}
			if got > tc.confirmDeadline {
				t.Errorf("clampHold returned %d, which is after the commit-confirm deadline %d — a hold may never keep the cluster open past the window",
					got, tc.confirmDeadline)
			}
		})
	}
}

// threeNodePlan is a plan of the shape BuildPlan produces for one node-file
// op per node, plus a trailing cluster-scope step.
func threeNodePlan() Plan {
	return Plan{Steps: []Step{
		{Kind: StepStageFile, Node: "pve1"},
		{Kind: StepReload, Node: "pve1"},
		{Kind: StepStageFile, Node: "pve2"},
		{Kind: StepReload, Node: "pve2"},
		{Kind: StepStageFile, Node: "pve3"},
		{Kind: StepReload, Node: "pve3"},
		{Kind: StepSDNApply},
	}}
}

func TestCanaryStageIndexes(t *testing.T) {
	tests := []struct {
		name       string
		canary     []string
		wantCanary []int
		wantRest   []int
	}{
		{
			name: "one canary node", canary: []string{"pve1"},
			wantCanary: []int{0, 1}, wantRest: []int{2, 3, 4, 5, 6},
		},
		{
			name: "two canary nodes, in plan order regardless of the order given", canary: []string{"pve3", "pve1"},
			wantCanary: []int{0, 1, 4, 5}, wantRest: []int{2, 3, 6},
		},
		{
			name: "a canary list naming nothing in the plan stages nothing", canary: []string{"pve9"},
			wantCanary: nil, wantRest: []int{0, 1, 2, 3, 4, 5, 6},
		},
		{
			name: "no canary nodes at all leaves every step in the remaining stage", canary: nil,
			wantCanary: nil, wantRest: []int{0, 1, 2, 3, 4, 5, 6},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			canary, rest := canaryStageIndexes(threeNodePlan(), tc.canary)
			if fmt.Sprint(canary) != fmt.Sprint(tc.wantCanary) {
				t.Errorf("canary stage = %v, want %v", canary, tc.wantCanary)
			}
			if fmt.Sprint(rest) != fmt.Sprint(tc.wantRest) {
				t.Errorf("remaining stage = %v, want %v", rest, tc.wantRest)
			}
			// Every step must land in exactly one stage: a step in neither is
			// silently never applied, and a step in both is applied twice.
			seen := map[int]int{}
			for _, i := range append(append([]int{}, canary...), rest...) {
				seen[i]++
			}
			for i := range threeNodePlan().Steps {
				if seen[i] != 1 {
					t.Errorf("step %d appears in %d stage(s), want exactly 1", i, seen[i])
				}
			}
		})
	}
}

// TestCanaryStageIndexes_ClusterScopeStepsRunLast pins the placement rule:
// a step with no node is never part of the canary stage.
func TestCanaryStageIndexes_ClusterScopeStepsRunLast(t *testing.T) {
	canary, rest := canaryStageIndexes(threeNodePlan(), []string{"pve1", "pve2", "pve3"})
	for _, i := range canary {
		if threeNodePlan().Steps[i].Node == "" {
			t.Errorf("cluster-scope step %d was placed in the canary stage", i)
		}
	}
	if fmt.Sprint(rest) != "[6]" {
		t.Errorf("remaining stage = %v, want [6] (the trailing sdn.apply)", rest)
	}
}

func TestPlanRestrictedToNodes(t *testing.T) {
	tests := []struct {
		name  string
		nodes []string
		want  []string
	}{
		{name: "one node", nodes: []string{"pve1"}, want: []string{"pve1"}},
		{name: "two nodes", nodes: []string{"pve1", "pve3"}, want: []string{"pve1", "pve3"}},
		{name: "no nodes", nodes: nil, want: nil},
		{name: "a node the plan does not touch", nodes: []string{"pve9"}, want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := threeNodePlan().restrictedToNodes(tc.nodes)
			if fmt.Sprint(got.affectedNodes()) != fmt.Sprint(tc.want) {
				t.Errorf("restrictedToNodes(%v).affectedNodes() = %v, want %v", tc.nodes, got.affectedNodes(), tc.want)
			}
			// Cluster-scope steps are dropped: a restricted rollback is a
			// per-node restore of the stages that ran, and a plan carrying an
			// sdn.apply step would make doRollbackScopedLocked try to revert
			// SDN config no stage ever touched.
			for _, st := range got.Steps {
				if st.Node == "" {
					t.Errorf("restrictedToNodes kept cluster-scope step %q", st.Kind)
				}
			}
		})
	}
}

func TestPlanStageNodes(t *testing.T) {
	plan := threeNodePlan()
	// Deliberately out of plan order: rollback walks nodes in reverse plan
	// order, so a staged rollback must reverse the same order the
	// all-at-once one does, which means stageNodes has to normalize.
	got := plan.stageNodes([]int{5, 4, 1, 0, 6})
	if fmt.Sprint(got) != "[pve1 pve3]" {
		t.Errorf("stageNodes = %v, want [pve1 pve3] (plan order, cluster-scope step contributing nothing)", got)
	}
	if n := plan.stageNodes(nil); len(n) != 0 {
		t.Errorf("stageNodes(nil) = %v, want empty", n)
	}
	if n := plan.stageNodes(allStepIndexes(plan)); fmt.Sprint(n) != fmt.Sprint(plan.affectedNodes()) {
		t.Errorf("stageNodes(every index) = %v, want the plan's whole node set %v", n, plan.affectedNodes())
	}
}

// TestExecutorSeedLog proves the property the promotion path depends on: a
// canary node's already-committed reload survives into the second stage's
// log, so a failure there restores that node instead of merely discarding a
// staged file it no longer has.
func TestExecutorSeedLog(t *testing.T) {
	plan := threeNodePlan()
	svc := &Service{now: time.Now}
	e := svc.newExecutor(Changeset{}, plan, nil, nil, 0)

	prev := ApplyLog{Steps: make([]StepLog, len(plan.Steps))}
	for i := range prev.Steps {
		prev.Steps[i] = StepLog{Index: i, Kind: plan.Steps[i].Kind, Node: plan.Steps[i].Node, Status: StepPending}
	}
	prev.Steps[0].Status = StepOK
	prev.Steps[1].Status = StepOK
	prev.Rollback = append(prev.Rollback, RollbackLog{Node: "pve1", Status: StepOK})
	prev.NodeTimers = append(prev.NodeTimers, NodeTimerLog{Node: "pve1", Status: NodeTimerStatusArmed, Deadline: 42})

	e.seedLog(prev)

	if e.log.Steps[0].Status != StepOK || e.log.Steps[1].Status != StepOK {
		t.Errorf("seeded steps 0/1 = %q/%q, want both %q", e.log.Steps[0].Status, e.log.Steps[1].Status, StepOK)
	}
	if e.log.Steps[2].Status != StepPending {
		t.Errorf("step 2 status = %q, want %q — a step the previous stage never ran must stay pending", e.log.Steps[2].Status, StepPending)
	}
	if reloadIdx, ok := e.loadIx["pve1"]; !ok || e.log.Steps[reloadIdx].Status != StepOK {
		t.Error("the canary node's reload must read as committed after seeding, or a later failure would only discard its staged file")
	}
	if len(e.log.Rollback) != 1 || len(e.log.NodeTimers) != 1 {
		t.Errorf("seeded rollback/timer entries = %d/%d, want 1/1", len(e.log.Rollback), len(e.log.NodeTimers))
	}
}

// TestPlanRequiresPVESession pins which step kinds make `gate: auto`
// unavailable — the unattended promotion has no user session, and the
// apply-time revert ticket exists only to undo a changeset, never to carry
// one forward.
func TestPlanRequiresPVESession(t *testing.T) {
	tests := []struct {
		name string
		plan Plan
		want bool
	}{
		{name: "node-file only", plan: Plan{Steps: []Step{{Kind: StepStageFile, Node: "pve1"}, {Kind: StepReload, Node: "pve1"}}}, want: false},
		{name: "qos and wireguard need no ticket", plan: Plan{Steps: []Step{{Kind: StepQosApply, Node: "pve1"}, {Kind: StepWgApply, Node: "pve1"}}}, want: false},
		{name: "sdn.apply", plan: Plan{Steps: []Step{{Kind: StepSDNApply}}}, want: true},
		{name: "sdn stage", plan: Plan{Steps: []Step{{Kind: StepSDNStage}}}, want: true},
		{name: "firewall apply", plan: Plan{Steps: []Step{{Kind: StepFwApply, Node: "pve1"}}}, want: true},
		{name: "firewall verify", plan: Plan{Steps: []Step{{Kind: StepFwVerify, Node: "pve1"}}}, want: true},
		{name: "ipam", plan: Plan{Steps: []Step{{Kind: StepIpamAlloc}}}, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := planRequiresPVESession(tc.plan); got != tc.want {
				t.Errorf("planRequiresPVESession = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestValidateApplyStrategy_RefusesUnstageablePlans covers the plan shapes a
// canary split cannot honour: cluster-scope steps the plan builder places
// BEFORE the per-node steps. Running them in the canary stage would already
// have changed cluster-wide state before the "only the canary nodes"
// promise; running them after would invert an ordering BuildPlan chose
// deliberately. Refusing is the only answer that is not quietly one of those
// two wrong ones.
func TestValidateApplyStrategy_RefusesUnstageablePlans(t *testing.T) {
	nodeSteps := []Step{
		{Kind: StepStageFile, Node: "pve1"}, {Kind: StepReload, Node: "pve1"},
		{Kind: StepStageFile, Node: "pve2"}, {Kind: StepReload, Node: "pve2"},
	}
	tests := []struct {
		name    string
		extra   Step
		wantErr bool
	}{
		{name: "switch push", extra: Step{Kind: StepSwitchApply, Target: "switch:sw1:Gi1/0/1"}, wantErr: true},
		{name: "sdn stage", extra: Step{Kind: StepSDNStage}, wantErr: true},
		{name: "ipam allocation", extra: Step{Kind: StepIpamAlloc}, wantErr: true},
		{name: "trailing sdn.apply is fine", extra: Step{Kind: StepSDNApply}, wantErr: false},
		{name: "a node-scoped firewall step is fine", extra: Step{Kind: StepFwApply, Node: "pve1", Target: "node:pve1:node"}, wantErr: false},
	}
	// validateApplyStrategy only asks whether a stage store EXISTS (a pause
	// with nowhere to be recorded is refused), never touches it, so a repo
	// over a nil DB is enough and keeps this a pure unit test.
	svc := &Service{stages: store.NewChangesetStageRepo(nil)}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := Plan{Steps: append([]Step{tc.extra}, nodeSteps...)}
			_, err := svc.validateApplyStrategy(
				ApplyStrategy{Mode: ApplyModeCanary, CanaryNodes: []string{"pve1"}, HoldForSec: 30},
				plan, 120*time.Second)
			if tc.wantErr && err == nil {
				t.Fatalf("validateApplyStrategy accepted a plan carrying a %s step; it must be refused", tc.extra.Kind)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateApplyStrategy rejected a stageable plan: %v", err)
			}
		})
	}
}
