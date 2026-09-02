//go:build linux

// SPDX-License-Identifier: Apache-2.0

package host

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// InstallLLDPD had no test, which is why it shipped a failure on the most
// ordinary input there is: a node that already has the package.
//
// "Install lldpd on all nodes" is a cluster-wide button, so a mixed cluster is
// the normal case, not an edge one. On the already-installed node apt did no
// work, said so, and then exited 100 because it could not write
// /var/lib/apt/extended_states — vnproxd runs under ProtectSystem=strict, so
// /var is read-only for the daemon. A correctly configured node reported as a
// failure, with fourteen lines of apt progress meters as the explanation.

// shellQuote renders s as a single-quoted POSIX shell word.
//
// The fixtures below interpolate t.TempDir() paths into a generated script,
// and t.TempDir() nests under $TMPDIR — which the test does not control and
// which may contain spaces. Unquoted, `touch /tmp/My Dir/x.ran` creates two
// wrong files, the marker never appears, and the assertions fail claiming a
// command did not run when it did.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// fakeCommand writes a shell script that records that it ran and then exits
// with the given status, printing stdout verbatim. It returns the argv to
// point one of the package's overridable command variables at, plus a func
// reporting whether it was invoked.
//
// The fixture's stdout is written to a file and `cat`-ed rather than embedded
// in a heredoc: a heredoc would need the payload escaped, and a payload
// containing the delimiter would silently truncate.
func fakeCommand(t *testing.T, name, stdout string, exit int) ([]string, func() bool) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, name+".sh")
	marker := filepath.Join(dir, name+".ran")
	payload := filepath.Join(dir, name+".out")
	if err := os.WriteFile(payload, []byte(stdout), 0o600); err != nil {
		t.Fatalf("writing %s: %v", payload, err)
	}
	body := "#!/bin/sh\ntouch " + shellQuote(marker) + "\ncat " + shellQuote(payload) +
		"\nexit " + strconv.Itoa(exit) + "\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatalf("writing %s: %v", script, err)
	}
	return []string{script}, func() bool {
		_, err := os.Stat(marker)
		return err == nil
	}
}

// swapCommands points the package's command variables at fakes for one test.
func swapCommands(t *testing.T, installed, install, enable []string) {
	t.Helper()
	oldInstalled, oldInstall, oldEnable := LLDPDInstalledCommand, InstallLLDPDCommand, EnableLLDPDCommand
	LLDPDInstalledCommand, InstallLLDPDCommand, EnableLLDPDCommand = installed, install, enable
	t.Cleanup(func() {
		LLDPDInstalledCommand, InstallLLDPDCommand, EnableLLDPDCommand = oldInstalled, oldInstall, oldEnable
	})
}

// captureLogs redirects slog's default logger into a buffer for one test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

// The reported failure, as a test. A node that already has lldpd must not run
// apt at all — which is what made it fail: apt rewrites its own bookkeeping
// even when it installs nothing.
func TestInstallLLDPD_AlreadyInstalledDoesNotRunApt(t *testing.T) {
	installed, _ := fakeCommand(t, "dpkg", "installed", 0)
	install, aptRan := fakeCommand(t, "apt", "", 0)
	enable, enableRan := fakeCommand(t, "systemctl", "", 0)
	swapCommands(t, installed, install, enable)

	if err := (&Real{}).InstallLLDPD(context.Background()); err != nil {
		t.Fatalf("an already-installed node reported failure: %v", err)
	}
	if aptRan() {
		t.Error("apt ran on a node that already had the package — the whole point is that it must not")
	}
	if !enableRan() {
		t.Error("enable was skipped; the package being present does not mean the service is running")
	}
}

// The other half: a node genuinely missing the package must still install it.
// A too-eager skip would silently leave a node without lldpd, which is worse
// than the failure this fix removes.
func TestInstallLLDPD_MissingPackageStillInstalls(t *testing.T) {
	installed, _ := fakeCommand(t, "dpkg", "unknown", 1)
	install, aptRan := fakeCommand(t, "apt", "", 0)
	enable, _ := fakeCommand(t, "systemctl", "", 0)
	swapCommands(t, installed, install, enable)

	if err := (&Real{}).InstallLLDPD(context.Background()); err != nil {
		t.Fatalf("InstallLLDPD: %v", err)
	}
	if !aptRan() {
		t.Error("a node without the package did not run apt")
	}
}

