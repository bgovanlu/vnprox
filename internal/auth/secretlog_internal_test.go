// SPDX-License-Identifier: Apache-2.0

package auth

import "testing"

// TestLogSessionID_TruncatesToPrefix pins logSessionID's contract directly
// (T-604 secrets-in-logs sweep): a real 256-bit base64url session id (43
// chars) must come back truncated, never verbatim, while short/edge-case
// inputs degrade sanely instead of panicking.
func TestLogSessionID_TruncatesToPrefix(t *testing.T) {
	realID := "AbCdEfGhIjKlMnOpQrStUvWxYz0123456789-_ABCDE" // 44 chars, real-shaped.

	tests := []struct {
		name  string
		id    string
		want  string
		exact bool
	}{
		{name: "real-length id is truncated", id: realID, want: realID[:redactedIDPrefixLen] + "…"},
		{name: "empty id", id: "", want: "", exact: true},
		{name: "shorter than prefix returned as-is", id: "abc", want: "abc", exact: true},
		{name: "exactly prefix length returned as-is", id: "abcdefgh", want: "abcdefgh", exact: true},
		{name: "one longer than prefix truncates", id: "abcdefghi", want: "abcdefgh…", exact: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := logSessionID(tt.id)
			if got != tt.want {
				t.Errorf("logSessionID(%q) = %q, want %q", tt.id, got, tt.want)
			}
			if tt.id == realID && got == tt.id {
				t.Error("logSessionID must not return the full id verbatim")
			}
		})
	}
}
