package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/store"
)

// fakeAuthWithCaps extends fakeAuthWithUser (layouts_test.go) with
// per-capability gating and CSRF enforcement, so this file's tests can
// exercise the changesets routes' capability gate (T-201 acceptance
// criterion 3: netWrite required to create) and CSRF requirement without
// a real internal/auth.Service.
type fakeAuthWithCaps struct {
	caps map[string]bool
	fakeAuthWithUser
	csrf bool
}

func (f fakeAuthWithCaps) RequireCap(cap string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !f.caps[cap] {
				writeJSONError(w, http.StatusForbidden, "forbidden", "missing required capability: "+cap)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (f fakeAuthWithCaps) CSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.csrf && r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get("X-VNPROX-CSRF") != "test-csrf-token" {
				writeJSONError(w, http.StatusForbidden, "csrf_required", "missing or invalid X-VNPROX-CSRF header")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func newChangesetTestService(t *testing.T) *change.Service {
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
		Now:        func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}
	return svc
}

func newChangesetTestRouter(svc *change.Service, auth fakeAuthWithCaps) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: auth, Topology: fakeTopologyService{}, Changesets: svc,
	})
}

func fullCapsAuth(username string) fakeAuthWithCaps {
	return fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: username},
		caps:             map[string]bool{capNetRead: true, capNetWrite: true},
	}
}

func TestChangesetsRoutes_NotMountedWithoutUsernameLookup(t *testing.T) {
	svc := newChangesetTestService(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, Topology: fakeTopologyService{}, Changesets: svc,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/changesets", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route should not be mounted), body: %s", rec.Code, rec.Body.String())
	}
}

func TestChangesetsRoutes_Unauthenticated401(t *testing.T) {
	svc := newChangesetTestService(t)
	auth := fakeAuthWithCaps{fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: false}, username: "alice"}, caps: map[string]bool{capNetRead: true, capNetWrite: true}}
	r := newChangesetTestRouter(svc, auth)

	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/v1/changesets", nil),
		httptest.NewRequest(http.MethodPost, "/api/v1/changesets", bytes.NewBufferString(`{"title":"x","ops":[]}`)),
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s (unauthenticated): status = %d, want 401", req.Method, req.URL.Path, rec.Code)
		}
	}
}

// TestChangesetsRoutes_CreateRequiresNetWrite is T-201 acceptance criterion
// 3: capability gating, netWrite required to create.
func TestChangesetsRoutes_CreateRequiresNetWrite(t *testing.T) {
	svc := newChangesetTestService(t)
	auth := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"},
		caps:             map[string]bool{capNetRead: true, capNetWrite: false},
	}
	r := newChangesetTestRouter(svc, auth)

	body := bytes.NewBufferString(`{"title":"add vmbr5","ops":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/changesets", body)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (missing netWrite), body: %s", rec.Code, rec.Body.String())
	}

	// A read-only (netRead only) user can still list/get.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/changesets", nil)
	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Errorf("GET /changesets with only netRead: status = %d, want 200, body: %s", getRec.Code, getRec.Body.String())
	}
}

func TestChangesetsRoutes_CreateRequiresCSRF(t *testing.T) {
	svc := newChangesetTestService(t)
	auth := fullCapsAuth("alice")
	auth.csrf = true
	r := newChangesetTestRouter(svc, auth)

	body := bytes.NewBufferString(`{"title":"add vmbr5","ops":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/changesets", body)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (missing CSRF header), body: %s", rec.Code, rec.Body.String())
	}

	body2 := bytes.NewBufferString(`{"title":"add vmbr5","ops":[]}`)
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/changesets", body2)
	req2.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusCreated {
		t.Errorf("status with valid CSRF = %d, want 201, body: %s", rec2.Code, rec2.Body.String())
	}
}

