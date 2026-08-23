package peer

import (
	"bytes"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// handlerSpy records whether it was ever invoked — used to assert the auth
// middleware rejects bad requests before any handler logic runs (T-301
// AC2).
type handlerSpy struct{ called bool }

func (h *handlerSpy) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.called = true
		w.WriteHeader(http.StatusOK)
	})
}

func newTestMiddleware(t *testing.T, secret []byte, now time.Time) (*Server, *handlerSpy, http.Handler) {
	t.Helper()
	srv := NewServer(ServerOptions{
		Secrets: newStaticSecretStore(secret),
		Version: "test",
		Logger:  discardLogger(),
		Now:     func() time.Time { return now },
	})
	spy := &handlerSpy{}
	return srv, spy, srv.authMiddleware(spy.handler())
}

// signedRequest builds a request signed with the pre-T-3703, four-field
// (no-nonce) formula and no nonce headers at all — i.e. exactly what a
// peer that predates the nonce still sends, and also exactly the
// HeaderSignature half of what this build's own client now sends (see
// client.go's do(): HeaderSignature is always this). Most existing tests
// in this file use it deliberately: they are pinning that the legacy wire
// format keeps working unchanged, which is this task's compatibility
// guarantee (see authMiddleware's doc comment). Tests that need the
// current, nonce'd wire format use signedNonceRequest instead.
func signedRequest(t *testing.T, secret []byte, method, target string, body []byte, ts int64) *http.Request {
	t.Helper()
	var bodyReader *bytes.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, bodyReader)
	req.Header.Set(HeaderTimestamp, strconv.FormatInt(ts, 10))
	req.Header.Set(HeaderSignature, sign(secret, method, req.URL.RequestURI(), body, ts, ""))
	return req
}

// signedNonceRequest builds a request exactly the way this build's own
// Client (client.go's do()) actually signs every outgoing request post-
// T-3703: starts from signedRequest's legacy HeaderSignature (unchanged,
// always four-field) and adds HeaderNonce + a HeaderNonceSignature bound
// to it via the five-field formula. Passing the same nonce to two calls
// lets a test capture-and-replay it.
func signedNonceRequest(t *testing.T, secret []byte, method, target string, body []byte, ts int64, nonce string) *http.Request {
	t.Helper()
	req := signedRequest(t, secret, method, target, body, ts)
	req.Header.Set(HeaderNonce, nonce)
	req.Header.Set(HeaderNonceSignature, sign(secret, method, req.URL.RequestURI(), body, ts, nonce))
	return req
}

func TestAuthMiddleware_ValidRequestPasses(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	_, spy, mw := newTestMiddleware(t, testSecret, now)

	req := signedRequest(t, testSecret, http.MethodGet, "/api/peer/health", nil, now.Unix())
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if !spy.called {
		t.Fatal("handler was not called for a validly-signed request")
	}
}

