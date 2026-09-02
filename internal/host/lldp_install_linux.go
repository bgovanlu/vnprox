//go:build linux

// SPDX-License-Identifier: Apache-2.0

package host

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// installStageMarker is echoed by the install script between its two
// stages, so a failure can be attributed to the right one.
//
// The two stages run inside a single transient unit (see
// InstallLLDPDCommand), which means there is no Go-side boundary between
// them to hang attribution on. The marker is that boundary: absent from the
// output, the failure was apt's; present, apt succeeded and systemctl
// failed. It is filtered out of every rendered message.
const installStageMarker = "vnprox-stage: enabling"

// aptLockTimeoutSeconds is how long apt waits for /var/lib/dpkg/lock-frontend
// instead of failing immediately.
//
// Two operators pressing "install lldpd on all nodes" at once — or one
// pressing it again after a slow first attempt appeared to fail — otherwise
// race on dpkg's lock, and the loser reports "E: Could not get lock
// /var/lib/dpkg/lock-frontend" as that node's failure. Omitting a unit name
// (below) keeps systemd from colliding, but systemd is not the thing that
// serialises package installs; dpkg is. 60s comfortably covers installing
// one small package.
const aptLockTimeoutSeconds = 60

// installScript is the body of the transient unit: install, then enable.
//
// Both steps live in one unit deliberately. `systemd-run` spawns the unit
// under PID 1, so it outlives the process that started it — if the caller's
// context is cancelled (the peer API's request timeout is the ordinary way
// this happens on a node that genuinely needs the install), the unit runs to
// completion anyway. With the enable inside it, that leaves a correctly
// configured node. With the enable back in Go, a cancellation would leave
// lldpd installed but never enabled, and nothing would revisit it.
//
// `set -e` stops at the first failure, so the marker is printed only when
// apt actually succeeded.
var installScript = strings.Join([]string{
	"set -e",
	fmt.Sprintf("apt-get install -y -o DPkg::Lock::Timeout=%d lldpd", aptLockTimeoutSeconds),
	"echo " + installStageMarker,
	"systemctl enable --now lldpd",
}, "\n")

// InstallLLDPDCommand, EnableLLDPDCommand and LLDPDInstalledCommand are the
// fixed argv used by InstallLLDPD, overridable for tests.
// docs/features/lldp-discovery.md §1: "The vnprox installer offers to install
// and enable it (--with-lldp, default yes)"; this method backs the runtime
// "guided install" flow (peer-API apt install with confirmation) for a node
// discovered to be missing lldpd after install time, not the installer script
// itself (packaging/bin/vnprox-setup).
var (
	// InstallLLDPDCommand runs its work through `systemd-run` rather than
	// directly, and that indirection is the whole reason a guided install can
	// work at all on a hardened host.
	//
	// vnproxd runs under `ProtectSystem=strict`, which mounts the entire
	// filesystem hierarchy read-only inside its namespace — confirmed by
	// reading /proc/<pid>/mountinfo on a live node, where `/` is `ro`, not
	// merely /var. Installing lldpd writes to /usr and /etc (`dpkg -L lldpd`),
	// so no set of ReadWritePaths short of granting /usr, /etc and /var makes
	// apt succeed, and granting those three is not a narrowing of the sandbox
	// — it is the end of it.
	//
	// `systemd-run` asks PID 1 to spawn a transient unit, so the work runs as
	// a child of init with its own (default) settings rather than inheriting
	// vnproxd's namespace. The daemon keeps `ProtectSystem=strict` intact and
	// the install still happens. Flags: --wait blocks and propagates the exit
	// status, --pipe connects the unit's stdio so failures are still
	// capturable, --collect reaps the unit even when it fails, --quiet drops
	// systemd-run's own "Running as unit" chatter so it cannot reach the UI.
	// No --unit name: two operators pressing the button at once would collide
	// on a fixed one, and the description keeps the journal legible without it.
	// (Concurrency between their *apt* invocations is handled by
	// DPkg::Lock::Timeout in installScript, which is the lock that actually
	// matters.)
	InstallLLDPDCommand = []string{
		"systemd-run", "--wait", "--collect", "--quiet", "--pipe",
		"--description=vnprox guided lldpd install",
		"sh", "-c", installScript,
	}
	// EnableLLDPDCommand is the already-installed path, and it needs the same
	// escape.
	//
	// It is tempting to call systemctl directly here on the grounds that it
	// only talks to PID 1 over D-Bus. That is not true on Debian for a package
	// like lldpd that ships both a native unit and an /etc/init.d script:
	// `systemctl enable` additionally execs /usr/lib/systemd/systemd-sysv-install
	// *client-side*, inside the caller's namespace, and that runs update-rc.d,
	// which renames K-links to S-links under /etc/rc[2-5].d — read-only under
	// ProtectSystem=strict. The evidence transcript
	// (planning/reports/evidence/lldp-install-sandbox-pvecube-2026-08-31.txt,
	// §3b) records exactly that exec happening. It succeeded there only
	// because the links were already S; on a node where someone had run
	// `systemctl disable lldpd`, the rename would hit EROFS and the
	// supposedly sandbox-independent path would fail.
	EnableLLDPDCommand = []string{
		"systemd-run", "--wait", "--collect", "--quiet", "--pipe",
		"--description=vnprox guided lldpd enable",
		"systemctl", "enable", "--now", "lldpd",
	}
	// LLDPDInstalledCommand asks dpkg whether the package is already present.
	// `${db:Status-Status}` prints just the third status word ("installed"),
	// without the "install ok " prefix `${Status}` carries, so the check is a
	// string equality rather than a substring match that "not-installed"
	// would also satisfy.
	LLDPDInstalledCommand = []string{"dpkg-query", "-W", "-f=${db:Status-Status}", "lldpd"}
)

