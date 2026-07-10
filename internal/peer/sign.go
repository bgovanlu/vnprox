package peer

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"
)

// HeaderTimestamp and HeaderSignature carry the peer-request signature
// (docs/security.md "Transport": "HMAC-SHA256 over (method, path, body
// hash, timestamp) with the cluster secret"). Neither is a standard HTTP
// header name — this is a private, intra-cluster scheme, not something any
// other client (browser, PVE) ever sends, so there is no collision risk to
// design around.
const (
	HeaderTimestamp = "X-Vnprox-Peer-Timestamp"
	HeaderSignature = "X-Vnprox-Peer-Signature"
)

// ReplayWindow is the maximum allowed clock skew between a peer request's
// signing timestamp and the verifier's clock (docs/security.md: "±30s
// replay window").
const ReplayWindow = 30 * time.Second

// canonicalRequest builds the exact byte string that gets HMAC'd: method,
// the request's path+query (RequestURI, so query-string tampering is
// covered too, not just the path), the hex-encoded SHA-256 of the body, and
// the signing timestamp — newline-joined so no field's contents can bleed
// into another's (methods are fixed tokens, a URI's path segments can't
// contain a raw newline, and the hash/timestamp are fixed-format
// hex/decimal).
func canonicalRequest(method, requestURI string, bodyHash [sha256.Size]byte, ts int64) []byte {
	buf := make([]byte, 0, len(method)+len(requestURI)+96)
	buf = append(buf, method...)
	buf = append(buf, '\n')
	buf = append(buf, requestURI...)
	buf = append(buf, '\n')
	buf = append(buf, hex.EncodeToString(bodyHash[:])...)
	buf = append(buf, '\n')
	buf = strconv.AppendInt(buf, ts, 10)
	return buf
}

// sign computes the hex-encoded HMAC-SHA256 signature for a peer request.
func sign(secret []byte, method, requestURI string, body []byte, ts int64) string {
	bodyHash := sha256.Sum256(body)
	mac := hmac.New(sha256.New, secret)
	mac.Write(canonicalRequest(method, requestURI, bodyHash, ts))
	return hex.EncodeToString(mac.Sum(nil))
}

// verifySignature reports whether supplied is the correct signature for
// this request under secret, using hmac.Equal — a constant-time
// comparison, per docs/security.md's explicit requirement — rather than
// ==, which short-circuits on the first differing byte and leaks timing
// information about how much of the signature matched.
//
// Malformed supplied values (odd-length hex, non-hex characters) simply
// fail to decode and are treated as a verification failure, never a panic
// or an error return — callers (the auth middleware, and the fuzz target)
// depend on this function being total over arbitrary input.
func verifySignature(secret []byte, method, requestURI string, body []byte, ts int64, supplied string) bool {
	if len(secret) == 0 {
		return false
	}
	got, err := hex.DecodeString(supplied)
	if err != nil {
		return false
	}
	bodyHash := sha256.Sum256(body)
	mac := hmac.New(sha256.New, secret)
	mac.Write(canonicalRequest(method, requestURI, bodyHash, ts))
	want := mac.Sum(nil)
	return hmac.Equal(want, got)
}
