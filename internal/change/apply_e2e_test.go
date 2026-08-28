// SPDX-License-Identifier: Apache-2.0

package change_test

import (
	"context"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
)

// Acceptance criterion 1: end-to-end against pvemock single-node — draft →
// validate → diff → apply → status stream (applying→awaiting_confirm) →
// confirm → committed, with the fixture file verifiably changed.
func TestApply_EndToEnd_SingleNode(t *testing.T) {
	h := newHarness(t, fixtureSingleNode)
	ctx := context.Background()

	cs := h.mustCreate(t, "root@pam", "add vmbr1", []change.Op{
		bridgeCreateOp("pve1", "vmbr1", nil),
	})

	// validate
	validated, err := h.svc.Validate(ctx, cs.ID, "root@pam")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if validated.Status != change.StatusValidated {
		t.Fatalf("status after validate = %s, want validated (findings: %+v)", validated.Status, validated.Findings)
	}

	// diff shows the bridge being added
	diff, err := h.svc.Diff(ctx, cs.ID)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(diff.Files) != 1 || !diff.Files[0].Changed || !strings.Contains(diff.Files[0].Unified, "vmbr1") {
		t.Fatalf("diff did not show vmbr1 addition: %+v", diff.Files)
	}

	if strings.Contains(h.agent.committedFile("pve1"), "vmbr1") {
		t.Fatal("precondition: vmbr1 already present before apply")
	}

	// apply
	applied, err := h.svc.Apply(ctx, cs.ID, "root@pam", nil, 0)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applied.Status != change.StatusAwaitingConfirm {
		t.Fatalf("status after apply = %s, want awaiting_confirm", applied.Status)
	}
	if applied.ConfirmDeadline == nil {
		t.Fatal("confirm deadline not set after apply")
	}

	// plan: stage + reload, both logged ok
	plan := h.plan(t, cs.ID)
	if len(plan.Steps) != 2 || plan.Steps[0].Kind != change.StepStageFile || plan.Steps[1].Kind != change.StepReload {
		t.Fatalf("unexpected plan: %+v", plan.Steps)
	}
	log := h.applyLog(t, cs.ID)
	for _, s := range log.Steps {
		if s.Status != change.StepOK {
			t.Fatalf("step %d status = %s, want ok", s.Index, s.Status)
		}
	}
	if log.FailedStep != nil {
		t.Fatalf("unexpected failed step: %v", *log.FailedStep)
	}

	// file changed after a successful reload
	if !strings.Contains(h.agent.committedFile("pve1"), "vmbr1") {
		t.Fatal("vmbr1 not present in committed file after apply")
	}

	// confirm
	committed, err := h.svc.Confirm(ctx, cs.ID, "root@pam")
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if committed.Status != change.StatusCommitted {
		t.Fatalf("status after confirm = %s, want committed", committed.Status)
	}
	if committed.ConfirmDeadline != nil {
		t.Fatal("confirm deadline not cleared after confirm")
	}

	// WS status stream
	got := h.ws.statuses(cs.ID)
	want := []string{"draft", "validated", "applying", "awaiting_confirm", "committed"}
	if !equalStrings(got, want) {
		t.Fatalf("status stream = %v, want %v", got, want)
	}

	// a post snapshot exists (pre + post)
	snaps, err := h.snapRepo.List(ctx, cs.ID)
	if err != nil {
		t.Fatalf("snapshots.List: %v", err)
	}
	if !hasKind(snaps, "pre") || !hasKind(snaps, "post") {
		t.Fatalf("want pre and post snapshots, got %d rows", len(snaps))
	}

	// timer was cancelled on confirm
	if h.timers.armedCount() != 0 {
		t.Fatalf("timer still armed after confirm: %d", h.timers.armedCount())
	}

	// RefreshNow fired after the terminal state
	if h.refresher.count() == 0 {
		t.Fatal("RefreshNow was not called after commit")
	}
}

// Acceptance criterion 2 (no-confirm path, deterministic): the deadline
// elapses with no confirmation → state restored byte-identically to the
// pre-snapshot, status rolled_back, attributed to system:rollback.
func TestApply_NoConfirm_AutoRollback(t *testing.T) {
	h := newHarness(t, fixtureSingleNode)
	ctx := context.Background()

	before := mustRead(t, h, "pve1")

	cs := h.mustCreate(t, "alice@pve", "add vmbr7", []change.Op{
		bridgeCreateOp("pve1", "vmbr7", nil),
	})
	if _, err := h.svc.Apply(ctx, cs.ID, "alice@pve", nil, 0); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(h.agent.committedFile("pve1"), "vmbr7") {
		t.Fatal("vmbr7 not applied")
	}

	// deadline elapses
	h.timers.fireLatest(t)

	rolled := h.get(t, cs.ID)
	if rolled.Status != change.StatusRolledBack {
		t.Fatalf("status after deadline = %s, want rolled_back", rolled.Status)
	}
	after := h.agent.committedFile("pve1")
	if after != before {
		t.Fatalf("file not restored byte-identically:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
	log := h.applyLog(t, cs.ID)
	if log.RolledBackBy != "system:rollback" {
		t.Fatalf("rolledBackBy = %q, want system:rollback", log.RolledBackBy)
	}
	if len(log.Rollback) == 0 {
		t.Fatal("rollback trail empty")
	}

	// lock released — a new apply is now possible
	cs2 := h.mustCreate(t, "alice@pve", "second", []change.Op{bridgeCreateOp("pve1", "vmbr8", nil)})
	if _, err := h.svc.Apply(ctx, cs2.ID, "alice@pve", nil, 0); err != nil {
		t.Fatalf("second Apply after rollback should succeed, got: %v", err)
	}

	// audit trail contains the automatic rollback attributed to system:rollback
	entries, err := h.auditRepo.List(ctx, cs.ID, 0)
	if err != nil {
		t.Fatalf("audit List: %v", err)
	}
	if !hasAudit(entries, "changeset.rollback", "system:rollback") {
		t.Fatal("audit trail missing system:rollback rollback entry")
	}
}

func mustRead(t *testing.T, h *applyHarness, node string) string {
	t.Helper()
	content, err := h.agent.ReadInterfaces(context.Background(), node)
	if err != nil {
		t.Fatalf("ReadInterfaces: %v", err)
	}
	return content
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