// installWaitDelay bounds how long Go waits, after the context is cancelled,
// for the killed `systemd-run` client's pipes to close.
//
// Without it a cancellation hangs: exec kills systemd-run, but the transient
// unit it started is a child of PID 1 and keeps running with the same stdout
// pipe open, so CombinedOutput blocks reading that pipe until apt finishes —
// long after the caller gave up. WaitDelay makes exec close the descriptors
// and return instead.
const installWaitDelay = 2 * time.Second

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
// Asking first turns that node into a no-op beyond enabling the service.
//
// Either path runs through `systemd-run`, so it escapes the daemon's
// namespace rather than fighting it — see InstallLLDPDCommand and
// EnableLLDPDCommand, and packaging/systemd/vnprox.service for the four times
// this sandbox has now blocked a subprocess.
func (r *Real) InstallLLDPD(ctx context.Context) error {
	if !r.lldpdInstalled(ctx) {
		out, err := runInstallStep(ctx, InstallLLDPDCommand)
		if err == nil {
			return nil
		}
		if strings.Contains(string(out), installStageMarker) {
			// apt succeeded; systemctl is what failed.
			logSubprocessFailure("enabling lldpd", EnableLLDPDCommand, out, err)
			return fmt.Errorf("host: enabling lldpd: %s: %w", systemctlFailure(out, err), err)
		}
		logSubprocessFailure("installing lldpd", InstallLLDPDCommand, out, err)
		return fmt.Errorf("host: installing lldpd: %s: %w", aptFailure(out, err), err)
	}
	out, err := runInstallStep(ctx, EnableLLDPDCommand)
	if err != nil {
		logSubprocessFailure("enabling lldpd", EnableLLDPDCommand, out, err)
		return fmt.Errorf("host: enabling lldpd: %s: %w", systemctlFailure(out, err), err)
	}
	return nil
}

// runInstallStep executes one of the package's fixed argvs, capturing merged
// stdout/stderr and refusing to block past installWaitDelay once the caller's
// context is done.
func runInstallStep(ctx context.Context, argv []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // fixed argv, not user input
	cmd.WaitDelay = installWaitDelay
	return cmd.CombinedOutput()
}

// logSubprocessFailure records the whole transcript, which the returned error
// deliberately does not carry.
//
// The error is rendered verbatim in the UI and stored as the audit row's
// detail, so it has to stay one line — but a dpkg postinst failure's actual
// diagnosis ("dpkg: error processing package lldpd (--configure): ...") has
// no "E: " prefix and would otherwise be lost, leaving only apt's generic
// "E: Sub-process /usr/bin/dpkg returned an error code (1)". It cannot be
// recovered from the journal either: `--pipe` gives the transient unit
// vnproxd's pipes instead of journal stdio. On a node with no SSH access
// (pve001) that would make the cause unknowable. So the summary goes to the
// operator and the transcript goes here.
func logSubprocessFailure(stage string, argv []string, out []byte, err error) {
	slog.Default().Warn("host: lldpd guided install step failed",
		"stage", stage,
		"command", strings.Join(argv, " "),
		"error", err,
		"output", strings.TrimSpace(string(out)),
	)
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

// missingExecutable reports whether err is exec's "not found in $PATH".
//
// It has to be asked of the error, not of the output: when the binary is
// missing exec fails before Start, so there is no output at all and the text
// lives only in err. Same detection convention as netlink_linux.go's
// ErrFRRUnavailable path.
func missingExecutable(err error) bool {
	var execErr *exec.Error
	return errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound)
}

