package peer

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"
)

// HeaderTimestamp and HeaderSignature carry the peer-request signature
// (docs/security.md "Transport": "HMAC-SHA256 over (method, path, body
// hash, timestamp) with the cluster secret"). HeaderNonce and
// HeaderNonceSignature (T-3703) are additive: every request this build
// originates carries all four headers, but HeaderSignature by itself is
// still exactly the pre-T-3703 four-field signature, unconditionally — see
// authMiddleware's doc comment for why that particular field never
// changes format. None of the four is a standard HTTP header name — this
// is a private, intra-cluster scheme, not something any other client
// (browser, PVE) ever sends, so there is no collision risk to design
// around.
const (
	HeaderTimestamp      = "X-Vnprox-Peer-Timestamp"
	HeaderSignature      = "X-Vnprox-Peer-Signature"
	HeaderNonce          = "X-Vnprox-Peer-Nonce"
	HeaderNonceSignature = "X-Vnprox-Peer-Nonce-Signature"
)

// ReplayWindow is the maximum allowed clock skew between a peer request's
// signing timestamp and the verifier's clock (docs/security.md: "±30s
// replay window").
const ReplayWindow = 30 * time.Second

// nonceBytes is the nonce size in bytes (128 bits, T-3703's audit finding
// — see generateNonce).
const nonceBytes = 16

// generateNonce returns a fresh, random, hex-encoded 128-bit value for
// HeaderNonce. It must come from crypto/rand, never math/rand: the nonce is
// what makes two identically-timestamped, identically-bodied requests
// distinguishable to the replay cache (see canonicalRequest and
// authMiddleware), so a predictable nonce would let an attacker who can
// guess or observe one signed request's nonce pre-compute another request's
// cache key — defeating the whole point of adding it.
func generateNonce() (string, error) {
	b := make([]byte, nonceBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("peer: generating request nonce: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// canonicalRequest builds the exact byte string that gets HMAC'd: method,
// the request's path+query (RequestURI, so query-string tampering is
// covered too, not just the path), the hex-encoded SHA-256 of the body, the
// signing timestamp, and — when nonce != "" — the request's nonce, all
// newline-joined so no field's contents can bleed into another's (methods
// are fixed tokens, a URI's path segments can't contain a raw newline, and
// the hash/timestamp/nonce are fixed-format hex/decimal/hex).
//
// This one function computes (and, via verifySignature, checks) *both*
// signatures a T-3703-and-later request carries: with nonce == "" it
// reproduces the exact pre-T-3703 four-field byte string
// (method\nrequestURI\nbodyHash\nts, no trailing newline) — that's
// HeaderSignature, always computed this way, by every client regardless of
// build age, because an older peer's authMiddleware only ever calls
// verifySignature with nonce == "" and only ever looks at that one header.
// With nonce set to the request's actual nonce, it produces the five-field
// string that's HeaderNonceSignature — a second, additive signature an
// older peer's authMiddleware simply never reads. Sending both, rather
// than putting the nonce inside HeaderSignature itself, is what keeps a
// pre-T-3703 verifier able to authenticate a post-T-3703 client's requests
// at all: see authMiddleware's doc comment for the deployment reason this
// matters (pve001) and why a single-signature, nonce-inside-the-primary-
// MAC design was rejected instead.
//
// Because HMAC is deterministic over its exact input, a signature computed
// over one formula can never be revalidated as having been computed over
// the other: an attacker cannot forge a valid HeaderNonceSignature for an
// arbitrary nonce without the secret, nor make a genuine HeaderSignature
// validate as a HeaderNonceSignature (or vice versa) by relabeling it.
// Dropping HeaderNonce/HeaderNonceSignature from a captured, genuinely
// nonce'd request and replaying just HeaderSignature does *not* get an
// attacker a replay the legacy path would otherwise refuse: authMiddleware
// records that request's HeaderSignature (not only its nonce) in the
// replay cache at the moment the nonce'd copy is accepted, specifically so
// the stripped copy is already-seen when it lands on the legacy check. See
// authMiddleware's and replayCache.seenBeforeNonce's doc comments for the
// mechanism and ServerOptions.RequireNonce for the switch that removes the
// legacy path (and this question) entirely.
func canonicalRequest(method, requestURI string, bodyHash [sha256.Size]byte, ts int64, nonce string) []byte {
	buf := make([]byte, 0, len(method)+len(requestURI)+len(nonce)+96)
	buf = append(buf, method...)
	buf = append(buf, '\n')
	buf = append(buf, requestURI...)
	buf = append(buf, '\n')
	buf = append(buf, hex.EncodeToString(bodyHash[:])...)
	buf = append(buf, '\n')
	buf = strconv.AppendInt(buf, ts, 10)
	if nonce != "" {
		buf = append(buf, '\n')
		buf = append(buf, nonce...)
	}
	return buf
}

// sign computes the hex-encoded HMAC-SHA256 signature for a peer request.
// Client.do calls this twice per request — once with nonce == "" for
// HeaderSignature, once with the request's real nonce for
// HeaderNonceSignature — per canonicalRequest's doc comment.
func sign(secret []byte, method, requestURI string, body []byte, ts int64, nonce string) string {
	bodyHash := sha256.Sum256(body)
	mac := hmac.New(sha256.New, secret)
	mac.Write(canonicalRequest(method, requestURI, bodyHash, ts, nonce))
	return hex.EncodeToString(mac.Sum(nil))
}

// verifySignature reports whether supplied is the correct signature for
// this request under secret, using hmac.Equal — a constant-time
// comparison, per docs/security.md's explicit requirement — rather than
// ==, which short-circuits on the first differing byte and leaks timing
// information about how much of the signature matched.
//
// nonce selects which of the two formulas canonicalRequest builds:
// authMiddleware passes "" to check HeaderSignature against the legacy
// four-field form, and the request's actual HeaderNonce value to check
// HeaderNonceSignature against the five-field form. See canonicalRequest's
// doc comment for why one function serving both call shapes is what keeps
// a pre-T-3703 peer's verifier (which only ever makes the first call)
// compatible with a post-T-3703 client.
//
// Malformed supplied values (odd-length hex, non-hex characters) simply
// fail to decode and are treated as a verification failure, never a panic
// or an error return — callers (the auth middleware, and the fuzz target)
// depend on this function being total over arbitrary input.
func verifySignature(secret []byte, method, requestURI string, body []byte, ts int64, nonce, supplied string) bool {
	if len(secret) == 0 {
		return false
	}
	got, err := hex.DecodeString(supplied)
	if err != nil {
		return false
	}
	bodyHash := sha256.Sum256(body)
	mac := hmac.New(sha256.New, secret)
	mac.Write(canonicalRequest(method, requestURI, bodyHash, ts, nonce))
	want := mac.Sum(nil)
	return hmac.Equal(want, got)
}
