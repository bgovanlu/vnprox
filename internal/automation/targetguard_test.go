// SPDX-License-Identifier: Apache-2.0

package automation

import (
	"strings"
	"testing"
)

// TestTargetPolicy_ValidateURL is T-2905's registration-time SSRF gate:
// non-public destinations and plain http are refused by default, each knob
// admits exactly its class, and the refusal names the config key.
func TestTargetPolicy_ValidateURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr string // "" = allowed
		policy  TargetPolicy
	}{
		{name: "public https allowed", url: "https://hooks.example.com/x"},
		{name: "loopback refused", url: "https://127.0.0.1/x", wantErr: "non-public address"},
		{name: "rfc1918 refused", url: "https://10.0.0.1/x", wantErr: "non-public address"},
		{name: "metadata address refused", url: "https://169.254.169.254/latest", wantErr: "non-public address"},
		{name: "v6 link-local refused", url: "https://[fe80::1]/x", wantErr: "non-public address"},
		{name: "plain http refused", url: "http://hooks.example.com/x", wantErr: "allow_insecure_targets"},
		{name: "allow_private admits loopback", policy: TargetPolicy{AllowPrivate: true}, url: "https://127.0.0.1/x"},
		{name: "allow_private does not admit http", policy: TargetPolicy{AllowPrivate: true}, url: "http://10.0.0.1/x", wantErr: "allow_insecure_targets"},
		{name: "allow_insecure admits http but not private", policy: TargetPolicy{AllowInsecure: true}, url: "http://127.0.0.1/x", wantErr: "non-public address"},
		{name: "both knobs admit http to loopback", policy: TargetPolicy{AllowPrivate: true, AllowInsecure: true}, url: "http://127.0.0.1/x"},
		{name: "garbage refused", url: "gopher://x", wantErr: "absolute http(s) URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.policy.ValidateURL(tt.url)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateURL(%q) = %v, want allowed", tt.url, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateURL(%q) = %v, want an error containing %q", tt.url, err, tt.wantErr)
			}
		})
	}
}

// TestTargetPolicy_GuardedClientRefusesResolvedPrivate is the dial-time
// half: a hostname is fine at registration, but if it RESOLVES to a
// non-public address the connection itself is refused — the rebinding case
// the URL check cannot see. localhost resolves to 127.0.0.1, standing in
// for any rebinding name.
func TestTargetPolicy_GuardedClientRefusesResolvedPrivate(t *testing.T) {
	client := TargetPolicy{}.GuardedClient(nil)
	_, err := client.Get("http://localhost:9/") // port 9: nothing listens; the guard must fire before any dial completes
	if err == nil || !strings.Contains(err.Error(), "non-public address") {
		t.Fatalf("dial to a loopback-resolving name = %v, want the address-class refusal", err)
	}
	permissive := TargetPolicy{AllowPrivate: true}.GuardedClient(nil)
	_, err = permissive.Get("http://localhost:9/")
	if err == nil || strings.Contains(err.Error(), "non-public address") {
		t.Fatalf("allow_private dial = %v, want an ordinary connection error, not the policy refusal", err)
	}
}
