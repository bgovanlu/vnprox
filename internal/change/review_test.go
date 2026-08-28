// SPDX-License-Identifier: Apache-2.0

package change_test

// review_test.go covers T-2003's review surface: per-op/changeset comments
// (survival across validate/diff, orphan cleanup on op removal) and the
// review-approval gate. AC2 is the criterion that matters: these tests call
// change.Service.Apply DIRECTLY — the same method both the HTTP handler
// (internal/api) and vnproxctl (over HTTP) ultimately call, with nothing in
// between but a plain Go function call — proving refusal is decided
// server-side from stored state, not from any client-supplied assertion the
// UI could have made up. internal/api/changesets_test.go additionally proves
// the HTTP layer surfaces the same refusal with the UI fully bypassed
// (a raw net/http request, no browser/JS involved).

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/store"
)

// newReviewHarness is newHarness plus Comments/Approvals/Approval wired from
// the SAME underlying db/agent/timers/etc newHarness already built — a
// second change.Service instance sharing every fake dependency, since
// newHarness's own opts hook runs before its db exists and so can't reach it.
func newReviewHarness(t *testing.T, fixturePath string, approval change.ApprovalConfig) *applyHarness {
	t.Helper()
	h := newHarness(t, fixturePath)
	comments := store.NewChangesetCommentRepo(h.db)
	approvals := store.NewChangesetApprovalRepo(h.db)
	protectedPath := filepath.Join(t.TempDir(), "protected.json")
	svc := newService(t, change.Config{
		Changesets: h.csRepo, Audit: h.auditRepo, WS: h.ws,
		Nodes: h.agent, Snapshots: h.snapRepo, Blobs: h.blobRepo, Refresher: h.refresher,
		TimerFunc: h.timers.New, ProtectedPath: protectedPath,
		Comments: comments, Approvals: approvals, Approval: approval,
	})
	h.svc = svc
	return h
}

func auditActions(t *testing.T, h *applyHarness, changesetID string) []string {
	t.Helper()
	entries, err := h.auditRepo.List(context.Background(), changesetID, 100)
	if err != nil {
		t.Fatalf("auditRepo.List: %v", err)
	}
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Action
	}
	return out
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// --- AC2: server-side apply refusal ---------------------------------------

// TestApply_ApprovalRequired_RefusesUnapproved is AC2's core proof: with
// [changesets] approval_required policy on, Apply refuses an unapproved
// changeset with *change.ErrApprovalRequired, BEFORE any node is ever
// touched (h.agent.stageCalls stays 0 — no snapshot, no stage, no reload —
// proving this is a pre-mutation authorization gate, not a late abort), and
// the changeset's status is left completely unchanged.
func TestApply_ApprovalRequired_RefusesUnapproved(t *testing.T) {
	h := newReviewHarness(t, fixtureSingleNode, change.ApprovalConfig{Required: true, AllowSelfApproval: true})
	ctx := context.Background()

	cs := h.mustCreate(t, "alice", "add vmbr1", []change.Op{bridgeCreateOp("pve1", "vmbr1", nil)})

	_, err := h.svc.Apply(ctx, cs.ID, "alice", nil, 0)
	var approvalErr *change.ErrApprovalRequired
	if !errors.As(err, &approvalErr) {
		t.Fatalf("Apply error = %v, want *change.ErrApprovalRequired", err)
	}
	if approvalErr.ID != cs.ID {
		t.Errorf("ErrApprovalRequired.ID = %q, want %q", approvalErr.ID, cs.ID)
	}

	if h.agent.stageCalls != 0 {
		t.Errorf("stageCalls = %d, want 0 — apply must refuse before any node mutation is attempted", h.agent.stageCalls)
	}

	got := h.get(t, cs.ID)
	if got.Status != change.StatusDraft {
		t.Errorf("status after refused apply = %s, want draft (unchanged)", got.Status)
	}
	if got.ConfirmDeadline != nil {
		t.Error("confirm deadline set after a refused apply")
	}

	if !containsString(auditActions(t, h, cs.ID), "changeset.apply") {
		t.Error("refused apply attempt was not audited")
	}
}

