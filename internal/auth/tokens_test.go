// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"errors"
	"testing"
)

func TestGenerateAPIToken_HashMatchesHashAPIToken(t *testing.T) {
	raw, hash, err := GenerateAPIToken()
	if err != nil {
		t.Fatalf("GenerateAPIToken: %v", err)
	}
	if raw == "" || hash == "" {
		t.Fatalf("GenerateAPIToken returned empty raw/hash: %q / %q", raw, hash)
	}
	if got := HashAPIToken(raw); got != hash {
		t.Errorf("HashAPIToken(raw) = %q, want %q", got, hash)
	}

	raw2, hash2, err := GenerateAPIToken()
	if err != nil {
		t.Fatalf("GenerateAPIToken (2nd): %v", err)
	}
	if raw == raw2 || hash == hash2 {
		t.Error("two GenerateAPIToken calls produced the same value — entropy source broken")
	}
}

func TestParseScopes_ValidatesAgainstCapVocabulary(t *testing.T) {
	got, err := ParseScopes([]string{"netRead", "automation", "netRead"})
	if err != nil {
		t.Fatalf("ParseScopes: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ParseScopes de-dup result = %v, want 2 entries", got)
	}
	if got[0] != CapNetRead || got[1] != CapAutomation {
		t.Errorf("ParseScopes() = %v, want [netRead automation]", got)
	}

	if _, err := ParseScopes([]string{"bogus"}); !errors.Is(err, ErrInvalidScope) {
		t.Errorf("ParseScopes(bogus) = %v, want ErrInvalidScope", err)
	}
}

func TestIdentity_CanGrantScope(t *testing.T) {
	id := Identity{Caps: map[string]Capabilities{
		"pve1": {NetRead: true, SDNWrite: false},
	}}

	if !id.CanGrantScope(CapNetRead) {
		t.Error("CanGrantScope(netRead) = false, want true (held on pve1)")
	}
	if id.CanGrantScope(CapSDNWrite) {
		t.Error("CanGrantScope(sdnWrite) = true, want false (not held anywhere)")
	}
	// Automation is always grantable — it's not PVE-derived, so there is
	// nothing for a minting session to "exceed".
	if !id.CanGrantScope(CapAutomation) {
		t.Error("CanGrantScope(automation) = false, want true (always mintable)")
	}

	// Even an entirely empty Identity (no PVE caps at all) can still grant
	// automation.
	empty := Identity{}
	if !empty.CanGrantScope(CapAutomation) {
		t.Error("empty Identity CanGrantScope(automation) = false, want true")
	}
	if empty.CanGrantScope(CapNetRead) {
		t.Error("empty Identity CanGrantScope(netRead) = true, want false")
	}
}

func TestIdentity_ValidateScopeGrant(t *testing.T) {
	id := Identity{Caps: map[string]Capabilities{"pve1": {NetRead: true}}}

	if err := id.ValidateScopeGrant([]Cap{CapNetRead, CapAutomation}); err != nil {
		t.Errorf("ValidateScopeGrant(within caps) = %v, want nil", err)
	}

	err := id.ValidateScopeGrant([]Cap{CapNetRead, CapSDNWrite})
	if !errors.Is(err, ErrScopeExceedsCapabilities) {
		t.Errorf("ValidateScopeGrant(exceeding) = %v, want ErrScopeExceedsCapabilities", err)
	}
}

func TestCapabilitiesFromScopes(t *testing.T) {
	got := CapabilitiesFromScopes([]Cap{CapNetRead, CapSDNWrite, CapAutomation})
	want := Capabilities{NetRead: true, SDNWrite: true, Automation: true}
	if got != want {
		t.Errorf("CapabilitiesFromScopes() = %+v, want %+v", got, want)
	}
}

// A "capture"-scoped token must actually carry the Capture capability —
// otherwise ParseScopes/ValidateScopeGrant accept the scope but every
// /captures call 403s (review-T-1301 MAJOR-2: vocabulary-vs-enforcement gap).
func TestCapabilitiesFromScopes_Capture(t *testing.T) {
	got := CapabilitiesFromScopes([]Cap{CapCapture})
	if !got.Has(CapCapture) {
		t.Errorf("a capture-scoped token must grant the capture capability; got %+v", got)
	}
}

func TestScopeStrings(t *testing.T) {
	got := ScopeStrings([]Cap{CapNetRead, CapAutomation})
	want := []string{"netRead", "automation"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("ScopeStrings() = %v, want %v", got, want)
	}
}
