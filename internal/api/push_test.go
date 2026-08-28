// SPDX-License-Identifier: Apache-2.0

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

// fakePushSubscriptionStore is an in-memory PushSubscriptionStore test
// double, mirroring fakeWebhookStore's shape.
type fakePushSubscriptionStore struct {
	items map[string]store.PushSubscription
}

func newFakePushSubscriptionStore() *fakePushSubscriptionStore {
	return &fakePushSubscriptionStore{items: map[string]store.PushSubscription{}}
}

func (f *fakePushSubscriptionStore) Create(_ context.Context, s store.PushSubscription) error {
	f.items[s.ID] = s
	return nil
}

func (f *fakePushSubscriptionStore) Get(_ context.Context, id string) (store.PushSubscription, error) {
	s, ok := f.items[id]
	if !ok {
		return store.PushSubscription{}, store.ErrNotFound
	}
	return s, nil
}

func (f *fakePushSubscriptionStore) GetByEndpointHash(_ context.Context, hash string) (store.PushSubscription, error) {
	for _, s := range f.items {
		if s.EndpointHash == hash {
			return s, nil
		}
	}
	return store.PushSubscription{}, store.ErrNotFound
}

func (f *fakePushSubscriptionStore) DeleteByEndpointHash(_ context.Context, hash string) error {
	for id, s := range f.items {
		if s.EndpointHash == hash {
			delete(f.items, id)
		}
	}
	return nil
}

func (f *fakePushSubscriptionStore) ListByUsername(_ context.Context, username string) ([]store.PushSubscription, error) {
	var out []store.PushSubscription
	for _, s := range f.items {
		if s.Username == username {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakePushSubscriptionStore) Delete(_ context.Context, id string) error {
	delete(f.items, id)
	return nil
}

// fakeAuthWithSession extends fakeAuthWithCaps (changesets_test.go) with
// SessionLookup, since push subscriptions need a session id to attach to —
// no other existing test double in this package implements it.
type fakeAuthWithSession struct {
	sessionID string
	fakeAuthWithCaps
	haveSess bool
}

func (f fakeAuthWithSession) SessionID(context.Context) (string, bool) {
	return f.sessionID, f.haveSess
}

func pushTestAuth(username, sessionID string) fakeAuthWithSession {
	return fakeAuthWithSession{
		sessionID: sessionID, haveSess: sessionID != "",
		fakeAuthWithCaps: fakeAuthWithCaps{
			csrf:             true,
			fakeAuthWithUser: fakeAuthWithUser{username: username, fakeAuth: fakeAuth{authenticated: true}},
		},
	}
}

func newPushTestRouter(t *testing.T, subs PushSubscriptionStore, cipher SecretCipher, audit tokenAuditor, vapidKey string, auth AuthService) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	mountPushRoutes(r, subs, cipher, audit, vapidKey, auth)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts
}

// validPushSubscriptionBody returns a well-formed POST /push/subscriptions
// body whose endpoint/keys are realistic-shaped (a real base64url P-256
// point / 16-byte auth secret) but synthetic — internal/push's own tests
// exercise the real crypto; this package only needs to prove HTTP-layer
// wiring (validation delegation, encryption, scoping).
func validPushSubscriptionBody(endpoint string, categories []string) []byte {
	body, _ := json.Marshal(map[string]any{
		"endpoint": endpoint,
		"keys": map[string]string{
			// A real (if arbitrary) uncompressed P-256 point and 16-byte
			// auth secret, base64url — internal/push.ParseSubscription
			// validates shape, so these must actually decode correctly.
			"p256dh": "BAii7QBWQqjJbaZ9dP7GHXHKGRB37XeEnPRXOB1LhU5uDpqIRZlrxlbFyLnCg8cP2mErF4rp4DYCA0ROOF1PXKA",
			"auth":   "wSlvzIyeaZgpH9UhKgnv2A",
		},
		"categories":  categories,
		"deviceLabel": "Test device",
	})
	return body
}

func TestGetVAPIDPublicKey(t *testing.T) {
	ts := newPushTestRouter(t, newFakePushSubscriptionStore(), fakeSecretCipher{}, &fakeTokenAuditor{}, "the-public-key", pushTestAuth("alice", "sess-1"))
	resp, err := http.Get(ts.URL + "/push/vapid-public-key")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got vapidPublicKeyResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Key != "the-public-key" {
		t.Errorf("Key = %q, want the-public-key", got.Key)
	}
}

func TestMountPushRoutes_NotMountedWithoutVAPIDKey(t *testing.T) {
	r := chi.NewRouter()
	mountPushRoutes(r, newFakePushSubscriptionStore(), fakeSecretCipher{}, &fakeTokenAuditor{}, "", pushTestAuth("alice", "sess-1"))
	ts := httptest.NewServer(r)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/push/vapid-public-key")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status with empty VAPID key = %d, want 404 (route not mounted at all)", resp.StatusCode)
	}
}

