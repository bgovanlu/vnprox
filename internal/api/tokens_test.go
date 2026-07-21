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

// fakeAPITokenStore is an in-memory APITokenStore test double.
type fakeAPITokenStore struct {
	items map[string]store.APIToken
}

func newFakeAPITokenStore() *fakeAPITokenStore {
	return &fakeAPITokenStore{items: map[string]store.APIToken{}}
}

func (f *fakeAPITokenStore) Create(_ context.Context, t store.APIToken) error {
	f.items[t.ID] = t
	return nil
}

func (f *fakeAPITokenStore) Get(_ context.Context, id string) (store.APIToken, error) {
	t, ok := f.items[id]
	if !ok {
		return store.APIToken{}, store.ErrNotFound
	}
	return t, nil
}

// GetByHash satisfies embedTokenReader (T-1706's embed view-route auth) — a
// linear scan is fine for a test double.
func (f *fakeAPITokenStore) GetByHash(_ context.Context, hash string) (store.APIToken, error) {
	for _, t := range f.items {
		if t.TokenHash == hash {
			return t, nil
		}
	}
	return store.APIToken{}, store.ErrNotFound
}

func (f *fakeAPITokenStore) List(context.Context) ([]store.APIToken, error) {
	var out []store.APIToken
	for _, t := range f.items {
		out = append(out, t)
	}
	return out, nil
}

func (f *fakeAPITokenStore) Revoke(_ context.Context, id string, now int64) error {
	t, ok := f.items[id]
	if !ok {
		return store.ErrNotFound
	}
	t.RevokedAt.Int64, t.RevokedAt.Valid = now, true
	f.items[id] = t
	return nil
}

// fakeTokenAuditor is an in-memory tokenAuditor test double.
type fakeTokenAuditor struct {
	entries []store.AuditEntry
}

func (f *fakeTokenAuditor) Append(_ context.Context, e store.AuditEntry) (int64, error) {
	f.entries = append(f.entries, e)
	return int64(len(f.entries)), nil
}

// fakeTokenWSCloser records CloseByTokenID calls.
type fakeTokenWSCloser struct {
	closed []string
}

func (f *fakeTokenWSCloser) CloseByTokenID(id string) int {
	f.closed = append(f.closed, id)
	return 1
}

// fakeTokenMinter is a scriptable TokenMinter test double. By default
// ValidateTokenScopes echoes every requested scope back unchanged (i.e.
// "always within capabilities") — real scope-vs-capability enforcement is
// internal/auth's own tested responsibility (tokens_test.go in that
// package); this double only needs to prove the HTTP-layer wiring. Set
// rejectScopes to simulate a scope-validation failure instead.
type fakeTokenMinter struct {
	rejectScopes *tokenScopeRejection
	username     string
	generated    int
	fakeAuth
}

// tokenScopeRejection scripts a ValidateTokenScopes failure response.
type tokenScopeRejection struct {
	code    string
	message string
	status  int
}

func (f *fakeTokenMinter) Username(context.Context) (string, bool) {
	if f.username == "" {
		return "", false
	}
	return f.username, true
}

func (f *fakeTokenMinter) ValidateTokenScopes(_ context.Context, raw []string) ([]string, int, string, string, bool) {
	if f.rejectScopes != nil {
		return nil, f.rejectScopes.status, f.rejectScopes.code, f.rejectScopes.message, false
	}
	return raw, 0, "", "", true
}

func (f *fakeTokenMinter) GenerateToken() (string, string, error) {
	f.generated++
	return "raw-token", "hash-token", nil
}

func newTokenTestRouter(t *testing.T, tokens APITokenStore, audit tokenAuditor, wsCloser TokenWSCloser, minter *fakeTokenMinter) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	mountTokenRoutes(r, tokens, audit, wsCloser, minter)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts
}