func TestAuthMiddleware_SecuritySuite(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)

	cases := []struct {
		request func(t *testing.T, secret []byte) *http.Request
		name    string
	}{
		{
			name: "missing signature headers",
			request: func(t *testing.T, _ []byte) *http.Request {
				return httptest.NewRequest(http.MethodGet, "/api/peer/health", nil)
			},
		},
		{
			name: "garbage signature",
			request: func(t *testing.T, _ []byte) *http.Request {
				req := httptest.NewRequest(http.MethodGet, "/api/peer/health", nil)
				req.Header.Set(HeaderTimestamp, strconv.FormatInt(now.Unix(), 10))
				req.Header.Set(HeaderSignature, "not-hex-at-all-zzz")
				return req
			},
		},
		{
			name: "garbage timestamp",
			request: func(t *testing.T, secret []byte) *http.Request {
				req := httptest.NewRequest(http.MethodGet, "/api/peer/health", nil)
				req.Header.Set(HeaderTimestamp, "not-a-number")
				req.Header.Set(HeaderSignature, sign(secret, http.MethodGet, "/api/peer/health", nil, now.Unix(), ""))
				return req
			},
		},
		{
			name: "expired timestamp",
			request: func(t *testing.T, secret []byte) *http.Request {
				ts := now.Add(-ReplayWindow - time.Second).Unix()
				return signedRequest(t, secret, http.MethodGet, "/api/peer/health", nil, ts)
			},
		},
		{
			name: "timestamp too far in the future",
			request: func(t *testing.T, secret []byte) *http.Request {
				ts := now.Add(ReplayWindow + time.Second).Unix()
				return signedRequest(t, secret, http.MethodGet, "/api/peer/health", nil, ts)
			},
		},
		{
			name: "wrong secret",
			request: func(t *testing.T, _ []byte) *http.Request {
				wrongSecret := bytes.Repeat([]byte{0x99}, secretLen)
				return signedRequest(t, wrongSecret, http.MethodGet, "/api/peer/health", nil, now.Unix())
			},
		},
		{
			name: "body tampered after signing",
			request: func(t *testing.T, secret []byte) *http.Request {
				// Sign over one body but send a different one — the
				// signature was computed over a different body hash, so
				// this must fail verification exactly like a wrong secret
				// would.
				sig := sign(secret, http.MethodPost, "/api/peer/host/ifreload", []byte(`{"node":"pve1"}`), now.Unix(), "")
				req := httptest.NewRequest(http.MethodPost, "/api/peer/host/ifreload", bytes.NewReader([]byte(`{"node":"pve2-tampered"}`)))
				req.Header.Set(HeaderTimestamp, strconv.FormatInt(now.Unix(), 10))
				req.Header.Set(HeaderSignature, sig)
				return req
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, spy, mw := newTestMiddleware(t, testSecret, now)
			req := tc.request(t, testSecret)
			rec := httptest.NewRecorder()
			mw.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 (body: %s)", rec.Code, rec.Body.String())
			}
			if spy.called {
				t.Fatal("handler was called despite an invalid/unauthorized request — auth must reject before handler logic runs")
			}
		})
	}
}

func TestAuthMiddleware_ReplayedRequestRejected(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	_, spy, mw := newTestMiddleware(t, testSecret, now)

	body := []byte(`{"node":"pve1"}`)
	// First send succeeds.
	req1 := signedRequest(t, testSecret, http.MethodPost, "/api/peer/host/ifreload", body, now.Unix())
	rec1 := httptest.NewRecorder()
	mw.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200 (body: %s)", rec1.Code, rec1.Body.String())
	}
	if !spy.called {
		t.Fatal("handler should have been called for the first, valid request")
	}
	spy.called = false

	// Exact byte-for-byte replay of the same signed request must be
	// rejected even though the signature itself is perfectly valid.
	req2 := signedRequest(t, testSecret, http.MethodPost, "/api/peer/host/ifreload", body, now.Unix())
	rec2 := httptest.NewRecorder()
	mw.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("replayed request status = %d, want 401 (body: %s)", rec2.Code, rec2.Body.String())
	}
	if spy.called {
		t.Fatal("handler was called for a replayed request")
	}
}

func TestAuthMiddleware_SPASessionCookieGrantsNothing(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	_, spy, mw := newTestMiddleware(t, testSecret, now)

	// A request carrying a plausible-looking SPA session cookie but no
	// peer signature at all must still be rejected: cookies are never
	// consulted on peer routes.
	req := httptest.NewRequest(http.MethodGet, "/api/peer/health", nil)
	req.AddCookie(&http.Cookie{Name: "vnprox_session", Value: "totally-legit-session-id"})
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if spy.called {
		t.Fatal("handler was called for a cookie-only, unsigned request")
	}

	// Even a *validly signed* request carrying that cookie must succeed
	// purely on the strength of the signature — the cookie itself grants
	// nothing extra and isn't required either.
	req2 := signedRequest(t, testSecret, http.MethodGet, "/api/peer/health", nil, now.Unix())
	req2.AddCookie(&http.Cookie{Name: "vnprox_session", Value: "totally-legit-session-id"})
	rec2 := httptest.NewRecorder()
	mw.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec2.Code)
	}
}

func TestAuthMiddleware_OversizeBodyRejected(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	srv := NewServer(ServerOptions{
		Secrets:      newStaticSecretStore(testSecret),
		Version:      "test",
		Logger:       discardLogger(),
		Now:          func() time.Time { return now },
		MaxBodyBytes: 8,
	})
	spy := &handlerSpy{}
	mw := srv.authMiddleware(spy.handler())

	body := []byte("this body is definitely longer than eight bytes")
	req := signedRequest(t, testSecret, http.MethodPost, "/api/peer/host/ifreload", body, now.Unix())
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body: %s)", rec.Code, rec.Body.String())
	}
	if spy.called {
		t.Fatal("handler was called for an oversize body")
	}
}