// A non-sandbox apt failure keeps apt's own diagnosis — just the `E:` line
// rather than everything it printed on the way there.
func TestInstallLLDPD_OtherAptFailureKeepsOnlyTheErrorLine(t *testing.T) {
	const aptOutput = `Reading package lists...
Building dependency tree...
E: Unable to locate package lldpd
W: Some index files failed to download.`

	installed, _ := fakeCommand(t, "dpkg", "unknown", 1)
	install, _ := fakeCommand(t, "apt", aptOutput, 1)
	enable, _ := fakeCommand(t, "systemctl", "", 0)
	swapCommands(t, installed, install, enable)

	err := (&Real{}).InstallLLDPD(context.Background())
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "Unable to locate package lldpd") {
		t.Errorf("apt's own diagnosis was dropped: %v", err)
	}
	if strings.Contains(err.Error(), "Reading package lists") {
		t.Errorf("apt transcript leaked: %v", err)
	}
	if n := strings.Count(err.Error(), "\n"); n != 0 {
		t.Errorf("message spans %d extra lines; the UI shows it verbatim:\n%s", n, err)
	}
}

// A read-only filesystem must NOT be reported as "the disk is fine".
//
// This is the inverse of what this file asserted when the fix landed. Back
// then apt ran inside vnproxd's namespace, so "Read-only file system" could
// only mean the sandbox, and the message said so. Now that apt runs through
// systemd-run — outside the namespace — the same string much more likely
// means a genuinely read-only filesystem: pvecube's root is mounted
// `errors=remount-ro`, so an I/O error produces exactly this. Telling that
// operator the disk is fine sends them away from a failing disk.
func TestInstallLLDPD_ReadOnlyFailureDoesNotBlameTheSandbox(t *testing.T) {
	const aptOutput = `Reading package lists...
W: Not using locking for read only lock file /var/lib/dpkg/lock-frontend
E: Could not create temporary file for /var/lib/apt/extended_states - mkstemp (30: Read-only file system)
E: Failed to write temporary StateFile /var/lib/apt/extended_states`

	installed, _ := fakeCommand(t, "dpkg", "unknown", 1)
	install, _ := fakeCommand(t, "apt", aptOutput, 1)
	enable, _ := fakeCommand(t, "systemctl", "", 0)
	swapCommands(t, installed, install, enable)

	err := (&Real{}).InstallLLDPD(context.Background())
	if err == nil {
		t.Fatal("want an error when apt cannot write")
	}
	msg := err.Error()
	// apt's own wording is true whichever the cause is, so it is what the
	// operator gets.
	if !strings.Contains(msg, "Read-only file system") {
		t.Errorf("apt's own diagnosis was dropped:\n%s", msg)
	}
	for _, forbidden := range []string{"filesystem itself is fine", "ProtectSystem=strict"} {
		if strings.Contains(msg, forbidden) {
			t.Errorf("message asserts a cause it cannot know (%q):\n%s", forbidden, msg)
		}
	}
	if n := strings.Count(msg, "\n"); n != 0 {
		t.Errorf("message spans %d extra lines; the UI shows it verbatim:\n%s", n, msg)
	}
}

// When systemd-run is missing, the crafted "install it directly" message has
// to actually fire. It used to be looked for in the command's output, but a
// missing binary produces no output at all — exec fails before Start and puts
// the text in err — so the branch was unreachable.
func TestInstallLLDPD_MissingSystemdRunIsDetectedOnTheError(t *testing.T) {
	installed, _ := fakeCommand(t, "dpkg", "unknown", 1)
	enable, _ := fakeCommand(t, "systemctl", "", 0)
	swapCommands(t, installed, []string{"vnprox-no-such-binary-systemd-run"}, enable)

	err := (&Real{}).InstallLLDPD(context.Background())
	if err == nil {
		t.Fatal("want an error when systemd-run is absent")
	}
	if !strings.Contains(err.Error(), "apt-get install -y lldpd") {
		t.Errorf("operator was not told how to proceed by hand: %v", err)
	}
	if !strings.Contains(err.Error(), "systemd-run is unavailable") {
		t.Errorf("the missing-binary case was not recognised: %v", err)
	}
	// And the detection must be on the error, not the (empty) output.
	if !missingExecutable(&exec.Error{Name: "x", Err: exec.ErrNotFound}) {
		t.Error("missingExecutable does not recognise exec.ErrNotFound")
	}
}