func TestCreateToken_OneTimeRevealAndAudited(t *testing.T) {
	tokens := newFakeAPITokenStore()
	audit := &fakeTokenAuditor{}
	minter := &fakeTokenMinter{username: "alice", fakeAuth: fakeAuth{authenticated: true}}
	ts := newTokenTestRouter(t, tokens, audit, nil, minter)

	body, _ := json.Marshal(map[string]any{"name": "ci-runner", "scopes": []string{"netRead", "automation"}})
	resp, err := http.Post(ts.URL+"/tokens", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /tokens: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var got tokenCreateResponse
	if decErr := json.NewDecoder(resp.Body).Decode(&got); decErr != nil {
		t.Fatalf("decode: %v", decErr)
	}
	if got.Token != "raw-token" {
		t.Errorf("Token = %q, want the one-time raw value", got.Token)
	}
	if got.CreatedBy != "alice" {
		t.Errorf("CreatedBy = %q, want alice", got.CreatedBy)
	}
	if len(got.Scopes) != 2 {
		t.Errorf("Scopes = %v, want 2 entries", got.Scopes)
	}

	stored, err := tokens.Get(context.Background(), got.ID)
	if err != nil {
		t.Fatalf("stored token missing: %v", err)
	}
	if stored.TokenHash != "hash-token" {
		t.Errorf("stored TokenHash = %q, want hash-token (never the raw value)", stored.TokenHash)
	}

	if len(audit.entries) != 1 || audit.entries[0].Action != "token.create" {
		t.Fatalf("audit entries = %+v, want one token.create", audit.entries)
	}
}

func TestCreateToken_MissingNameIs400(t *testing.T) {
	minter := &fakeTokenMinter{username: "alice", fakeAuth: fakeAuth{authenticated: true}}
	ts := newTokenTestRouter(t, newFakeAPITokenStore(), &fakeTokenAuditor{}, nil, minter)

	body, _ := json.Marshal(map[string]any{"scopes": []string{"netRead"}})
	resp, err := http.Post(ts.URL+"/tokens", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /tokens: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestCreateToken_ScopeExceedsCapabilitiesIs403(t *testing.T) {
	minter := &fakeTokenMinter{
		username: "alice", fakeAuth: fakeAuth{authenticated: true},
		rejectScopes: &tokenScopeRejection{status: http.StatusForbidden, code: "forbidden", message: "scope exceeds capabilities"},
	}
	ts := newTokenTestRouter(t, newFakeAPITokenStore(), &fakeTokenAuditor{}, nil, minter)

	body, _ := json.Marshal(map[string]any{"name": "x", "scopes": []string{"sdnWrite"}})
	resp, err := http.Post(ts.URL+"/tokens", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /tokens: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestListTokens_OnlyReturnsCallersOwnTokens(t *testing.T) {
	tokens := newFakeAPITokenStore()
	_ = tokens.Create(context.Background(), store.APIToken{ID: "a", Name: "alice-tok", ScopesJSON: `["netRead"]`, CreatedBy: "alice", CreatedAt: 1})
	_ = tokens.Create(context.Background(), store.APIToken{ID: "b", Name: "bob-tok", ScopesJSON: `["netRead"]`, CreatedBy: "bob", CreatedAt: 2})

	minter := &fakeTokenMinter{username: "alice", fakeAuth: fakeAuth{authenticated: true}}
	ts := newTokenTestRouter(t, tokens, &fakeTokenAuditor{}, nil, minter)

	resp, err := http.Get(ts.URL + "/tokens")
	if err != nil {
		t.Fatalf("GET /tokens: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var got tokensListResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].ID != "a" {
		t.Fatalf("Items = %+v, want exactly alice's own token", got.Items)
	}
}

func TestDeleteToken_OtherUsersTokenIs404(t *testing.T) {
	tokens := newFakeAPITokenStore()
	_ = tokens.Create(context.Background(), store.APIToken{ID: "b", Name: "bob-tok", ScopesJSON: `["netRead"]`, CreatedBy: "bob", CreatedAt: 1})

	minter := &fakeTokenMinter{username: "alice", fakeAuth: fakeAuth{authenticated: true}}
	closer := &fakeTokenWSCloser{}
	ts := newTokenTestRouter(t, tokens, &fakeTokenAuditor{}, closer, minter)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/tokens/b", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /tokens/b: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (not another user's token)", resp.StatusCode)
	}
	if len(closer.closed) != 0 {
		t.Errorf("CloseByTokenID called for a token deletion that was rejected: %v", closer.closed)
	}

	stillThere, err := tokens.Get(context.Background(), "b")
	if err != nil || stillThere.RevokedAt.Valid {
		t.Errorf("bob's token should be untouched, got %+v (err %v)", stillThere, err)
	}
}

func TestDeleteToken_OwnTokenRevokesAndForceClosesWS(t *testing.T) {
	tokens := newFakeAPITokenStore()
	_ = tokens.Create(context.Background(), store.APIToken{ID: "a", Name: "alice-tok", ScopesJSON: `["netRead"]`, CreatedBy: "alice", CreatedAt: 1})

	minter := &fakeTokenMinter{username: "alice", fakeAuth: fakeAuth{authenticated: true}}
	audit := &fakeTokenAuditor{}
	closer := &fakeTokenWSCloser{}
	ts := newTokenTestRouter(t, tokens, audit, closer, minter)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/tokens/a", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /tokens/a: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	revoked, err := tokens.Get(context.Background(), "a")
	if err != nil || !revoked.RevokedAt.Valid {
		t.Errorf("token should be revoked, got %+v (err %v)", revoked, err)
	}
	if len(closer.closed) != 1 || closer.closed[0] != "a" {
		t.Errorf("CloseByTokenID calls = %v, want exactly [\"a\"]", closer.closed)
	}
	found := false
	for _, e := range audit.entries {
		if e.Action == "token.revoke" {
			found = true
		}
	}
	if !found {
		t.Errorf("no token.revoke audit entry, got %+v", audit.entries)
	}
}

func TestTokenRoutes_NotMountedWithoutTokenMinter(t *testing.T) {
	r := chi.NewRouter()
	mountTokenRoutes(r, newFakeAPITokenStore(), &fakeTokenAuditor{}, nil, fakeAuth{authenticated: true})
	ts := httptest.NewServer(r)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/tokens")
	if err != nil {
		t.Fatalf("GET /tokens: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route not mounted — fakeAuth doesn't implement TokenMinter)", resp.StatusCode)
	}
}