// TestVerifySignature_HexRoundTrip is a small sanity check that sign/
// verifySignature agree with each other and with a manually hex-decoded
// comparison, pinning the wire format (hex, not base64).
func TestVerifySignature_HexRoundTrip(t *testing.T) {
	sig := sign(testSecret, http.MethodGet, "/api/peer/health", nil, 1700000000, "")
	if _, err := hex.DecodeString(sig); err != nil {
		t.Fatalf("signature %q is not valid hex: %v", sig, err)
	}
	if !verifySignature(testSecret, http.MethodGet, "/api/peer/health", nil, 1700000000, "", sig) {
		t.Fatal("verifySignature rejected a signature sign() just produced")
	}
}

// TestAuthMiddleware_SameSecondDuplicateBothAccepted is T-3703's core
// regression test, direction one: two GETs, identical in every way a
// pre-T-3703 signature covered (method, path, body, and — critically —
// the same unix-second timestamp, since ts is second-resolution and this
// is exactly what a sub-second poller produces), must BOTH be accepted.
// Before this task, the second one was rejected as a "replay" purely
// because it collided with the first on signature — the bug documented in
// planning/reports/audit-2026-08-21-peer-replay.md (~2,885 dropped reads/
// day on pvecube). Each request here gets its own nonce, exactly as
// client.go's do() generates one per call, which is what makes the two
// requests distinguishable to the replay cache despite being otherwise
// byte-identical.
func TestAuthMiddleware_SameSecondDuplicateBothAccepted(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	_, spy, mw := newTestMiddleware(t, testSecret, now)

	nonce1, err := generateNonce()
	if err != nil {
		t.Fatalf("generateNonce: %v", err)
	}
	nonce2, err := generateNonce()
	if err != nil {
		t.Fatalf("generateNonce: %v", err)
	}
	if nonce1 == nonce2 {
		t.Fatalf("two independently generated nonces collided: %q", nonce1)
	}

	req1 := signedNonceRequest(t, testSecret, http.MethodGet, "/api/peer/host/neighbors", nil, now.Unix(), nonce1)
	rec1 := httptest.NewRecorder()
	mw.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first poll status = %d, want 200 (body: %s)", rec1.Code, rec1.Body.String())
	}
	if !spy.called {
		t.Fatal("handler was not called for the first, validly-signed poll")
	}
	spy.called = false

	// Same method, same path, same (nil) body, same unix-second
	// timestamp — a pre-T-3703 signature would be byte-identical to
	// req1's. Only the nonce differs, exactly as two real polls of the
	// same peer route inside one wall-clock second would.
	req2 := signedNonceRequest(t, testSecret, http.MethodGet, "/api/peer/host/neighbors", nil, now.Unix(), nonce2)
	rec2 := httptest.NewRecorder()
	mw.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second, same-second poll status = %d, want 200 — a legitimate repeated poll must not be treated as a replay (body: %s)", rec2.Code, rec2.Body.String())
	}
	if !spy.called {
		t.Fatal("handler was not called for the second, same-second poll — it was wrongly rejected as a replay")
	}
}

// TestAuthMiddleware_CapturedNonceReplayRejected is T-3703's core
// regression test, direction two: replaying a captured nonce — the exact
// same signed request sent twice — must still be rejected. Direction one
// alone would prove nothing (a middleware that stopped replay-checking
// entirely would also pass it); this is the test that proves the nonce
// actually functions as a replay key rather than just an ignored header.
func TestAuthMiddleware_CapturedNonceReplayRejected(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	_, spy, mw := newTestMiddleware(t, testSecret, now)

	nonce, err := generateNonce()
	if err != nil {
		t.Fatalf("generateNonce: %v", err)
	}
	body := []byte(`{"node":"pve1"}`)

	req1 := signedNonceRequest(t, testSecret, http.MethodPost, "/api/peer/host/ifreload", body, now.Unix(), nonce)
	rec1 := httptest.NewRecorder()
	mw.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200 (body: %s)", rec1.Code, rec1.Body.String())
	}
	if !spy.called {
		t.Fatal("handler should have been called for the first, valid request")
	}
	spy.called = false

	// An attacker (or a buggy retry) replaying the captured nonce
	// verbatim — same nonce, same everything — must be rejected, even
	// though the underlying signature is (as always in a replay) still
	// perfectly valid on its own.
	req2 := signedNonceRequest(t, testSecret, http.MethodPost, "/api/peer/host/ifreload", body, now.Unix(), nonce)
	rec2 := httptest.NewRecorder()
	mw.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("replayed-nonce request status = %d, want 401 (body: %s)", rec2.Code, rec2.Body.String())
	}
	if spy.called {
		t.Fatal("handler was called for a request replaying a captured nonce")
	}
}

