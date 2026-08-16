package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/auth"
	"github.com/bgovanlu/vnprox/internal/store"
)

// newBearerTestServer wires a minimal router exercising the exact
// middleware chain a real capability-gated route uses
// (SessionMiddleware -> CSRFMiddleware -> RequireCap), against both the
// cookie-session and (T-1104) bearer-token paths, so this file's tests
// exercise the real production chain rather than reimplementing it.
func newBearerTestServer(t *testing.T, svc *auth.Service, cap auth.Cap) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	r.With(svc.SessionMiddleware, svc.CSRFMiddleware, svc.RequireCap(cap)).
		Handle("/protected", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, _ := auth.IdentityFromContext(r.Context())
			w.Header().Set("X-Username", id.Username)
			w.WriteHeader(http.StatusOK)
		}))
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts
}

func newTokenTestSetup(t *testing.T, cap auth.Cap) (*httptest.Server, *store.APITokenRepo, *store.DB) {
	t.Helper()
	sessions, audit, db := newTestStore(t)
	tokens := store.NewAPITokenRepo(db)
	svc, err := auth.NewService(auth.Config{
		Sessions: sessions, Audit: audit, Tokens: tokens,
		NewIdentity: stubFactory(stubIdentity{}),
	})
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	return newBearerTestServer(t, svc, cap), tokens, db
}

func mintToken(t *testing.T, tokens *store.APITokenRepo, id string, scopesJSON string) string {
	t.Helper()
	return mintTokenFor(t, tokens, id, scopesJSON, "root@pam")
}

// mintTokenFor is mintToken with an explicit creating user — the dimension
// T-2604's distinct-approver rule turns on.
func mintTokenFor(t *testing.T, tokens *store.APITokenRepo, id, scopesJSON, createdBy string) string {
	t.Helper()
	raw, hash, err := auth.GenerateAPIToken()
	if err != nil {
		t.Fatalf("GenerateAPIToken: %v", err)
	}
	if err := tokens.Create(context.Background(), store.APIToken{
		ID: id, Name: "test-token-" + id, TokenHash: hash, ScopesJSON: scopesJSON,
		CreatedBy: createdBy, CreatedAt: time.Now().Unix(),
	}); err != nil {
		t.Fatalf("tokens.Create: %v", err)
	}
	return raw
}

// TestBearerAuth_TwoTokensOfOnePersonCarryOneUsername pins the premise
// T-2604's two-person rule rests on: a bearer token's identity is its
// CREATING USER, so two different tokens minted by the same person present
// as the same principal — which is why the sign-off table, keyed on
// principal, counts them once (internal/change/twoperson.go, and the
// end-to-end assertion in internal/api/changesets_twoperson_test.go).
//
// The control leg is the third token, minted by someone else: it presents as
// a different principal, so this test says something about identity rather
// than about the header being ignored.
func TestBearerAuth_TwoTokensOfOnePersonCarryOneUsername(t *testing.T) {
	ts, tokens, _ := newTokenTestSetup(t, auth.CapNetRead)

	usernameFor := func(raw string) string {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/protected", nil)
		req.Header.Set("Authorization", "Bearer "+raw)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /protected: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		return resp.Header.Get("X-Username")
	}

	first := usernameFor(mintTokenFor(t, tokens, "tok-a", `["netRead"]`, "bob@pam"))
	second := usernameFor(mintTokenFor(t, tokens, "tok-b", `["netRead"]`, "bob@pam"))
	if first != "bob@pam" || second != "bob@pam" {
		t.Fatalf("usernames = (%q, %q), want both bob@pam — two tokens, one person", first, second)
	}

	if other := usernameFor(mintTokenFor(t, tokens, "tok-c", `["netRead"]`, "carol@pam")); other != "carol@pam" {
		t.Fatalf("a third person's token presented as %q, want carol@pam", other)
	}
}

func TestBearerAuth_ValidTokenWithScopeSucceeds(t *testing.T) {
	ts, tokens, _ := newTokenTestSetup(t, auth.CapNetRead)
	raw := mintToken(t, tokens, "tok1", `["netRead","automation"]`)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/protected", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /protected: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Username"); got != "root@pam" {
		t.Errorf("X-Username = %q, want root@pam (the token's creator)", got)
	}
}