func TestChangesetsRoutes_CreateGetListRoundTrip(t *testing.T) {
	svc := newChangesetTestService(t)
	r := newChangesetTestRouter(svc, fullCapsAuth("alice"))

	createBody := `{"title":"add vmbr5","ops":[{"op":"bridge.create","target":"bridge:pve1:vmbr5","params":{"mtu":1500}}]}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets", bytes.NewBufferString(createBody))
	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("POST /changesets status = %d, want 201, body: %s", createRec.Code, createRec.Body.String())
	}

	var created changesetResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decoding create response: %v", err)
	}
	if created.Status != "draft" {
		t.Errorf("Status = %q, want draft", created.Status)
	}
	if created.Author != "alice" {
		t.Errorf("Author = %q, want alice", created.Author)
	}
	if len(created.Ops) != 1 || created.Ops[0].Type != change.OpBridgeCreate {
		t.Fatalf("Ops = %+v, want one bridge.create op", created.Ops)
	}
	if created.Findings == nil {
		t.Error("Findings should be an empty array, not null, in the JSON response")
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/changesets/"+created.ID, nil)
	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /changesets/{id} status = %d, want 200, body: %s", getRec.Code, getRec.Body.String())
	}
	var got changesetResponse
	if err := json.NewDecoder(getRec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding get response: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("Get ID = %q, want %q", got.ID, created.ID)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/changesets", nil)
	listRec := httptest.NewRecorder()
	r.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET /changesets status = %d, want 200, body: %s", listRec.Code, listRec.Body.String())
	}
	var list []changesetResponse
	if err := json.NewDecoder(listRec.Body).Decode(&list); err != nil {
		t.Fatalf("decoding list response: %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Errorf("List = %+v, want exactly the one created changeset", list)
	}
}

// TestChangesetsRoutes_GetMissing_404 proves a never-created id 404s.
func TestChangesetsRoutes_GetMissing_404(t *testing.T) {
	svc := newChangesetTestService(t)
	r := newChangesetTestRouter(svc, fullCapsAuth("alice"))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/changesets/does-not-exist", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404, body: %s", rec.Code, rec.Body.String())
	}
}

// TestChangesetsRoutes_CreateUnknownOpType_400 is T-201 acceptance
// criterion 1's API-level check: an unknown op type in the request body
// gets a 400 validation_failed with the offending path in details.
func TestChangesetsRoutes_CreateUnknownOpType_400(t *testing.T) {
	svc := newChangesetTestService(t)
	r := newChangesetTestRouter(svc, fullCapsAuth("alice"))

	body := `{"title":"bad","ops":[{"op":"bridge.teleport","target":"bridge:pve1:vmbr0","params":{}}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/changesets", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}

	var got struct {
		Error struct {
			Details map[string]any `json:"details"`
			Code    string         `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding error response: %v", err)
	}
	if got.Error.Code != "validation_failed" {
		t.Errorf("error.code = %q, want validation_failed", got.Error.Code)
	}
	if got.Error.Details["path"] != "ops[0].op" {
		t.Errorf("error.details.path = %v, want %q", got.Error.Details["path"], "ops[0].op")
	}
}

// TestChangesetsRoutes_CreateUnknownParamField_400 covers the params-level
// half of the same acceptance criterion.
func TestChangesetsRoutes_CreateUnknownParamField_400(t *testing.T) {
	svc := newChangesetTestService(t)
	r := newChangesetTestRouter(svc, fullCapsAuth("alice"))

	body := `{"title":"bad","ops":[{"op":"bridge.update","target":"bridge:pve1:vmbr0","params":{"mtuu":9000}}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/changesets", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding error response: %v", err)
	}
	if got.Error.Details["path"] != "ops[0].params.mtuu" {
		t.Errorf("error.details.path = %v, want %q", got.Error.Details["path"], "ops[0].params.mtuu")
	}
}

// TestChangesetsRoutes_CreateBadOpInMultiOpBody_IndexedPath is the
// audit-phase-2 F-19 regression check: with several ops in the body, the
// decode error's details.path must identify *which* op failed —
// "ops[1].params.mtuu", not a bare "params.mtuu".
func TestChangesetsRoutes_CreateBadOpInMultiOpBody_IndexedPath(t *testing.T) {
	svc := newChangesetTestService(t)
	r := newChangesetTestRouter(svc, fullCapsAuth("alice"))

	body := `{"title":"bad","ops":[` +
		`{"op":"bridge.update","target":"bridge:pve1:vmbr0","params":{"mtu":9000}},` +
		`{"op":"bridge.update","target":"bridge:pve1:vmbr1","params":{"mtuu":9000}}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/changesets", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding error response: %v", err)
	}
	if got.Error.Details["path"] != "ops[1].params.mtuu" {
		t.Errorf("error.details.path = %v, want %q", got.Error.Details["path"], "ops[1].params.mtuu")
	}
}

func TestChangesetsRoutes_UpdateDraft_ReplacesOps(t *testing.T) {
	svc := newChangesetTestService(t)
	r := newChangesetTestRouter(svc, fullCapsAuth("alice"))

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets", bytes.NewBufferString(`{"title":"draft","ops":[]}`))
	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, createReq)
	var created changesetResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decoding create response: %v", err)
	}

	updateBody := `{"ops":[{"op":"bond.delete","target":"bond:pve1:bond0","params":{}}]}`
	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/changesets/"+created.ID, bytes.NewBufferString(updateBody))
	updateRec := httptest.NewRecorder()
	r.ServeHTTP(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200, body: %s", updateRec.Code, updateRec.Body.String())
	}
	var updated changesetResponse
	if err := json.NewDecoder(updateRec.Body).Decode(&updated); err != nil {
		t.Fatalf("decoding update response: %v", err)
	}
	if len(updated.Ops) != 1 || updated.Ops[0].Type != change.OpBondDelete {
		t.Fatalf("Ops after update = %+v, want one bond.delete op", updated.Ops)
	}
}

func TestChangesetsRoutes_UpdateMissing_404(t *testing.T) {
	svc := newChangesetTestService(t)
	r := newChangesetTestRouter(svc, fullCapsAuth("alice"))

	req := httptest.NewRequest(http.MethodPut, "/api/v1/changesets/does-not-exist", bytes.NewBufferString(`{"ops":[]}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404, body: %s", rec.Code, rec.Body.String())
	}
}

func TestChangesetsRoutes_DiscardDraft(t *testing.T) {
	svc := newChangesetTestService(t)
	r := newChangesetTestRouter(svc, fullCapsAuth("alice"))

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets", bytes.NewBufferString(`{"title":"draft","ops":[]}`))
	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, createReq)
	var created changesetResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decoding create response: %v", err)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/changesets/"+created.ID, nil)
	delRec := httptest.NewRecorder()
	r.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204, body: %s", delRec.Code, delRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/changesets/"+created.ID, nil)
	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, getReq)
	var got changesetResponse
	if err := json.NewDecoder(getRec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding get-after-discard response: %v", err)
	}
	if got.Status != "discarded" {
		t.Errorf("Status after discard = %q, want discarded", got.Status)
	}

	// Discarding again is an illegal transition -> 409.
	delReq2 := httptest.NewRequest(http.MethodDelete, "/api/v1/changesets/"+created.ID, nil)
	delRec2 := httptest.NewRecorder()
	r.ServeHTTP(delRec2, delReq2)
	if delRec2.Code != http.StatusConflict {
		t.Errorf("second DELETE status = %d, want 409, body: %s", delRec2.Code, delRec2.Body.String())
	}
}

