package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/store"
)

// fakeAlertRuleStore is an in-memory AlertRuleStore test double.
type fakeAlertRuleStore struct {
	items map[string]store.AlertRule
}

func newFakeAlertRuleStore() *fakeAlertRuleStore {
	return &fakeAlertRuleStore{items: map[string]store.AlertRule{}}
}

func (f *fakeAlertRuleStore) List(context.Context) ([]store.AlertRule, error) {
	var out []store.AlertRule
	for _, a := range f.items {
		out = append(out, a)
	}
	return out, nil
}

func (f *fakeAlertRuleStore) Get(_ context.Context, id string) (store.AlertRule, error) {
	a, ok := f.items[id]
	if !ok {
		return store.AlertRule{}, store.ErrNotFound
	}
	return a, nil
}

func (f *fakeAlertRuleStore) Insert(_ context.Context, a store.AlertRule) error {
	f.items[a.ID] = a
	return nil
}

func (f *fakeAlertRuleStore) Update(_ context.Context, a store.AlertRule) error {
	if _, ok := f.items[a.ID]; !ok {
		return store.ErrNotFound
	}
	f.items[a.ID] = a
	return nil
}

func (f *fakeAlertRuleStore) Delete(_ context.Context, id string) error {
	delete(f.items, id)
	return nil
}

// fakeAlertDeliveryStore is an in-memory AlertDeliveryStore test double.
type fakeAlertDeliveryStore struct {
	rows []store.AlertDelivery
}

func (f *fakeAlertDeliveryStore) Insert(_ context.Context, d store.AlertDelivery) error {
	f.rows = append(f.rows, d)
	return nil
}

func (f *fakeAlertDeliveryStore) List(_ context.Context, ruleID, status string) ([]store.AlertDelivery, error) {
	var out []store.AlertDelivery
	for _, d := range f.rows {
		if ruleID != "" && d.RuleID != ruleID {
			continue
		}
		if status != "" && d.Status != status {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

// fakeSecretCipher is a deterministic, insecure stand-in for
// *store.SessionCipher: Encrypt prefixes the plaintext so tests can assert
// the raw secret is never returned to a client, without needing real
// AES-GCM key material.
type fakeSecretCipher struct{ failEncrypt, failDecrypt bool }

func (c fakeSecretCipher) Encrypt(plaintext []byte) ([]byte, error) {
	if c.failEncrypt {
		return nil, errors.New("encrypt failed")
	}
	return append([]byte("enc:"), plaintext...), nil
}

func (c fakeSecretCipher) Decrypt(sealed []byte) ([]byte, error) {
	if c.failDecrypt {
		return nil, errors.New("decrypt failed")
	}
	if len(sealed) < 4 || string(sealed[:4]) != "enc:" {
		return nil, errors.New("not encrypted by fakeSecretCipher")
	}
	return sealed[4:], nil
}

func alertRuleTestAuth(caps map[string]bool) fakeAuthWithCaps {
	return fakeAuthWithCaps{
		caps: caps, csrf: true,
		fakeAuthWithUser: fakeAuthWithUser{username: "root@pam", fakeAuth: fakeAuth{authenticated: true}},
	}
}

func newAlertRulesTestRouter(t *testing.T, caps map[string]bool, rules *fakeAlertRuleStore, deliveries *fakeAlertDeliveryStore, cipher SecretCipher) http.Handler {
	t.Helper()
	return NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: alertRuleTestAuth(caps), Topology: fakeTopologyService{},
		AlertRules: rules, AlertDeliveries: deliveries, AlertSecretCipher: cipher,
	})
}

func TestAlertRulesRoutes_NotMountedWithoutCipher(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: alertRuleTestAuth(map[string]bool{"netRead": true}), Topology: fakeTopologyService{},
		AlertRules: newFakeAlertRuleStore(), AlertDeliveries: &fakeAlertDeliveryStore{},
		// AlertSecretCipher deliberately omitted.
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/alert-rules", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route should not be mounted)", rec.Code)
	}
}

