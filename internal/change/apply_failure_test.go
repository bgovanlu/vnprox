package change_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
)

// Acceptance criterion 3: an injected step failure at EACH position of a
// 5-step plan rolls back every completed step, leaves the changeset failed,
// pinpoints the failed step in the apply log, and converges every affected
// node's file back to its pre-apply content (property: post-rollback state ==
// pre-apply state, for every position).
//
// The 5-step plan (three-node fixture, two nodes touched + sdn.apply):
//
//	0 stage_file pve1   3 reload    pve2
//	1 reload     pve1   4 sdn_apply
//	2 stage_file pve2
//
// Failure mechanisms per step, using pvemock's documented flags where the
// mock supports them (see the T-205 report's residual-risk note):
//
//	reload steps (1,3): pvemock per-node NetworkReloadFail (native flag)
//	stage steps (0,2):  host-writer seam fault (pvemock has no host-write flag)
//	sdn.apply step (4): PVE-gateway seam fault (typed client can't pass the
//	                    per-request mock_fail query without corrupting reloads)
func TestApply_StepFailure_AtEachPosition(t *testing.T) {
	const steps = 5
	for pos := 0; pos < steps; pos++ {
		pos := pos
		t.Run(fmt.Sprintf("fail_step_%d", pos), func(t *testing.T) {
			h := newHarness(t, fixtureThreeNode)
			ctx := context.Background()

			// Seed and capture pre-apply state for both touched nodes.
			pre1 := mustRead(t, h, "pve1")
			pre2 := mustRead(t, h, "pve2")

			ops := []change.Op{
				bridgeCreateOp("pve1", "vmbrX", nil),
				bridgeCreateOp("pve2", "vmbrY", nil),
				sdnApplyOp(),
			}
			cs := h.mustCreate(t, "root@pam", "5-step", ops)

			// Confirm the plan is exactly the 5 steps we reason about.
			plan := mustPlan(t, ops)
			if len(plan.Steps) != steps {
				t.Fatalf("plan has %d steps, want %d: %+v", len(plan.Steps), steps, plan.Steps)
			}

			pveGW := &fakePVEGateway{client: h.client, pollNode: "pve1"}
			injectFailure(t, h, pveGW, pos)

			_, err := h.svc.Apply(ctx, cs.ID, "root@pam", pveGW, 0)
			if err == nil {
				t.Fatalf("apply should have failed at step %d", pos)
			}

			got := h.get(t, cs.ID)
			if got.Status != change.StatusFailed {
				t.Fatalf("status = %s, want failed", got.Status)
			}

			// Apply log pinpoints the failed step and classifies the rest.
			log := h.applyLog(t, cs.ID)
			if log.FailedStep == nil || *log.FailedStep != pos {
				t.Fatalf("failedStep = %v, want %d", log.FailedStep, pos)
			}
			for i, s := range log.Steps {
				switch {
				case i < pos && s.Status != change.StepOK:
					t.Fatalf("step %d = %s, want ok (before failure)", i, s.Status)
				case i == pos && s.Status != change.StepFailed:
					t.Fatalf("step %d = %s, want failed", i, s.Status)
				case i > pos && s.Status != change.StepSkipped:
					t.Fatalf("step %d = %s, want skipped (after failure)", i, s.Status)
				}
			}

			// Property: post-rollback file state == pre-apply file state.
			if h.agent.committedFile("pve1") != pre1 {
				t.Fatalf("pve1 not converged to pre-apply state after failure at step %d", pos)
			}
			if h.agent.committedFile("pve2") != pre2 {
				t.Fatalf("pve2 not converged to pre-apply state after failure at step %d", pos)
			}

			// Lock released after the terminal (failed) state.
			cs2 := h.mustCreate(t, "root@pam", "after", []change.Op{bridgeCreateOp("pve1", "vmbrZ", nil)})
			h.agent.setFailStage("pve1", false)
			h.setReloadFail(t, "pve1", false)
			if _, err := h.svc.Apply(ctx, cs2.ID, "root@pam", nil, 0); err != nil {
				t.Fatalf("apply after failed changeset should succeed (lock released): %v", err)
			}
		})
	}
}

// injectFailure arms the failure mechanism for the plan step at pos.
func injectFailure(t *testing.T, h *applyHarness, gw *fakePVEGateway, pos int) {
	t.Helper()
	switch pos {
	case 0:
		h.agent.setFailStage("pve1", true)
	case 1:
		h.setReloadFail(t, "pve1", true)
	case 2:
		h.agent.setFailStage("pve2", true)
	case 3:
		h.setReloadFail(t, "pve2", true)
	case 4:
		gw.fail = true
	default:
		t.Fatalf("no failure mechanism for step %d", pos)
	}
}

func mustPlan(t *testing.T, ops []change.Op) change.Plan {
	t.Helper()
	p, err := change.BuildPlan(ops)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	return p
}