func TestBearerAuth_MissingScope403s(t *testing.T) {
	ts, tokens, _ := newTokenTestSetup(t, auth.CapNetWrite)
	raw := mintToken(t, tokens, "tok1", `["netRead"]`)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/protected", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /protected: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestBearerAuth_UnknownOrGarbageToken401s(t *testing.T) {
	ts, _, _ := newTokenTestSetup(t, auth.CapNetRead)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/protected", nil)
	req.Header.Set("Authorization", "Bearer not-a-real-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /protected: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestBearerAuth_RevokedTokenRejectedImmediately(t *testing.T) {
	ts, tokens, _ := newTokenTestSetup(t, auth.CapNetRead)
	raw := mintToken(t, tokens, "tok1", `["netRead"]`)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/protected", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /protected (before revoke): %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status before revoke = %d, want 200", resp.StatusCode)
	}

	if revokeErr := tokens.Revoke(context.Background(), "tok1", time.Now().Unix()); revokeErr != nil {
		t.Fatalf("Revoke: %v", revokeErr)
	}

	req2, _ := http.NewRequest(http.MethodGet, ts.URL+"/protected", nil)
	req2.Header.Set("Authorization", "Bearer "+raw)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("GET /protected (after revoke): %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("status after revoke = %d, want 401", resp2.StatusCode)
	}
}

func TestBearerAuth_SkipsCSRFOnMutatingRequest(t *testing.T) {
	ts, tokens, _ := newTokenTestSetup(t, auth.CapNetWrite)
	raw := mintToken(t, tokens, "tok1", `["netWrite"]`)

	// A mutating (PUT, via the "/protected" route registered for any
	// method) request with a bearer token and NO X-VNPROX-CSRF header must
	// still succeed — bearer requests are not cookie-based, so there is no
	// CSRF token to double-submit (docs/api.md's Tokens section).
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/protected", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /protected: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (CSRF must be skipped for bearer auth)", resp.StatusCode)
	}
}

func TestBearerAuth_UpdatesLastUsedAndAppendsAuditEntry(t *testing.T) {
	ts, tokens, db := newTokenTestSetup(t, auth.CapNetRead)
	raw := mintToken(t, tokens, "tok1", `["netRead"]`)
	audit := store.NewAuditRepo(db)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/protected", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /protected: %v", err)
	}
	_ = resp.Body.Close()

	tok, err := tokens.Get(context.Background(), "tok1")
	if err != nil {
		t.Fatalf("tokens.Get: %v", err)
	}
	if !tok.LastUsedAt.Valid {
		t.Error("token last_used_at was not stamped by a successful bearer-authenticated request")
	}

	entries, err := audit.List(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("audit.List: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Action == "token.use" && e.Username == "root@pam" {
			found = true
		}
	}
	if !found {
		t.Errorf("no token.use audit entry found among %+v", entries)
	}
}

func TestBearerAuth_DisabledWhenNoTokensRepoConfigured(t *testing.T) {
	sessions, audit, _ := newTestStore(t)
	svc, err := auth.NewService(auth.Config{Sessions: sessions, Audit: audit, NewIdentity: stubFactory(stubIdentity{})})
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	ts := newBearerTestServer(t, svc, auth.CapNetRead)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/protected", nil)
	req.Header.Set("Authorization", "Bearer whatever")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /protected: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (bearer auth disabled without a Tokens repo)", resp.StatusCode)
	}
}

func TestBearerAuth_RateLimitedAfterBurstCapacity(t *testing.T) {
	sessions, audit, db := newTestStore(t)
	tokens := store.NewAPITokenRepo(db)
	svc, err := auth.NewService(auth.Config{
		Sessions: sessions, Audit: audit, Tokens: tokens,
		NewIdentity:     stubFactory(stubIdentity{}),
		BearerRateLimit: auth.RateLimitConfig{Capacity: 3, RefillEvery: time.Hour},
	})
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	ts := newBearerTestServer(t, svc, auth.CapNetRead)
	raw := mintToken(t, tokens, "tok1", `["netRead"]`)

	var lastStatus int
	for i := 0; i < 5; i++ {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/protected", nil)
		req.Header.Set("Authorization", "Bearer "+raw)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /protected (attempt %d): %v", i, err)
		}
		lastStatus = resp.StatusCode
		_ = resp.Body.Close()
	}
	if lastStatus != http.StatusTooManyRequests {
		t.Errorf("status after exceeding burst capacity = %d, want 429", lastStatus)
	}
}