func TestAlertRulesRoutes_Unauthenticated401(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: false}, Topology: fakeTopologyService{},
		AlertRules: newFakeAlertRuleStore(), AlertDeliveries: &fakeAlertDeliveryStore{}, AlertSecretCipher: fakeSecretCipher{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/alert-rules", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestAlertRulesRoutes_CreateRequiresNetWrite(t *testing.T) {
	rules := newFakeAlertRuleStore()
	r := newAlertRulesTestRouter(t, map[string]bool{"netRead": true}, rules, &fakeAlertDeliveryStore{}, fakeSecretCipher{})

	body := bytes.NewBufferString(`{"name":"x","enabled":true,"targetKind":"generic","targetUrl":"https://example.com/hook"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alert-rules", body)
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (missing netWrite)", rec.Code)
	}
}

func TestAlertRulesRoutes_CreateGetUpdateDelete_RoundTrips(t *testing.T) {
	rules := newFakeAlertRuleStore()
	r := newAlertRulesTestRouter(t, map[string]bool{"netRead": true, "netWrite": true}, rules, &fakeAlertDeliveryStore{}, fakeSecretCipher{})

	createBody := bytes.NewBufferString(`{
		"name": "Errors to Slack", "enabled": true,
		"severityFilter": ["error"],
		"targetKind": "slack", "targetUrl": "https://hooks.slack.com/services/x",
		"targetSecret": "s3cr3t"
	}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/alert-rules", createBody)
	createReq.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201, body: %s", createRec.Code, createRec.Body.String())
	}
	var created alertRuleResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decoding POST body: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected a non-empty server-assigned id")
	}
	if !created.HasSecret {
		t.Error("HasSecret = false, want true")
	}
	if created.Name != "Errors to Slack" || created.TargetKind != "slack" {
		t.Errorf("created = %+v, fields don't match request", created)
	}
	// Secret must never be echoed back, plaintext or ciphertext.
	raw := createRec.Body.String()
	if bytes.Contains([]byte(raw), []byte("s3cr3t")) || bytes.Contains([]byte(raw), []byte("enc:")) {
		t.Errorf("response leaked the secret: %s", raw)
	}
	// The store itself must hold the encrypted form.
	stored := rules.items[created.ID]
	if string(stored.TargetSecretEnc) != "enc:s3cr3t" {
		t.Errorf("stored TargetSecretEnc = %q, want %q", stored.TargetSecretEnc, "enc:s3cr3t")
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/alert-rules/"+created.ID, nil)
	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body: %s", getRec.Code, getRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/alert-rules", nil)
	listRec := httptest.NewRecorder()
	r.ServeHTTP(listRec, listReq)
	var listed alertRulesListResponse
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decoding GET list body: %v", err)
	}
	if len(listed.Items) != 1 || listed.Items[0].ID != created.ID {
		t.Errorf("list = %+v, want exactly the created rule", listed)
	}

	// Update: change name/enabled, leave secret untouched (omit targetSecret).
	updateBody := bytes.NewBufferString(`{"name":"renamed","enabled":false,"targetKind":"slack","targetUrl":"https://hooks.slack.com/services/x"}`)
	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/alert-rules/"+created.ID, updateBody)
	updateReq.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	updateRec := httptest.NewRecorder()
	r.ServeHTTP(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body: %s", updateRec.Code, updateRec.Body.String())
	}
	var updated alertRuleResponse
	if err := json.NewDecoder(updateRec.Body).Decode(&updated); err != nil {
		t.Fatalf("decoding PUT body: %v", err)
	}
	if updated.Name != "renamed" || updated.Enabled {
		t.Errorf("updated = %+v, want name=renamed enabled=false", updated)
	}
	if !updated.HasSecret {
		t.Error("HasSecret after update-without-targetSecret = false, want true (secret preserved)")
	}

	// Update: clear the secret with targetSecret: "".
	clearBody := bytes.NewBufferString(`{"name":"renamed","enabled":false,"targetKind":"slack","targetUrl":"https://hooks.slack.com/services/x","targetSecret":""}`)
	clearReq := httptest.NewRequest(http.MethodPut, "/api/v1/alert-rules/"+created.ID, clearBody)
	clearReq.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	clearRec := httptest.NewRecorder()
	r.ServeHTTP(clearRec, clearReq)
	if clearRec.Code != http.StatusOK {
		t.Fatalf("PUT (clear secret) status = %d, body: %s", clearRec.Code, clearRec.Body.String())
	}
	var cleared alertRuleResponse
	if err := json.NewDecoder(clearRec.Body).Decode(&cleared); err != nil {
		t.Fatalf("decoding PUT body: %v", err)
	}
	if cleared.HasSecret {
		t.Error("HasSecret after targetSecret:\"\" = true, want false")
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/alert-rules/"+created.ID, nil)
	deleteReq.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	deleteRec := httptest.NewRecorder()
	r.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204", deleteRec.Code)
	}
	getAfterDeleteReq := httptest.NewRequest(http.MethodGet, "/api/v1/alert-rules/"+created.ID, nil)
	getAfterDeleteRec := httptest.NewRecorder()
	r.ServeHTTP(getAfterDeleteRec, getAfterDeleteReq)
	if getAfterDeleteRec.Code != http.StatusNotFound {
		t.Errorf("GET after DELETE status = %d, want 404", getAfterDeleteRec.Code)
	}
}

