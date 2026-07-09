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
	if got.Error.Details["path"] != "op" {
		t.Errorf("error.details.path = %v, want %q", got.Error.Details["path"], "op")
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
	if got.Error.Details["path"] != "params.mtuu" {
		t.Errorf("error.details.path = %v, want %q", got.Error.Details["path"], "params.mtuu")
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

// TestChangesetsStubRoutes_501 proves validate/diff/apply/confirm/rollback
// are registered (matching docs/api.md's route shape) but return 501 until
// T-202/T-205 land.
func TestChangesetsStubRoutes_501(t *testing.T) {
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
		"/api/v1/changesets/" + created.ID + "/validate",
		"/api/v1/changesets/" + created.ID + "/apply",
		"/api/v1/changesets/" + created.ID + "/confirm",
		"/api/v1/changesets/" + created.ID + "/rollback",
	} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("POST %s: status = %d, want 501", path, rec.Code)
		}
	}

	diffReq := httptest.NewRequest(http.MethodGet, "/api/v1/changesets/"+created.ID+"/diff", nil)
	diffRec := httptest.NewRecorder()
	r.ServeHTTP(diffRec, diffReq)
	if diffRec.Code != http.StatusNotImplemented {
		t.Errorf("GET diff: status = %d, want 501", diffRec.Code)
	}
}