// TestChangesetsRoutes_TwoUsersDraftsCoexist is T-201 acceptance criterion
// 4: two parked drafts by different users coexist and list correctly, at
// the full HTTP-API level.
func TestChangesetsRoutes_TwoUsersDraftsCoexist(t *testing.T) {
	svc := newChangesetTestService(t)
	aliceRouter := newChangesetTestRouter(svc, fullCapsAuth("alice"))
	bobRouter := newChangesetTestRouter(svc, fullCapsAuth("bob"))

	for _, tc := range []struct {
		router http.Handler
		title  string
	}{
		{aliceRouter, "alice's parked draft"},
		{bobRouter, "bob's parked draft"},
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/changesets", bytes.NewBufferString(`{"title":"`+tc.title+`","ops":[]}`))
		rec := httptest.NewRecorder()
		tc.router.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("creating draft %q: status = %d, body: %s", tc.title, rec.Code, rec.Body.String())
		}
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/changesets", nil)
	listRec := httptest.NewRecorder()
	aliceRouter.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body: %s", listRec.Code, listRec.Body.String())
	}
	var list []changesetResponse
	if err := json.NewDecoder(listRec.Body).Decode(&list); err != nil {
		t.Fatalf("decoding list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list has %d changesets, want 2", len(list))
	}
	authors := map[string]bool{}
	for _, c := range list {
		authors[c.Author] = true
	}
	if !authors["alice"] || !authors["bob"] {
		t.Errorf("authors = %+v, want both alice and bob represented", authors)
	}
}