// TestApply_ApprovalRequired_NotRequired_Unaffected proves the policy is a
// complete no-op when Required is false (the default, every pre-T-2003
// deployment): apply succeeds with no approval ever recorded.
func TestApply_ApprovalRequired_NotRequired_Unaffected(t *testing.T) {
	h := newReviewHarness(t, fixtureSingleNode, change.ApprovalConfig{})
	ctx := context.Background()

	cs := h.mustCreate(t, "alice", "add vmbr1", []change.Op{bridgeCreateOp("pve1", "vmbr1", nil)})
	applied, err := h.svc.Apply(ctx, cs.ID, "alice", nil, 0)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applied.Status != change.StatusAwaitingConfirm {
		t.Errorf("status = %s, want awaiting_confirm", applied.Status)
	}
}

// TestApply_ApprovalRequired_AllowsAfterApprove proves the OTHER half of
// AC2: a changeset that HAS a stored "approved" decision applies normally —
// the gate is a real gate, not a permanent block.
func TestApply_ApprovalRequired_AllowsAfterApprove(t *testing.T) {
	h := newReviewHarness(t, fixtureSingleNode, change.ApprovalConfig{Required: true, AllowSelfApproval: true})
	ctx := context.Background()

	cs := h.mustCreate(t, "alice", "add vmbr1", []change.Op{bridgeCreateOp("pve1", "vmbr1", nil)})

	if _, err := h.svc.ReviewApprove(ctx, cs.ID, "bob"); err != nil {
		t.Fatalf("ReviewApprove: %v", err)
	}
	approval, err := h.svc.GetApproval(ctx, cs.ID)
	if err != nil {
		t.Fatalf("GetApproval: %v", err)
	}
	if approval.Status != change.ApprovalApproved || approval.DecidedBy != "bob" {
		t.Fatalf("GetApproval = %+v, want approved by bob", approval)
	}

	applied, err := h.svc.Apply(ctx, cs.ID, "alice", nil, 0)
	if err != nil {
		t.Fatalf("Apply after approval: %v", err)
	}
	if applied.Status != change.StatusAwaitingConfirm {
		t.Errorf("status = %s, want awaiting_confirm", applied.Status)
	}
	if !containsString(auditActions(t, h, cs.ID), "changeset.review_approve") {
		t.Error("changeset.review_approve was not audited")
	}
}

// TestApply_ApprovalRequired_RejectedStillRefuses proves a rejection does
// NOT satisfy the gate — only an "approved" decision does.
func TestApply_ApprovalRequired_RejectedStillRefuses(t *testing.T) {
	h := newReviewHarness(t, fixtureSingleNode, change.ApprovalConfig{Required: true, AllowSelfApproval: true})
	ctx := context.Background()

	cs := h.mustCreate(t, "alice", "add vmbr1", []change.Op{bridgeCreateOp("pve1", "vmbr1", nil)})
	if _, err := h.svc.ReviewReject(ctx, cs.ID, "bob", "not yet"); err != nil {
		t.Fatalf("ReviewReject: %v", err)
	}

	_, err := h.svc.Apply(ctx, cs.ID, "alice", nil, 0)
	var approvalErr *change.ErrApprovalRequired
	if !errors.As(err, &approvalErr) {
		t.Fatalf("Apply error = %v, want *change.ErrApprovalRequired (rejected is not approved)", err)
	}
	if !containsString(auditActions(t, h, cs.ID), "changeset.review_reject") {
		t.Error("changeset.review_reject was not audited")
	}
}