// TestCreatePushSubscription_NeverReturnsEndpointOrKeysAndEncryptsAtRest is
// the security-relevant test for this route: the response must not echo
// back the endpoint or keys (T-2005's "push subscriptions treated as
// credentials at rest"), AND what actually reaches the store must be
// encrypted, not plaintext with the fake "enc:" prefix stripped off by
// accident.
func TestCreatePushSubscription_NeverReturnsEndpointOrKeysAndEncryptsAtRest(t *testing.T) {
	subs := newFakePushSubscriptionStore()
	cipher := fakeSecretCipher{}
	audit := &fakeTokenAuditor{}
	auth := pushTestAuth("alice", "sess-1")
	ts := newPushTestRouter(t, subs, cipher, audit, "vapid-pub", auth)

	const endpoint = "https://push.example.com/send/super-secret-endpoint-id"
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/push/subscriptions", bytes.NewReader(validPushSubscriptionBody(endpoint, []string{"critical", "awaitingConfirm"})))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		body, _ := readBody(t, resp)
		t.Fatalf("status = %d, want 201; body: %s", resp.StatusCode, body)
	}

	raw, _ := readBody(t, resp)
	if bytes.Contains(raw, []byte(endpoint)) {
		t.Errorf("response body leaked the raw endpoint: %s", raw)
	}
	if bytes.Contains(raw, []byte("BAii7QBWQqjJbaZ9dP7GHXHKGRB37XeEnPRXOB1LhU5uDpqIRZlrxlbFyLnCg8cP2mErF4rp4DYCA0ROOF1PXKA")) {
		t.Errorf("response body leaked the raw p256dh key: %s", raw)
	}

	var got pushSubscriptionResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Categories) != 2 {
		t.Errorf("Categories = %v, want 2 entries", got.Categories)
	}

	if len(subs.items) != 1 {
		t.Fatalf("store has %d rows, want 1", len(subs.items))
	}
	var stored store.PushSubscription
	for _, s := range subs.items {
		stored = s
	}
	if stored.SessionID != "sess-1" || stored.Username != "alice" {
		t.Errorf("stored row = %+v, want session sess-1 / user alice", stored)
	}
	// fakeSecretCipher prefixes ciphertext with "enc:" — confirms Encrypt
	// was actually called on the endpoint/keys rather than storing them
	// verbatim.
	if !bytes.HasPrefix(stored.EndpointEnc, []byte("enc:")) {
		t.Error("stored EndpointEnc was not passed through the cipher")
	}
	if bytes.Contains(stored.EndpointEnc, []byte(endpoint)) && !bytes.Equal(stored.EndpointEnc, append([]byte("enc:"), endpoint...)) {
		t.Error("stored EndpointEnc does not look like ciphertext of the endpoint")
	}
}