// TestAuthMiddleware_StrippedNonceReplayRejected closes the gap the
// coordinator flagged in this task's first draft: accepting a request via
// the nonce path must also poison the legacy signature for it, not just
// the nonce, or an attacker who captures a genuinely nonce'd request and
// strips HeaderNonce/HeaderNonceSignature before replaying it gets a
// replay the pre-T-3703 code would have refused (it recorded every
// accepted signature, unconditionally; a nonce-only recording leaves the
// legacy slot looking "first-seen"). Take a request accepted via the
// nonce path, strip the nonce headers, keep the legacy signature intact,
// and replay it: it must be rejected, not accepted-once-more.
func TestAuthMiddleware_StrippedNonceReplayRejected(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	_, spy, mw := newTestMiddleware(t, testSecret, now)

	nonce, err := generateNonce()
	if err != nil {
		t.Fatalf("generateNonce: %v", err)
	}
	body := []byte(`{"node":"pve1"}`)

	// The genuine, nonce'd request — accepted via the nonce path.
	req1 := signedNonceRequest(t, testSecret, http.MethodPost, "/api/peer/host/ifreload", body, now.Unix(), nonce)
	legacySig := req1.Header.Get(HeaderSignature)
	rec1 := httptest.NewRecorder()
	mw.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first (nonce'd) request status = %d, want 200 (body: %s)", rec1.Code, rec1.Body.String())
	}
	if !spy.called {
		t.Fatal("handler should have been called for the first, valid request")
	}
	spy.called = false

	// An attacker who captured req1 in flight strips HeaderNonce and
	// HeaderNonceSignature and replays the rest verbatim — same
	// HeaderTimestamp, same HeaderSignature, same body. This falls to the
	// legacy verification path (no usable nonce), and legacySig must
	// already be recorded there from req1's acceptance.
	req2 := signedRequest(t, testSecret, http.MethodPost, "/api/peer/host/ifreload", body, now.Unix())
	if got := req2.Header.Get(HeaderSignature); got != legacySig {
		t.Fatalf("test setup error: stripped replay's signature %q != original %q", got, legacySig)
	}
	rec2 := httptest.NewRecorder()
	mw.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("nonce-stripped replay status = %d, want 401 — the nonce path must poison the legacy signature too, or a captured nonce'd request can be replayed once by stripping the new headers", rec2.Code)
	}
	if spy.called {
		t.Fatal("handler was called for a nonce-stripped replay of an already-accepted request")
	}
}

// TestAuthMiddleware_LegacyPeerStillAcceptedByPatchedServer pins the
// rolling-upgrade compatibility decision written up in authMiddleware's
// doc comment: a peer that predates T-3703 and never sends HeaderNonce
// must keep working against a patched server, unchanged — including
// still being subject to the pre-existing same-second collision this task
// does not (and cannot, without that peer also upgrading) fix for it.
func TestAuthMiddleware_LegacyPeerStillAcceptedByPatchedServer(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	_, spy, mw := newTestMiddleware(t, testSecret, now)

	req := signedRequest(t, testSecret, http.MethodGet, "/api/peer/host/neighbors", nil, now.Unix())
	if req.Header.Get(HeaderNonce) != "" {
		t.Fatal("test setup error: signedRequest must not set HeaderNonce")
	}
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy (no-nonce) request status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if !spy.called {
		t.Fatal("handler was not called for a validly-signed legacy request")
	}
	spy.called = false

	// The pre-existing bug, unchanged: a second legacy request identical
	// in every way still collides on signature and is rejected, exactly
	// as it was before this task. Documenting this in a test (rather than
	// only in the doc comment) is what keeps a future change from
	// "fixing" it in a way that silently drops the fallback old peers
	// depend on.
	req2 := signedRequest(t, testSecret, http.MethodGet, "/api/peer/host/neighbors", nil, now.Unix())
	rec2 := httptest.NewRecorder()
	mw.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("second legacy request status = %d, want 401 (unchanged pre-T-3703 behavior for a peer that never sends a nonce)", rec2.Code)
	}
}

