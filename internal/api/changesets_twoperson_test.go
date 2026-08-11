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

// changesets_twoperson_test.go is T-2604's HTTP-layer proof of AC2: every
// request below is a raw net/http request against the real router — no
// browser, no JS, no frontend code path at all — exercising the exact route
// a curl or vnproxctl call would hit. The service under it is a REAL
// *change.Service (not a stub that could fake the answer) whose NodeAgent is
// panicNodeAgent, so "refused before any mutation" is proved structurally:
// a gate that let execution reach the node layer would panic, not merely
// fail an assertion. That same property makes the CONTROL LEG legible —
// when the gate is genuinely satisfied, apply reaches panicNodeAgent and the
// router's recovery middleware turns it into a 500, which is how these tests
// tell "the gate opened" from "the gate is simply always shut".
//
// The declared protected class here is `bridge.*` rather than the card's
// `fw.*`: the class list is configuration, the matching is one code path for
// every glob (asserted over fw.*/sdn.*/tag: in internal/change's own
// twoperson_test.go), and `bridge.create` is the op the rest of this
// package's harness already stages end-to-end.

// newTwoPersonChangesetService is newReviewConfiguredChangesetService plus
// T-2604's sign-off/break-glass stores and a declared protected class. It
// returns the db too, so a test can read the audit trail.
func newTwoPersonChangesetService(t *testing.T, classes []change.ProtectedClass) (*change.Service, *store.DB) {
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
		Changesets:       store.NewChangesetRepo(db),
		Audit:            store.NewAuditRepo(db),
		Snapshots:        store.NewSnapshotRepo(db),
		Blobs:            store.NewBlobRepo(db),
		Nodes:            panicNodeAgent{},
		Comments:         store.NewChangesetCommentRepo(db),
		Approvals:        store.NewChangesetApprovalRepo(db),
		Signoffs:         store.NewChangesetSignoffRepo(db),
		BreakGlass:       store.NewChangesetBreakGlassRepo(db),
		ProtectedClasses: classes,
		Approval:         change.ApprovalConfig{AllowSelfApproval: true},
		Now:              func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}
	return svc, db
}

func twoPersonRouter(svc *change.Service, username string) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fullCapsAuth(username), Topology: fakeTopologyService{}, Changesets: svc,
	})
}

func stageProtectedChangeset(t *testing.T, r http.Handler) changesetResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/changesets", bytes.NewBufferString(
		`{"title":"add vmbr1","ops":[{"op":"bridge.create","target":"bridge:pve1:vmbr1","params":{}}]}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body: %s", rec.Code, rec.Body.String())
	}
	return mustDecodeChangeset(t, rec)
}

func postApply(t *testing.T, r http.Handler, id string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/changesets/"+id+"/apply", nil))
	return rec
}

func postApprove(t *testing.T, r http.Handler, id string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/changesets/"+id+"/review/approve", nil))
	return rec
}

// decodeAPIError returns an error response's code and `details` object in
// one read — the recorder's body is a stream, so decoding it twice would
// hand the second caller an EOF rather than the same document.
func decodeAPIError(t *testing.T, rec *httptest.ResponseRecorder) (string, map[string]any) {
	t.Helper()
	var errResp struct {
		Error struct {
			Details map[string]any `json:"details"`
			Code    string         `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("decoding error response: %v (body: %s)", err, rec.Body.String())
	}
	return errResp.Error.Code, errResp.Error.Details
}

