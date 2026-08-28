// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/bgovanlu/vnprox/internal/ifcounters"
	"github.com/bgovanlu/vnprox/internal/store"
)

// This file is T-4013's HTTP-level coverage for GET /snmp/counters and the
// /snmp/targets CRUD surface — internal/ifcounters' own poll/correlation
// logic is covered by that package's tests; this file proves the router
// mounts the routes correctly gated and that the community string is never
// echoed back over the wire (reusing fakeSecretCipher from
// alertrules_test.go, alongside this package's fakeAuth/fakeAuthWithUser/
// fakeAuthWithCaps test doubles).

type fakeIfCountersService struct {
	results []ifcounters.Result
}

func (f *fakeIfCountersService) Results() []ifcounters.Result { return f.results }

// fakeIfCounterTargetStore is an in-memory IfCounterTargetStore test double,
// mirroring fakeLayoutStore's shape.
type fakeIfCounterTargetStore struct {
	data map[string]store.SwitchSNMPTarget
}

func newFakeIfCounterTargetStore() *fakeIfCounterTargetStore {
	return &fakeIfCounterTargetStore{data: map[string]store.SwitchSNMPTarget{}}
}

func (f *fakeIfCounterTargetStore) List(context.Context) ([]store.SwitchSNMPTarget, error) {
	out := make([]store.SwitchSNMPTarget, 0, len(f.data))
	for _, t := range f.data {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ChassisID < out[j].ChassisID })
	return out, nil
}

func (f *fakeIfCounterTargetStore) GetByChassisID(_ context.Context, chassisID string) (store.SwitchSNMPTarget, error) {
	t, ok := f.data[chassisID]
	if !ok {
		return store.SwitchSNMPTarget{}, store.ErrNotFound
	}
	return t, nil
}

func (f *fakeIfCounterTargetStore) Insert(_ context.Context, t store.SwitchSNMPTarget) error {
	f.data[t.ChassisID] = t
	return nil
}

func (f *fakeIfCounterTargetStore) Update(_ context.Context, t store.SwitchSNMPTarget) error {
	if _, ok := f.data[t.ChassisID]; !ok {
		return store.ErrNotFound
	}
	f.data[t.ChassisID] = t
	return nil
}

func (f *fakeIfCounterTargetStore) DeleteByChassisID(_ context.Context, chassisID string) error {
	delete(f.data, chassisID)
	return nil
}

func ifCountersTestAuth(caps map[string]bool) fakeAuthWithCaps {
	return fakeAuthWithCaps{
		caps: caps, csrf: true,
		fakeAuthWithUser: fakeAuthWithUser{username: "root@pam", fakeAuth: fakeAuth{authenticated: true}},
	}
}

func ifCountersTestRouter(svc IfCountersService, targets IfCounterTargetStore, caps map[string]bool) http.Handler {
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: ifCountersTestAuth(caps), IfCounters: svc, IfCounterTargets: targets,
		IfCounterSecretCipher: fakeSecretCipher{},
	})
}

func TestIfCounterResults_ReturnsItems(t *testing.T) {
	svc := &fakeIfCountersService{results: []ifcounters.Result{
		{ChassisID: "aa:bb", SwitchName: "sw-aa", Node: "pve1", LocalIface: "eth0",
			SwitchPort: "24", State: ifcounters.StateOK, Counters: ifcounters.Counters{InErrors: 5, OperUp: true}, At: 100},
	}}
	r := ifCountersTestRouter(svc, newFakeIfCounterTargetStore(), map[string]bool{"netRead": true})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/snmp/counters", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []ifCounterResultResponse `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("items = %+v, want 1", body.Items)
	}
	got := body.Items[0]
	if got.ChassisID != "aa:bb" || got.State != "ok" || got.InErrors != 5 || !got.OperUp {
		t.Fatalf("unexpected item shape: %+v", got)
	}
}