// TestChangesetsApplyRoutes_Unconfigured proves diff/apply/confirm/rollback
// are registered and implemented (T-205), returning 503 apply_unavailable
// when the Service was built without the apply engine (this test's Service
// has no NodeAgent/SnapshotRepo). validate is covered separately (T-202).
func TestChangesetsApplyRoutes_Unconfigured(t *testing.T) {
	svc := newChangesetTestService(t)
	r := newChangesetTestRouter(svc, fullCapsAuth("alice"))

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets", bytes.NewBufferString(`{"title":"draft","ops":[]}`))
	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, createReq)
	var created changesetResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decoding create response: %v", err)
	}

	for _, path := range []string{
		"/api/v1/changesets/" + created.ID + "/apply",
		"/api/v1/changesets/" + created.ID + "/confirm",
		"/api/v1/changesets/" + created.ID + "/rollback",
		// T-2602's staged-apply promotion route: registered and implemented
		// on the same terms as the rest of the apply family.
		"/api/v1/changesets/" + created.ID + "/continue",
	} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("POST %s: status = %d, want 503", path, rec.Code)
		}
	}

	diffReq := httptest.NewRequest(http.MethodGet, "/api/v1/changesets/"+created.ID+"/diff", nil)
	diffRec := httptest.NewRecorder()
	r.ServeHTTP(diffRec, diffReq)
	if diffRec.Code != http.StatusServiceUnavailable {
		t.Errorf("GET diff: status = %d, want 503", diffRec.Code)
	}
}

// TestChangesetsValidate_Route is T-202's API-layer coverage for
// `POST /changesets/{id}/validate`: it runs the real pipeline (rather than
// the 501 stub) and returns the changeset with findings populated, and a
// clean changeset is promoted from draft to validated.
func TestChangesetsValidate_Route(t *testing.T) {
	svc := newChangesetTestService(t)
	r := newChangesetTestRouter(svc, fullCapsAuth("alice"))

	// A clean op (schema/referential valid against the empty test-service
	// inventory: a bridge.create never needs its target to already exist;
	// "comments" is set so the advisory class's missing-description check
	// doesn't add a warning finding here).
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets",
		bytes.NewBufferString(`{"title":"draft","ops":[{"op":"bridge.create","target":"bridge:pve1:vmbr5","params":{"mtu":1500,"comments":"guest bridge"}}]}`))
	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, createReq)
	var created changesetResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decoding create response: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/changesets/"+created.ID+"/validate", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("validate status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var validated changesetResponse
	if err := json.NewDecoder(rec.Body).Decode(&validated); err != nil {
		t.Fatalf("decoding validate response: %v", err)
	}
	if validated.Status != "validated" {
		t.Errorf("Status = %q, want validated", validated.Status)
	}
	if len(validated.Findings) != 0 {
		t.Errorf("Findings = %+v, want none for a clean changeset", validated.Findings)
	}
}

