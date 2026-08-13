// webpush.go implements RFC 8291 (message encryption, aes128gcm) and RFC
// 8292 (VAPID request authentication) end to end, and the HTTP delivery
// that uses both. No third-party web-push library is used — this
// codebase's dependency policy (docs/development.md: "Adding any other
// dependency requires a justification note in the task report. Prefer
// stdlib.") and CLAUDE.md's "no new major dependencies without a note"
// both push toward implementing ~250 lines of well-specified, RFC-pinned
// crypto over adding a dependency for it, the same call this codebase
// already made for its other crypto (AES-256-GCM session sealing,
// Ed25519 blueprint signing, X25519 WireGuard keys — all stdlib). Flagged
// in the task report per CLAUDE.md regardless, since it is new, nontrivial
// code: correctness is proven byte-for-byte against RFC 8291 Appendix A's
// own worked example in webpush_rfc8291_test.go, not just "it round-trips
// against itself".

package push

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Subscription is a browser's web-push registration (the shape
// `PushManager.subscribe()` returns, and POST /push/subscriptions'
// request body): Endpoint is the push service URL to POST to; P256dh/Auth
// are the subscriber's ECDH public key and authentication secret, both
// base64url (RFC 8291 §4's "keys" object, unpadded — this package accepts
// either padding via base64.RawURLEncoding/URLEncoding fallback in
// decodeBase64URL below, since browsers are inconsistent about emitting
// padding).
type Subscription struct {
	Endpoint string
	P256dh   string
	Auth     string
}

const (
	// recordSize is the aes128gcm "rs" (record size) header field. This
	// package only ever sends single-record ("last record") messages —
	// web-push payloads are small, enumerated notifications (payload.go),
	// never large enough to need splitting — so any value at least
	// len(plaintext)+17 works; 4096 matches every reference web-push
	// implementation's default.
	recordSize = 4096
	saltLen    = 16
	aesKeyLen  = 16 // AES-128
	nonceLen   = 12
	authLen    = 16 // RFC 8291's fixed 16-byte auth secret

	// DefaultTTL is how long a push service should hold and retry
	// delivery of a message if the device is offline (RFC 8030 §5's TTL
	// header, seconds). 12 hours comfortably covers an overnight-offline
	// phone without holding a stale "awaiting confirm" ping indefinitely
	// — a changeset's own confirm deadline (docs/api.md: minutes, not
	// hours) will long since have resolved one way or another by then.
	DefaultTTL = 12 * time.Hour

	// vapidTokenTTL is how long the RFC 8292 JWT stays valid. Kept well
	// under the RFC's own recommended 24h ceiling; regenerated fresh on
	// every Send call rather than cached, since minting one is a single
	// ECDSA signature — not worth the complexity of a cache with its own
	// expiry-tracking bug surface.
	vapidTokenTTL = 1 * time.Hour
)

var errAuthSecretLen = fmt.Errorf("push: auth secret must be %d bytes", authLen)

// hkdfExtract is RFC 5869's HKDF-Extract: PRK = HMAC-Hash(salt, IKM).
func hkdfExtract(salt, ikm []byte) []byte {
	mac := hmac.New(sha256.New, salt)
	mac.Write(ikm)
	return mac.Sum(nil)
}

// hkdfExpandOnce is RFC 5869's HKDF-Expand restricted to a single output
// block (T(1) = HMAC-Hash(PRK, info || 0x01)) — every length this package
// ever derives (32-byte IKM, 16-byte CEK, 12-byte nonce) fits in one
// HMAC-SHA256 block (32 bytes), so a general multi-block implementation
// would be unused code. webpush_rfc8291_test.go proves this reproduces RFC
// 8291 Appendix A's worked example byte-for-byte.
func hkdfExpandOnce(prk, info []byte, length int) ([]byte, error) {
	if length > sha256.Size {
		return nil, fmt.Errorf("push: hkdfExpandOnce length %d exceeds single-block limit %d", length, sha256.Size)
	}
	mac := hmac.New(sha256.New, prk)
	mac.Write(info)
	mac.Write([]byte{0x01})
	return mac.Sum(nil)[:length], nil
}