func TestIfCounterResults_EmptyNotNull(t *testing.T) {
	r := ifCountersTestRouter(&fakeIfCountersService{}, newFakeIfCounterTargetStore(), map[string]bool{"netRead": true})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/snmp/counters", nil))
	var body struct {
		Items []ifCounterResultResponse `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if body.Items == nil {
		t.Fatal("items must be [] not null")
	}
}

func TestIfCounterRoutes_NilServiceSkipsMountingCounters(t *testing.T) {
	r := ifCountersTestRouter(nil, newFakeIfCounterTargetStore(), map[string]bool{"netRead": true})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/snmp/counters", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when IfCounters is nil", rec.Code)
	}
}

func TestIfCounterRoutes_NilTargetsSkipsMountingTargets(t *testing.T) {
	r := ifCountersTestRouter(&fakeIfCountersService{}, nil, map[string]bool{"netRead": true, "netWrite": true})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/snmp/targets", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when IfCounterTargets is nil", rec.Code)
	}
}

func TestIfCounterTargets_RequireAuth(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: false}, IfCounters: &fakeIfCountersService{},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/snmp/counters", nil))
	if rec.Code == http.StatusOK {
		t.Fatalf("unauthenticated request got 200, want a rejection")
	}
}

func TestIfCounterTargets_PutCreatesAndNeverEchoesCommunity(t *testing.T) {
	targets := newFakeIfCounterTargetStore()
	r := ifCountersTestRouter(&fakeIfCountersService{}, targets, map[string]bool{"netRead": true, "netWrite": true})

	body := `{"chassisIdType":"mac-address","mgmtAddr":"10.0.0.9","port":161,"enabled":true,"community":"s3cr3t-community"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/snmp/targets/aa:bb:cc:dd:ee:ff", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("s3cr3t-community")) {
		t.Fatalf("PUT response leaked the plaintext community string: %s", rec.Body.String())
	}
	var resp ifCounterTargetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp.ChassisID != "aa:bb:cc:dd:ee:ff" || !resp.Enabled || !resp.HasCommunity || resp.Port != 161 {
		t.Fatalf("unexpected response shape: %+v", resp)
	}

	// Storage-level opacity (the real AES-256-GCM cipher, not this test's
	// prefix-based fake) is covered by
	// TestSwitchSNMPTargetRepo_CommunityEncryptedAtRest in internal/store;
	// this test's job is only the wire contract above (response body).
	if _, err := targets.GetByChassisID(context.Background(), "aa:bb:cc:dd:ee:ff"); err != nil {
		t.Fatalf("GetByChassisID: %v", err)
	}
}

func TestIfCounterTargets_PutUpdateLeavesCommunityUntouchedWhenOmitted(t *testing.T) {
	targets := newFakeIfCounterTargetStore()
	cipher := fakeSecretCipher{}
	enc, err := cipher.Encrypt([]byte("original-community"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	targets.data["aa:bb"] = store.SwitchSNMPTarget{
		ID: "t1", ChassisID: "aa:bb", MgmtAddr: "10.0.0.1", Port: 161,
		CommunityEnc: enc, Enabled: false, AddedBy: "root@pam", AddedAt: 100,
	}
	r := ifCountersTestRouter(&fakeIfCountersService{}, targets, map[string]bool{"netRead": true, "netWrite": true})

	// No "community" field at all in this update — must leave it untouched.
	body := `{"mgmtAddr":"10.0.0.2","port":1161,"enabled":true}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/snmp/targets/aa:bb", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	stored, err := targets.GetByChassisID(context.Background(), "aa:bb")
	if err != nil {
		t.Fatalf("GetByChassisID: %v", err)
	}
	if !bytes.Equal(stored.CommunityEnc, enc) {
		t.Errorf("community_enc changed on an update that omitted the community field")
	}
	if stored.MgmtAddr != "10.0.0.2" || stored.Port != 1161 || !stored.Enabled {
		t.Errorf("unexpected stored target: %+v", stored)
	}
}

func TestIfCounterTargets_PutEmptyStringClearsCommunity(t *testing.T) {
	targets := newFakeIfCounterTargetStore()
	cipher := fakeSecretCipher{}
	enc, _ := cipher.Encrypt([]byte("original-community"))
	targets.data["aa:bb"] = store.SwitchSNMPTarget{
		ID: "t1", ChassisID: "aa:bb", Enabled: true, CommunityEnc: enc, AddedBy: "root@pam", AddedAt: 100,
	}
	r := ifCountersTestRouter(&fakeIfCountersService{}, targets, map[string]bool{"netRead": true, "netWrite": true})

	body := `{"enabled":false,"community":""}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/snmp/targets/aa:bb", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	stored, err := targets.GetByChassisID(context.Background(), "aa:bb")
	if err != nil {
		t.Fatalf("GetByChassisID: %v", err)
	}
	if len(stored.CommunityEnc) != 0 {
		t.Errorf("community_enc = %q, want cleared", stored.CommunityEnc)
	}
}

func TestIfCounterTargets_List_NeverIncludesCommunity(t *testing.T) {
	targets := newFakeIfCounterTargetStore()
	cipher := fakeSecretCipher{}
	enc, _ := cipher.Encrypt([]byte("plain-community-value"))
	targets.data["aa:bb"] = store.SwitchSNMPTarget{
		ID: "t1", ChassisID: "aa:bb", Enabled: true, CommunityEnc: enc, AddedBy: "root@pam", AddedAt: 100,
	}
	r := ifCountersTestRouter(&fakeIfCountersService{}, targets, map[string]bool{"netRead": true})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/snmp/targets", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("plain-community-value")) {
		t.Fatalf("GET /snmp/targets leaked the plaintext community string: %s", rec.Body.String())
	}
	var body struct {
		Items []ifCounterTargetResponse `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(body.Items) != 1 || !body.Items[0].HasCommunity {
		t.Fatalf("unexpected items: %+v", body.Items)
	}
}

func TestIfCounterTargets_Delete(t *testing.T) {
	targets := newFakeIfCounterTargetStore()
	targets.data["aa:bb"] = store.SwitchSNMPTarget{ID: "t1", ChassisID: "aa:bb", AddedBy: "root@pam", AddedAt: 100}
	r := ifCountersTestRouter(&fakeIfCountersService{}, targets, map[string]bool{"netRead": true, "netWrite": true})

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/snmp/targets/aa:bb", nil)
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if _, err := targets.GetByChassisID(context.Background(), "aa:bb"); err != store.ErrNotFound {
		t.Fatalf("GetByChassisID after Delete = %v, want ErrNotFound", err)
	}
}

func TestIfCounterTargets_WriteRequiresNetWriteCap(t *testing.T) {
	targets := newFakeIfCounterTargetStore()
	r := ifCountersTestRouter(&fakeIfCountersService{}, targets, map[string]bool{"netRead": true})

	body := `{"enabled":true}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/snmp/targets/aa:bb", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 without netWrite", rec.Code)
	}
}
