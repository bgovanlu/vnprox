// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun_NoArgsPrintsUsageAndFails(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(nil, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "vnproxctl") {
		t.Errorf("stderr = %q, want usage mentioning vnproxctl", stderr.String())
	}
}

func TestRun_Help(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "vnproxctl status") {
		t.Errorf("stdout = %q, want usage text", stdout.String())
	}
}

func TestRun_Version(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "vnproxctl") {
		t.Errorf("stdout = %q, want it to mention vnproxctl", stdout.String())
	}
}

func TestRun_UnknownCommandFails(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"not-a-real-command"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "not-a-real-command") {
		t.Errorf("stderr = %q, want it to mention the bad command", stderr.String())
	}
}

func TestRun_SnapshotsNoSubcommandFails(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"snapshots"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestRun_SnapshotsUnknownSubcommandFails(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"snapshots", "frobnicate"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

// TestRun_ExistingCommandsUnchangedByRemoteFamily is T-1105's required
// naming-collision regression test (see remote.go's package doc comment for
// the full write-up): status/snapshots/rollback-now are daemon-independent
// disaster-recovery tools by original design (T-206), and this task's new
// HTTP-backed commands live entirely under their own `remote`/`apply`
// top-level names instead of overloading any of the three — so their
// existing dispatch, flags, and 0/1/2 exit-code convention must be provably
// unaffected by this task's changes. This test re-asserts exactly the
// behaviors status_test.go/snapshots_test.go/rollback_test.go already pin
// (those files are untouched aside from an additive `-o json` flag) plus
// checks that `remote`/`apply` are recognized as distinct top-level commands
// with their own dispatch, never aliases of or overloads onto the existing
// three.
func TestRun_ExistingCommandsUnchangedByRemoteFamily(t *testing.T) {
	// The three pre-existing commands still dispatch and fail on bad usage
	// exactly as before (2 = usage, matching TestRun_SnapshotsNoSubcommandFails
	// and this file's own pre-existing assertions).
	for _, args := range [][]string{
		{"rollback-now"},       // missing changeset id
		{"snapshots"},          // missing subcommand
		{"snapshots", "bogus"}, // unknown subcommand
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 2 {
			t.Errorf("run(%v) = %d, want 2 (pre-existing usage-error convention unchanged)", args, code)
		}
	}

	// `remote` and `apply` are recognized as their own top-level commands
	// (dispatch added in main.go's switch), never folded into snapshots'/
	// rollback-now's namespace or behavior.
	var stdout, stderr bytes.Buffer
	if code := run([]string{"remote"}, &stdout, &stderr); code != ExitUsage {
		t.Errorf("run([remote]) = %d, want ExitUsage (own dispatch, own usage message)", code)
	}
	if !strings.Contains(stderr.String(), "vnproxctl remote") {
		t.Errorf("stderr = %q, want it to name the remote command specifically (not reuse snapshots'/rollback-now's usage text)", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"apply"}, &stdout, &stderr); code != ExitUsage {
		t.Errorf("run([apply]) = %d, want ExitUsage (missing the required spec-file argument)", code)
	}

	// The top-level --help text documents both the pre-existing daemon-
	// independent commands and the new HTTP-backed family side by side,
	// without either set's description bleeding into the other's.
	stdout.Reset()
	stderr.Reset()
	_ = run([]string{"--help"}, &stdout, &stderr)
	help := stdout.String()
	for _, want := range []string{
		"vnproxctl status", "vnproxctl snapshots", "vnproxctl rollback-now",
		"vnproxctl remote", "vnproxctl apply",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("--help output missing %q", want)
		}
	}
}

func TestRun_ApplyUnknownSubcommandStyleUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"remote", "bogus-subcommand"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want ExitUsage", code)
	}
	if !strings.Contains(stderr.String(), "bogus-subcommand") {
		t.Errorf("stderr = %q, want it to mention the bad subcommand", stderr.String())
	}
}
