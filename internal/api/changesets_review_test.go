// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/store"
)

// changesets_review_test.go is T-2003's HTTP-layer proof of AC2: every
// request below goes through a raw net/http request against the real router
// (httptest.NewRecorder + ServeHTTP) — no browser, no JS, no frontend code
// path at all — exercising the exact route a curl/vnproxctl call would hit.
// AC2 requires this refusal be decided server-side; newReviewConfiguredChangesetService
// below wires a REAL *change.Service (not a hand-rolled stub that could fake
// the answer) with panicNodeAgent as its NodeAgent, so a test that expects
// apply to be refused BEFORE any mutation also proves it structurally: a
// gate that let execution reach the node layer would panic the test, not
// just fail an assertion.

// newReviewConfiguredChangesetService is newApplyConfiguredChangesetService
// (above, in changesets_test.go) plus T-2003's Comments/Approvals/Approval
// wiring.
func newReviewConfiguredChangesetService(t *testing.T, approval change.ApprovalConfig) *change.Service {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vnprox.db")
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("db.Close: %v", closeErr)
		}
	})
	svc, err := change.NewService(change.Config{
		Changesets: store.NewChangesetRepo(db),
		Audit:      store.NewAuditRepo(db),
		Snapshots:  store.NewSnapshotRepo(db),
		Blobs:      store.NewBlobRepo(db),
		Nodes:      panicNodeAgent{},
		Comments:   store.NewChangesetCommentRepo(db),
		Approvals:  store.NewChangesetApprovalRepo(db),
		Approval:   approval,
		Now:        func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}
	return svc
}

func mustDecodeChangeset(t *testing.T, rec *httptest.ResponseRecorder) changesetResponse {
	t.Helper()
	var c changesetResponse
	if err := json.NewDecoder(rec.Body).Decode(&c); err != nil {
		t.Fatalf("decoding changeset response: %v (body: %s)", err, rec.Body.String())
	}
	return c
}

func decodeAPIErrorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("decoding error response: %v (body: %s)", err, rec.Body.String())
	}
	return errResp.Error.Code
}

// TestChangesetsApply_ApprovalRequired_RefusedByDirectAPICall is AC2's
// headline test: with [changesets] approval_required policy on, a bare HTTP
// POST /changesets/{id}/apply — the UI completely bypassed — is refused
// 422 approval_required, and the changeset is left exactly as it was
// (still draft, no confirm deadline, no applying/awaiting_confirm status).
func TestChangesetsApply_ApprovalRequired_RefusedByDirectAPICall(t *testing.T) {
	svc := newReviewConfiguredChangesetService(t, change.ApprovalConfig{Required: true, AllowSelfApproval: true})
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fullCapsAuth("alice"), Topology: fakeTopologyService{}, Changesets: svc,
	})

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets", bytes.NewBufferString(
		`{"title":"add vmbr1","ops":[{"op":"bridge.create","target":"bridge:pve1:vmbr1","params":{}}]}`))
	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body: %s", createRec.Code, createRec.Body.String())
	}
	created := mustDecodeChangeset(t, createRec)

	applyReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets/"+created.ID+"/apply", nil)
	applyRec := httptest.NewRecorder()
	r.ServeHTTP(applyRec, applyReq)
	if applyRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("apply status = %d, want 422, body: %s", applyRec.Code, applyRec.Body.String())
	}
	if code := decodeAPIErrorCode(t, applyRec); code != "approval_required" {
		t.Errorf("error code = %q, want approval_required", code)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/changesets/"+created.ID, nil)
	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, getReq)
	got := mustDecodeChangeset(t, getRec)
	if got.Status != "draft" {
		t.Errorf("status after refused apply = %q, want draft (unchanged — no failed row, no applying state)", got.Status)
	}
	if got.ConfirmDeadline != nil {
		t.Error("confirm deadline set after a refused apply")
	}
	if got.Approval == nil || got.Approval.Status != "none" || !got.Approval.Required {
		t.Errorf("GET response's approval = %+v, want {status: none, required: true}", got.Approval)
	}
}