func TestAlertRulesRoutes_CreateValidation(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"missing name", `{"targetKind":"generic","targetUrl":"https://example.com"}`},
		{"bad target kind", `{"name":"x","targetKind":"carrier-pigeon","targetUrl":"https://example.com"}`},
		{"bad target url", `{"name":"x","targetKind":"generic","targetUrl":"not-a-url"}`},
		{"non-http(s) scheme", `{"name":"x","targetKind":"generic","targetUrl":"ftp://example.com/x"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rules := newFakeAlertRuleStore()
			r := newAlertRulesTestRouter(t, map[string]bool{"netWrite": true}, rules, &fakeAlertDeliveryStore{}, fakeSecretCipher{})
			req := httptest.NewRequest(http.MethodPost, "/api/v1/alert-rules", bytes.NewBufferString(tt.body))
			req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAlertRulesRoutes_Test_DeliversAndLogs(t *testing.T) {
	var received string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		received = string(buf)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rules := newFakeAlertRuleStore()
	rules.items["r1"] = store.AlertRule{
		ID: "r1", Name: "Test rule", Enabled: true, TargetKind: "generic", TargetURL: srv.URL,
		CreatedAt: 1, UpdatedAt: 1,
	}
	deliveries := &fakeAlertDeliveryStore{}
	r := newAlertRulesTestRouter(t, map[string]bool{"netWrite": true}, rules, deliveries, fakeSecretCipher{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/alert-rules/r1/test", nil)
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var got alertRuleTestResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Status != "delivered" {
		t.Errorf("Status = %q, want %q (error: %s)", got.Status, "delivered", got.Error)
	}
	if received == "" {
		t.Error("test receiver never got a request body")
	}
	if len(deliveries.rows) != 1 {
		t.Fatalf("delivery log has %d rows, want 1", len(deliveries.rows))
	}
	if deliveries.rows[0].RuleID != "r1" || deliveries.rows[0].Status != "delivered" {
		t.Errorf("logged delivery = %+v, want RuleID=r1 Status=delivered", deliveries.rows[0])
	}
}

func TestAlertRulesRoutes_Test_UnreachableTargetLogsFailed(t *testing.T) {
	rules := newFakeAlertRuleStore()
	rules.items["r1"] = store.AlertRule{
		ID: "r1", Name: "Test rule", Enabled: true, TargetKind: "generic", TargetURL: "http://127.0.0.1:1/no-such-port",
		CreatedAt: 1, UpdatedAt: 1,
	}
	deliveries := &fakeAlertDeliveryStore{}
	r := newAlertRulesTestRouter(t, map[string]bool{"netWrite": true}, rules, deliveries, fakeSecretCipher{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/alert-rules/r1/test", nil)
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the outcome is reported in the body, not the HTTP status), body: %s", rec.Code, rec.Body.String())
	}
	var got alertRuleTestResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Status != "failed" || got.Error == "" {
		t.Errorf("got %+v, want Status=failed with a non-empty Error", got)
	}
	if len(deliveries.rows) != 1 || deliveries.rows[0].Status != "failed" {
		t.Fatalf("delivery log = %+v, want one failed row", deliveries.rows)
	}
}

func TestAlertRulesRoutes_TestNotFound(t *testing.T) {
	rules := newFakeAlertRuleStore()
	r := newAlertRulesTestRouter(t, map[string]bool{"netWrite": true}, rules, &fakeAlertDeliveryStore{}, fakeSecretCipher{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alert-rules/missing/test", nil)
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestAlertDeliveriesRoute_FiltersByRuleAndStatus(t *testing.T) {
	deliveries := &fakeAlertDeliveryStore{rows: []store.AlertDelivery{
		{ID: "d1", RuleID: "r1", FindingID: "f1", At: 1, Attempt: 1, Status: "retrying"},
		{ID: "d2", RuleID: "r1", FindingID: "f1", At: 2, Attempt: 2, Status: "delivered"},
		{ID: "d3", RuleID: "r2", FindingID: "f2", At: 3, Attempt: 1, Status: "failed"},
	}}
	r := newAlertRulesTestRouter(t, map[string]bool{"netRead": true}, newFakeAlertRuleStore(), deliveries, fakeSecretCipher{})

	for _, tt := range []struct {
		query string
		want  int
	}{
		{"", 3},
		{"?ruleId=r1", 2},
		{"?status=delivered", 1},
		{"?ruleId=r1&status=retrying", 1},
		{"?ruleId=no-such-rule", 0},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/alert-deliveries"+tt.query, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("query %q status = %d, body: %s", tt.query, rec.Code, rec.Body.String())
		}
		var got alertDeliveriesListResponse
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatalf("decoding response for %q: %v", tt.query, err)
		}
		if len(got.Items) != tt.want {
			t.Errorf("query %q: got %d items, want %d", tt.query, len(got.Items), tt.want)
		}
	}
}

func TestValidateAlertRuleRequest(t *testing.T) {
	valid := alertRuleRequest{Name: "x", TargetKind: "generic", TargetURL: "https://example.com/hook"}
	if msg := validateAlertRuleRequest(valid); msg != "" {
		t.Errorf("valid request rejected: %s", msg)
	}
}

func TestEncryptDecryptRoundTrip_ViaFakeCipher(t *testing.T) {
	c := fakeSecretCipher{}
	enc, err := c.Encrypt([]byte("hello"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	dec, err := c.Decrypt(enc)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(dec) != "hello" {
		t.Errorf("Decrypt(Encrypt(x)) = %q, want %q", dec, "hello")
	}
}
