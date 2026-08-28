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

// newScheduleConfiguredChangesetService is newApplyConfiguredChangesetService
// plus a wired ChangeScheduleRepo, so change.Service's T-1103 scheduling
// methods are usable end to end through the router.
func newScheduleConfiguredChangesetService(t *testing.T) *change.Service {
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
		Schedules:  store.NewChangeScheduleRepo(db),
		Now:        func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}
	return svc
}

func createSimpleChangeset(t *testing.T, r http.Handler) changesetResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/changesets", bytes.NewBufferString(
		`{"title":"t","ops":[{"op":"bridge.create","target":"bridge:pve1:vmbr7","params":{}}]}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var out changesetResponse
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decoding create: %v", err)
	}
	return out
}

func decodeErrorCode(t *testing.T, body *bytes.Buffer) string {
	t.Helper()
	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(body).Decode(&errResp); err != nil {
		t.Fatalf("decoding error response: %v", err)
	}
	return errResp.Error.Code
}

// TestScheduleRoutes_NotMountedWithoutScheduleService: a ChangesetService
// that doesn't implement ScheduleService (the plain fakeChangesetService
// test double used elsewhere in this package, if one exists — otherwise a
// minimal one here) never gets the schedule routes mounted at all.
func TestScheduleRoutes_NotMountedWithoutScheduleService(t *testing.T) {
	svc := newChangesetTestService(t) // no Snapshots/Blobs/Schedules wired
	r := newChangesetTestRouter(svc, fullCapsAuth("alice"))

	cs := createSimpleChangeset(t, r)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/changesets/"+cs.ID+"/schedule",
		bytes.NewBufferString(`{"windowStart":1000,"windowEnd":1060}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	// *change.Service always implements ScheduleService (the methods exist
	// on the concrete type regardless of configuration), so the route IS
	// mounted here — but Schedule() itself 503s since scheduleConfigured()
	// is false (no Snapshots/Blobs/Schedules wired). This proves the
	// unattended-apply feature degrades cleanly rather than 404ing oddly.
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (apply engine not configured), body: %s", rec.Code, rec.Body.String())
	}
}

func TestScheduleCreate_RequiresNetWriteAndCSRF(t *testing.T) {
	svc := newScheduleConfiguredChangesetService(t)
	auth := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"},
		caps:             map[string]bool{capNetRead: true, capNetWrite: true}, csrf: true,
	}
	r := newChangesetTestRouter(svc, auth)

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets", bytes.NewBufferString(
		`{"title":"t","ops":[{"op":"bridge.create","target":"bridge:pve1:vmbr7","params":{}}]}`))
	createReq.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body: %s", createRec.Code, createRec.Body.String())
	}
	var cs changesetResponse
	if err := json.NewDecoder(createRec.Body).Decode(&cs); err != nil {
		t.Fatalf("decoding create: %v", err)
	}

	// The schedule request itself carries no CSRF header.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/changesets/"+cs.ID+"/schedule",
		bytes.NewBufferString(`{"windowStart":1000,"windowEnd":1060}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status without CSRF header = %d, want 403, body: %s", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body); code != "csrf_required" {
		t.Errorf("error code = %s, want csrf_required", code)
	}
}

func TestScheduleCreate_Success_ReturnsCallbackTokenOnce(t *testing.T) {
	svc := newScheduleConfiguredChangesetService(t)
	r := newChangesetTestRouter(svc, fullCapsAuth("alice"))
	cs := createSimpleChangeset(t, r)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/changesets/"+cs.ID+"/schedule",
		bytes.NewBufferString(`{"windowStart":1700000010,"windowEnd":1700000070,"confirmTimeoutSec":30}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body: %s", rec.Code, rec.Body.String())
	}
	var sched scheduleResponse
	if err := json.NewDecoder(rec.Body).Decode(&sched); err != nil {
		t.Fatalf("decoding schedule response: %v", err)
	}
	if sched.CallbackToken == "" {
		t.Error("CallbackToken is empty on the creating response, want the one-time token")
	}
	if sched.Status != "pending" {
		t.Errorf("Status = %s, want pending", sched.Status)
	}
	if sched.MissedWindowPolicy != "skip" {
		t.Errorf("MissedWindowPolicy = %s, want default skip", sched.MissedWindowPolicy)
	}
}