// buildKeyInfo constructs RFC 8291 §3.4's "key_info": the label, a 0x00
// terminator, the subscriber's public key, and the sender's (ephemeral)
// public key, each a raw 65-byte uncompressed P-256 point.
func buildKeyInfo(uaPublic, asPublic []byte) []byte {
	const label = "WebPush: info"
	info := make([]byte, 0, len(label)+1+len(uaPublic)+len(asPublic))
	info = append(info, label...)
	info = append(info, 0x00)
	info = append(info, uaPublic...)
	info = append(info, asPublic...)
	return info
}

// encryptAES128GCM implements RFC 8291's message encryption over
// plaintext, addressed to a subscriber whose ECDH public key is uaPublic
// (the subscription's raw p256dh bytes) and whose authentication secret is
// authSecret (the subscription's raw auth bytes). salt and asPriv (the
// ephemeral "application server" keypair) are normally random per call
// (Send below supplies fresh ones each time — reusing a salt/key pair
// across messages is an RFC 8291 §4 MUST-NOT); webpush_rfc8291_test.go
// passes RFC 8291 Appendix A's fixed values to prove this function
// reproduces that worked example exactly.
func encryptAES128GCM(plaintext, uaPublic, authSecret, salt []byte, asPriv *ecdh.PrivateKey) ([]byte, error) {
	if len(authSecret) != authLen {
		return nil, errAuthSecretLen
	}
	curve := ecdh.P256()
	uaPub, err := curve.NewPublicKey(uaPublic)
	if err != nil {
		return nil, fmt.Errorf("push: parsing subscriber public key: %w", err)
	}
	ecdhSecret, err := asPriv.ECDH(uaPub)
	if err != nil {
		return nil, fmt.Errorf("push: computing ECDH shared secret: %w", err)
	}
	asPublic := asPriv.PublicKey().Bytes()

	// Stage 1 (RFC 8291 §3.4): derive IKM from the ECDH secret, salted by
	// the subscription's own auth secret — NOT the per-message salt,
	// which only salts stage 2 below.
	prkKey := hkdfExtract(authSecret, ecdhSecret)
	ikm, err := hkdfExpandOnce(prkKey, buildKeyInfo(uaPublic, asPublic), 32)
	if err != nil {
		return nil, err
	}

	// Stage 2 (RFC 8188 §2.1, the aes128gcm content-coding): derive the
	// content-encryption key and nonce from IKM, salted by the
	// per-message salt.
	prk := hkdfExtract(salt, ikm)
	cek, err := hkdfExpandOnce(prk, []byte("Content-Encoding: aes128gcm\x00"), aesKeyLen)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdfExpandOnce(prk, []byte("Content-Encoding: nonce\x00"), nonceLen)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, fmt.Errorf("push: constructing AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("push: constructing GCM mode: %w", err)
	}

	// RFC 8188 §2's "last record" padding delimiter (0x02) appended to the
	// plaintext before sealing; this package never splits a payload
	// across multiple records (recordSize's doc comment), so there is
	// never any padding after the delimiter.
	padded := make([]byte, 0, len(plaintext)+1)
	padded = append(padded, plaintext...)
	padded = append(padded, 0x02)
	sealed := gcm.Seal(nil, nonce, padded, nil)

	header := make([]byte, saltLen+4+1+len(asPublic))
	copy(header[:saltLen], salt)
	binary.BigEndian.PutUint32(header[saltLen:saltLen+4], recordSize)
	header[saltLen+4] = byte(len(asPublic))
	copy(header[saltLen+5:], asPublic)

	return append(header, sealed...), nil
}

