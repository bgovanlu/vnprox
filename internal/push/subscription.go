// subscription.go validates and normalizes what a browser's
// `PushManager.subscribe()` call hands back — the shape POST
// /push/subscriptions (internal/api/push.go) accepts on the wire — before
// it ever becomes a Subscription this package's Send/Dispatcher can use.

package push

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
)

// ParseSubscription validates and decodes a subscription request's raw
// wire fields into a Subscription, or returns a descriptive error suitable
// for a 400 response. It enforces exactly what RFC 8291/8292 and this
// package's Send need to hold true, not merely "non-empty":
//
//   - endpoint must be an absolute https URL (an http push endpoint would
//     mean the daemon delivers over plaintext to whatever is at that
//     address — not a real deployment's push service, ever).
//   - p256dh must decode to a 65-byte uncompressed P-256 point (0x04 prefix).
//   - auth must decode to exactly 16 bytes (RFC 8291 §4's fixed auth secret
//     length).
func ParseSubscription(endpoint, p256dhB64, authB64 string) (Subscription, error) {
	u, err := url.ParseRequestURI(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return Subscription{}, errors.New("push: endpoint must be an absolute https URL")
	}

	p256dh, err := decodeBase64URL(p256dhB64)
	if err != nil {
		return Subscription{}, fmt.Errorf("push: p256dh is not valid base64url: %w", err)
	}
	if len(p256dh) != uncompressedPointLen || p256dh[0] != 0x04 {
		return Subscription{}, fmt.Errorf("push: p256dh must decode to a %d-byte uncompressed P-256 point", uncompressedPointLen)
	}

	auth, err := decodeBase64URL(authB64)
	if err != nil {
		return Subscription{}, fmt.Errorf("push: auth is not valid base64url: %w", err)
	}
	if len(auth) != authLen {
		return Subscription{}, fmt.Errorf("push: auth must decode to %d bytes", authLen)
	}

	return Subscription{Endpoint: endpoint, P256dh: p256dhB64, Auth: authB64}, nil
}

// EndpointHash returns the hex SHA-256 of endpoint's raw bytes — the value
// stored (unencrypted) in push_subscriptions.endpoint_hash so the daemon
// can recognize a resubscribe to the same push service endpoint without
// ever decrypting an existing row to compare it (0046's migration doc
// comment).
func EndpointHash(endpoint string) string {
	sum := sha256.Sum256([]byte(endpoint))
	return hex.EncodeToString(sum[:])
}
