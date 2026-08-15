package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"

	"github.com/bgovanlu/vnprox/internal/store"
)

// embedShellFS is a minimal SPA build tree for the embed view-route tests:
// newSPAHandler serves index.html for any non-file path (i.e. /embed/map).
var embedShellFS = fstest.MapFS{
	"index.html": &fstest.MapFile{Data: []byte("<html><body>vnprox embed shell</body></html>")},
}

func newEmbedMintRouter(t *testing.T, tokens APITokenStore, audit tokenAuditor, minter *fakeTokenMinter) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	mountEmbedTokenRoute(r, tokens, audit, minter)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts
}

func newEmbedViewRouter(t *testing.T, tokens APITokenStore, postureAvailable bool) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	mountEmbedViewRoutes(r, tokens, embedShellFS, postureAvailable, nil)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts
}

// TestCreateEmbedToken_RejectsWriteScope is AC1: minting an embed token with
// any write scope is a 400 regardless of the minting user's own
// capabilities (the default fakeTokenMinter echoes every scope back, i.e.
// "the user holds everything") — the read-only ceiling is enforced before
// the per-user ceiling.
func TestCreateEmbedToken_RejectsWriteScope(t *testing.T) {
	writeScopes := []string{"netWrite", "sdnWrite", "fwWrite", "guestNet", "capture", "automation"}
	for _, scope := range writeScopes {
		t.Run(scope, func(t *testing.T) {
			minter := &fakeTokenMinter{username: "admin", fakeAuth: fakeAuth{authenticated: true}}
			ts := newEmbedMintRouter(t, newFakeAPITokenStore(), &fakeTokenAuditor{}, minter)

			body, _ := json.Marshal(map[string]any{"name": "wiki", "scopes": []string{"netRead", scope}})
			resp, err := http.Post(ts.URL+"/embed/tokens", "application/json", bytes.NewReader(body))
			if err != nil {
				t.Fatalf("POST /embed/tokens: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 for write scope %q", resp.StatusCode, scope)
			}
			if minter.generated != 0 {
				t.Errorf("a rejected mint must not generate a token (generated=%d)", minter.generated)
			}
		})
	}
}

