// SPDX-License-Identifier: Apache-2.0

package push

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

// testSubscriber builds a fresh, valid subscriber keypair the same shape a
// real browser's PushManager.subscribe() would produce, for tests that need
// a Subscription to encrypt/send against without RFC 8291's fixed vectors.
func testSubscriber(t *testing.T) (sub Subscription, uaPriv *ecdh.PrivateKey, authSecret []byte) {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating subscriber key: %v", err)
	}
	auth := make([]byte, authLen)
	if _, err := io.ReadFull(rand.Reader, auth); err != nil {
		t.Fatalf("generating auth secret: %v", err)
	}
	return Subscription{
		Endpoint: "https://push.example.com/send/abc123",
		P256dh:   base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()),
		Auth:     base64.RawURLEncoding.EncodeToString(auth),
	}, priv, auth
}

func testVAPIDKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating VAPID test key: %v", err)
	}
	return priv
}

func TestSend_SetsExpectedHeadersAndDeliversDecryptablePayload(t *testing.T) {
	sub, uaPriv, authSecret := testSubscriber(t)
	vapidPriv := testVAPIDKey(t)
	wantPayload := []byte(`{"event":"changeset.awaiting_confirm","title":"Changeset awaiting confirm","body":"A changeset is waiting for confirmation.","url":"/changesets/01H_TEST/review"}`)

	var gotContentType, gotContentEncoding, gotTTL, gotAuth string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		gotContentEncoding = r.Header.Get("Content-Encoding")
		gotTTL = r.Header.Get("TTL")
		gotAuth = r.Header.Get("Authorization")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request body: %v", err)
		}
		gotBody = body
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	sub.Endpoint = srv.URL + "/send/abc123"

	fixedNow := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	err := Send(context.Background(), sub, wantPayload, SendConfig{
		VAPIDPrivateKey: vapidPriv,
		VAPIDSubject:    "mailto:ops@example.com",
		Client:          srv.Client(),
		Now:             func() time.Time { return fixedNow },
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if gotContentType != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", gotContentType)
	}
	if gotContentEncoding != "aes128gcm" {
		t.Errorf("Content-Encoding = %q, want aes128gcm", gotContentEncoding)
	}
	if gotTTL == "" {
		t.Error("TTL header not set")
	}
	if !strings.HasPrefix(gotAuth, "vapid t=") || !strings.Contains(gotAuth, ", k=") {
		t.Errorf("Authorization header = %q, want the vapid t=..., k=... scheme", gotAuth)
	}

	// Round-trip: decrypt what was actually sent over the wire using the
	// subscriber's OWN private key, proving the server encrypted TO this
	// subscriber (not e.g. to itself, or with a fixed/reused key) and that
	// the recovered plaintext is exactly the payload passed to Send.
	got := decryptForTest(t, gotBody, uaPriv, authSecret)
	if string(got) != string(wantPayload) {
		t.Errorf("decrypted payload = %q, want %q", got, wantPayload)
	}

	// The VAPID JWT's signature must verify against the VAPID key's OWN
	// public key — proves Send signed with the key it was configured
	// with, not some other key or an unsigned/malformed token.
	tParam := extractVAPIDParam(t, gotAuth, "t")
	verifyVAPIDJWT(t, tParam, &vapidPriv.PublicKey, srv.URL, "mailto:ops@example.com", fixedNow)
}

// TestSend_WrongVAPIDKeyFailsVerification is the negative leg proving the
// JWT-verification check above actually exercises the signature rather
// than trivially passing: signing with a DIFFERENT VAPID key must fail
// verification against the original key's public half.
func TestSend_WrongVAPIDKeyFailsVerification(t *testing.T) {
	sub, _, _ := testSubscriber(t)
	rightKey := testVAPIDKey(t)
	wrongKey := testVAPIDKey(t)

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	sub.Endpoint = srv.URL + "/send/abc123"

	now := time.Now()
	if err := Send(context.Background(), sub, []byte("x"), SendConfig{
		VAPIDPrivateKey: rightKey, VAPIDSubject: "mailto:ops@example.com", Client: srv.Client(), Now: func() time.Time { return now },
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	tParam := extractVAPIDParam(t, gotAuth, "t")
	parts := strings.Split(tParam, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT does not have 3 parts: %q", tParam)
	}
	digest := sha256Sum(parts[0] + "." + parts[1])
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decoding signature: %v", err)
	}
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	if ecdsa.Verify(&wrongKey.PublicKey, digest, r, s) {
		t.Error("JWT signed with rightKey verified against wrongKey's public key — verification check is not actually checking the key")
	}
	if !ecdsa.Verify(&rightKey.PublicKey, digest, r, s) {
		t.Error("JWT signed with rightKey did not verify against rightKey's own public key")
	}
}

func TestSend_MapsGoneAndNotFoundToErrGone(t *testing.T) {
	for _, status := range []int{http.StatusGone, http.StatusNotFound} {
		sub, _, _ := testSubscriber(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))
		sub.Endpoint = srv.URL + "/send/dead"

		err := Send(context.Background(), sub, []byte("x"), SendConfig{
			VAPIDPrivateKey: testVAPIDKey(t), VAPIDSubject: "mailto:ops@example.com", Client: srv.Client(),
		})
		srv.Close()
		if err == nil || !isErrGone(err) {
			t.Errorf("status %d: Send error = %v, want ErrGone", status, err)
		}
	}
}

