//go:build linux

// SPDX-License-Identifier: Apache-2.0

package host

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
)

// Services implements Reader by asking systemd for each of
// WatchedServices' load and active state — the standard, dependency-free way
// to query unit state without a D-Bus client library
// (docs/development.md's stdlib-first rule; adding a D-Bus binding purely
// for this one read did not seem justified against a two-unit, low-
// frequency check).
//
// This used to shell out to `systemctl is-active <unit>` and classify the
// stdout word, on the documented belief that a never-installed unit reports
// "unknown". **It does not.** On systemd 257 an absent unit prints
// `inactive` — byte-identical to an installed-but-stopped one — and the two
// are distinguishable only by exit status (4 vs 3) or by a different query.
// pvecube has no dnsmasq package at all and reported `inactive`; see
// planning/reports/evidence/pve-9.2.4-systemctl-is-active.txt.
//
// The consequence was a false positive with teeth: vnprox told operators
// "dnsmasq is not running" on nodes where dnsmasq had never been installed,
// and — once T-3604 gave that finding a button — offered to start a unit
// that cannot exist. Most PVE nodes have no reason to run dnsmasq unless
// they use SDN DHCP, so it fired widely.
//
// `systemctl show --property=LoadState --property=ActiveState` answers both
// questions in one call, in a machine-readable form, without depending on
// exit-code conventions that differ across systemd versions.
// `LoadState=not-found` is the unambiguous "this unit does not exist here",
// which is what the Reader doc comment means by a missing key: absent from
// the map, never read as "down".
func (r *Real) Services(ctx context.Context, _ string) (map[string]bool, error) {
	out := make(map[string]bool, len(WatchedServices))
	for _, unit := range WatchedServices {
		active, known := systemctlIsActive(ctx, unit)
		if known {
			out[unit] = active
		}
	}
	return out, nil
}

// systemctlIsActive asks systemd for one unit's LoadState/ActiveState.
// known is false when the unit is not installed/loadable at all (nothing to
// report); active is only meaningful when known is true.
//
// Parsed from `--property=` output rather than `--value`, because
// --property ordering is not guaranteed and a positional read would silently
// invert the two states if systemd ever reordered them.
func systemctlIsActive(ctx context.Context, unit string) (active, known bool) {
	cmd := exec.CommandContext(ctx, "systemctl", "show", unit, "--property=LoadState", "--property=ActiveState")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	// Exit status is deliberately ignored: `systemctl show` reports
	// LoadState=not-found for an absent unit and still exits 0, and on a
	// host with no systemd at all it fails with no output — which the empty
	// loadState below already handles as "nothing to report".
	_ = cmd.Run()

	return parseUnitShow(stdout.String())
}

// parseUnitShow classifies `systemctl show --property=LoadState
// --property=ActiveState` output. Split out from the exec so the
// classification — the part that was wrong before, and that no test could
// reach while it lived behind a subprocess — is directly testable.
//
// Parsed by key rather than by position: --property ordering is not
// guaranteed, and a positional read would silently invert the two states if
// systemd ever reordered them.
func parseUnitShow(out string) (active, known bool) {
	var loadState, activeState string
	for _, line := range strings.Split(out, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		switch key {
		case "LoadState":
			loadState = value
		case "ActiveState":
			activeState = value
		}
	}

	switch loadState {
	case "loaded", "masked":
		// A masked unit exists and is deliberately unstartable. It is
		// reported (known) and inactive, which is the truth: an operator
		// whose SDN DHCP is masked wants to know, and T-3604's start
		// attempt will surface systemd's own "Unit is masked." message
		// rather than this code guessing at intent.
		return activeState == "active", true
	default:
		// "not-found", "bad-setting", "error", or no output at all (no
		// systemd, systemctl absent from PATH). Nothing trustworthy to
		// report; the Reader doc comment is explicit that a missing key
		// must never be read as "down".
		return false, false
	}
}
