// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"reflect"
	"testing"
)

// TestExplain_GrantedIsUnaffected pins that a capability the identity
// actually holds gets a plain "yes", never a Missing/Reason payload — the
// permitted-action case T-4105's card requires stay unaffected.
func TestExplain_GrantedIsUnaffected(t *testing.T) {
	id := Identity{Caps: map[string]Capabilities{
		"pve1": {NetRead: true, NetWrite: true},
	}}
	got := id.Explain(CapNetWrite, "pve1", false)
	want := Explanation{Capability: "netWrite", Granted: true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Explain(netWrite, granted) = %+v, want %+v", got, want)
	}
}

// TestExplain_SingleMissingPrivilege is the "genuinely restricted
// principal" auditor shape (Sys.Audit/VM.Audit/SDN.Audit only, per
// testdata/clusters/three-node-vlan.yaml — see explain_integration_test.go
// for the end-to-end version against real pvemock): missing exactly one
// capability names exactly one privilege, precisely and confirmed.
func TestExplain_SingleMissingPrivilege(t *testing.T) {
	auditor := Capabilities{NetRead: true, SDNRead: true, FWRead: true, Audit: true}
	id := Identity{Caps: map[string]Capabilities{"pve1": auditor}}

	cases := []struct {
		cap  Cap
		want []PrivilegeRequirement
	}{
		{CapNetWrite, []PrivilegeRequirement{{Privilege: "Sys.Modify", Path: "/nodes/pve1", Confirmed: true}}},
		{CapFWWrite, []PrivilegeRequirement{{Privilege: "Sys.Modify", Path: "/nodes/pve1", Confirmed: true}}},
		{CapSDNWrite, []PrivilegeRequirement{{Privilege: "SDN.Allocate", Path: "/nodes/pve1", Confirmed: true}}},
		{CapGuestNet, []PrivilegeRequirement{{Privilege: "VM.Config.Network", Path: "/nodes/pve1", Confirmed: true}}},
	}
	for _, tc := range cases {
		t.Run(string(tc.cap), func(t *testing.T) {
			got := id.Explain(tc.cap, "pve1", false)
			if got.Granted {
				t.Fatalf("Explain(%s) reports Granted=true, want false", tc.cap)
			}
			if got.Reason != "" {
				t.Fatalf("Explain(%s) set Reason %q, want none (privilege-derived)", tc.cap, got.Reason)
			}
			if !reflect.DeepEqual(got.Missing, tc.want) {
				t.Errorf("Explain(%s).Missing = %+v, want %+v", tc.cap, got.Missing, tc.want)
			}
		})
	}
}

