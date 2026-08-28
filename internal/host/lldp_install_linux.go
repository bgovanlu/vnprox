//go:build linux

// SPDX-License-Identifier: Apache-2.0

package host

import (
	"context"
	"fmt"
	"os/exec"
)

// InstallLLDPDCommand and EnableLLDPDCommand are the fixed argv used by
// InstallLLDPD, overridable for tests. docs/features/lldp-discovery.md §1:
// "The vnprox installer offers to install and enable it (--with-lldp,
// default yes)"; this method backs the runtime "guided install" flow
// (peer-API apt install with confirmation) for a node discovered to be
// missing lldpd after install time, not the installer script itself
// (packaging/bin/vnprox-setup).
var (
	InstallLLDPDCommand = []string{"apt-get", "install", "-y", "lldpd"}
	EnableLLDPDCommand  = []string{"systemctl", "enable", "--now", "lldpd"}
)

// InstallLLDPD installs and enables lldpd via fixed-argv apt-get/systemctl
// invocations (never shell-interpolated) — docs/features/lldp-discovery.md
// §1's guided install flow. Callers are responsible for the "changeset-like
// confirmation" the spec describes and for audit logging the action; this
// method only performs the OS-level mutation once called.
func (r *Real) InstallLLDPD(ctx context.Context) error {
	if out, err := exec.CommandContext(ctx, InstallLLDPDCommand[0], InstallLLDPDCommand[1:]...).CombinedOutput(); err != nil { //nolint:gosec // fixed argv, not user input
		return fmt.Errorf("host: installing lldpd (%v): %w: %s", InstallLLDPDCommand, err, out)
	}
	if out, err := exec.CommandContext(ctx, EnableLLDPDCommand[0], EnableLLDPDCommand[1:]...).CombinedOutput(); err != nil { //nolint:gosec // fixed argv, not user input
		return fmt.Errorf("host: enabling lldpd (%v): %w: %s", EnableLLDPDCommand, err, out)
	}
	return nil
}