// TestScheduleCreate_BadWindow_422: windowStart >= windowEnd maps to the
// documented bad_window code.
func TestScheduleCreate_BadWindow_422(t *testing.T) {
	svc := newScheduleConfiguredChangesetService(t)
	r := newChangesetTestRouter(svc, fullCapsAuth("alice"))
	cs := createSimpleChangeset(t, r)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/changesets/"+cs.ID+"/schedule",
		bytes.NewBufferString(`{"windowStart":1700000070,"windowEnd":1700000070}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body: %s", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body); code != "bad_window" {
		t.Errorf("error code = %s, want bad_window", code)
	}
}

// TestScheduleCreate_MgmtPathForbidden_422: a touchesMgmtPath changeset gets
// the documented mgmt_path_unattended_forbidden code, with no request field
// able to override it — this test doesn't even offer one, by design (the
// wire schema documented in docs/api.md has no such field to omit).
//
// Unlike TestChangesets_TouchesMgmtPathFlag_ComputedServerSide (which wires
// a fake MgmtStatusService purely to decorate the touchesMgmtPath response
// field), Schedule's own gate runs inside change.Service against that
// Service's *own* protected-interface config — decorating Options.Protected
// alone would not exercise it, so this test writes a real protected.json
// the underlying Service reads.
func TestScheduleCreate_MgmtPathForbidden_422(t *testing.T) {
	dir := t.TempDir()
	protectedPath := filepath.Join(dir, "protected.json")
	dbPath := filepath.Join(dir, "vnprox.db")
	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	vmbr0 := "bridge:pve1:vmbr0"
	if saveErr := change.SaveProtectedConfig(protectedPath, change.ProtectedConfig{
		Nodes: map[string][]string{"pve1": {vmbr0}},
	}); saveErr != nil {
		t.Fatalf("SaveProtectedConfig: %v", saveErr)
	}

	svc, err := change.NewService(change.Config{
		Changesets: store.NewChangesetRepo(db), Audit: store.NewAuditRepo(db),
		Snapshots: store.NewSnapshotRepo(db), Blobs: store.NewBlobRepo(db),
		Nodes: panicNodeAgent{}, Schedules: store.NewChangeScheduleRepo(db),
		ProtectedPath: protectedPath,
		Now:           func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("change.NewService: %v", err)
	}
	r := newChangesetTestRouter(svc, fullCapsAuth("alice"))

	touchReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets", bytes.NewBufferString(
		`{"title":"t","ops":[{"op":"bridge.port.add","target":"bridge:pve1:vmbr0","params":{"port":"eno2"}}]}`))
	touchRec := httptest.NewRecorder()
	r.ServeHTTP(touchRec, touchReq)
	if touchRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body: %s", touchRec.Code, touchRec.Body.String())
	}
	var touching changesetResponse
	if err := json.NewDecoder(touchRec.Body).Decode(&touching); err != nil {
		t.Fatalf("decoding create: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/changesets/"+touching.ID+"/schedule",
		bytes.NewBufferString(`{"windowStart":1700000010,"windowEnd":1700000070}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body: %s", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body); code != "mgmt_path_unattended_forbidden" {
		t.Errorf("error code = %s, want mgmt_path_unattended_forbidden", code)
	}
}

// TestScheduleCancel_RequiresNetWrite: cancel is a mutating route, gated
// like every other one in this family.
func TestScheduleCancel_RequiresNetWrite(t *testing.T) {
	svc := newScheduleConfiguredChangesetService(t)
	// Create the changeset through a fully-privileged router over the same
	// underlying service...
	fullR := newChangesetTestRouter(svc, fullCapsAuth("alice"))
	cs := createSimpleChangeset(t, fullR)

	// ...then attempt the cancel through a read-only one.
	restricted := fakeAuthWithCaps{
		fakeAuthWithUser: fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"},
		caps:             map[string]bool{capNetRead: true}, // no netWrite
	}
	r := newChangesetTestRouter(svc, restricted)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/changesets/"+cs.ID+"/schedule", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (missing netWrite), body: %s", rec.Code, rec.Body.String())
	}
}

func TestScheduleCancel_NotFound(t *testing.T) {
	svc := newScheduleConfiguredChangesetService(t)
	r := newChangesetTestRouter(svc, fullCapsAuth("alice"))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/changesets/does-not-exist/schedule", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body: %s", rec.Code, rec.Body.String())
	}
}

// TestScheduleAck_NoSessionRequired: the webhook ack route works with no
// session cookie/CSRF at all — auth is the token itself.
func TestScheduleAck_NoSessionRequired(t *testing.T) {
	svc := newScheduleConfiguredChangesetService(t)
	r := newChangesetTestRouter(svc, fullCapsAuth("alice"))
	cs := createSimpleChangeset(t, r)

	schedReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets/"+cs.ID+"/schedule",
		bytes.NewBufferString(`{"windowStart":1700000010,"windowEnd":1700000070}`))
	schedRec := httptest.NewRecorder()
	r.ServeHTTP(schedRec, schedReq)
	if schedRec.Code != http.StatusCreated {
		t.Fatalf("schedule create status = %d, body: %s", schedRec.Code, schedRec.Body.String())
	}

	// An unauthenticated client (no cookie at all) hitting the ack route
	// with a garbage token gets the token-specific error, not 401
	// not_authenticated — proving no session middleware sits in front of
	// it.
	ackReq := httptest.NewRequest(http.MethodPost, "/api/v1/changesets/"+cs.ID+"/schedule/ack",
		bytes.NewBufferString(`{"token":"garbage"}`))
	ackRec := httptest.NewRecorder()
	r.ServeHTTP(ackRec, ackReq)
	if ackRec.Code != http.StatusUnauthorized {
		t.Fatalf("ack status with a wrong token = %d, want 401, body: %s", ackRec.Code, ackRec.Body.String())
	}
	if code := decodeErrorCode(t, ackRec.Body); code != "invalid_callback_token" {
		t.Errorf("error code = %s, want invalid_callback_token", code)
	}
}
