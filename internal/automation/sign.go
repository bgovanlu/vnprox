// SPDX-License-Identifier: Apache-2.0

// Package automation implements T-1104's webhook half of the automation
// firehose: signing/verifying delivery payloads and the retrying
// dispatcher that posts internal/topology.Hub's "events" envelope to every
// registered target. It deliberately mirrors internal/peer's HMAC
// construction (docs/api.md's Webhooks section: "mirrors the peer API's
// HMAC construction") and internal/findings/webhook.go's retry/backoff
// shape, rather than inventing new conventions for what is, at its core,
// the same "sign a body, retry with backoff, track consecutive failures"
// problem those two packages already solve independently for their own
// callers.
package automation

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// HeaderSignature is the header a webhook delivery carries its signature
// in (docs/api.md's Webhooks section). Unlike internal/peer's
// X-Vnprox-Peer-Signature (an intra-cluster, HMAC-over-method-path-body-
// timestamp scheme with a replay window), this is a one-shot outbound POST
// to an external, caller-chosen URL: there is no shared clock/replay
// window to defend since vnproxd is always the one initiating the
// request, so the signed material is simply the request body — the same
// "HMAC-SHA256 of the body, hex-encoded" convention GitHub/Stripe/Slack
// webhook signatures already use, mirroring internal/peer's choice of
// primitive (HMAC-SHA256, hex, constant-time compare) without importing
// its method/path/timestamp canonicalization, which has no equivalent
// here.
const HeaderSignature = "X-VNPROX-SIGNATURE"

// Sign returns the hex-encoded HMAC-SHA256 signature of body under secret —
// the value a webhook target verifies its received body against.
func Sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature reports whether supplied is the correct signature for
// body under secret, using hmac.Equal (constant-time) rather than a
// direct byte comparison — the same replay-safety precedent
// internal/peer/sign.go's verifySignature sets, applied here to a webhook
// target verifying vnprox's outbound delivery instead of a peer verifying
// an inbound one. Malformed supplied values (odd-length hex, non-hex
// characters) simply fail to decode and are treated as a verification
// failure rather than a panic, exactly like internal/peer's own function.
func VerifySignature(secret, body []byte, supplied string) bool {
	if len(secret) == 0 {
		return false
	}
	got, err := hex.DecodeString(supplied)
	if err != nil {
		return false
	}
	want := Sign(secret, body)
	wantBytes, err := hex.DecodeString(want)
	if err != nil {
		return false
	}
	return hmac.Equal(wantBytes, got)
}
