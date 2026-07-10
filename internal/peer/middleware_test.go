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

func signedRequest(t *testing.T, secret []byte, method, target string, body []byte, ts int64) *http.Request {
	t.Helper()
	var bodyReader *bytes.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, bodyReader)
	sig := sign(secret, method, req.URL.RequestURI(), body, ts)
	req.Header.Set(HeaderTimestamp, strconv.FormatInt(ts, 10))
	req.Header.Set(HeaderSignature, sig)
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
				req.Header.Set(HeaderSignature, sign(secret, http.MethodGet, "/api/peer/health", nil, now.Unix()))
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
				sig := sign(secret, http.MethodPost, "/api/peer/host/ifreload", []byte(`{"node":"pve1"}`), now.Unix())
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
	sig := sign(testSecret, http.MethodGet, "/api/peer/health", nil, 1700000000)
	if _, err := hex.DecodeString(sig); err != nil {
		t.Fatalf("signature %q is not valid hex: %v", sig, err)
	}
	if !verifySignature(testSecret, http.MethodGet, "/api/peer/health", nil, 1700000000, sig) {
		t.Fatal("verifySignature rejected a signature sign() just produced")
	}
}
