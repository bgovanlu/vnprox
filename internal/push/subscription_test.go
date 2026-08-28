// SPDX-License-Identifier: Apache-2.0

package push

import (
	"encoding/base64"
	"testing"
)

func validKeys(t *testing.T) (p256dhB64, authB64 string) {
	t.Helper()
	sub, _, authSecret := testSubscriber(t)
	return sub.P256dh, base64.RawURLEncoding.EncodeToString(authSecret)
}

func TestParseSubscription_Valid(t *testing.T) {
	p256dh, auth := validKeys(t)
	sub, err := ParseSubscription("https://push.example.com/send/abc", p256dh, auth)
	if err != nil {
		t.Fatalf("ParseSubscription: %v", err)
	}
	if sub.Endpoint != "https://push.example.com/send/abc" || sub.P256dh != p256dh || sub.Auth != auth {
		t.Errorf("ParseSubscription() = %+v, want fields echoed back verbatim", sub)
	}
}

func TestParseSubscription_RejectsNonHTTPSEndpoint(t *testing.T) {
	p256dh, auth := validKeys(t)
	for _, endpoint := range []string{
		"http://push.example.com/send/abc",
		"ftp://push.example.com/send/abc",
		"not a url",
		"",
	} {
		if _, err := ParseSubscription(endpoint, p256dh, auth); err == nil {
			t.Errorf("ParseSubscription(endpoint=%q) error = nil, want a rejection", endpoint)
		}
	}
}

func TestParseSubscription_RejectsMalformedKeys(t *testing.T) {
	p256dh, auth := validKeys(t)
	const endpoint = "https://push.example.com/send/abc"

	if _, err := ParseSubscription(endpoint, "not-base64!!!", auth); err == nil {
		t.Error("ParseSubscription with malformed p256dh error = nil, want a rejection")
	}
	if _, err := ParseSubscription(endpoint, base64URLEncode([]byte("too short")), auth); err == nil {
		t.Error("ParseSubscription with a too-short p256dh error = nil, want a rejection")
	}
	if _, err := ParseSubscription(endpoint, p256dh, base64URLEncode([]byte("wrong length"))); err == nil {
		t.Error("ParseSubscription with a wrong-length auth error = nil, want a rejection")
	}
}

func TestEndpointHash_SameInputSameHash_DifferentInputDifferentHash(t *testing.T) {
	a := EndpointHash("https://push.example.com/send/abc")
	b := EndpointHash("https://push.example.com/send/abc")
	c := EndpointHash("https://push.example.com/send/xyz")
	if a != b {
		t.Errorf("EndpointHash is not deterministic: %q != %q", a, b)
	}
	if a == c {
		t.Error("EndpointHash produced the same hash for two different endpoints")
	}
	if len(a) != 64 { // hex-encoded SHA-256
		t.Errorf("EndpointHash length = %d, want 64 (hex SHA-256)", len(a))
	}
}