func TestSend_OtherErrorStatusIsNotErrGone(t *testing.T) {
	sub, _, _ := testSubscriber(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	sub.Endpoint = srv.URL + "/send/x"

	err := Send(context.Background(), sub, []byte("x"), SendConfig{
		VAPIDPrivateKey: testVAPIDKey(t), VAPIDSubject: "mailto:ops@example.com", Client: srv.Client(),
	})
	if err == nil {
		t.Fatal("Send against a 500 returned nil error")
	}
	if isErrGone(err) {
		t.Error("a transient 500 was classified as ErrGone — would wrongly prune a live subscription")
	}
}

func isErrGone(err error) bool {
	for err != nil {
		if err == ErrGone { //nolint:errorlint // exact sentinel used deliberately by Send
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// --- test-only helpers below: an independent decrypt/verify path so the
// tests above are not just "Send doesn't error", but confirm what actually
// went over the wire. ---

func decryptForTest(t *testing.T, message []byte, uaPriv *ecdh.PrivateKey, authSecret []byte) []byte {
	t.Helper()
	if len(message) < saltLen+4+1 {
		t.Fatalf("message too short: %d bytes", len(message))
	}
	salt := message[:saltLen]
	idlen := int(message[saltLen+4])
	asPublic := message[saltLen+5 : saltLen+5+idlen]
	ciphertext := message[saltLen+5+idlen:]

	asPub, err := ecdh.P256().NewPublicKey(asPublic)
	if err != nil {
		t.Fatalf("parsing ephemeral public key from message: %v", err)
	}
	ecdhSecret, err := uaPriv.ECDH(asPub)
	if err != nil {
		t.Fatalf("ECDH: %v", err)
	}

	uaPublic := uaPriv.PublicKey().Bytes()
	prkKey := hkdfExtract(authSecret, ecdhSecret)
	ikm, err := hkdfExpandOnce(prkKey, buildKeyInfo(uaPublic, asPublic), 32)
	if err != nil {
		t.Fatalf("deriving IKM: %v", err)
	}
	prk := hkdfExtract(salt, ikm)
	cek, err := hkdfExpandOnce(prk, []byte("Content-Encoding: aes128gcm\x00"), aesKeyLen)
	if err != nil {
		t.Fatalf("deriving CEK: %v", err)
	}
	nonce, err := hkdfExpandOnce(prk, []byte("Content-Encoding: nonce\x00"), nonceLen)
	if err != nil {
		t.Fatalf("deriving nonce: %v", err)
	}

	block, err := aes.NewCipher(cek)
	if err != nil {
		t.Fatalf("constructing AES cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("constructing GCM AEAD: %v", err)
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		t.Fatalf("decrypting: %v", err)
	}
	// Strip the RFC 8188 last-record delimiter (0x02) this package appends.
	if len(plain) == 0 || plain[len(plain)-1] != 0x02 {
		t.Fatalf("decrypted plaintext missing the 0x02 last-record delimiter: %x", plain)
	}
	return plain[:len(plain)-1]
}

var vapidParamRE = regexp.MustCompile(`(?:^|, )(\w)=([^,]+)`)

func extractVAPIDParam(t *testing.T, authHeader, name string) string {
	t.Helper()
	// authHeader looks like: vapid t=<jwt>, k=<pubkey>
	trimmed := strings.TrimPrefix(authHeader, "vapid ")
	for _, m := range vapidParamRE.FindAllStringSubmatch(trimmed, -1) {
		if m[1] == name {
			return m[2]
		}
	}
	t.Fatalf("Authorization header %q has no %s= parameter", authHeader, name)
	return ""
}

func verifyVAPIDJWT(t *testing.T, jwt string, pub *ecdsa.PublicKey, wantAud, wantSub string, now time.Time) {
	t.Helper()
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT does not have 3 dot-separated parts: %q", jwt)
	}
	digest := sha256Sum(parts[0] + "." + parts[1])
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		t.Fatalf("decoding JWT signature: err=%v len=%d", err, len(sig))
	}
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(pub, digest, r, s) {
		t.Fatal("JWT signature does not verify against the expected VAPID public key")
	}

	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decoding claims: %v", err)
	}
	var claims struct {
		Aud string `json:"aud"`
		Sub string `json:"sub"`
		Exp int64  `json:"exp"`
	}
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		t.Fatalf("unmarshaling claims: %v", err)
	}
	if claims.Aud != wantAud {
		t.Errorf("claims.aud = %q, want %q", claims.Aud, wantAud)
	}
	if claims.Sub != wantSub {
		t.Errorf("claims.sub = %q, want %q", claims.Sub, wantSub)
	}
	if claims.Exp <= now.Unix() {
		t.Errorf("claims.exp = %d is not after now (%d)", claims.Exp, now.Unix())
	}
}

func sha256Sum(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}