// TestApply_ApprovalRequired_EditClearsApproval proves an edit invalidates a
// prior approval — otherwise an approved draft's ops could be swapped for
// something else and applied without a fresh review, defeating the whole
// point of AC2.
func TestApply_ApprovalRequired_EditClearsApproval(t *testing.T) {
	h := newReviewHarness(t, fixtureSingleNode, change.ApprovalConfig{Required: true, AllowSelfApproval: true})
	ctx := context.Background()

	cs := h.mustCreate(t, "alice", "add vmbr1", []change.Op{bridgeCreateOp("pve1", "vmbr1", nil)})
	if _, err := h.svc.ReviewApprove(ctx, cs.ID, "bob"); err != nil {
		t.Fatalf("ReviewApprove: %v", err)
	}

	if _, err := h.svc.UpdateDraft(ctx, cs.ID, "alice", nil, []change.Op{bridgeCreateOp("pve1", "vmbr2", nil)}); err != nil {
		t.Fatalf("UpdateDraft: %v", err)
	}

	approval, err := h.svc.GetApproval(ctx, cs.ID)
	if err != nil {
		t.Fatalf("GetApproval: %v", err)
	}
	if approval.Status != change.ApprovalNone {
		t.Errorf("approval after edit = %+v, want none (cleared)", approval)
	}

	_, err = h.svc.Apply(ctx, cs.ID, "alice", nil, 0)
	var approvalErr *change.ErrApprovalRequired
	if !errors.As(err, &approvalErr) {
		t.Fatalf("Apply error = %v, want *change.ErrApprovalRequired after the edit cleared approval", err)
	}
}

// --- AC3: self-approval permitted/refused per configuration ---------------

func TestReviewApprove_SelfApprovalForbiddenByPolicy(t *testing.T) {
	h := newReviewHarness(t, fixtureSingleNode, change.ApprovalConfig{Required: true, AllowSelfApproval: false})
	ctx := context.Background()

	cs := h.mustCreate(t, "alice", "add vmbr1", []change.Op{bridgeCreateOp("pve1", "vmbr1", nil)})

	_, err := h.svc.ReviewApprove(ctx, cs.ID, "alice") // the changeset's own author
	var selfErr *change.ErrSelfApprovalForbidden
	if !errors.As(err, &selfErr) {
		t.Fatalf("ReviewApprove(self) error = %v, want *change.ErrSelfApprovalForbidden", err)
	}

	// A different identity may still approve it.
	if _, approveErr := h.svc.ReviewApprove(ctx, cs.ID, "bob"); approveErr != nil {
		t.Fatalf("ReviewApprove(bob): %v", approveErr)
	}
	approval, err := h.svc.GetApproval(ctx, cs.ID)
	if err != nil {
		t.Fatalf("GetApproval: %v", err)
	}
	if approval.Status != change.ApprovalApproved {
		t.Errorf("approval = %+v, want approved", approval)
	}
}

func TestReviewApprove_SelfApprovalPermittedByPolicy(t *testing.T) {
	h := newReviewHarness(t, fixtureSingleNode, change.ApprovalConfig{Required: true, AllowSelfApproval: true})
	ctx := context.Background()

	cs := h.mustCreate(t, "alice", "add vmbr1", []change.Op{bridgeCreateOp("pve1", "vmbr1", nil)})
	if _, err := h.svc.ReviewApprove(ctx, cs.ID, "alice"); err != nil {
		t.Fatalf("ReviewApprove(self, permitted by policy): %v", err)
	}
}

func TestReviewApprove_NotAnApprover(t *testing.T) {
	h := newReviewHarness(t, fixtureSingleNode, change.ApprovalConfig{Required: true, AllowSelfApproval: true, Approvers: []string{"carol"}})
	ctx := context.Background()

	cs := h.mustCreate(t, "alice", "add vmbr1", []change.Op{bridgeCreateOp("pve1", "vmbr1", nil)})

	_, err := h.svc.ReviewApprove(ctx, cs.ID, "bob")
	var notApprover *change.ErrNotAnApprover
	if !errors.As(err, &notApprover) {
		t.Fatalf("ReviewApprove(bob, not on approvers list) error = %v, want *change.ErrNotAnApprover", err)
	}

	if _, err := h.svc.ReviewApprove(ctx, cs.ID, "carol"); err != nil {
		t.Fatalf("ReviewApprove(carol, on approvers list): %v", err)
	}
}

// --- AC1: comments persist across validate/diff; op removal doesn't
// silently orphan a comment ------------------------------------------------