// vapidJWT mints RFC 8292's compact JWS: header {"typ":"JWT","alg":"ES256"},
// claims {"aud": audience, "exp": <now+ttl>, "sub": subject}, signed with
// ES256 over the ASCII header.payload — a raw r||s signature (each
// zero-padded to 32 bytes), NOT the ASN.1 DER encoding ecdsa.Sign's sibling
// SignASN1 would produce, per JOSE's ES256 wire format (RFC 7518 §3.4).
func vapidJWT(priv *ecdsa.PrivateKey, audience, subject string, now time.Time, ttl time.Duration) (string, error) {
	header := base64URLEncode([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, err := json.Marshal(struct {
		Aud string `json:"aud"`
		Sub string `json:"sub"`
		Exp int64  `json:"exp"`
	}{Aud: audience, Exp: now.Add(ttl).Unix(), Sub: subject})
	if err != nil {
		return "", fmt.Errorf("push: encoding VAPID claims: %w", err)
	}
	signingInput := header + "." + base64URLEncode(claims)

	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, priv, digest[:])
	if err != nil {
		return "", fmt.Errorf("push: signing VAPID JWT: %w", err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])

	return signingInput + "." + base64URLEncode(sig), nil
}

func base64URLEncode(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// decodeBase64URL decodes s as base64url, trying the unpadded encoding
// first (what every browser's PushManager.subscribe() JSON serialization
// emits) and falling back to padded — some subscription JSON in the wild
// (and at least one older Firefox build) includes padding.
func decodeBase64URL(s string) ([]byte, error) {
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(s)
}

// audienceOf returns the scheme+host of endpoint (RFC 8292 §2's required
// VAPID `aud` claim: "the origin of the push resource").
func audienceOf(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("push: parsing subscription endpoint: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", errors.New("push: subscription endpoint is not an absolute URL")
	}
	return u.Scheme + "://" + u.Host, nil
}

// ErrGone is returned by Send when the push service reports the
// subscription is permanently invalid (HTTP 404 Not Found or 410 Gone —
// RFC 8030 §7's documented "the push service does not recognize the
// subscription" outcomes). Callers (Dispatcher) treat this as "prune the
// subscription", distinct from a transient delivery failure.
var ErrGone = errors.New("push: subscription no longer valid")

// SendConfig bundles what Send needs beyond the subscription and payload:
// the daemon's own VAPID identity and a mailto/https contact URI (RFC 8292
// §2's required `sub` claim — a push service may contact this address about
// delivery problems).
type SendConfig struct {
	VAPIDPrivateKey *ecdsa.PrivateKey
	Client          *http.Client
	Now             func() time.Time
	VAPIDSubject    string
}

// Send delivers one push message to sub's endpoint, encrypted per RFC 8291
// and authenticated per RFC 8292. It generates a fresh ephemeral ECDH
// keypair and a fresh random salt for this call (RFC 8291 §4's "these
// values MUST be unique to each message" requirement — reusing either
// across two messages to the same subscriber would let a passive observer
// of both ciphertexts recover the plaintext of both, the same reason AES-
// GCM nonces must never repeat under one key).
func Send(ctx context.Context, sub Subscription, payload []byte, cfg SendConfig) error {
	uaPublic, err := decodeBase64URL(sub.P256dh)
	if err != nil {
		return fmt.Errorf("push: decoding subscription p256dh: %w", err)
	}
	authSecret, err := decodeBase64URL(sub.Auth)
	if err != nil {
		return fmt.Errorf("push: decoding subscription auth: %w", err)
	}

	curve := ecdh.P256()
	asPriv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("push: generating ephemeral ECDH key: %w", err)
	}
	salt := make([]byte, saltLen)
	if _, saltErr := io.ReadFull(rand.Reader, salt); saltErr != nil {
		return fmt.Errorf("push: generating salt: %w", saltErr)
	}

	body, err := encryptAES128GCM(payload, uaPublic, authSecret, salt, asPriv)
	if err != nil {
		return fmt.Errorf("push: encrypting payload: %w", err)
	}

	audience, err := audienceOf(sub.Endpoint)
	if err != nil {
		return err
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	jwt, err := vapidJWT(cfg.VAPIDPrivateKey, audience, cfg.VAPIDSubject, now(), vapidTokenTTL)
	if err != nil {
		return err
	}
	vapidPub := PublicKeyBase64URL(&cfg.VAPIDPrivateKey.PublicKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("push: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", strconv.Itoa(int(DefaultTTL.Seconds())))
	req.Header.Set("Authorization", fmt.Sprintf("vapid t=%s, k=%s", jwt, vapidPub))

	client := cfg.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("push: delivering to %s: %w", audience, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return ErrGone
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return fmt.Errorf("push: %s responded %d", audience, resp.StatusCode)
	}
	return nil
}
