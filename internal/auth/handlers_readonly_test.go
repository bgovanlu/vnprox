package auth

import "testing"

// TestForceReadOnly_PinsExactlyWhichFlagsItClears exists because this
// function's own doc comment was wrong about what it does until 2026-08-16 —
// it claimed to zero "every flag except netRead/sdnRead/fwRead/audit", and it
// zeroes four.
//
// The gap matters rather than being pedantry: Capture gates POST /captures
// (which starts real packet captures on hosts) and Automation gates
// POST /webhooks (which registers an outbound destination), so a `read_only`
// deployment currently permits two mutating route families while forbidding a
// bridge rename. docs/security.md said the opposite in the same paragraph that
// argues capture is stronger than netWrite.
//
// This test does not assert that the current behaviour is RIGHT. It asserts
// exactly what it is, so that changing it has to be a decision somebody makes
// on purpose — see T-3003-followup-01 in planning/tasks/phase-30.md.
func TestForceReadOnly_PinsExactlyWhichFlagsItClears(t *testing.T) {
	t.Parallel()

	all := Capabilities{
		NetRead: true, NetWrite: true,
		SDNRead: true, SDNWrite: true,
		FWRead: true, FWWrite: true,
		GuestNet: true, Audit: true,
		Automation: true, Capture: true,
	}
	caps := map[string]Capabilities{"pve1": all}
	forceReadOnly(caps)
	got := caps["pve1"]

	for _, tc := range []struct {
		name     string
		got      bool
		wantTrue bool
	}{
		// Cleared — the four config-write flags.
		{"netWrite", got.NetWrite, false},
		{"sdnWrite", got.SDNWrite, false},
		{"fwWrite", got.FWWrite, false},
		{"guestNet", got.GuestNet, false},

		// Preserved, and expected to be: the read flags and audit.
		{"netRead", got.NetRead, true},
		{"sdnRead", got.SDNRead, true},
		{"fwRead", got.FWRead, true},
		{"audit", got.Audit, true},

		// Preserved, and THIS is the finding. Both gate mutating routes.
		// If either of these two lines starts failing, someone has decided
		// the question T-3003-followup-01 records — make sure they meant to,
		// and update docs/security.md's read_only sentence with them.
		{"capture (gates POST /captures — SURVIVES read_only)", got.Capture, true},
		{"automation (gates POST /webhooks — SURVIVES read_only)", got.Automation, true},
	} {
		if tc.got != tc.wantTrue {
			t.Errorf("after forceReadOnly, %s = %v, want %v", tc.name, tc.got, tc.wantTrue)
		}
	}
}
