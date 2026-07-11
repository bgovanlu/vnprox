//go:build linux

package host

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
)

// Services implements Reader by shelling out to `systemctl is-active
// <unit>` for each of WatchedServices — the standard, dependency-free way
// to query systemd unit state without a D-Bus client library
// (docs/development.md's stdlib-first rule; adding a D-Bus binding purely
// for this one read did not seem justified against a two-unit, low-
// frequency check).
//
// systemctl is-active exits 0 with stdout "active" for a running unit;
// non-zero with "inactive"/"failed"/"deactivating" for a unit that exists
// but isn't running; and non-zero with "inactive"/"unknown" and a "could
// not be found" stderr for a unit that was never installed. Only the first
// two cases produce a map entry (true/false respectively) — a unit that
// was never installed on this node is omitted entirely (see the Reader
// doc comment on why a missing key must never be read as "down").
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

// systemctlIsActive runs `systemctl is-active <unit>` and classifies the
// result. known is false when the unit is not installed/loadable at all
// (nothing to report); active is only meaningful when known is true.
func systemctlIsActive(ctx context.Context, unit string) (active, known bool) {
	cmd := exec.CommandContext(ctx, "systemctl", "is-active", unit)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err := cmd.Run()
	status := strings.TrimSpace(stdout.String())

	switch status {
	case "active":
		return true, true
	case "inactive", "failed", "deactivating", "activating", "reloading":
		return false, true
	default:
		// "unknown" (unit file not found) or systemctl itself missing/erroring
		// (a non-systemd host, or the binary genuinely absent from PATH).
		_ = err
		return false, false
	}
}