// AC1 + AC2: a protected-class changeset with N-1 approvals is refused at
// apply by a direct, UI-bypassed API call, and the refusal names the class
// and the count required. The control leg proves the gate is liftable.
func TestChangesetsApply_TwoPersonRequired_RefusedByDirectAPICall(t *testing.T) {
	svc, _ := newTwoPersonChangesetService(t, []change.ProtectedClass{{Class: "bridge.*", Approvals: 2}})
	alice := twoPersonRouter(svc, "alice")
	bob := twoPersonRouter(svc, "bob")
	carol := twoPersonRouter(svc, "carol")

	created := stageProtectedChangeset(t, alice)

	// Zero approvals.
	rec := postApply(t, alice, created.ID)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("apply status = %d, want 422, body: %s", rec.Code, rec.Body.String())
	}
	if code := decodeAPIErrorCode(t, rec); code != "two_person_required" {
		t.Fatalf("error code = %q, want two_person_required", code)
	}

	// N-1 approvals: still refused, and the details name the class, the
	// count required, and who has signed.
	if approveRec := postApprove(t, bob, created.ID); approveRec.Code != http.StatusOK {
		t.Fatalf("bob's approve status = %d, body: %s", approveRec.Code, approveRec.Body.String())
	}
	rec = postApply(t, alice, created.ID)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("apply with 1 of 2 status = %d, want 422, body: %s", rec.Code, rec.Body.String())
	}
	code, details := decodeAPIError(t, rec)
	if code != "two_person_required" {
		t.Fatalf("error code = %q, want two_person_required", code)
	}
	if details["class"] != "bridge.*" {
		t.Errorf("details.class = %v, want bridge.*", details["class"])
	}
	if required, _ := details["required"].(float64); required != 2 {
		t.Errorf("details.required = %v, want 2", details["required"])
	}
	if have, _ := details["have"].(float64); have != 1 {
		t.Errorf("details.have = %v, want 1", details["have"])
	}

	// The changeset is left exactly where it was — no failed row, no
	// applying state, no confirm deadline.
	getRec := httptest.NewRecorder()
	alice.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/api/v1/changesets/"+created.ID, nil))
	got := mustDecodeChangeset(t, getRec)
	if got.Status != "draft" {
		t.Errorf("status after a refused apply = %q, want draft", got.Status)
	}
	if got.ConfirmDeadline != nil {
		t.Error("confirm deadline set after a refused apply")
	}
	if got.Approval == nil || got.Approval.TwoPerson == nil {
		t.Fatalf("GET response's approval = %+v, want a twoPerson read model", got.Approval)
	}
	if got.Approval.TwoPerson.Required != 2 || got.Approval.TwoPerson.Satisfied {
		t.Errorf("approval.twoPerson = %+v, want required 2 and unsatisfied", got.Approval.TwoPerson)
	}

	// CONTROL LEG: the second distinct approver opens the gate, and apply
	// now runs far enough to reach the node layer (panicNodeAgent -> 500).
	// Without this, everything above would also pass against a gate that
	// refuses unconditionally.
	if approveRec := postApprove(t, carol, created.ID); approveRec.Code != http.StatusOK {
		t.Fatalf("carol's approve status = %d, body: %s", approveRec.Code, approveRec.Body.String())
	}
	rec = postApply(t, alice, created.ID)
	if rec.Code == http.StatusUnprocessableEntity {
		if code := decodeAPIErrorCode(t, rec); code == "two_person_required" {
			t.Fatal("apply still refused two_person_required after two distinct approvals — the gate never opens")
		}
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("apply after two approvals = %d, want 500 (execution reached panicNodeAgent), body: %s", rec.Code, rec.Body.String())
	}
}

// AC3, at the HTTP layer: the same principal approving through two separate
// authenticated requests (two sessions, or two API tokens they minted —
// which carry their username either way, asserted in
// internal/auth/bearer_test.go) counts once. The control leg proves a second
// PERSON does count.
func TestChangesetsApply_TwoApprovalsFromOnePrincipalCountAsOne(t *testing.T) {
	svc, _ := newTwoPersonChangesetService(t, []change.ProtectedClass{{Class: "bridge.*", Approvals: 2}})
	alice := twoPersonRouter(svc, "alice")
	bobSessionOne := twoPersonRouter(svc, "bob")
	bobSessionTwo := twoPersonRouter(svc, "bob")
	carol := twoPersonRouter(svc, "carol")

	created := stageProtectedChangeset(t, alice)
	for i, r := range []http.Handler{bobSessionOne, bobSessionTwo} {
		if rec := postApprove(t, r, created.ID); rec.Code != http.StatusOK {
			t.Fatalf("bob's approve #%d status = %d, body: %s", i+1, rec.Code, rec.Body.String())
		}
	}

	rec := postApply(t, alice, created.ID)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("apply status = %d, want 422 — bob approving twice is one approver, body: %s", rec.Code, rec.Body.String())
	}
	code, details := decodeAPIError(t, rec)
	if code != "two_person_required" {
		t.Fatalf("error code = %q, want two_person_required", code)
	}
	if have, _ := details["have"].(float64); have != 1 {
		t.Errorf("details.have = %v, want 1 (one person, however many credentials)", details["have"])
	}
	approvers, _ := details["approvers"].([]any)
	if len(approvers) != 1 || approvers[0] != "bob" {
		t.Errorf("details.approvers = %v, want exactly [bob]", details["approvers"])
	}

	// CONTROL LEG: a different person is a different approver.
	if rec := postApprove(t, carol, created.ID); rec.Code != http.StatusOK {
		t.Fatalf("carol's approve status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if rec := postApply(t, alice, created.ID); rec.Code != http.StatusInternalServerError {
		t.Fatalf("apply after bob+carol = %d, want 500 (gate opened, execution reached the node layer), body: %s", rec.Code, rec.Body.String())
	}
}