func TestCreatePushSubscription_RejectsUnknownCategory(t *testing.T) {
	ts := newPushTestRouter(t, newFakePushSubscriptionStore(), fakeSecretCipher{}, &fakeTokenAuditor{}, "vapid-pub", pushTestAuth("alice", "sess-1"))
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/push/subscriptions", bytes.NewReader(validPushSubscriptionBody("https://push.example.com/send/x", []string{"urgent"})))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestCreatePushSubscription_RequiresARealSessionNotABearerToken(t *testing.T) {
	// haveSess=false simulates a bearer-token-authenticated caller (no
	// session id at all) — this route must refuse it, since
	// push_subscriptions.session_id is a required FK (0046's migration).
	auth := pushTestAuth("alice", "")
	ts := newPushTestRouter(t, newFakePushSubscriptionStore(), fakeSecretCipher{}, &fakeTokenAuditor{}, "vapid-pub", auth)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/push/subscriptions", bytes.NewReader(validPushSubscriptionBody("https://push.example.com/send/x", []string{"critical"})))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestCreatePushSubscription_ResubscribeSameEndpointReplacesPriorRow(t *testing.T) {
	subs := newFakePushSubscriptionStore()
	ts := newPushTestRouter(t, subs, fakeSecretCipher{}, &fakeTokenAuditor{}, "vapid-pub", pushTestAuth("alice", "sess-1"))
	const endpoint = "https://push.example.com/send/same-device"

	for i := 0; i < 2; i++ {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/push/subscriptions", bytes.NewReader(validPushSubscriptionBody(endpoint, []string{"critical"})))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST #%d: %v", i, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("POST #%d status = %d, want 201", i, resp.StatusCode)
		}
	}

	if len(subs.items) != 1 {
		t.Errorf("store has %d rows after resubscribing to the same endpoint twice, want 1", len(subs.items))
	}
}

func TestListPushSubscriptions_ScopedToCaller(t *testing.T) {
	subs := newFakePushSubscriptionStore()
	_ = subs.Create(context.Background(), store.PushSubscription{ID: "a1", Username: "alice", SessionID: "s1", CategoriesJSON: `["critical"]`, CreatedAt: 1})
	_ = subs.Create(context.Background(), store.PushSubscription{ID: "b1", Username: "bob", SessionID: "s2", CategoriesJSON: `["drift"]`, CreatedAt: 2})

	ts := newPushTestRouter(t, subs, fakeSecretCipher{}, &fakeTokenAuditor{}, "vapid-pub", pushTestAuth("alice", "sess-1"))
	resp, err := http.Get(ts.URL + "/push/subscriptions")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var got pushSubscriptionsListResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].ID != "a1" {
		t.Errorf("Items = %+v, want exactly alice's own subscription (a1)", got.Items)
	}
}

func TestDeletePushSubscription_CannotDeleteAnotherUsersSubscription(t *testing.T) {
	subs := newFakePushSubscriptionStore()
	_ = subs.Create(context.Background(), store.PushSubscription{ID: "b1", Username: "bob", SessionID: "s2", CategoriesJSON: `["drift"]`, CreatedAt: 2})

	ts := newPushTestRouter(t, subs, fakeSecretCipher{}, &fakeTokenAuditor{}, "vapid-pub", pushTestAuth("alice", "sess-1"))
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/push/subscriptions/b1", nil)
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (never confirm another user's subscription exists)", resp.StatusCode)
	}
	if _, err := subs.Get(context.Background(), "b1"); err != nil {
		t.Error("bob's subscription was deleted by alice's request")
	}
}

func TestDeletePushSubscription_OwnerCanDeleteTheirOwn(t *testing.T) {
	subs := newFakePushSubscriptionStore()
	_ = subs.Create(context.Background(), store.PushSubscription{ID: "a1", Username: "alice", SessionID: "s1", CategoriesJSON: `["critical"]`, CreatedAt: 1})

	ts := newPushTestRouter(t, subs, fakeSecretCipher{}, &fakeTokenAuditor{}, "vapid-pub", pushTestAuth("alice", "sess-1"))
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/push/subscriptions/a1", nil)
	req.Header.Set("X-VNPROX-CSRF", "test-csrf-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204", resp.StatusCode)
	}
	if _, err := subs.Get(context.Background(), "a1"); err == nil {
		t.Error("subscription still exists after DELETE")
	}
}