// TestChangesetsValidate_NotFound proves the 404 mapping applies to
// validate the same way it does to the other mutation-shaped routes.
func TestChangesetsValidate_NotFound(t *testing.T) {
	svc := newChangesetTestService(t)
	r := newChangesetTestRouter(svc, fullCapsAuth("alice"))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/changesets/nope/validate", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404, body: %s", rec.Code, rec.Body.String())
	}
}

// --- T-701 acceptance criterion 5: pve_session_required fail-fast --------

// panicNodeAgent implements change.NodeAgent with every method panicking —
// wired into an apply-configured *change.Service purely so
// applyConfigured() returns true (letting this test reach the new
// pve_session_required pre-check instead of the unrelated
// ErrApplyNotConfigured 503 path); an sdn-carrying changeset's plan never
// has a per-node file step, so nothing should ever call it, and this test
// asserts apply is rejected before any step — including a node-file one —
// ever runs.
type panicNodeAgent struct{}

func (panicNodeAgent) ReadInterfaces(context.Context, string) (string, error) {
	panic("panicNodeAgent: ReadInterfaces unexpectedly called — apply should have been rejected pre-flight")
}
func (panicNodeAgent) StageInterfaces(context.Context, string, string) error {
	panic("panicNodeAgent: StageInterfaces unexpectedly called — apply should have been rejected pre-flight")
}
func (panicNodeAgent) ReloadInterfaces(context.Context, string) error {
	panic("panicNodeAgent: ReloadInterfaces unexpectedly called — apply should have been rejected pre-flight")
}
func (panicNodeAgent) DiscardStaged(context.Context, string) error {
	panic("panicNodeAgent: DiscardStaged unexpectedly called — apply should have been rejected pre-flight")
}

// noGatewayProvider always reports no PVE session available — models an
// expired/unrenewable ticket (T-701 root-cause analysis §5).
type noGatewayProvider struct{}

func (noGatewayProvider) GatewayFor(context.Context) (change.PVEGateway, bool) { return nil, false }

// newApplyConfiguredChangesetService is newChangesetTestService plus Nodes/
// Snapshots/Blobs wired so change.Service.applyConfigured() is true — this
// test needs Apply to get past the "not configured" 503 and reach the new
// pre-flight gateway check.
func newApplyConfiguredChangesetService(t *testing.T) *change.Service {
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
		Now:        func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}
	return svc
}

// TestChangesetsApply_PVESessionRequired is T-701 acceptance criterion 5:
// applying an sdn-carrying changeset with no resolvable PVEGateway is
// rejected up front with the stable code pve_session_required, and the
// changeset is left exactly as it was (no "failed" row, no attempted
// snapshot/mutation — panicNodeAgent above proves no step ever runs).
func TestChangesetsApply_PVESessionRequired(t *testing.T) {
	svc := newApplyConfiguredChangesetService(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fullCapsAuth("alice"), Topology: fakeTopologyService{},
		Changesets: svc, PVEGateways: noGatewayProvider{},
	})

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets", bytes.NewBufferString(
		`{"title":"sdn draft","ops":[{"op":"sdn.zone.create","target":"sdn-zone::z1","params":{"type":"simple"}},{"op":"sdn.apply","params":{}}]}`))
	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, createReq)
	var created changesetResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decoding create response: %v", err)
	}

	applyReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets/"+created.ID+"/apply", nil)
	applyRec := httptest.NewRecorder()
	r.ServeHTTP(applyRec, applyReq)
	if applyRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("apply status = %d, want 422, body: %s", applyRec.Code, applyRec.Body.String())
	}
	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(applyRec.Body).Decode(&errResp); err != nil {
		t.Fatalf("decoding error response: %v", err)
	}
	if errResp.Error.Code != "pve_session_required" {
		t.Errorf("error code = %q, want pve_session_required", errResp.Error.Code)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/changesets/"+created.ID, nil)
	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, getReq)
	var got changesetResponse
	if err := json.NewDecoder(getRec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding get response: %v", err)
	}
	if got.Status != "draft" {
		t.Errorf("status after rejected apply = %q, want draft (unchanged — no failed row)", got.Status)
	}
}