// TestLegacyVerifier_AcceptsANewClientsRequest is the direction that
// actually matters operationally, and the one the first draft of this fix
// got wrong: pvecube has root SSH, but pve001 — its live peer — has none;
// this project cannot upgrade it, possibly ever. So pvecube's own
// *outbound* calls to pve001 must keep verifying under pve001's existing,
// unmodifiable code once pvecube's client starts sending nonce headers.
//
// pve001's authMiddleware doesn't exist in this repo to invoke directly —
// it's a different, older binary — but its verification logic is exactly
// "check HeaderTimestamp/HeaderSignature against the four-field formula",
// i.e. verifySignature called with nonce == "" and nothing else consulted.
// That's reproduced directly here, against a request built exactly the
// way this build's own client (client.go's do()) actually builds one, via
// signedNonceRequest.
func TestLegacyVerifier_AcceptsANewClientsRequest(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	nonce, err := generateNonce()
	if err != nil {
		t.Fatalf("generateNonce: %v", err)
	}
	body := []byte(`{"node":"pve1"}`)
	req := signedNonceRequest(t, testSecret, http.MethodPost, "/api/peer/host/ifreload", body, now.Unix(), nonce)

	legacySig := req.Header.Get(HeaderSignature)
	if legacySig == "" {
		t.Fatal("test setup error: request carries no HeaderSignature")
	}
	// A pre-T-3703 verifier never reads HeaderNonce/HeaderNonceSignature —
	// it doesn't know they exist — so this call is exactly what it does.
	if !verifySignature(testSecret, http.MethodPost, req.URL.RequestURI(), body, now.Unix(), "", legacySig) {
		t.Fatal("a pre-T-3703 (four-field, nonce == \"\") verifier rejected a request built by this build's own client — " +
			"this is exactly the pvecube -> pve001 call path T-3702 fixed, breaking again through a different mechanism")
	}
}

// TestAuthMiddleware_RequireNonceRejectsLegacy pins ServerOptions.
// RequireNonce's behavior: once flipped, a request with no valid nonce
// binding is refused outright — no falling back to the legacy signature —
// while a properly nonce'd request is unaffected. This is the switch
// authMiddleware's doc comment describes as "the thing to flip once every
// peer sends a nonce"; it does nothing by itself (defaults to false) and
// nothing in this package ever sets it, by design.
func TestAuthMiddleware_RequireNonceRejectsLegacy(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	srv := NewServer(ServerOptions{
		Secrets:      newStaticSecretStore(testSecret),
		Version:      "test",
		Logger:       discardLogger(),
		Now:          func() time.Time { return now },
		RequireNonce: true,
	})
	spy := &handlerSpy{}
	mw := srv.authMiddleware(spy.handler())

	legacyReq := signedRequest(t, testSecret, http.MethodGet, "/api/peer/health", nil, now.Unix())
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, legacyReq)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("legacy request under RequireNonce status = %d, want 401 (body: %s)", rec.Code, rec.Body.String())
	}
	if spy.called {
		t.Fatal("handler was called for a legacy request while RequireNonce is set")
	}

	nonce, err := generateNonce()
	if err != nil {
		t.Fatalf("generateNonce: %v", err)
	}
	nonceReq := signedNonceRequest(t, testSecret, http.MethodGet, "/api/peer/health", nil, now.Unix(), nonce)
	rec2 := httptest.NewRecorder()
	mw.ServeHTTP(rec2, nonceReq)
	if rec2.Code != http.StatusOK {
		t.Fatalf("nonce'd request under RequireNonce status = %d, want 200 (body: %s)", rec2.Code, rec2.Body.String())
	}
	if !spy.called {
		t.Fatal("handler was not called for a validly nonce'd request under RequireNonce")
	}
}