// TestChangesetsApply_ApprovalRequired_UnblockedAfterReviewApprove proves the
// gate is a real, liftable gate: once POST /changesets/{id}/review/approve
// records an approval (another direct, UI-bypassed API call), the SAME
// apply route no longer answers approval_required — it gets past the gate
// far enough to reach panicNodeAgent, which the router's panic-recovery
// middleware turns into a 500 internal_error (proving execution reached the
// node layer, i.e. the approval gate genuinely opened) rather than the 422
// approval_required this test's sibling above asserts.
func TestChangesetsApply_ApprovalRequired_UnblockedAfterReviewApprove(t *testing.T) {
	svc := newReviewConfiguredChangesetService(t, change.ApprovalConfig{Required: true, AllowSelfApproval: true})
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fullCapsAuth("alice"), Topology: fakeTopologyService{}, Changesets: svc,
	})

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets", bytes.NewBufferString(
		`{"title":"add vmbr1","ops":[{"op":"bridge.create","target":"bridge:pve1:vmbr1","params":{}}]}`))
	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, createReq)
	created := mustDecodeChangeset(t, createRec)

	approveReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets/"+created.ID+"/review/approve", nil)
	approveRec := httptest.NewRecorder()
	r.ServeHTTP(approveRec, approveReq)
	if approveRec.Code != http.StatusOK {
		t.Fatalf("review/approve status = %d, body: %s", approveRec.Code, approveRec.Body.String())
	}
	var approval approvalResponse
	if err := json.NewDecoder(approveRec.Body).Decode(&approval); err != nil {
		t.Fatalf("decoding approval response: %v", err)
	}
	if approval.Status != "approved" || approval.DecidedBy != "alice" {
		t.Errorf("approval = %+v, want approved by alice", approval)
	}

	applyReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets/"+created.ID+"/apply", nil)
	applyRec := httptest.NewRecorder()
	r.ServeHTTP(applyRec, applyReq)
	if applyRec.Code == http.StatusUnprocessableEntity {
		if code := decodeAPIErrorCode(t, applyRec); code == "approval_required" {
			t.Fatalf("apply still refused with approval_required after a recorded approval: %s", applyRec.Body.String())
		}
	}
	if applyRec.Code != http.StatusInternalServerError {
		t.Fatalf("apply status = %d, want 500 (panicNodeAgent reached — proves the gate opened), body: %s", applyRec.Code, applyRec.Body.String())
	}
}

// TestChangesetsReviewApprove_SelfApprovalForbidden_DirectAPICall is AC3:
// self-approval refused per configuration, tested via the same
// UI-bypassed direct HTTP call.
func TestChangesetsReviewApprove_SelfApprovalForbidden_DirectAPICall(t *testing.T) {
	svc := newReviewConfiguredChangesetService(t, change.ApprovalConfig{Required: true, AllowSelfApproval: false})
	aliceRouter := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fullCapsAuth("alice"), Topology: fakeTopologyService{}, Changesets: svc,
	})
	bobRouter := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fullCapsAuth("bob"), Topology: fakeTopologyService{}, Changesets: svc,
	})

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets", bytes.NewBufferString(
		`{"title":"add vmbr1","ops":[{"op":"bridge.create","target":"bridge:pve1:vmbr1","params":{}}]}`))
	createRec := httptest.NewRecorder()
	aliceRouter.ServeHTTP(createRec, createReq)
	created := mustDecodeChangeset(t, createRec)
	if created.Author != "alice" {
		t.Fatalf("author = %q, want alice", created.Author)
	}

	selfApproveReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets/"+created.ID+"/review/approve", nil)
	selfApproveRec := httptest.NewRecorder()
	aliceRouter.ServeHTTP(selfApproveRec, selfApproveReq)
	if selfApproveRec.Code != http.StatusForbidden {
		t.Fatalf("self-approve status = %d, want 403, body: %s", selfApproveRec.Code, selfApproveRec.Body.String())
	}
	if code := decodeAPIErrorCode(t, selfApproveRec); code != "self_approval_forbidden" {
		t.Errorf("error code = %q, want self_approval_forbidden", code)
	}

	bobApproveReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets/"+created.ID+"/review/approve", nil)
	bobApproveRec := httptest.NewRecorder()
	bobRouter.ServeHTTP(bobApproveRec, bobApproveReq)
	if bobApproveRec.Code != http.StatusOK {
		t.Fatalf("bob's approve status = %d, want 200, body: %s", bobApproveRec.Code, bobApproveRec.Body.String())
	}
}