// TestChangesetsApply_NoGatewayButNoSDNSteps proves the pre-check is
// narrowly scoped to plans that actually need a PVEGateway: a node-file-only
// changeset with no live session still reaches svc.Apply and its real
// pre-apply-snapshot step — panicNodeAgent's ReadInterfaces panics there,
// which the router's own panic-recovery middleware turns into a 500
// internal_error rather than propagating — proving apply proceeded past
// the pre-check instead of being short-circuited with
// pve_session_required, which this changeset (no sdn/fw/ipam ops) must
// not be.
func TestChangesetsApply_NoGatewayButNoSDNSteps(t *testing.T) {
	svc := newApplyConfiguredChangesetService(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fullCapsAuth("alice"), Topology: fakeTopologyService{},
		Changesets: svc, PVEGateways: noGatewayProvider{},
	})

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets", bytes.NewBufferString(
		`{"title":"iface draft","ops":[{"op":"bridge.create","target":"bridge:pve1:vmbr7","params":{"mtu":1500,"comments":"x"}}]}`))
	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, createReq)
	var created changesetResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decoding create response: %v", err)
	}

	applyReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets/"+created.ID+"/apply", nil)
	applyRec := httptest.NewRecorder()
	r.ServeHTTP(applyRec, applyReq)
	if applyRec.Code == http.StatusUnprocessableEntity {
		t.Fatalf("apply status = 422 (%s) — a node-file-only changeset must not be blocked by the sdn/fw/ipam-only pve_session_required pre-check", applyRec.Body.String())
	}
	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(applyRec.Body).Decode(&errResp); err == nil && errResp.Error.Code == "pve_session_required" {
		t.Fatalf("apply returned pve_session_required for a node-file-only changeset")
	}
}