func TestAddComment_PersistsAcrossValidateAndDiff(t *testing.T) {
	h := newReviewHarness(t, fixtureSingleNode, change.ApprovalConfig{})
	ctx := context.Background()

	cs := h.mustCreate(t, "alice", "add vmbr1", []change.Op{bridgeCreateOp("pve1", "vmbr1", nil)})
	opID := cs.Ops[0].ID
	if opID == "" {
		t.Fatal("op has no id assigned at create time")
	}

	comment, err := h.svc.AddComment(ctx, cs.ID, "bob", opID, "double-check the MTU here")
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	if comment.Author != "bob" || comment.OpID != opID {
		t.Errorf("comment = %+v, want author bob attached to op %s", comment, opID)
	}

	if _, validateErr := h.svc.Validate(ctx, cs.ID, "alice"); validateErr != nil {
		t.Fatalf("Validate: %v", validateErr)
	}
	if _, diffErr := h.svc.Diff(ctx, cs.ID); diffErr != nil {
		t.Fatalf("Diff: %v", diffErr)
	}

	comments, err := h.svc.ListComments(ctx, cs.ID)
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if len(comments) != 1 || comments[0].ID != comment.ID {
		t.Fatalf("comments after validate+diff = %+v, want the one comment still attached", comments)
	}

	if !containsString(auditActions(t, h, cs.ID), "changeset.comment_add") {
		t.Error("changeset.comment_add was not audited")
	}
}

func TestAddComment_ChangesetLevel(t *testing.T) {
	h := newReviewHarness(t, fixtureSingleNode, change.ApprovalConfig{})
	ctx := context.Background()
	cs := h.mustCreate(t, "alice", "add vmbr1", []change.Op{bridgeCreateOp("pve1", "vmbr1", nil)})

	comment, err := h.svc.AddComment(ctx, cs.ID, "bob", "", "looks good overall")
	if err != nil {
		t.Fatalf("AddComment (changeset-level): %v", err)
	}
	if comment.OpID != "" {
		t.Errorf("comment.OpID = %q, want empty (changeset-level)", comment.OpID)
	}
}

func TestAddComment_UnknownOp(t *testing.T) {
	h := newReviewHarness(t, fixtureSingleNode, change.ApprovalConfig{})
	ctx := context.Background()
	cs := h.mustCreate(t, "alice", "add vmbr1", []change.Op{bridgeCreateOp("pve1", "vmbr1", nil)})

	_, err := h.svc.AddComment(ctx, cs.ID, "bob", "no-such-op-id", "x")
	var opNotFound *change.ErrCommentOpNotFound
	if !errors.As(err, &opNotFound) {
		t.Fatalf("AddComment(unknown op) error = %v, want *change.ErrCommentOpNotFound", err)
	}
}

// TestUpdateDraft_RemovingOp_CleansUpCommentAndAudits is T-2003's "deleting
// an op does not orphan its comment silently" acceptance criterion: removing
// op1 (which carries a comment) from the ops array explicitly deletes that
// comment — the comment is gone, and the deletion is NOT silent (an
// changeset.comment_orphan_cleanup audit row is written naming which op ids
// were cleaned up).
func TestUpdateDraft_RemovingOp_CleansUpCommentAndAudits(t *testing.T) {
	h := newReviewHarness(t, fixtureSingleNode, change.ApprovalConfig{})
	ctx := context.Background()

	cs := h.mustCreate(t, "alice", "two bridges", []change.Op{
		bridgeCreateOp("pve1", "vmbr1", nil),
		bridgeCreateOp("pve1", "vmbr2", nil),
	})
	op1ID, op2ID := cs.Ops[0].ID, cs.Ops[1].ID

	if _, err := h.svc.AddComment(ctx, cs.ID, "bob", op1ID, "worried about vmbr1"); err != nil {
		t.Fatalf("AddComment on op1: %v", err)
	}
	if _, err := h.svc.AddComment(ctx, cs.ID, "bob", op2ID, "vmbr2 looks fine"); err != nil {
		t.Fatalf("AddComment on op2: %v", err)
	}

	// Remove op1 (and its comment along with it), keep op2 UNCHANGED
	// (same object, same id) so its own comment must survive.
	remaining := []change.Op{cs.Ops[1]}
	if _, err := h.svc.UpdateDraft(ctx, cs.ID, "alice", nil, remaining); err != nil {
		t.Fatalf("UpdateDraft: %v", err)
	}

	comments, err := h.svc.ListComments(ctx, cs.ID)
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("comments after removing op1 = %+v, want exactly op2's comment to survive", comments)
	}
	if comments[0].OpID != op2ID {
		t.Errorf("surviving comment.OpID = %q, want %q (op2, untouched)", comments[0].OpID, op2ID)
	}

	if !containsString(auditActions(t, h, cs.ID), "changeset.comment_orphan_cleanup") {
		t.Error("removing op1's comment was silent — no changeset.comment_orphan_cleanup audit row")
	}
}