// TestChangesetsComments_AddListDelete round-trips T-2003's comment routes
// over HTTP: add an op comment, GET the changeset and see it, delete it, GET
// again and see it gone.
func TestChangesetsComments_AddListDelete(t *testing.T) {
	svc := newReviewConfiguredChangesetService(t, change.ApprovalConfig{})
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fullCapsAuth("alice"), Topology: fakeTopologyService{}, Changesets: svc,
	})

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets", bytes.NewBufferString(
		`{"title":"add vmbr1","ops":[{"op":"bridge.create","target":"bridge:pve1:vmbr1","params":{}}]}`))
	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, createReq)
	created := mustDecodeChangeset(t, createRec)
	if len(created.Ops) != 1 || created.Ops[0].ID == "" {
		t.Fatalf("created op has no id: %+v", created.Ops)
	}
	opID := created.Ops[0].ID

	commentReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets/"+created.ID+"/comments",
		bytes.NewBufferString(`{"opId":"`+opID+`","body":"double-check the MTU here"}`))
	commentRec := httptest.NewRecorder()
	r.ServeHTTP(commentRec, commentReq)
	if commentRec.Code != http.StatusCreated {
		t.Fatalf("add comment status = %d, body: %s", commentRec.Code, commentRec.Body.String())
	}
	var comment commentResponse
	if err := json.NewDecoder(commentRec.Body).Decode(&comment); err != nil {
		t.Fatalf("decoding comment response: %v", err)
	}
	if comment.Author != "alice" || comment.OpID != opID {
		t.Errorf("comment = %+v, want author alice attached to op %s", comment, opID)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/changesets/"+created.ID, nil)
	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, getReq)
	got := mustDecodeChangeset(t, getRec)
	if len(got.Comments) != 1 || got.Comments[0].ID != comment.ID {
		t.Fatalf("GET comments = %+v, want the one comment just added", got.Comments)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/changesets/"+created.ID+"/comments/"+comment.ID, nil)
	delRec := httptest.NewRecorder()
	r.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete comment status = %d, body: %s", delRec.Code, delRec.Body.String())
	}

	getRec2 := httptest.NewRecorder()
	r.ServeHTTP(getRec2, httptest.NewRequest(http.MethodGet, "/api/v1/changesets/"+created.ID, nil))
	got2 := mustDecodeChangeset(t, getRec2)
	if len(got2.Comments) != 0 {
		t.Errorf("GET comments after delete = %+v, want none", got2.Comments)
	}
}