// TestChangesetsApplyStrategy_Route is T-2602's API-layer contract coverage:
// `applyStrategy` decodes on the apply body and its refusal reaches the
// caller as the documented `422 invalid_apply_strategy` envelope, and
// `POST /changesets/{id}/continue` refuses a changeset that is not paused
// between stages with `409 invalid_transition`.
//
// The engine-level behaviour (which nodes are touched, what an abort
// restores, restart recovery) is asserted in internal/change's own suite
// against a real node agent; this test exists so the wire contract other
// callers depend on cannot drift silently.
func TestChangesetsApplyStrategy_Route(t *testing.T) {
	svc := newStagedChangesetTestService(t)
	r := newChangesetTestRouter(svc, fullCapsAuth("alice"))

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets", bytes.NewBufferString(`{"title":"draft","ops":[]}`))
	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, createReq)
	var created changesetResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decoding create response: %v", err)
	}
	if created.ApplyStage != nil {
		t.Error("a freshly created draft must carry no applyStage")
	}

	// An unknown mode is refused with the strategy's own stable code — never
	// folded into validation_failed or invalid_transition.
	body := `{"confirmTimeoutSec":120,"applyStrategy":{"mode":"rolling"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/changesets/"+created.ID+"/apply", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("apply with an unknown mode: status = %d, want 422 (body %s)", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decoding error envelope: %v", err)
	}
	if envelope.Error.Code != "invalid_apply_strategy" {
		t.Errorf("error code = %q, want %q", envelope.Error.Code, "invalid_apply_strategy")
	}

	// Continuing a changeset that is not paused between stages is a refusal,
	// not a second apply.
	contReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets/"+created.ID+"/continue", nil)
	contRec := httptest.NewRecorder()
	r.ServeHTTP(contRec, contReq)
	if contRec.Code != http.StatusConflict {
		t.Fatalf("continue on a draft: status = %d, want 409 (body %s)", contRec.Code, contRec.Body.String())
	}
}

// newStagedChangesetTestService is newChangesetTestService with the apply
// engine and T-2602's stage store wired, so the strategy gate is reached
// rather than short-circuited by apply_unavailable.
func newStagedChangesetTestService(t *testing.T) *change.Service {
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
		Stages:     store.NewChangesetStageRepo(db),
		Nodes:      stubNodeAgent{},
		Now:        func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}
	// An apply that returns 202 arms a commit-confirm timer whose callback
	// runs on its own goroutine, outliving the test unless it is stopped. Left
	// armed, it fires after t.Cleanup has closed the database and logs
	// "auto-rollback: loading changeset ... sql: database is closed" — noise
	// that reads exactly like a real failure to load a changeset.
	//
	// Registered BEFORE the db.Close cleanup above runs (cleanups run
	// last-registered-first), so the timers are stopped while the database is
	// still open. The daemon does the same thing at shutdown via StopTimers;
	// this is the test harness catching up with it, not a production defect.
	t.Cleanup(svc.StopTimers)
	return svc
}

// stubNodeAgent satisfies change.NodeAgent so the apply engine counts as
// configured. No test in this file reaches a step that calls it: the
// strategy gate runs before any snapshot or mutation, which is the property
// under test.
type stubNodeAgent struct{}

func (stubNodeAgent) ReadInterfaces(context.Context, string) (string, error) { return "", nil }
func (stubNodeAgent) StageInterfaces(context.Context, string, string) error  { return nil }
func (stubNodeAgent) ReloadInterfaces(context.Context, string) error         { return nil }
func (stubNodeAgent) DiscardStaged(context.Context, string) error            { return nil }

// TestChangesetsApply_AutoRollbackOnErrorRoute is T-2603's API-layer contract:
// `autoRollbackOnError` decodes on the apply body, reaches
// change.Service.ApplyWithOptions, and carries any `applyStrategy` named
// alongside it rather than silently replacing it.
//
// The engine-level behaviour (what arms, what triggers, what never does) is
// asserted in internal/change's own suite against a real node agent and a real
// findings stream; this test exists so the wire contract cannot drift.
func TestChangesetsApply_AutoRollbackOnErrorRoute(t *testing.T) {
	svc := newStagedChangesetTestService(t)
	r := newChangesetTestRouter(svc, fullCapsAuth("alice"))

	create := func(t *testing.T) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/changesets", bytes.NewBufferString(`{"title":"draft","ops":[]}`))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		var created changesetResponse
		if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
			t.Fatalf("decoding create response: %v", err)
		}
		return created.ID
	}

	// The flag alone applies normally — proving the field is accepted by the
	// strict decoder and routed to a service that understands it (a body with
	// an unknown field is a 400, so a 202 here IS the routing assertion).
	id := create(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/changesets/"+id+"/apply",
		bytes.NewBufferString(`{"confirmTimeoutSec":120,"autoRollbackOnError":true}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("apply with autoRollbackOnError: status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
	}

	// The flag does not swallow the strategy travelling with it: an
	// unhonourable strategy is still refused with its own stable code.
	//
	// On a SEPARATE service, deliberately. The apply above returns 202 and
	// holds the engine's apply lock while it runs; the lock is checked before
	// the strategy is validated, so on a loaded machine the request below
	// raced it and got 409 changeset_locked instead of 422 — which is what
	// failed in CI while passing locally and 5/5 in isolation. Waiting for the
	// first apply to release would make this a timing test; giving the
	// strategy assertion its own engine removes the coupling entirely, and
	// keeps it true no matter what an earlier assertion in this test does
	// later.
	otherSvc := newStagedChangesetTestService(t)
	otherRouter := newChangesetTestRouter(otherSvc, fullCapsAuth("alice"))
	otherReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets",
		bytes.NewBufferString(`{"title":"draft","ops":[]}`))
	otherRec := httptest.NewRecorder()
	otherRouter.ServeHTTP(otherRec, otherReq)
	var otherCreated changesetResponse
	if err := json.NewDecoder(otherRec.Body).Decode(&otherCreated); err != nil {
		t.Fatalf("decoding create response on the second service: %v", err)
	}
	other := otherCreated.ID
	r = otherRouter
	req = httptest.NewRequest(http.MethodPost, "/api/v1/changesets/"+other+"/apply",
		bytes.NewBufferString(`{"confirmTimeoutSec":120,"autoRollbackOnError":true,"applyStrategy":{"mode":"rolling"}}`))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("apply with autoRollbackOnError + a bad strategy: status = %d, want 422 (body %s)", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decoding error envelope: %v", err)
	}
	if envelope.Error.Code != "invalid_apply_strategy" {
		t.Errorf("error code = %q, want %q — the strategy must still be validated when autoRollbackOnError is set", envelope.Error.Code, "invalid_apply_strategy")
	}
}

