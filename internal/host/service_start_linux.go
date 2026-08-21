//go:build linux

package host

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// StartService starts a systemd unit, for T-3604's "dnsmasq is not running"
// remedy.
//
// This is the narrowest possible version of a genuinely new power, and the
// narrowness is the design:
//
//   - It starts. It does not restart, stop, enable, mask or reload. An
//     operator who wants the unit to survive a reboot still has to say so
//     themselves; vnprox does not quietly change a node's boot
//     configuration on the strength of a button labelled "start".
//   - `unit` is checked against IsWatchedService HERE, in the function that
//     runs the command, not by whatever called it. A caller-side check is a
//     convention; this is an invariant.
//   - argv is fixed. `unit` reaches exec.CommandContext as one argument of
//     a fixed-shape command line, never through a shell, and only after the
//     allow-list has reduced it to one of two constants — so even a
//     compromised caller cannot turn it into a command.
//
// The systemd error text is returned verbatim rather than summarised. A
// unit that is masked, or that fails its own start-up, says why, and that
// sentence is far more useful to the operator than "failed to start".
func (r *Real) StartService(ctx context.Context, unit string) error {
	if !IsWatchedService(unit) {
		// Not %w-wrapping a sentinel: there is no recovery path a caller
		// could take, and this is a refusal to act rather than a failure to.
		return fmt.Errorf("host: refusing to start %q: not one of vnprox's watched services %v", unit, WatchedServices)
	}
	cmd := exec.CommandContext(ctx, "systemctl", "start", unit) //nolint:gosec // fixed argv; unit is allow-listed to a constant above
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(out.String())
		if msg == "" {
			return fmt.Errorf("systemctl start %s: %w", unit, err)
		}
		return fmt.Errorf("systemctl start %s: %w: %s", unit, err, msg)
	}
	return nil
}
