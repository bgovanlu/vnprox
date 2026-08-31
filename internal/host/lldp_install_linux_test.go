//go:build linux

// SPDX-License-Identifier: Apache-2.0

package host

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
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

// fakeCommand writes a shell script that records that it ran and then exits
// with the given status, printing stdout verbatim. It returns the argv to
// point one of the package's overridable command variables at, plus a func
// reporting whether it was invoked.
func fakeCommand(t *testing.T, name, stdout string, exit int) ([]string, func() bool) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, name+".sh")
	marker := filepath.Join(dir, name+".ran")
	body := "#!/bin/sh\ntouch " + marker + "\ncat <<'EOF'\n" + stdout + "\nEOF\nexit " + strconv.Itoa(exit) + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil { //nolint:gosec // test fixture
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

// The reported failure, as a test. A node that already has lldpd must not run
// apt at all — which is what makes it immune to the sandbox, since
// `systemctl enable` needs no /var writes.
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

// Verbatim from the failure report. The message an operator sees must name
// the actual cause — vnproxd's own systemd sandbox — because apt's wording
// ("Read-only file system") reads as a broken disk and sends people to look
// at hardware that is fine.
func TestInstallLLDPD_ReadOnlyFailureNamesTheSandboxNotTheDisk(t *testing.T) {
	const aptOutput = `Reading package lists...
Building dependency tree...
W: Not using locking for read only lock file /var/lib/dpkg/lock-frontend
E: Could not create temporary file for /var/lib/apt/extended_states - mkstemp (30: Read-only file system)
E: Failed to write temporary StateFile /var/lib/apt/extended_states
W: chmod 0700 of directory /var/cache/apt/archives/partial failed - SetupAPTPartialDirectory (30: Read-only file system)`

	installed, _ := fakeCommand(t, "dpkg", "unknown", 1)
	install, _ := fakeCommand(t, "apt", aptOutput, 1)
	enable, _ := fakeCommand(t, "systemctl", "", 0)
	swapCommands(t, installed, install, enable)

	err := (&Real{}).InstallLLDPD(context.Background())
	if err == nil {
		t.Fatal("want an error when apt cannot write")
	}
	msg := err.Error()
	for _, want := range []string{"ProtectSystem=strict", "apt-get install -y lldpd"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not mention %q, so the operator cannot act on it:\n%s", want, msg)
		}
	}
	// And it must be ONE line, not apt's transcript. The error is rendered
	// verbatim in the UI (internal/api/lldpinstall.go's toLLDPInstallResult).
	if n := strings.Count(msg, "\n"); n != 0 {
		t.Errorf("message spans %d extra lines; the UI shows it verbatim:\n%s", n, msg)
	}
	for _, noise := range []string{"Reading package lists", "W: Not using locking", "Building dependency tree"} {
		if strings.Contains(msg, noise) {
			t.Errorf("apt transcript leaked into the message (%q):\n%s", noise, msg)
		}
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
}

// The install must go through systemd-run, not straight to apt. Direct apt
// cannot work on a hardened host: /proc/<pid>/mountinfo shows `/` mounted `ro`
// inside vnproxd's namespace, and `dpkg -L lldpd` writes to /usr and /etc — so
// there is no ReadWritePaths set that fixes it short of granting the whole
// filesystem, which would end the sandbox rather than narrow it.
//
// Asserted on the shipped default rather than a fake, because this is a
// property of the command itself.
func TestInstallLLDPDCommand_EscapesTheSandboxViaSystemdRun(t *testing.T) {
	if InstallLLDPDCommand[0] != "systemd-run" {
		t.Fatalf("install runs %q directly; a hardened daemon cannot write /usr, so it must go "+
			"through systemd-run", InstallLLDPDCommand[0])
	}
	joined := strings.Join(InstallLLDPDCommand, " ")
	for _, want := range []string{"--wait", "--pipe", "--collect", "apt-get install -y lldpd"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q: %s", want, joined)
		}
	}
	// --wait is what propagates apt's exit status; without it systemd-run
	// returns success as soon as the unit is queued and every failed install
	// would report as an success.
	if !strings.Contains(joined, "--wait") {
		t.Error("without --wait a failed install reports success")
	}
	// A fixed --unit name would collide when two operators press the button at
	// the same time, turning a concurrent install into a spurious failure.
	if strings.Contains(joined, "--unit=") {
		t.Errorf("a fixed unit name collides on concurrent installs: %s", joined)
	}
	// enable needs no escape and must not grow one: systemctl writes nothing
	// itself, it asks PID 1 over D-Bus.
	if EnableLLDPDCommand[0] != "systemctl" {
		t.Errorf("enable should call systemctl directly, got %q", EnableLLDPDCommand[0])
	}
}