// TestCreateEmbedToken_Success mints a read-only embed token: 201, embed:true,
// one-time raw reveal, hash-only persistence.
func TestCreateEmbedToken_Success(t *testing.T) {
	tokens := newFakeAPITokenStore()
	audit := &fakeTokenAuditor{}
	minter := &fakeTokenMinter{username: "alice", fakeAuth: fakeAuth{authenticated: true}}
	ts := newEmbedMintRouter(t, tokens, audit, minter)

	body, _ := json.Marshal(map[string]any{"name": "noc-screen", "scopes": []string{"netRead", "fwRead"}})
	resp, err := http.Post(ts.URL+"/embed/tokens", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /embed/tokens: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var got embedTokenCreateResponse
	if decErr := json.NewDecoder(resp.Body).Decode(&got); decErr != nil {
		t.Fatalf("decode: %v", decErr)
	}
	if !got.Embed {
		t.Error("response embed = false, want true")
	}
	if got.Token != "raw-token" {
		t.Errorf("Token = %q, want the one-time raw value", got.Token)
	}
	if len(got.Scopes) != 2 {
		t.Errorf("Scopes = %v, want [netRead fwRead]", got.Scopes)
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

// TestCreateEmbedToken_EmptyScopesIs400 rejects a scope-less embed token.
func TestCreateEmbedToken_EmptyScopesIs400(t *testing.T) {
	minter := &fakeTokenMinter{username: "alice", fakeAuth: fakeAuth{authenticated: true}}
	ts := newEmbedMintRouter(t, newFakeAPITokenStore(), &fakeTokenAuditor{}, minter)

	body, _ := json.Marshal(map[string]any{"name": "x", "scopes": []string{}})
	resp, err := http.Post(ts.URL+"/embed/tokens", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /embed/tokens: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// seedEmbedToken inserts a token whose raw value hashes to its stored hash,
// so the embed view-route auth (which hashes ?token=) can find it.
func seedEmbedToken(t *testing.T, tokens *fakeAPITokenStore, id, raw string, scopes []string, revoked bool) {
	t.Helper()
	scopesJSON, _ := json.Marshal(scopes)
	rec := store.APIToken{ID: id, Name: id, TokenHash: hashEmbedToken(raw), ScopesJSON: string(scopesJSON), CreatedBy: "alice", CreatedAt: 1}
	if revoked {
		rec.RevokedAt.Int64, rec.RevokedAt.Valid = 2, true
	}
	if err := tokens.Create(context.Background(), rec); err != nil {
		t.Fatalf("seed token: %v", err)
	}
}

// TestEmbedView_ValidTokenServesShell is the backend half of AC2: a valid
// read-scoped token in the query string serves the SPA shell.
func TestEmbedView_ValidTokenServesShell(t *testing.T) {
	tokens := newFakeAPITokenStore()
	seedEmbedToken(t, tokens, "e1", "raw-embed", []string{"netRead"}, false)
	ts := newEmbedViewRouter(t, tokens, true)

	for _, view := range []string{"map", "dashboard", "posture"} {
		resp, err := http.Get(ts.URL + "/embed/" + view + "?token=raw-embed")
		if err != nil {
			t.Fatalf("GET /embed/%s: %v", view, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /embed/%s status = %d, want 200", view, resp.StatusCode)
		}
		if !strings.Contains(string(body), "embed shell") {
			t.Errorf("GET /embed/%s did not serve the SPA shell: %q", view, string(body))
		}
	}
}

// TestEmbedView_RevokedTokenIs401 is the other half of AC2.
func TestEmbedView_RevokedTokenIs401(t *testing.T) {
	tokens := newFakeAPITokenStore()
	seedEmbedToken(t, tokens, "e1", "raw-embed", []string{"netRead"}, true)
	ts := newEmbedViewRouter(t, tokens, true)

	resp, err := http.Get(ts.URL + "/embed/map?token=raw-embed")
	if err != nil {
		t.Fatalf("GET /embed/map: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for a revoked embed token", resp.StatusCode)
	}
}

// TestEmbedView_UnknownTokenIs401 covers a token value that matches nothing.
func TestEmbedView_UnknownTokenIs401(t *testing.T) {
	ts := newEmbedViewRouter(t, newFakeAPITokenStore(), true)
	resp, err := http.Get(ts.URL + "/embed/map?token=nope")
	if err != nil {
		t.Fatalf("GET /embed/map: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

// TestEmbedView_RejectsSessionCookie is AC6: an embed route never accepts a
// session cookie in place of an embed token. Even with a cookie present, no
// ?token= means 401 — embedViewAuth never inspects the cookie.
func TestEmbedView_RejectsSessionCookie(t *testing.T) {
	tokens := newFakeAPITokenStore()
	seedEmbedToken(t, tokens, "e1", "raw-embed", []string{"netRead"}, false)
	ts := newEmbedViewRouter(t, tokens, true)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/embed/map", nil)
	req.Header.Set("Cookie", "vnprox_session=some-live-session-id")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /embed/map: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (a session cookie is not an embed token)", resp.StatusCode)
	}
}

// TestEmbedView_WriteScopedTokenIs403 confirms a token carrying any write
// scope is refused at an embed route even if it is otherwise valid — a
// write-capable automation token is not an embed token.
func TestEmbedView_WriteScopedTokenIs403(t *testing.T) {
	tokens := newFakeAPITokenStore()
	seedEmbedToken(t, tokens, "e1", "raw-embed", []string{"netRead", "netWrite"}, false)
	ts := newEmbedViewRouter(t, tokens, true)

	resp, err := http.Get(ts.URL + "/embed/map?token=raw-embed")
	if err != nil {
		t.Fatalf("GET /embed/map: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a write-scoped token at an embed route", resp.StatusCode)
	}
}

// TestEmbedPosture_DarkState is AC5: /embed/posture exists but returns the
// documented "wired but dark" state until posture scoring is available.
func TestEmbedPosture_DarkState(t *testing.T) {
	tokens := newFakeAPITokenStore()
	seedEmbedToken(t, tokens, "e1", "raw-embed", []string{"netRead"}, false)
	ts := newEmbedViewRouter(t, tokens, false /* postureAvailable */)

	resp, err := http.Get(ts.URL + "/embed/posture?token=raw-embed")
	if err != nil {
		t.Fatalf("GET /embed/posture: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (documented dark state)", resp.StatusCode)
	}
	if !strings.Contains(string(body), embedPostureDarkMarker) {
		t.Errorf("dark-state body = %q, want the %q marker", string(body), embedPostureDarkMarker)
	}
	// The map view is unaffected by posture availability.
	mapResp, err := http.Get(ts.URL + "/embed/map?token=raw-embed")
	if err != nil {
		t.Fatalf("GET /embed/map: %v", err)
	}
	defer func() { _ = mapResp.Body.Close() }()
	if mapResp.StatusCode != http.StatusOK {
		t.Errorf("map status = %d, want 200 regardless of posture availability", mapResp.StatusCode)
	}
}

// TestEmbedView_FrameHeaders_DefaultSelfOnly is T-2901 AC3, asserted
// through the full router so the interplay with the router-wide
// securityHeadersMiddleware is what's under test: an /embed/* response
// carries no X-Frame-Options and `frame-ancestors 'self'` (same-origin
// embedding only — the default with no embed_frame_ancestors configured),
// while an app route still carries DENY + `frame-ancestors 'none'`.
func TestEmbedView_FrameHeaders_DefaultSelfOnly(t *testing.T) {
	tokens := newFakeAPITokenStore()
	seedEmbedToken(t, tokens, "e1", "raw-embed", []string{"netRead"}, false)
	r := NewRouter(Options{Version: "test", DistFS: embedShellFS, Logger: testLogger(), Tokens: tokens})

	req := httptest.NewRequest(http.MethodGet, "/embed/map?token=raw-embed", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /embed/map status = %d, want 200", rec.Code)
	}
	if got, present := rec.Header()["X-Frame-Options"]; present {
		t.Errorf("X-Frame-Options = %q on an embed route, want the header absent", got)
	}
	// The full policy, exactly: identical to the app CSP except for
	// frame-ancestors — the embed relaxation must not drift any other
	// directive.
	const wantCSP = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; frame-src 'none'; worker-src 'self'; manifest-src 'self'; form-action 'self'; frame-ancestors 'self'; base-uri 'self'"
	if csp := rec.Header().Get("Content-Security-Policy"); csp != wantCSP {
		t.Errorf("embed CSP = %q, want exactly %q", csp, wantCSP)
	}

	// An app route through the same router is unchanged: still DENY + 'none'.
	appReq := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	appRec := httptest.NewRecorder()
	r.ServeHTTP(appRec, appReq)
	if got := appRec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("app-route X-Frame-Options = %q, want DENY", got)
	}
	if csp := appRec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("app-route CSP = %q, want frame-ancestors 'none'", csp)
	}
}

// TestEmbedView_FrameHeaders_ConfiguredOrigins is T-2901 AC4's directive
// half: with embed_frame_ancestors = ["https://wiki.example"], the emitted
// directive is exactly `frame-ancestors 'self' https://wiki.example` —
// 'self' always first, the configured origins verbatim after it.
func TestEmbedView_FrameHeaders_ConfiguredOrigins(t *testing.T) {
	tokens := newFakeAPITokenStore()
	seedEmbedToken(t, tokens, "e1", "raw-embed", []string{"netRead"}, false)
	r := NewRouter(Options{
		Version:             "test",
		DistFS:              embedShellFS,
		Logger:              testLogger(),
		Tokens:              tokens,
		EmbedFrameAncestors: []string{"https://wiki.example"},
	})

	req := httptest.NewRequest(http.MethodGet, "/embed/dashboard?token=raw-embed", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /embed/dashboard status = %d, want 200", rec.Code)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	var got string
	for _, dir := range strings.Split(csp, ";") {
		if dir = strings.TrimSpace(dir); strings.HasPrefix(dir, "frame-ancestors") {
			got = dir
		}
	}
	if want := "frame-ancestors 'self' https://wiki.example"; got != want {
		t.Errorf("frame-ancestors directive = %q, want exactly %q (full CSP: %q)", got, want, csp)
	}
	if got, present := rec.Header()["X-Frame-Options"]; present {
		t.Errorf("X-Frame-Options = %q on an embed route, want the header absent", got)
	}
}

// TestEmbedView_UnknownViewIs404 rejects an unregistered view name.
func TestEmbedView_UnknownViewIs404(t *testing.T) {
	tokens := newFakeAPITokenStore()
	seedEmbedToken(t, tokens, "e1", "raw-embed", []string{"netRead"}, false)
	ts := newEmbedViewRouter(t, tokens, true)

	resp, err := http.Get(ts.URL + "/embed/firewall?token=raw-embed")
	if err != nil {
		t.Fatalf("GET /embed/firewall: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for an unknown embed view", resp.StatusCode)
	}
}