// TestBearerAuth_ReadOnlyConstrainsTokens is T-2903 AC1: read_only = true
// is a server-enforced restriction for bearer tokens exactly as it is for
// cookie sessions — a write-scoped token minted before the flag flipped
// gets 403 on a write-gated route and 200 on a read-gated one. Before
// T-2903, forceReadOnly ran only on the cookie path, so such a token kept
// full write capability in a read-only deployment.
func TestBearerAuth_ReadOnlyConstrainsTokens(t *testing.T) {
	sessions, audit, db := newTestStore(t)
	tokens := store.NewAPITokenRepo(db)

	for _, tt := range []struct {
		name       string
		gate       auth.Cap
		wantStatus int
		readOnly   bool
	}{
		{name: "read-only refuses the write gate", readOnly: true, gate: auth.CapNetWrite, wantStatus: http.StatusForbidden},
		{name: "read-only still allows the read gate", readOnly: true, gate: auth.CapNetRead, wantStatus: http.StatusOK},
		{name: "writable deployment allows the write gate (control)", readOnly: false, gate: auth.CapNetWrite, wantStatus: http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc, err := auth.NewService(auth.Config{
				Sessions: sessions, Audit: audit, Tokens: tokens,
				ReadOnly:    tt.readOnly,
				NewIdentity: stubFactory(stubIdentity{}),
			})
			if err != nil {
				t.Fatalf("auth.NewService: %v", err)
			}
			ts := newBearerTestServer(t, svc, tt.gate)
			raw := mintToken(t, tokens, "tok-ro-"+tt.name, `["netWrite","netRead"]`)

			req, _ := http.NewRequest(http.MethodGet, ts.URL+"/protected", nil)
			req.Header.Set("Authorization", "Bearer "+raw)
			resp, err := ts.Client().Do(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
		})
	}
}

// TestBearerAuth_ExpiredTokenRefused is T-2903 AC2: a token past its
// expires_at is refused with the revoked-token error shape; a future
// expiry and a NULL expiry (pre-0048 tokens) both keep working.
func TestBearerAuth_ExpiredTokenRefused(t *testing.T) {
	ts, tokens, _ := newTokenTestSetup(t, auth.CapNetRead)

	now := time.Now().Unix()
	for _, tt := range []struct {
		name       string
		expiresAt  int64 // 0 = NULL
		wantStatus int
	}{
		{name: "expired token refused", expiresAt: now - 60, wantStatus: http.StatusUnauthorized},
		{name: "future expiry works", expiresAt: now + 3600, wantStatus: http.StatusOK},
		{name: "NULL expiry (pre-0048) works", expiresAt: 0, wantStatus: http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raw, hash, err := auth.GenerateAPIToken()
			if err != nil {
				t.Fatalf("GenerateAPIToken: %v", err)
			}
			tok := store.APIToken{
				ID: "tok-exp-" + tt.name, Name: tt.name, TokenHash: hash,
				ScopesJSON: `["netRead"]`, CreatedBy: "root@pam", CreatedAt: now,
			}
			if tt.expiresAt != 0 {
				tok.ExpiresAt.Int64, tok.ExpiresAt.Valid = tt.expiresAt, true
			}
			if createErr := tokens.Create(context.Background(), tok); createErr != nil {
				t.Fatalf("tokens.Create: %v", createErr)
			}

			req, _ := http.NewRequest(http.MethodGet, ts.URL+"/protected", nil)
			req.Header.Set("Authorization", "Bearer "+raw)
			resp, err := ts.Client().Do(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
		})
	}
}

// TestBearerAuth_TokenUseAggregatedHourly is T-2903 AC4 (as amended in the
// card's delivery record): N same-hour requests append ONE token.use row;
// the first request of the next hour appends a second row whose detail
// carries the previous hour's count. Audit rows are append-only, so the
// count travels in the next row rather than updating the first.
func TestBearerAuth_TokenUseAggregatedHourly(t *testing.T) {
	ts, tokens, db := newTokenTestSetup(t, auth.CapNetRead)
	raw := mintToken(t, tokens, "tok-agg", `["netRead"]`)

	countRows := func() int {
		entries, err := store.NewAuditRepo(db).List(context.Background(), "", 0)
		if err != nil {
			t.Fatalf("audit List: %v", err)
		}
		n := 0
		for _, e := range entries {
			if e.Action == "token.use" {
				n++
			}
		}
		return n
	}

	for i := 0; i < 5; i++ {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/protected", nil)
		req.Header.Set("Authorization", "Bearer "+raw)
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: status %d", i, resp.StatusCode)
		}
	}
	if got := countRows(); got != 1 {
		t.Fatalf("token.use rows after 5 same-hour requests = %d, want 1", got)
	}
}
