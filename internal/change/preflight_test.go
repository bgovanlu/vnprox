// SPDX-License-Identifier: Apache-2.0

package change_test

// T-1604 AC6: the failure-impact pre-flight is an additive veto on unattended
// applies, audited distinctly from — and never an override of — T-1103's
// existing touchesMgmtPath exclusion.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

// fakePreflighter is a stand-in for the failsim-backed ImpactPreflighter. The
// failsim side (that a SPOF bond really yields quorum/mgmt-path loss) is
// proven in internal/failsim; this fake isolates the scheduler's *reaction* to
// a veto so the additive/override behavior is tested without dragging the
// whole simulator into the change harness.
type fakePreflighter struct {
	reason  string
	gotRefs []inventory.Ref
	calls   int
	veto    bool
}

func (f *fakePreflighter) PreflightImpact(_ context.Context, refs []inventory.Ref) (bool, string, map[string]any, error) {
	f.calls++
	f.gotRefs = refs
	return f.veto, f.reason, nil, nil
}

func hasAuditResult(entries []store.AuditEntry, action, result string) bool {
	for _, e := range entries {
		if e.Action == action && e.Result == result {
			return true
		}
	}
	return false
}

// TestSchedule_FailureImpactVeto_Aborts: a scheduled changeset whose touched
// bond the impact model rates unsafe (mgmt-path loss) is aborted at
// windowStart, blocked and audited distinctly from the mgmt-path exclusion,
// with no step executed.
func TestSchedule_FailureImpactVeto_Aborts(t *testing.T) {
	pf := &fakePreflighter{veto: true, reason: "mgmt_path_loss"}
	h, _, clk := newScheduleHarness(t, fixtureSingleNode, func(cfg *change.Config) {
		cfg.ImpactPreflight = pf
	})
	ctx := context.Background()
	before := h.agent.committedFile("pve1")

	target := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr9"}
	cs := h.mustCreate(t, "alice@pam", "add vmbr9", []change.Op{{
		Type: change.OpBridgeCreate, Target: target, Params: &change.BridgeCreateParams{},
	}})
	mustSchedule(t, h, cs.ID, change.ScheduleParams{
		WindowStart: scheduleEpoch + 10, WindowEnd: scheduleEpoch + 70,
	})

	clk.t = time.Unix(scheduleEpoch+10, 0)
	h.svc.TickSchedules(ctx)

	if pf.calls == 0 {
		t.Fatal("impact pre-flight was never consulted at windowStart")
	}
	if len(pf.gotRefs) != 1 || pf.gotRefs[0] != target {
		t.Errorf("pre-flight touched refs = %v, want [%s]", pf.gotRefs, target)
	}
	if got := h.agent.committedFile("pve1"); got != before {
		t.Fatalf("a step executed despite the impact veto:\n--- want ---\n%s\n--- got ---\n%s", before, got)
	}

	row, err := h.svc.GetSchedule(ctx, cs.ID)
	if err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}
	if row.Status != store.ScheduleStatusBlocked {
		t.Errorf("schedule status = %s, want blocked", row.Status)
	}
	entries, err := h.auditRepo.List(ctx, cs.ID, 0)
	if err != nil {
		t.Fatalf("audit List: %v", err)
	}
	// Distinct from the mgmt-path exclusion's "mgmt_path_unattended_forbidden".
	if !hasAuditResult(entries, "changeset.schedule_fire_blocked", "failure_impact_mgmt_path_loss") {
		t.Error("audit trail missing the distinct failure_impact_mgmt_path_loss block reason")
	}
	if hasAuditResult(entries, "changeset.schedule_fire_blocked", "mgmt_path_unattended_forbidden") {
		t.Error("impact veto was mis-audited as a mgmt-path exclusion")
	}
}

// TestSchedule_MgmtExclusionWinsOverCleanImpact: a changeset that touches a
// management path at fire time is blocked by the unconditional mgmt-path
// exclusion even when the impact model returns a clean verdict — proving the
// impact hook is additive, never an override that could bypass an existing
// gate. The mgmt exclusion runs first, so the (clean) pre-flight is not even
// consulted.
func TestSchedule_MgmtExclusionWinsOverCleanImpact(t *testing.T) {
	pf := &fakePreflighter{veto: false} // impact says "safe"
	protectedPath := filepath.Join(t.TempDir(), "protected.json")
	h, _, clk := newScheduleHarness(t, fixtureSingleNode, func(cfg *change.Config) {
		cfg.ImpactPreflight = pf
		cfg.ProtectedPath = protectedPath
	})
	ctx := context.Background()
	before := h.agent.committedFile("pve1")

	vmbr9 := inventory.Ref{Kind: inventory.KindBridge, Node: "pve1", ID: "vmbr9"}
	cs := h.mustCreate(t, "alice@pam", "touch mgmt bridge", []change.Op{{
		Type:   change.OpBridgeCreate,
		Target: vmbr9,
		Params: &change.BridgeCreateParams{},
	}})
	// Scheduled while nothing is marked protected, so Schedule() succeeds.
	mustSchedule(t, h, cs.ID, change.ScheduleParams{
		WindowStart: scheduleEpoch + 10, WindowEnd: scheduleEpoch + 70,
	})

	// Between scheduling and the window, vmbr9 becomes a protected mgmt path.
	if err := change.SaveProtectedConfig(protectedPath, change.ProtectedConfig{
		Nodes: map[string][]string{"pve1": {vmbr9.String()}},
	}); err != nil {
		t.Fatalf("SaveProtectedConfig: %v", err)
	}

	clk.t = time.Unix(scheduleEpoch+10, 0)
	h.svc.TickSchedules(ctx)

	if got := h.agent.committedFile("pve1"); got != before {
		t.Fatalf("a step executed despite the mgmt-path exclusion:\n--- want ---\n%s\n--- got ---\n%s", before, got)
	}
	entries, err := h.auditRepo.List(ctx, cs.ID, 0)
	if err != nil {
		t.Fatalf("audit List: %v", err)
	}
	if !hasAuditResult(entries, "changeset.schedule_fire_blocked", "mgmt_path_unattended_forbidden") {
		t.Error("mgmt-path exclusion did not win: expected mgmt_path_unattended_forbidden block")
	}
	if pf.calls != 0 {
		t.Errorf("clean impact pre-flight was consulted (%d calls) despite the mgmt exclusion running first — additive-not-override violated", pf.calls)
	}
}
