//go:build linux

// SPDX-License-Identifier: Apache-2.0

package host

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// InstallLLDPDCommand, EnableLLDPDCommand and LLDPDInstalledCommand are the
// fixed argv used by InstallLLDPD, overridable for tests.
// docs/features/lldp-discovery.md §1: "The vnprox installer offers to install
// and enable it (--with-lldp, default yes)"; this method backs the runtime
// "guided install" flow (peer-API apt install with confirmation) for a node
// discovered to be missing lldpd after install time, not the installer script
// itself (packaging/bin/vnprox-setup).
var (
	// InstallLLDPDCommand runs apt through `systemd-run` rather than directly,
	// and that indirection is the whole reason a guided install can work at
	// all on a hardened host.
	//
	// vnproxd runs under `ProtectSystem=strict`, which mounts the entire
	// filesystem hierarchy read-only inside its namespace — confirmed by
	// reading /proc/<pid>/mountinfo on a live node, where `/` is `ro`, not
	// merely /var. Installing lldpd writes to /usr and /etc (`dpkg -L lldpd`),
	// so no set of ReadWritePaths short of granting /usr, /etc and /var makes
	// apt succeed, and granting those three is not a narrowing of the sandbox
	// — it is the end of it.
	//
	// `systemd-run` asks PID 1 to spawn a transient unit, so apt runs as a
	// child of init with its own (default) settings rather than inheriting
	// vnproxd's namespace. The daemon keeps `ProtectSystem=strict` intact and
	// the install still happens. Flags: --wait blocks and propagates the exit
	// status, --pipe connects the unit's stdio so failures are still
	// capturable, --collect reaps the unit even when it fails, --quiet drops
	// systemd-run's own "Running as unit" chatter so it cannot reach the UI.
	// No --unit name: two operators pressing the button at once would collide
	// on a fixed one, and the description keeps the journal legible without it.
	InstallLLDPDCommand = []string{
		"systemd-run", "--wait", "--collect", "--quiet", "--pipe",
		"--description=vnprox guided lldpd install",
		"apt-get", "install", "-y", "lldpd",
	}
	// EnableLLDPDCommand needs no such escape: systemctl asks PID 1 to do the
	// work over D-Bus and writes nothing to the filesystem itself.
	EnableLLDPDCommand = []string{"systemctl", "enable", "--now", "lldpd"}
	// LLDPDInstalledCommand asks dpkg whether the package is already present.
	// `${db:Status-Status}` prints just the third status word ("installed"),
	// without the "install ok " prefix `${Status}` carries, so the check is a
	// string equality rather than a substring match that "not-installed"
	// would also satisfy.
	LLDPDInstalledCommand = []string{"dpkg-query", "-W", "-f=${db:Status-Status}", "lldpd"}
)

// InstallLLDPD ensures lldpd is installed and enabled — docs/features/
// lldp-discovery.md §1's guided install flow. Callers are responsible for the
// "changeset-like confirmation" the spec describes and for audit logging the
// action; this method only performs the OS-level mutation once called.
//
// # Why the install step is conditional
//
// It skips apt entirely when the package is already there, and that is the
// fix for a real failure rather than an optimisation. "Install lldpd on all
// nodes" is a cluster-wide button, so the ordinary case is a mix: some nodes
// have it, some do not. On a node that already had it, `apt-get install -y`
// does no work but still rewrites its own bookkeeping
// (/var/lib/apt/extended_states) — and vnproxd runs under
// `ProtectSystem=strict`, which makes /var read-only for the daemon. So apt
// printed "lldpd is already the newest version", then exited 100 because it
// could not write a state file about the nothing it had just done, and a node
// that was already correctly configured reported as a failure.
//
// Asking first turns that node into a no-op. It also means the common case
// stops depending on the sandbox at all, since `systemctl enable --now` needs
// no /var writes.
//
// A node that genuinely lacks the package runs apt through `systemd-run`, so
// it escapes the daemon's namespace rather than fighting it — see
// InstallLLDPDCommand, and packaging/systemd/vnprox.service for the four
// times this sandbox has now blocked a subprocess.
func (r *Real) InstallLLDPD(ctx context.Context) error {
	if !r.lldpdInstalled(ctx) {
		if out, err := exec.CommandContext(ctx, InstallLLDPDCommand[0], InstallLLDPDCommand[1:]...).CombinedOutput(); err != nil { //nolint:gosec // fixed argv, not user input
			return fmt.Errorf("installing lldpd: %s", aptFailure(out, err))
		}
	}
	if out, err := exec.CommandContext(ctx, EnableLLDPDCommand[0], EnableLLDPDCommand[1:]...).CombinedOutput(); err != nil { //nolint:gosec // fixed argv, not user input
		return fmt.Errorf("enabling lldpd: %s", firstLine(string(out), err))
	}
	return nil
}

// lldpdInstalled reports whether dpkg considers the package installed.
//
// Any failure to ask — dpkg-query missing, an unknown package, a non-Debian
// host — answers false, which routes to the install path. That is the safe
// direction: a wrong "false" costs one redundant apt run, while a wrong
// "true" would silently skip an install the operator asked for and leave the
// node without lldpd.
func (r *Real) lldpdInstalled(ctx context.Context) bool {
	out, err := exec.CommandContext(ctx, LLDPDInstalledCommand[0], LLDPDInstalledCommand[1:]...).Output() //nolint:gosec // fixed argv, not user input
	return err == nil && strings.TrimSpace(string(out)) == "installed"
}

// aptFailure renders an apt failure as one line an operator can act on.
//
// It exists because the previous form appended apt's whole CombinedOutput to
// the error, and that error is surfaced verbatim in the UI
// (internal/api/lldpinstall.go's toLLDPInstallResult). A single already-
// installed node produced fourteen lines of progress meters, autoremove
// suggestions and `W:` warnings, in which the one line that mattered was not
// obviously the one that mattered.
//
// The read-only case is special-cased because the message apt gives for it is
// actively misleading: "Read-only file system" reads as a broken or full
// disk, and the disk is fine — it is vnproxd's own systemd sandbox. An
// operator who believes the first reading goes looking at hardware.
func aptFailure(out []byte, err error) string {
	text := string(out)
	if strings.Contains(text, "Read-only file system") {
		// Should be unreachable now that apt runs via systemd-run, outside the
		// daemon's namespace. If it fires anyway, systemd-run is not doing what
		// this code assumes — an unusual unit override, a container without a
		// system bus — and saying "read-only file system" alone would send the
		// operator to check a disk that is fine.
		return "apt could not write to the filesystem, which means it did not escape vnproxd's " +
			"ProtectSystem=strict sandbox as intended (the host filesystem itself is fine). " +
			"Install it on this node directly with `apt-get install -y lldpd`"
	}
	if strings.Contains(text, "executable file not found") || strings.Contains(text, "systemd-run: not found") {
		return "systemd-run is unavailable on this node, so apt cannot be run outside vnproxd's " +
			"sandbox. Install it directly with `apt-get install -y lldpd`"
	}
	// apt marks real errors with a leading "E: "; everything else it prints is
	// progress or advice. Prefer the first of those, and fall back to the
	// process error when apt failed without saying why.
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "E: ") {
			return strings.TrimPrefix(trimmed, "E: ")
		}
	}
	return firstLine(text, err)
}

// firstLine returns the first non-empty line of out, or err's own text when
// the command failed silently. Callers use it to keep a subprocess failure to
// one line.
func firstLine(out string, err error) string {
	for _, line := range strings.Split(out, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return err.Error()
}