// TestUpdateDraft_KeepingOpUnchanged_PreservesItsComment is the converse:
// re-submitting an edit that ADDS a new op alongside an existing, untouched
// one keeps the untouched op's own id (and thus its comment) intact — this
// is exactly useDrawerActions.ts's addOps/replaceOps shape (spread the
// existing ops array, append/replace the rest).
func TestUpdateDraft_KeepingOpUnchanged_PreservesItsComment(t *testing.T) {
	h := newReviewHarness(t, fixtureSingleNode, change.ApprovalConfig{})
	ctx := context.Background()

	cs := h.mustCreate(t, "alice", "one bridge", []change.Op{bridgeCreateOp("pve1", "vmbr1", nil)})
	op1ID := cs.Ops[0].ID
	if _, err := h.svc.AddComment(ctx, cs.ID, "bob", op1ID, "keep an eye on this"); err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	updated, err := h.svc.UpdateDraft(ctx, cs.ID, "alice", nil, append(cs.Ops, bridgeCreateOp("pve1", "vmbr2", nil)))
	if err != nil {
		t.Fatalf("UpdateDraft (append): %v", err)
	}
	if updated.Ops[0].ID != op1ID {
		t.Fatalf("op1's id changed across an edit that left it untouched: got %q, want %q", updated.Ops[0].ID, op1ID)
	}

	comments, err := h.svc.ListComments(ctx, cs.ID)
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if len(comments) != 1 || comments[0].OpID != op1ID {
		t.Fatalf("comments after appending a sibling op = %+v, want op1's comment untouched", comments)
	}
}

func TestDeleteComment(t *testing.T) {
	h := newReviewHarness(t, fixtureSingleNode, change.ApprovalConfig{})
	ctx := context.Background()
	cs := h.mustCreate(t, "alice", "add vmbr1", []change.Op{bridgeCreateOp("pve1", "vmbr1", nil)})

	comment, err := h.svc.AddComment(ctx, cs.ID, "bob", "", "a note")
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	if deleteErr := h.svc.DeleteComment(ctx, cs.ID, comment.ID, "bob"); deleteErr != nil {
		t.Fatalf("DeleteComment: %v", deleteErr)
	}
	comments, err := h.svc.ListComments(ctx, cs.ID)
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if len(comments) != 0 {
		t.Errorf("comments after delete = %+v, want none", comments)
	}
	if !containsString(auditActions(t, h, cs.ID), "changeset.comment_delete") {
		t.Error("changeset.comment_delete was not audited")
	}
}

func TestDeleteComment_WrongChangeset_NotFound(t *testing.T) {
	h := newReviewHarness(t, fixtureSingleNode, change.ApprovalConfig{})
	ctx := context.Background()
	cs1 := h.mustCreate(t, "alice", "cs1", []change.Op{bridgeCreateOp("pve1", "vmbr1", nil)})
	cs2 := h.mustCreate(t, "alice", "cs2", []change.Op{bridgeCreateOp("pve1", "vmbr2", nil)})

	comment, err := h.svc.AddComment(ctx, cs1.ID, "bob", "", "a note on cs1")
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	err = h.svc.DeleteComment(ctx, cs2.ID, comment.ID, "bob")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("DeleteComment(cs1's comment via cs2) error = %v, want store.ErrNotFound", err)
	}
}