// aptFailure renders an apt failure as one line an operator can act on.
//
// It exists because the previous form appended apt's whole CombinedOutput to
// the error, and that error is surfaced verbatim in the UI
// (internal/api/lldpinstall.go's toLLDPInstallResult). A single already-
// installed node produced fourteen lines of progress meters, autoremove
// suggestions and `W:` warnings, in which the one line that mattered was not
// obviously the one that mattered. The full transcript is not discarded —
// logSubprocessFailure keeps it.
func aptFailure(out []byte, err error) string {
	if missingExecutable(err) {
		return "systemd-run is unavailable on this node, so apt cannot be run outside vnproxd's " +
			"sandbox. Install it directly with `apt-get install -y lldpd`"
	}
	// apt marks real errors with a leading "E: "; everything else it prints is
	// progress or advice. Prefer the first of those, and fall back to the
	// process error when apt failed without saying why.
	//
	// "Read-only file system" is deliberately NOT special-cased any more. It
	// was, back when apt ran inside vnproxd's namespace and that string could
	// only mean the sandbox. Now that apt runs via systemd-run, outside it,
	// the likelier cause is the opposite one — a genuinely read-only
	// filesystem, which is exactly what `errors=remount-ro` produces after an
	// I/O error — and a message asserting "the host filesystem itself is
	// fine" would send the operator away from a failing disk. apt's own
	// wording is true in both cases; the guess was only ever true in one.
	for _, line := range meaningfulLines(string(out)) {
		if strings.HasPrefix(line, "E: ") {
			return strings.TrimPrefix(line, "E: ")
		}
	}
	return firstLine(string(out), err)
}

// systemctlFailure renders a `systemctl enable --now` failure as one line.
//
// It cannot simply take the first line. On Debian, enabling a unit whose
// package also ships an init script prints two informational lines first —
// "Synchronizing state of lldpd.service with SysV service script with
// /usr/lib/systemd/systemd-sysv-install." and "Executing:
// /usr/lib/systemd/systemd-sysv-install enable lldpd" — as the evidence
// transcript records. Returning the first line therefore reported a start
// failure with a sentence that sounds like success and dropped "Job for
// lldpd.service failed", the only part the operator needed.
func systemctlFailure(out []byte, err error) string {
	if missingExecutable(err) {
		return "systemd-run is unavailable on this node, so systemctl cannot be run outside " +
			"vnproxd's sandbox. Enable it directly with `systemctl enable --now lldpd`"
	}
	lines := meaningfulLines(string(out))
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "Job for "),
			strings.HasPrefix(line, "Failed to "),
			strings.HasPrefix(line, "A dependency job "),
			strings.HasPrefix(line, "Error: "):
			return line
		}
	}
	// No recognisable failure line. The informational chatter comes first, so
	// the last line is a better guess than the first.
	if len(lines) > 0 {
		return lines[len(lines)-1]
	}
	return err.Error()
}

// systemctlNoise is the output `systemctl enable` prints on the way to doing
// its job, none of which describes a failure.
var systemctlNoise = []string{
	"Synchronizing state of",
	"Executing: /usr/lib/systemd/systemd-sysv-install",
	"Created symlink",
	"Removed ",
}

// meaningfulLines trims out.txt to non-empty lines that could plausibly be a
// diagnosis: no blanks, no stage marker, no systemctl progress chatter.
func meaningfulLines(out string) []string {
	var kept []string
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == installStageMarker {
			continue
		}
		noise := false
		for _, prefix := range systemctlNoise {
			if strings.HasPrefix(trimmed, prefix) {
				noise = true
				break
			}
		}
		if !noise {
			kept = append(kept, trimmed)
		}
	}
	return kept
}

// firstLine returns the first meaningful line of out, or err's own text when
// the command failed silently. Callers use it to keep a subprocess failure to
// one line.
func firstLine(out string, err error) string {
	if lines := meaningfulLines(out); len(lines) > 0 {
		return lines[0]
	}
	return err.Error()
}