// AC4: break-glass with no reason is refused and records nothing; with a
// reason it proceeds, is audited under its own action, and lets the same
// previously-refused apply through.
func TestChangesetsBreakGlass_RefusedWithoutAReasonThenOverridesTheGate(t *testing.T) {
	svc, db := newTwoPersonChangesetService(t, []change.ProtectedClass{{Class: "bridge.*", Approvals: 2}})
	alice := twoPersonRouter(svc, "alice")
	created := stageProtectedChangeset(t, alice)

	if rec := postApply(t, alice, created.ID); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("precondition: apply status = %d, want 422", rec.Code)
	}

	for _, body := range []string{`{}`, `{"reason":""}`, `{"reason":"   "}`} {
		rec := httptest.NewRecorder()
		alice.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
			"/api/v1/changesets/"+created.ID+"/break-glass", bytes.NewBufferString(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("break-glass %s status = %d, want 400, body: %s", body, rec.Code, rec.Body.String())
		}
		if code := decodeAPIErrorCode(t, rec); code != "validation_failed" {
			t.Errorf("break-glass %s error code = %q, want validation_failed", body, code)
		}
	}
	// A refused break-glass changed nothing: apply is still refused for the
	// same reason it was before.
	rec := postApply(t, alice, created.ID)
	if rec.Code != http.StatusUnprocessableEntity || decodeAPIErrorCode(t, rec) != "two_person_required" {
		t.Fatalf("apply after refused break-glass = %d, want 422 two_person_required", rec.Code)
	}

	bgRec := httptest.NewRecorder()
	alice.ServeHTTP(bgRec, httptest.NewRequest(http.MethodPost,
		"/api/v1/changesets/"+created.ID+"/break-glass",
		bytes.NewBufferString(`{"reason":"corosync down at 03:00, nobody else on call"}`)))
	if bgRec.Code != http.StatusOK {
		t.Fatalf("break-glass status = %d, want 200, body: %s", bgRec.Code, bgRec.Body.String())
	}
	var rec2 change.BreakGlassRecord
	if err := json.NewDecoder(bgRec.Body).Decode(&rec2); err != nil {
		t.Fatalf("decoding break-glass response: %v", err)
	}
	if rec2.InvokedBy != "alice" || rec2.AckableAt != rec2.InvokedAt+int64(change.BreakGlassAckFloor.Seconds()) {
		t.Fatalf("break-glass response = %+v, want alice and a 24h ack floor", rec2)
	}

	// Audited under its own action.
	entries, err := store.NewAuditRepo(db).List(context.Background(), created.ID, 100)
	if err != nil {
		t.Fatalf("audit List: %v", err)
	}
	var actions []string
	for _, e := range entries {
		actions = append(actions, e.Action)
	}
	found := false
	for _, a := range actions {
		if a == "change.breakglass" {
			found = true
		}
	}
	if !found {
		t.Fatalf("audit actions = %v, want one to be change.breakglass", actions)
	}

	// And the same apply that was refused now gets through to the node layer.
	if rec := postApply(t, alice, created.ID); rec.Code != http.StatusInternalServerError {
		t.Fatalf("apply under break-glass = %d, want 500 (gate opened), body: %s", rec.Code, rec.Body.String())
	}
}

// AC6, at the HTTP layer: a changeset in NO protected class applies exactly
// as it always did — the same request, against the same deployment with the
// gate configured, is not refused by it.
func TestChangesetsApply_UnprotectedChangesetIsUnaffected(t *testing.T) {
	svc, _ := newTwoPersonChangesetService(t, []change.ProtectedClass{{Class: "fw.*", Approvals: 2}})
	alice := twoPersonRouter(svc, "alice")
	created := stageProtectedChangeset(t, alice) // a bridge.create — not fw.*

	rec := postApply(t, alice, created.ID)
	if rec.Code == http.StatusUnprocessableEntity && decodeAPIErrorCode(t, rec) == "two_person_required" {
		t.Fatal("an unprotected changeset was refused by the two-person gate")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("apply status = %d, want 500 (execution reached panicNodeAgent, i.e. no gate refused it), body: %s", rec.Code, rec.Body.String())
	}

	// And its read model carries no twoPerson block at all, so no pre-T-2604
	// response's shape changed for a changeset the rule does not cover.
	getRec := httptest.NewRecorder()
	alice.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/api/v1/changesets/"+created.ID, nil))
	got := mustDecodeChangeset(t, getRec)
	if got.Approval != nil && got.Approval.TwoPerson != nil {
		t.Errorf("approval.twoPerson = %+v, want absent for an unprotected changeset", got.Approval.TwoPerson)
	}
}