// `systemctl enable --now` prints two informational lines FIRST on Debian for
// a package that also ships an init script, so taking the first line reported
// a start failure with a sentence that sounds like success.
func TestInstallLLDPD_EnableFailureSkipsSystemctlPreamble(t *testing.T) {
	const systemctlOutput = `Synchronizing state of lldpd.service with SysV service script with /usr/lib/systemd/systemd-sysv-install.
Executing: /usr/lib/systemd/systemd-sysv-install enable lldpd
Job for lldpd.service failed because the control process exited with error code.
See "systemctl status lldpd.service" and "journalctl -xeu lldpd.service" for details.`

	installed, _ := fakeCommand(t, "dpkg", "installed", 0)
	install, _ := fakeCommand(t, "apt", "", 0)
	enable, _ := fakeCommand(t, "systemctl", systemctlOutput, 1)
	swapCommands(t, installed, install, enable)

	err := (&Real{}).InstallLLDPD(context.Background())
	if err == nil {
		t.Fatal("want an error when the unit fails to start")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Job for lldpd.service failed") {
		t.Errorf("the actual failure was dropped:\n%s", msg)
	}
	if strings.Contains(msg, "Synchronizing state") || strings.Contains(msg, "systemd-sysv-install") {
		t.Errorf("informational preamble reported as the failure:\n%s", msg)
	}
}

// A failure after apt succeeded must be attributed to the enable stage, not
// to the install — both now happen inside one transient unit, so the marker
// is the only thing that can tell them apart.
func TestInstallLLDPD_FailureAfterTheMarkerIsAttributedToEnable(t *testing.T) {
	output := "Setting up lldpd ...\n" + installStageMarker + "\nJob for lldpd.service failed because of a bad config.\n"

	installed, _ := fakeCommand(t, "dpkg", "unknown", 1)
	install, _ := fakeCommand(t, "apt", output, 1)
	enable, _ := fakeCommand(t, "systemctl", "", 0)
	swapCommands(t, installed, install, enable)

	err := (&Real{}).InstallLLDPD(context.Background())
	if err == nil {
		t.Fatal("want an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "enabling lldpd") {
		t.Errorf("a post-install failure was blamed on the install:\n%s", msg)
	}
	if strings.Contains(msg, installStageMarker) {
		t.Errorf("the internal stage marker leaked into the operator's message:\n%s", msg)
	}
}

// The one-line message is for the UI; the transcript still has to survive
// somewhere, or a dpkg postinst failure on a node with no SSH is
// undiagnosable. --pipe means it is not in the journal under the transient
// unit either.
func TestInstallLLDPD_LogsTheFullTranscript(t *testing.T) {
	const aptOutput = `Reading package lists...
dpkg: error processing package lldpd (--configure):
 installed lldpd package post-installation script subprocess returned error exit status 1
E: Sub-process /usr/bin/dpkg returned an error code (1)`

	logs := captureLogs(t)
	installed, _ := fakeCommand(t, "dpkg", "unknown", 1)
	install, _ := fakeCommand(t, "apt", aptOutput, 1)
	enable, _ := fakeCommand(t, "systemctl", "", 0)
	swapCommands(t, installed, install, enable)

	err := (&Real{}).InstallLLDPD(context.Background())
	if err == nil {
		t.Fatal("want an error")
	}
	// The operator's line is the generic one apt prints...
	if !strings.Contains(err.Error(), "Sub-process /usr/bin/dpkg returned an error code") {
		t.Errorf("unexpected summary: %v", err)
	}
	// ...but the line that actually says what broke must be recoverable.
	if !strings.Contains(logs.String(), "post-installation script subprocess returned error") {
		t.Errorf("dpkg's real diagnosis was not logged; it is now unrecoverable:\n%s", logs.String())
	}
}

// Errors must wrap, per CLAUDE.md ("Errors are wrapped with context
// (%w); no bare err returns across package boundaries") and to match
// lldp_install_other.go's `host: InstallLLDPD: %w`. Stringifying the exec
// error made errors.Is/As useless across the host->api and host->peer
// boundaries.
func TestInstallLLDPD_ErrorsWrapAndCarryThePackagePrefix(t *testing.T) {
	installed, _ := fakeCommand(t, "dpkg", "unknown", 1)
	install, _ := fakeCommand(t, "apt", "E: Unable to locate package lldpd", 1)
	enable, _ := fakeCommand(t, "systemctl", "", 0)
	swapCommands(t, installed, install, enable)

	err := (&Real{}).InstallLLDPD(context.Background())
	if err == nil {
		t.Fatal("want an error")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Errorf("the exec error is not recoverable from the chain: %v", err)
	}
	if !strings.HasPrefix(err.Error(), "host: ") {
		t.Errorf("dropped the package prefix every other file in internal/host carries: %v", err)
	}
}

// A cancelled context must not block. systemd-run's transient unit is a child
// of PID 1, so killing the client leaves the real work running with vnproxd's
// stdout pipe still open — CombinedOutput would read that pipe until the work
// finished, long after the caller gave up. WaitDelay is what stops it.
func TestInstallLLDPD_CancellationDoesNotBlockOnAnOrphanHoldingThePipe(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "slow.sh")
	// `sleep &` inherits stdout, so the pipe stays open after the direct
	// child is killed — exactly the orphaned-unit shape.
	body := "#!/bin/sh\nsleep 30 &\nsleep 30\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatalf("writing %s: %v", script, err)
	}
	installed, _ := fakeCommand(t, "dpkg", "unknown", 1)
	enable, _ := fakeCommand(t, "systemctl", "", 0)
	swapCommands(t, installed, []string{script}, enable)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()

	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- (&Real{}).InstallLLDPD(ctx) }()

	select {
	case <-done:
		if took := time.Since(start); took > installWaitDelay+8*time.Second {
			t.Errorf("returned only after %s; WaitDelay should have bounded it", took)
		}
	case <-time.After(installWaitDelay + 8*time.Second):
		t.Fatal("InstallLLDPD blocked on an orphan holding the output pipe after cancellation")
	}
}

