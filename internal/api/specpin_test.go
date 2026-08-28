// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/bgovanlu/vnprox/internal/store"
)

// newSpecPinTestStores builds a real *store.PinnedSpecRepo + *store.AuditRepo
// over a temp SQLite file — the same "small interface, real type satisfies
// it for free" approach newBlueprintTestService uses, rather than a hand
// rolled fake, since both repos are trivial and this exercises the actual
// migration end to end.
func newSpecPinTestStores(t *testing.T) (*store.PinnedSpecRepo, *store.AuditRepo) {
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
	return store.NewPinnedSpecRepo(db), store.NewAuditRepo(db)
}

func TestSpecPinRoute_Unauthenticated401(t *testing.T) {
	pinStore, audit := newSpecPinTestStores(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: false}, SpecPin: pinStore, SpecPinAudit: audit,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/spec/pin", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestGetSpecPin_NothingPinned(t *testing.T) {
	pinStore, audit := newSpecPinTestStores(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: blueprintTestAuth(map[string]bool{"netRead": true}), SpecPin: pinStore, SpecPinAudit: audit,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/spec/pin", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var got specPinResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Pinned {
		t.Errorf("Pinned = true, want false (nothing pinned yet)")
	}
	if got.Content != "" || got.PinnedBy != "" || got.PinnedAt != 0 {
		t.Errorf("got = %+v, want every other field empty when unpinned", got)
	}
}

// TestPinSpec_RoundTrip covers the pin -> GET -> unpin -> GET cycle
// (AC1/AC4's API-level half), including that POST/DELETE are both audited
// (spec.pin/spec.unpin) with the acting user recorded.
func TestPinSpec_RoundTrip(t *testing.T) {
	pinStore, audit := newSpecPinTestStores(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: blueprintTestAuth(map[string]bool{"netRead": true, "netWrite": true}), SpecPin: pinStore, SpecPinAudit: audit,
	})

	doc := "specVersion: 1\n" +
		"nodes:\n" +
		"  - name: pve1\n" +
		"    bridges:\n" +
		"      - name: vmbr0\n" +
		"        mtu: 1500\n"
	body, _ := json.Marshal(specPinRequest{Content: doc})
	postReq := httptest.NewRequest(http.MethodPost, "/api/v1/spec/pin", bytes.NewReader(body))
	postReq.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	postRec := httptest.NewRecorder()
	r.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, body: %s", postRec.Code, postRec.Body.String())
	}
	var posted specPinResponse
	if err := json.Unmarshal(postRec.Body.Bytes(), &posted); err != nil {
		t.Fatalf("decode POST response: %v", err)
	}
	if !posted.Pinned || posted.Content != doc || posted.PinnedBy != "root@pam" || posted.PinnedAt == 0 {
		t.Errorf("POST response = %+v, want the pinned content/user/timestamp", posted)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/spec/pin", nil)
	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, getReq)
	var got specPinResponse
	if err := json.Unmarshal(getRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if got != posted {
		t.Errorf("GET after POST = %+v, want it to match the POST response %+v", got, posted)
	}

	entries, err := audit.List(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("audit.List: %v", err)
	}
	if len(entries) != 1 || entries[0].Action != "spec.pin" || entries[0].Username != "root@pam" {
		t.Fatalf("audit entries = %+v, want one spec.pin row by root@pam", entries)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/spec/pin", nil)
	delReq.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	delRec := httptest.NewRecorder()
	r.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, body: %s", delRec.Code, delRec.Body.String())
	}

	getReq2 := httptest.NewRequest(http.MethodGet, "/api/v1/spec/pin", nil)
	getRec2 := httptest.NewRecorder()
	r.ServeHTTP(getRec2, getReq2)
	var afterUnpin specPinResponse
	if unmarshalErr := json.Unmarshal(getRec2.Body.Bytes(), &afterUnpin); unmarshalErr != nil {
		t.Fatalf("decode GET-after-DELETE response: %v", unmarshalErr)
	}
	if afterUnpin.Pinned {
		t.Errorf("Pinned = true after DELETE, want false")
	}

	entries2, err := audit.List(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("audit.List (after unpin): %v", err)
	}
	if len(entries2) != 2 {
		t.Fatalf("audit entries after unpin = %d, want 2 (spec.pin + spec.unpin)", len(entries2))
	}
	var sawUnpin bool
	for _, e := range entries2 {
		if e.Action == "spec.unpin" && e.Username == "root@pam" {
			sawUnpin = true
		}
	}
	if !sawUnpin {
		t.Errorf("audit entries = %+v, want a spec.unpin row by root@pam", entries2)
	}
}

func TestPinSpec_RejectsUnknownVersion(t *testing.T) {
	pinStore, audit := newSpecPinTestStores(t)
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: blueprintTestAuth(map[string]bool{"netRead": true, "netWrite": true}), SpecPin: pinStore, SpecPinAudit: audit,
	})
	body, _ := json.Marshal(specPinRequest{Content: "specVersion: 2\n"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/spec/pin", bytes.NewReader(body))
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (unknown specVersion)", rec.Code)
	}

	// A rejected pin attempt must not have been stored.
	if _, err := pinStore.Get(context.Background()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("pinStore.Get error = %v, want ErrNotFound (nothing should have been pinned)", err)
	}

	// A malformed/invalid request is not audited (matching the codebase-wide
	// convention docs/api.md's Live path probe section documents).
	entries, err := audit.List(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("audit.List: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("audit entries = %+v, want none (rejected pin should not be audited)", entries)
	}
}

func TestPinSpec_RequiresNetWriteAndCSRF(t *testing.T) {
	pinStore, audit := newSpecPinTestStores(t)
	body, _ := json.Marshal(specPinRequest{Content: "specVersion: 1\n"})

	// netRead only: the write route must 403.
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: blueprintTestAuth(map[string]bool{"netRead": true}), SpecPin: pinStore, SpecPinAudit: audit,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/spec/pin", bytes.NewReader(body))
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (missing netWrite)", rec.Code)
	}

	// netWrite but no CSRF header: still 403.
	r2 := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: blueprintTestAuth(map[string]bool{"netRead": true, "netWrite": true}), SpecPin: pinStore, SpecPinAudit: audit,
	})
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/spec/pin", bytes.NewReader(body))
	rec2 := httptest.NewRecorder()
	r2.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (missing CSRF)", rec2.Code)
	}
}