// TestExplain_CaptureMissingSeveral is the "user missing several gets all
// of them, not just the first found" acceptance criterion: capture needs
// BOTH Sys.Modify and Sys.Console (DeriveCapabilities). An identity
// lacking netWrite entirely gets BOTH named, not just the one Explain can
// see directly — Sys.Console is included as required even though its own
// absence can't be independently confirmed from Capabilities' flags
// alone (no dedicated field), which Confirmed:false says honestly rather
// than silently omitting it.
func TestExplain_CaptureMissingSeveral(t *testing.T) {
	id := Identity{Caps: map[string]Capabilities{"pve1": {NetRead: true, Audit: true}}}
	got := id.Explain(CapCapture, "pve1", false)
	want := Explanation{
		Capability: "capture",
		Granted:    false,
		Missing: []PrivilegeRequirement{
			{Privilege: "Sys.Modify", Path: "/nodes/pve1", Confirmed: true},
			{Privilege: "Sys.Console", Path: "/nodes/pve1", Confirmed: false},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Explain(capture, missing both) = %+v, want %+v", got, want)
	}
}

// TestExplain_CaptureMissingOnlyConsole: netWrite held (Sys.Modify
// present) but capture still denied — Sys.Console must be the sole gap,
// and Explain can say so with certainty (Confirmed:true), not "several".
func TestExplain_CaptureMissingOnlyConsole(t *testing.T) {
	id := Identity{Caps: map[string]Capabilities{"pve1": {NetRead: true, NetWrite: true, FWRead: true, FWWrite: true, Audit: true}}}
	got := id.Explain(CapCapture, "pve1", false)
	want := Explanation{
		Capability: "capture",
		Granted:    false,
		Missing:    []PrivilegeRequirement{{Privilege: "Sys.Console", Path: "/nodes/pve1", Confirmed: true}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Explain(capture, missing only console) = %+v, want %+v", got, want)
	}
}

// TestExplain_AutomationNotPrivilegeDerived: automation/automationWrite
// never come from a PVE privilege at all (DeriveCapabilities never sets
// them) — Explain must go vague (Reason) rather than invent a privilege
// name, even for a wildcard-denied identity.
func TestExplain_AutomationNotPrivilegeDerived(t *testing.T) {
	id := Identity{Caps: map[string]Capabilities{"": {}}}
	for _, cap := range []Cap{CapAutomation, CapAutomationWrite} {
		got := id.Explain(cap, "", false)
		if got.Granted {
			t.Fatalf("Explain(%s) Granted=true, want false", cap)
		}
		if len(got.Missing) != 0 {
			t.Errorf("Explain(%s).Missing = %+v, want empty (not privilege-derived)", cap, got.Missing)
		}
		if got.Reason != reasonNotPrivilegeDerived {
			t.Errorf("Explain(%s).Reason = %q, want %q", cap, got.Reason, reasonNotPrivilegeDerived)
		}
	}
}

// TestExplain_BearerTokenGoesVague: a bearer-token identity's Capabilities
// come from the token's own minted scopes (tokens.go's
// CapabilitiesFromScopes), never a PVE ACL lookup — Explain must not tell
// a token holder to go ask a PVE admin for a privilege, which would be
// simply wrong regardless of whether the underlying capability is
// normally privilege-derived (netWrite here).
func TestExplain_BearerTokenGoesVague(t *testing.T) {
	id := Identity{TokenID: "tok1", Caps: map[string]Capabilities{"": {}}}
	got := id.Explain(CapNetWrite, "", false)
	if len(got.Missing) != 0 {
		t.Errorf("Explain(netWrite) for a bearer identity set Missing = %+v, want empty", got.Missing)
	}
	if got.Reason != reasonBearerToken {
		t.Errorf("Explain(netWrite) for a bearer identity Reason = %q, want %q", got.Reason, reasonBearerToken)
	}
}

// TestExplain_OIDCSessionDoesNotLeakUnderlyingPVEGrant is the leak case:
// an OIDC-authenticated session's stored Capabilities are already
// IntersectCaps(oidcBundle, pveDerivedCaps) (oidc_caps.go) — a false flag
// can come from either side, and Explain has structurally no access to
// the pre-intersection PVE-derived component (it only ever sees the
// already-intersected id.Caps), so it cannot and must not assert a
// specific PVE privilege is missing: doing so would either be a guess, or
// (if it somehow had the raw PVE-side data) would confirm the caller's
// real PVE grant to a session whose whole point is that org policy caps
// it below that — the same "don't confirm what a policy layer means to
// hide" reasoning internal/api/tenant.go's tenantMutationScope applies to
// a non-member's tenant existence.
func TestExplain_OIDCSessionDoesNotLeakUnderlyingPVEGrant(t *testing.T) {
	id := Identity{Realm: "oidc", Caps: map[string]Capabilities{"": {NetRead: true}}}
	got := id.Explain(CapNetWrite, "", false)
	if got.Granted {
		t.Fatal("Explain(netWrite) Granted=true, want false")
	}
	if len(got.Missing) != 0 {
		t.Errorf("Explain(netWrite) for an OIDC session named privileges %+v — this leaks which layer (OIDC group policy vs underlying PVE ACL) is limiting access", got.Missing)
	}
	if got.Reason != reasonOIDCIntersection {
		t.Errorf("Explain(netWrite) for an OIDC session Reason = %q, want %q", got.Reason, reasonOIDCIntersection)
	}
}

// TestExplain_NodeFallbackUsesUnionAcrossKnownNodes mirrors
// Identity.HasCap's own "no entry for this node -> any node in the map"
// fallback (service.go), so Explain's path label never claims a
// node-specific grant location the enforcement check didn't actually
// consult. Also exercises AutomationWrite through the union path, which
// oidc_caps.go's own unionCaps helper does not carry (see orCaps' doc
// comment) — regression guard for that gap.
func TestExplain_NodeFallbackUsesUnionAcrossKnownNodes(t *testing.T) {
	id := Identity{Caps: map[string]Capabilities{
		"pve1": {NetRead: true},
		"pve2": {NetWrite: true, AutomationWrite: true},
	}}
	// "pve3" has no entry of its own: falls through to the any-node union,
	// which DOES grant netWrite (via pve2) — so this must report Granted.
	got := id.Explain(CapNetWrite, "pve3", false)
	if !got.Granted {
		t.Errorf("Explain(netWrite, unknown node) = %+v, want Granted=true via any-node fallback", got)
	}

	// AutomationWrite is likewise granted only via pve2's entry in the
	// union — regression guard for orCaps carrying every field unionCaps
	// (oidc_caps.go) predates.
	gotAW := id.Explain(CapAutomationWrite, "pve3", false)
	if !gotAW.Granted {
		t.Errorf("Explain(automationWrite, unknown node) = %+v, want Granted=true via any-node fallback", gotAW)
	}

	// A capability neither pve1 nor pve2 grants stays denied, with the
	// cluster-wide "/" path label (not a fabricated "/nodes/pve3" — the
	// fallback never consulted a pve3-specific grant location).
	gotSDN := id.Explain(CapSDNWrite, "pve3", false)
	if gotSDN.Granted {
		t.Fatal("Explain(sdnWrite, unknown node) Granted=true, want false")
	}
	want := []PrivilegeRequirement{{Privilege: "SDN.Allocate", Path: "/", Confirmed: true}}
	if !reflect.DeepEqual(gotSDN.Missing, want) {
		t.Errorf("Explain(sdnWrite, unknown node).Missing = %+v, want %+v", gotSDN.Missing, want)
	}
}

// TestExplain_ForcedReadOnlyGoesVague: a session that genuinely holds
// Sys.Modify at the PVE layer still gets netWrite=false once
// handlers.go's forceReadOnly has run ([server] read_only=true) — by the
// time Explain sees it, that's indistinguishable from a real PVE ACL gap.
// Explain must not claim Sys.Modify is missing (the caller may already
// hold it); it must name the actual, only fix instead.
func TestExplain_ForcedReadOnlyGoesVague(t *testing.T) {
	// Same Capabilities shape TestExplain_SingleMissingPrivilege used for
	// a genuine ACL gap — the only difference here is readOnly=true, and
	// that alone must flip the answer from "Sys.Modify" to a Reason.
	id := Identity{Caps: map[string]Capabilities{"pve1": {NetRead: true, Audit: true}}}
	got := id.Explain(CapNetWrite, "pve1", true)
	if got.Granted {
		t.Fatal("Explain(netWrite, readOnly) Granted=true, want false")
	}
	if len(got.Missing) != 0 {
		t.Errorf("Explain(netWrite, readOnly).Missing = %+v, want empty — a Sys.Modify claim here may be false", got.Missing)
	}
	if got.Reason != reasonForcedReadOnly {
		t.Errorf("Explain(netWrite, readOnly).Reason = %q, want %q", got.Reason, reasonForcedReadOnly)
	}

	// audit is NOT one of the six forceReadOnly clears (it's a read
	// capability) — readOnly=true must not change its answer at all: the
	// identity here genuinely holds Audit, so it stays Granted.
	gotAudit := id.Explain(CapAudit, "pve1", true)
	if !gotAudit.Granted {
		t.Errorf("Explain(audit, readOnly) = %+v, want Granted=true — read_only never touches audit", gotAudit)
	}
}

// TestExplain_ForcedReadOnlyCapsMatchForceReadOnly keeps
// forceReadOnlyClears (explain.go) pinned to handlers.go's forceReadOnly:
// if a future change to forceReadOnly's field list isn't mirrored here,
// Explain would either wrongly excuse a genuine ACL gap as
// "read_only-forced" or wrongly claim a PVE privilege is missing for a
// flag read_only actually zeroed.
func TestExplain_ForcedReadOnlyCapsMatchForceReadOnly(t *testing.T) {
	all := Capabilities{
		NetRead: true, NetWrite: true, SDNRead: true, SDNWrite: true,
		FWRead: true, FWWrite: true, GuestNet: true, Audit: true,
		Automation: true, AutomationWrite: true, Capture: true,
	}
	caps := map[string]Capabilities{"pve1": all}
	forceReadOnly(caps)
	cleared := caps["pve1"]

	want := map[Cap]bool{
		CapNetWrite: !cleared.NetWrite, CapSDNWrite: !cleared.SDNWrite,
		CapFWWrite: !cleared.FWWrite, CapGuestNet: !cleared.GuestNet,
		CapCapture: !cleared.Capture, CapAutomationWrite: !cleared.AutomationWrite,
	}
	for cap, wasCleared := range want {
		if wasCleared != forceReadOnlyClears[cap] {
			t.Errorf("forceReadOnly clears %s = %v, but explain.go's forceReadOnlyClears[%s] = %v", cap, wasCleared, cap, forceReadOnlyClears[cap])
		}
	}
	for cap := range forceReadOnlyClears {
		if _, ok := want[cap]; !ok {
			t.Errorf("explain.go's forceReadOnlyClears names %s, which forceReadOnly does not clear at all", cap)
		}
	}
}
