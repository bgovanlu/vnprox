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

func TestRun_SnapshotsListStub(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"snapshots", "list"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), "T-206") {
		t.Errorf("stdout = %q, want it to point at T-206", stdout.String())
	}
}

func TestRun_SnapshotsRestoreStub(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"snapshots", "restore", "snap-123"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), "T-206") {
		t.Errorf("stdout = %q, want it to point at T-206", stdout.String())
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

func TestRun_RollbackNowStub(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"rollback-now", "cs-42"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), "T-206") {
		t.Errorf("stdout = %q, want it to point at T-206", stdout.String())
	}
}
