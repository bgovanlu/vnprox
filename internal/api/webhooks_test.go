package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/store"
)

// fakeWebhookStore is an in-memory WebhookStore test double.
type fakeWebhookStore struct {
	items map[string]store.Webhook
}

func newFakeWebhookStore() *fakeWebhookStore {
	return &fakeWebhookStore{items: map[string]store.Webhook{}}
}

func (f *fakeWebhookStore) Create(_ context.Context, w store.Webhook) error {
	f.items[w.ID] = w
	return nil
}

func (f *fakeWebhookStore) Get(_ context.Context, id string) (store.Webhook, error) {
	w, ok := f.items[id]
	if !ok {
		return store.Webhook{}, store.ErrNotFound
	}
	return w, nil
}

func (f *fakeWebhookStore) List(context.Context) ([]store.Webhook, error) {
	var out []store.Webhook
	for _, w := range f.items {
		out = append(out, w)
	}
	return out, nil
}

func (f *fakeWebhookStore) Delete(_ context.Context, id string) error {
	delete(f.items, id)
	return nil
}

func newWebhookTestRouter(t *testing.T, webhooks WebhookStore, cipher SecretCipher, audit tokenAuditor, auth AuthService) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	mountWebhookRoutes(r, webhooks, cipher, audit, auth)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts
}

func TestCreateWebhook_EncryptsSecretAndNeverReturnsIt(t *testing.T) {
	webhooks := newFakeWebhookStore()
	cipher := fakeSecretCipher{}
	audit := &fakeTokenAuditor{}
	auth := fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"}
	ts := newWebhookTestRouter(t, webhooks, cipher, audit, auth)

	body, _ := json.Marshal(map[string]any{"url": "https://example.com/hook", "secret": "s3cret", "events": []string{"changeset.status"}})
	resp, err := http.Post(ts.URL+"/webhooks", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /webhooks: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	raw, _ := readBody(t, resp)
	if bytes.Contains(raw, []byte("s3cret")) {
		t.Errorf("response body leaked the plaintext secret: %s", raw)
	}

	var got webhookResponse
	if decErr := json.Unmarshal(raw, &got); decErr != nil {
		t.Fatalf("decode: %v", decErr)
	}
	if got.CreatedBy != "alice" {
		t.Errorf("CreatedBy = %q, want alice", got.CreatedBy)
	}
	if len(got.Events) != 1 || got.Events[0] != "changeset.status" {
		t.Errorf("Events = %v, want [changeset.status]", got.Events)
	}

	stored, err := webhooks.Get(context.Background(), got.ID)
	if err != nil {
		t.Fatalf("stored webhook missing: %v", err)
	}
	if bytes.Equal(stored.SecretEnc, []byte("s3cret")) {
		t.Error("stored secret is plaintext, want encrypted")
	}
	decrypted, err := cipher.Decrypt(stored.SecretEnc)
	if err != nil || string(decrypted) != "s3cret" {
		t.Errorf("stored secret does not round-trip through the cipher: %q, %v", decrypted, err)
	}

	found := false
	for _, e := range audit.entries {
		if e.Action == "webhook.create" {
			found = true
		}
	}
	if !found {
		t.Errorf("no webhook.create audit entry, got %+v", audit.entries)
	}
}

func TestCreateWebhook_ValidatesURL(t *testing.T) {
	auth := fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"}
	ts := newWebhookTestRouter(t, newFakeWebhookStore(), fakeSecretCipher{}, &fakeTokenAuditor{}, auth)

	tests := []struct {
		body map[string]any
	}{
		{body: map[string]any{"url": "not-a-url", "secret": "s"}},
		{body: map[string]any{"url": "ftp://example.com", "secret": "s"}},
		{body: map[string]any{"url": "https://example.com/hook", "secret": ""}},
	}
	for _, tt := range tests {
		body, _ := json.Marshal(tt.body)
		resp, err := http.Post(ts.URL+"/webhooks", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("POST /webhooks: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("body %v: status = %d, want 400", tt.body, resp.StatusCode)
		}
	}
}

func TestListWebhooks_NeverReturnsSecret(t *testing.T) {
	webhooks := newFakeWebhookStore()
	_ = webhooks.Create(context.Background(), store.Webhook{ID: "w1", URL: "https://example.com", SecretEnc: []byte("enc-secret"), CreatedBy: "alice", CreatedAt: 1})
	auth := fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"}
	ts := newWebhookTestRouter(t, webhooks, fakeSecretCipher{}, &fakeTokenAuditor{}, auth)

	resp, err := http.Get(ts.URL + "/webhooks")
	if err != nil {
		t.Fatalf("GET /webhooks: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := readBody(t, resp)
	if bytes.Contains(raw, []byte("enc-secret")) {
		t.Errorf("GET /webhooks leaked the secret: %s", raw)
	}
	var got webhooksListResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].ID != "w1" {
		t.Fatalf("Items = %+v, want [w1]", got.Items)
	}
}

func TestDeleteWebhook_RemovesRegistrationAndAudits(t *testing.T) {
	webhooks := newFakeWebhookStore()
	_ = webhooks.Create(context.Background(), store.Webhook{ID: "w1", URL: "https://example.com", SecretEnc: []byte("x"), CreatedBy: "alice", CreatedAt: 1})
	audit := &fakeTokenAuditor{}
	auth := fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"}
	ts := newWebhookTestRouter(t, webhooks, fakeSecretCipher{}, audit, auth)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/webhooks/w1", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /webhooks/w1: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204", resp.StatusCode)
	}
	if _, err := webhooks.Get(context.Background(), "w1"); err != store.ErrNotFound {
		t.Errorf("webhook still present after delete: err=%v", err)
	}
	found := false
	for _, e := range audit.entries {
		if e.Action == "webhook.delete" {
			found = true
		}
	}
	if !found {
		t.Errorf("no webhook.delete audit entry, got %+v", audit.entries)
	}
}

func TestDeleteWebhook_UnknownIDIs404(t *testing.T) {
	auth := fakeAuthWithUser{fakeAuth: fakeAuth{authenticated: true}, username: "alice"}
	ts := newWebhookTestRouter(t, newFakeWebhookStore(), fakeSecretCipher{}, &fakeTokenAuditor{}, auth)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/webhooks/no-such-id", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestWebhookRoutes_NotMountedWithoutUsernameLookup(t *testing.T) {
	r := chi.NewRouter()
	mountWebhookRoutes(r, newFakeWebhookStore(), fakeSecretCipher{}, &fakeTokenAuditor{}, fakeAuth{authenticated: true})
	ts := httptest.NewServer(r)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/webhooks")
	if err != nil {
		t.Fatalf("GET /webhooks: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route not mounted — fakeAuth doesn't implement UsernameLookup)", resp.StatusCode)
	}
}

func readBody(t *testing.T, resp *http.Response) ([]byte, error) {
	t.Helper()
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("reading response body: %v", err)
	}
	return buf.Bytes(), nil
}