// TestChangesetsRoutes_MCPProvenanceSurvivesToTheReviewAPI is T-2705
// acceptance criterion 4's HTTP half: a changeset staged by an MCP tool
// carries `origin`, `originTokenId`, AND `originTool` on the canonical review
// read, so a reviewer can see which AI-operator action produced the draft —
// and an ordinary UI-staged changeset carries none of the token/tool fields at
// all (the field is omitempty, so no existing response's byte shape changed).
func TestChangesetsRoutes_MCPProvenanceSurvivesToTheReviewAPI(t *testing.T) {
	svc := newChangesetTestService(t)
	r := newChangesetTestRouter(svc, fullCapsAuth("alice"))
	ctx := context.Background()

	staged, err := svc.CreateWithProvenance(ctx, "mcp:ci-bot", "ai-drafted", nil, change.Provenance{
		Origin: change.OriginMCP, TokenID: "tok-7", Tool: "changesets.stage.bridge",
	})
	if err != nil {
		t.Fatalf("CreateWithProvenance: %v", err)
	}
	human, err := svc.Create(ctx, "alice", "human-drafted", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	cases := []struct {
		name       string
		id         string
		wantOrigin string
		wantTool   string
		wantToken  string
	}{
		{"mcp-staged", staged.ID, change.OriginMCP, "changesets.stage.bridge", "tok-7"},
		{"ui-staged", human.ID, change.OriginUI, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/changesets/"+tc.id, nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET /changesets/{id} status = %d, want 200, body: %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			var got changesetResponse
			if derr := json.NewDecoder(strings.NewReader(body)).Decode(&got); derr != nil {
				t.Fatalf("decoding response: %v", derr)
			}
			if got.Origin != tc.wantOrigin {
				t.Errorf("origin = %q, want %q", got.Origin, tc.wantOrigin)
			}
			if got.OriginTool != tc.wantTool {
				t.Errorf("originTool = %q, want %q", got.OriginTool, tc.wantTool)
			}
			if got.OriginTokenID != tc.wantToken {
				t.Errorf("originTokenId = %q, want %q", got.OriginTokenID, tc.wantToken)
			}
			// The wire bytes, not only the decoded struct: an omitempty field
			// must actually be absent for a UI-staged changeset.
			if tc.wantTool == "" && strings.Contains(body, "originTool") {
				t.Errorf("UI-staged changeset response carries an originTool key: %s", body)
			}
			if tc.wantTool != "" && !strings.Contains(body, `"originTool":"`+tc.wantTool+`"`) {
				t.Errorf("MCP-staged changeset response is missing originTool: %s", body)
			}
		})
	}
}
