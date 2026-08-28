// SPDX-License-Identifier: Apache-2.0

package auth

import "testing"

// TestForceReadOnly_PinsExactlyWhichFlagsItClears exists because this
// function's own doc comment was wrong about what it did until 2026-08-16 —
// it claimed to zero "every flag except netRead/sdnRead/fwRead/audit", and it
// zeroed only four (NetWrite/SDNWrite/FWWrite/GuestNet), leaving Capture and
// Automation untouched even though each gated a genuinely mutating route
// family (POST /captures[/stop], POST/DELETE /webhooks).
//
// T-3003-followup-01 (2026-08-19, owner decision) closed that gap:
//   - Capture is now cleared outright by read_only. It gates ALL FOUR
//     /captures routes (including list/get/download), so read_only refuses
//     the whole family, not just start/stop.
//   - Automation was split into two flags (caps.go's Capabilities.Automation
//     / AutomationWrite) specifically so read_only could clear the write
//     half (webhook registration/deletion) while leaving the read half (the
//     WS "events" topic + GET /webhooks) reachable — the owner's "most
//     correct, most work" option, chosen over clearing both (which would
//     have silently removed a read capability from anyone relying on it) or
//     leaving the behaviour as documented-but-wrong.
//
// This test pins the resulting behaviour exactly, the same way its
// predecessor pinned the wrong behaviour: so a future change to what
// read_only restrains has to touch this test on purpose.
func TestForceReadOnly_PinsExactlyWhichFlagsItClears(t *testing.T) {
	t.Parallel()

	all := Capabilities{
		NetRead: true, NetWrite: true,
		SDNRead: true, SDNWrite: true,
		FWRead: true, FWWrite: true,
		GuestNet: true, Audit: true,
		Automation: true, AutomationWrite: true, Capture: true,
	}
	caps := map[string]Capabilities{"pve1": all}
	forceReadOnly(caps)
	got := caps["pve1"]

	for _, tc := range []struct {
		name     string
		got      bool
		wantTrue bool
	}{
		// Cleared — the original four config-write flags.
		{"netWrite", got.NetWrite, false},
		{"sdnWrite", got.SDNWrite, false},
		{"fwWrite", got.FWWrite, false},
		{"guestNet", got.GuestNet, false},

		// Cleared — T-3003-followup-01's additions. Capture gates
		// POST /captures[/stop] (root-shell-equivalent access); it is
		// cleared entirely, taking the read routes in the same family with
		// it (internal/api/captures.go has no read/write split of its own).
		{"capture (gates ALL /captures routes — CLEARED by read_only)", got.Capture, false},
		// AutomationWrite gates POST/DELETE /webhooks — a real outbound HTTP
		// registration, cleared just like capture.
		{"automationWrite (gates POST/DELETE /webhooks — CLEARED by read_only)", got.AutomationWrite, false},

		// Preserved — the read flags and audit.
		{"netRead", got.NetRead, true},
		{"sdnRead", got.SDNRead, true},
		{"fwRead", got.FWRead, true},
		{"audit", got.Audit, true},

		// Preserved — Automation is now specifically the READ half (the WS
		// "events" topic + GET /webhooks), split out precisely so it could
		// survive read_only without dragging the write half along.
		{"automation (read half: WS events + GET /webhooks — PRESERVED by read_only)", got.Automation, true},
	} {
		if tc.got != tc.wantTrue {
			t.Errorf("after forceReadOnly, %s = %v, want %v", tc.name, tc.got, tc.wantTrue)
		}
	}
}