// Properties of the shipped commands themselves, asserted on the defaults
// rather than on a fake.
func TestInstallLLDPDCommands_EscapeTheSandboxViaSystemdRun(t *testing.T) {
	for _, c := range []struct {
		name string
		argv []string
	}{
		{"install", InstallLLDPDCommand},
		{"enable", EnableLLDPDCommand},
	} {
		if c.argv[0] != "systemd-run" {
			t.Errorf("%s runs %q directly; a hardened daemon cannot write /usr or /etc, so it "+
				"must go through systemd-run", c.name, c.argv[0])
		}
		joined := strings.Join(c.argv, " ")
		// --wait is what propagates the exit status; without it systemd-run
		// returns success as soon as the unit is queued and every failure
		// would report as a success.
		for _, want := range []string{"--wait", "--pipe", "--collect", "--quiet"} {
			if !strings.Contains(joined, want) {
				t.Errorf("%s missing %q: %s", c.name, want, joined)
			}
		}
		// A fixed --unit name would collide when two operators press the
		// button at the same time, turning a concurrent install into a
		// spurious failure.
		if strings.Contains(joined, "--unit=") {
			t.Errorf("%s: a fixed unit name collides on concurrent installs: %s", c.name, joined)
		}
	}
}

// The install and the enable share one transient unit on purpose: the unit
// outlives the request, so a caller that times out still ends up with a node
// that is both installed and enabled.
func TestInstallScript_EnablesInsideTheSameUnit(t *testing.T) {
	joined := strings.Join(InstallLLDPDCommand, " ")
	if !strings.Contains(joined, "apt-get install -y") {
		t.Errorf("install argv no longer installs: %s", joined)
	}
	if !strings.Contains(joined, "systemctl enable --now lldpd") {
		t.Error("the enable is outside the transient unit; a cancelled request would leave " +
			"lldpd installed but never enabled, and nothing would revisit it")
	}
	if !strings.Contains(joined, installStageMarker) {
		t.Error("without the stage marker a failure cannot be attributed to install vs enable")
	}
	if !strings.Contains(installScript, "set -e") {
		t.Error("without set -e the script would enable after a failed install")
	}
}

// Concurrent presses race on dpkg's lock, not systemd's unit names. Omitting
// --unit is necessary but not sufficient.
func TestInstallScript_WaitsForTheDpkgLock(t *testing.T) {
	if !strings.Contains(installScript, "DPkg::Lock::Timeout") {
		t.Error("a second press while apt is running fails with 'Could not get lock " +
			"/var/lib/dpkg/lock-frontend' instead of waiting")
	}
}
