package peer

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// FuzzPeerAuth fuzzes the peer auth middleware's request-parsing/signature-
// verification path (T-301 acceptance criterion 5: "Fuzz the signature
// parser/verifier 60s clean") with arbitrary timestamp headers, signature
// headers, and bodies. The only invariant asserted is that it never panics:
// every input is expected to either pass (200, only for the specific
// deliberately-valid seed) or be cleanly rejected (401/413), since almost
// all fuzzer-generated input is exactly the malformed/garbage/hostile
// input this middleware exists to reject safely.
//
// Run for the CI-documented 60s budget with:
//
//	go test -run='^$' -fuzz=FuzzPeerAuth -fuzztime=60s ./internal/peer/
func FuzzPeerAuth(f *testing.F) {
	now := time.Unix(1_700_000_000, 0)
	srv := NewServer(ServerOptions{
		Secrets: newStaticSecretStore(testSecret),
		Reader:  newSpyHostReader(),
		Writer:  newSpyHostWriter(),
		Version: "fuzz",
		Logger:  discardLogger(),
		Now:     func() time.Time { return now },
	})
	mw := srv.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	validSigLegacy := sign(testSecret, "POST", "/api/peer/host/ifreload", []byte(`{"node":"pve1"}`), now.Unix(), "")
	const validNonce = "0123456789abcdef0123456789abcdef"
	validNonceSig := sign(testSecret, "POST", "/api/peer/host/ifreload", []byte(`{"node":"pve1"}`), now.Unix(), validNonce)

	seeds := []struct {
		ts, sig, nonce, nonceSig string
		body                     []byte
	}{
		{"", "", "", "", nil},
		{"0", "", "", "", nil},
		{"not-a-number", "deadbeef", "", "", []byte("{}")},
		{"1700000000", "", "", "", nil},
		// Legacy: exactly what a pre-T-3703 peer, or this build with the
		// nonce headers stripped, sends.
		{"1700000000", validSigLegacy, "", "", []byte(`{"node":"pve1"}`)},
		// T-3703: the full nonce'd wire format this build's own client
		// actually sends — HeaderSignature always the legacy value,
		// HeaderNonceSignature bound to HeaderNonce.
		{"1700000000", validSigLegacy, validNonce, validNonceSig, []byte(`{"node":"pve1"}`)},
		// A nonce header with no (or a garbage) nonce signature must fall
		// back to the legacy check on HeaderSignature (still valid here),
		// never crash and never wrongly grant the nonce-keyed path.
		{"1700000000", validSigLegacy, validNonce, "", []byte(`{"node":"pve1"}`)},
		{"1700000000", validSigLegacy, validNonce, "not-a-real-signature", []byte(`{"node":"pve1"}`)},
		// A nonce signature that doesn't match the claimed nonce (wrong
		// nonce value alongside a real signature for a different one)
		// must not verify either.
		{"1700000000", validSigLegacy, "not-the-real-nonce", validNonceSig, []byte(`{"node":"pve1"}`)},
		{"1700000000", "zz", "", "", []byte(`{"node":"pve1"}`)},
		{"1700000000", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "", "", nil},
		{"-99999999999999999999999999999999999", "ab", "", "", []byte{0x00, 0x01, 0xff}},
		{"99999999999999999999999999999999999", "ab", "", "", nil},
		{"1700000000", "0", "", "", []byte(bytes.Repeat([]byte{'a'}, 10000))},
	}
	for _, s := range seeds {
		f.Add(s.ts, s.sig, s.nonce, s.nonceSig, s.body)
	}

	f.Fuzz(func(t *testing.T, ts, sig, nonce, nonceSig string, body []byte) {
		req := httptest.NewRequest("POST", "/api/peer/host/ifreload", bytes.NewReader(body))
		req.Header.Set(HeaderTimestamp, ts)
		req.Header.Set(HeaderSignature, sig)
		if nonce != "" {
			req.Header.Set(HeaderNonce, nonce)
		}
		if nonceSig != "" {
			req.Header.Set(HeaderNonceSignature, nonceSig)
		}
		rec := httptest.NewRecorder()

		// Must never panic; any status code is acceptable (200 for the
		// deliberately validly-signed seeds, 401/413 otherwise).
		mw.ServeHTTP(rec, req)
	})
}