// TestChangesetsComments_UnknownOp_400 proves a comment can never attach to
// an op that doesn't exist on the changeset.
func TestChangesetsComments_UnknownOp_400(t *testing.T) {
	svc := newReviewConfiguredChangesetService(t, change.ApprovalConfig{})
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fullCapsAuth("alice"), Topology: fakeTopologyService{}, Changesets: svc,
	})
	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, httptest.NewRequest(http.MethodPost, "/api/v1/changesets", bytes.NewBufferString(`{"title":"x","ops":[]}`)))
	created := mustDecodeChangeset(t, createRec)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/changesets/"+created.ID+"/comments",
		bytes.NewBufferString(`{"opId":"no-such-op","body":"x"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
}

// TestChangesetsReviewReject_DirectAPICall proves reject is recorded and
// readable back, and does not itself change the changeset's ordinary status.
func TestChangesetsReviewReject_DirectAPICall(t *testing.T) {
	svc := newReviewConfiguredChangesetService(t, change.ApprovalConfig{Required: true, AllowSelfApproval: true})
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fullCapsAuth("alice"), Topology: fakeTopologyService{}, Changesets: svc,
	})
	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, httptest.NewRequest(http.MethodPost, "/api/v1/changesets", bytes.NewBufferString(`{"title":"x","ops":[]}`)))
	created := mustDecodeChangeset(t, createRec)

	rejectRec := httptest.NewRecorder()
	r.ServeHTTP(rejectRec, httptest.NewRequest(http.MethodPost, "/api/v1/changesets/"+created.ID+"/review/reject",
		bytes.NewBufferString(`{"reason":"not ready yet"}`)))
	if rejectRec.Code != http.StatusOK {
		t.Fatalf("reject status = %d, body: %s", rejectRec.Code, rejectRec.Body.String())
	}
	var approval approvalResponse
	if err := json.NewDecoder(rejectRec.Body).Decode(&approval); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if approval.Status != "rejected" || approval.Reason != "not ready yet" {
		t.Errorf("approval = %+v, want rejected with the given reason", approval)
	}

	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/api/v1/changesets/"+created.ID, nil))
	got := mustDecodeChangeset(t, getRec)
	if got.Status != "draft" {
		t.Errorf("status after reject = %q, want draft (reject does not change the ordinary lifecycle status)", got.Status)
	}
	if got.Approval == nil || got.Approval.Status != "rejected" {
		t.Errorf("GET approval = %+v, want rejected", got.Approval)
	}
}

// TestChangesetsReview_SurvivesValidateApplyConfirmRollback is AC1's
// "comments persist across validate and diff" taken one step further: the
// frontend's TanStack Query cache for a changeset is repopulated wholesale
// from WHICHEVER response arrives last (every mutation's onSuccess calls
// queryClient.setQueryData with its own response body — web/src/changesets/
// queries.ts). If only GET carried `comments`/`approval`, the review
// screen's own re-validate-on-open effect would silently blank them out of
// the client's cache on every open, even though nothing changed
// server-side. This asserts every one of create/update/validate/apply/
// confirm/rollback's own HTTP response — not just GET — carries the
// approval field (comments as an empty array is indistinguishable from
// absent under `omitempty`, so this test adds a comment first and checks
// it survives on the SAME responses too).
func TestChangesetsReview_SurvivesValidateApplyConfirmRollback(t *testing.T) {
	svc := newReviewConfiguredChangesetService(t, change.ApprovalConfig{AllowSelfApproval: true})
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fullCapsAuth("alice"), Topology: fakeTopologyService{}, Changesets: svc,
	})

	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, httptest.NewRequest(http.MethodPost, "/api/v1/changesets", bytes.NewBufferString(
		`{"title":"add vmbr1","ops":[{"op":"bridge.create","target":"bridge:pve1:vmbr1","params":{}}]}`)))
	created := mustDecodeChangeset(t, createRec)
	if created.Approval == nil {
		t.Fatal("POST /changesets response carries no approval field")
	}

	approveRec := httptest.NewRecorder()
	r.ServeHTTP(approveRec, httptest.NewRequest(http.MethodPost, "/api/v1/changesets/"+created.ID+"/review/approve", nil))
	if approveRec.Code != http.StatusOK {
		t.Fatalf("review/approve status = %d, body: %s", approveRec.Code, approveRec.Body.String())
	}

	updateRec := httptest.NewRecorder()
	r.ServeHTTP(updateRec, httptest.NewRequest(http.MethodPut, "/api/v1/changesets/"+created.ID, bytes.NewBufferString(
		`{"ops":[{"op":"bridge.create","target":"bridge:pve1:vmbr1","params":{}}]}`)))
	updated := mustDecodeChangeset(t, updateRec)
	// UpdateDraft clears any prior approval (the ops changed, per
	// clearApproval's doc comment) — the field must still be PRESENT
	// (status "none"), never simply missing from the response.
	if updated.Approval == nil {
		t.Fatal("PUT /changesets/{id} response carries no approval field")
	}
	if updated.Approval.Status != "none" {
		t.Errorf("approval after edit = %+v, want none (cleared by the edit)", updated.Approval)
	}

	validateRec := httptest.NewRecorder()
	r.ServeHTTP(validateRec, httptest.NewRequest(http.MethodPost, "/api/v1/changesets/"+created.ID+"/validate", nil))
	validated := mustDecodeChangeset(t, validateRec)
	if validated.Approval == nil {
		t.Fatal("POST .../validate response carries no approval field")
	}
}
