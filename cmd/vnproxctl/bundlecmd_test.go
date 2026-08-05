package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/backup"
	"github.com/bgovanlu/vnprox/internal/config"
)

// bundlecmd_test.go drives `vnproxctl support-bundle` through run(), the
// same entry point main() uses, so the flag wiring, the exit codes and the
// output are exercised rather than internal/backup.Bundle a second time.

// TestSupportBundleCommand_WritesARedactedArchive is the end-to-end run.
func TestSupportBundleCommand_WritesARedactedArchive(t *testing.T) {
	configPath, _, keyDir := seedBackupFixture(t)
	outDir := filepath.Join(t.TempDir(), "support")
	logPath := writeBundleLog(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"support-bundle",
		"--config", configPath, "--out-dir", outDir,
		"--log-file", logPath, "--no-probe",
		"--interfaces", filepath.Join(keyDir, "no-such-interfaces"),
		"--corosync", filepath.Join(keyDir, "no-such-corosync.conf"),
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit %d, want %d (stderr: %s)", code, ExitSuccess, stderr.String())
	}

	archives := archivesIn(t, outDir)
	if len(archives) != 1 {
		t.Fatalf("wrote %d archives, want 1: %v", len(archives), archives)
	}
	path := archives[0]
	if !strings.HasPrefix(filepath.Base(path), "vnprox-support-") {
		t.Errorf("bundle named %q; it must not look like a backup archive", filepath.Base(path))
	}

	// The archive is a support bundle, carries no key material, and the
	// planted credential in the log is gone.
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening the bundle: %v", err)
	}
	defer func() { _ = f.Close() }()
	m, err := backup.Inspect(f, backup.DefaultLimits())
	if err != nil {
		t.Fatalf("the written bundle does not validate: %v", err)
	}
	if m.Kind != backup.KindSupportBundle {
		t.Errorf("kind = %q, want %q", m.Kind, backup.KindSupportBundle)
	}
	if m.IncludesKeyMaterial || len(m.SecretClasses) != 0 {
		t.Errorf("the bundle declares key material %v / classes %v", m.IncludesKeyMaterial, m.SecretClasses)
	}

	body := archiveBytes(t, path)
	if bytes.Contains(body, []byte("CTLMARK-planted-token")) {
		t.Error("the planted credential from the log reached the bundle in the clear")
	}
	if !bytes.Contains(body, []byte("CTLMARK-ordinary-log-line")) {
		t.Fatal("CONTROL FAILED: the ordinary log line is missing too, so the log was not collected " +
			"and 'the credential is gone' proves nothing")
	}

	out := stdout.String()
	for _, want := range []string{"Wrote ", "Contents:", "Deliberately NOT collected:", "readme.txt"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout does not mention %q:\n%s", want, out)
		}
	}
}

// TestSupportBundleCommand_DryRunWritesNothing is the CLI half of AC4.
func TestSupportBundleCommand_DryRunWritesNothing(t *testing.T) {
	configPath, _, keyDir := seedBackupFixture(t)
	outDir := filepath.Join(t.TempDir(), "support")

	var stdout, stderr bytes.Buffer
	code := run([]string{"support-bundle",
		"--config", configPath, "--out-dir", outDir, "--dry-run", "--no-probe",
		"--log-file", writeBundleLog(t),
		"--interfaces", filepath.Join(keyDir, "none"), "--corosync", filepath.Join(keyDir, "none"),
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit %d (stderr: %s)", code, stderr.String())
	}
	if _, err := os.Stat(outDir); err == nil {
		t.Errorf("--dry-run created %s", outDir)
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stat %s: %v", outDir, err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Would write ") || !strings.Contains(out, "Nothing was written (--dry-run).") {
		t.Errorf("--dry-run output does not say it changed nothing:\n%s", out)
	}
	// It still describes the contents, which is the entire point of the
	// flag: an operator decides before producing anything.
	for _, want := range []string{"environment.json", "probes.json", "redaction:"} {
		if !strings.Contains(out, want) {
			t.Errorf("--dry-run output does not mention %q:\n%s", want, out)
		}
	}
}

// TestSupportBundleCommand_JSONOutput: -o json is what a script (and
// T-1904's doctor) consumes.
func TestSupportBundleCommand_JSONOutput(t *testing.T) {
	configPath, _, keyDir := seedBackupFixture(t)
	outDir := filepath.Join(t.TempDir(), "support")

	var stdout, stderr bytes.Buffer
	code := run([]string{"support-bundle",
		"--config", configPath, "--out-dir", outDir, "-o", "json", "--no-probe",
		"--log-file", writeBundleLog(t),
		"--interfaces", filepath.Join(keyDir, "none"), "--corosync", filepath.Join(keyDir, "none"),
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit %d (stderr: %s)", code, stderr.String())
	}
	//nolint:govet // fieldalignment: mirrors bundlecmd.go's wire DTO; field order documents the JSON shape.
	var got struct {
		Path  string            `json:"path"`
		Plan  backup.BundlePlan `json:"plan"`
		Bytes int64             `json:"bytes"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decoding -o json output: %v\n%s", err, stdout.String())
	}
	if got.Path == "" || got.Bytes <= 0 {
		t.Errorf("json output has no path/size: %+v", got)
	}
	if len(got.Plan.Entries) < 8 || len(got.Plan.Omitted) < 10 {
		t.Errorf("json plan is thin: %d entries, %d omissions", len(got.Plan.Entries), len(got.Plan.Omitted))
	}
	if got.Plan.DryRun {
		t.Error("a real run's plan says dryRun")
	}
}

// TestSupportBundleCommand_HasNoIncludeKeysEquivalent is the CLI-level
// statement of the card's central hazard: there is no flag that makes a
// support bundle carry key material, and there must never be one.
func TestSupportBundleCommand_HasNoIncludeKeysEquivalent(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"support-bundle", "--include-keys"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("`support-bundle --include-keys` exited %d, want %d — the flag must not exist", code, ExitUsage)
	}
	// The control: `backup` DOES have it, so the refusal above is about
	// support-bundle rather than about the flag parser rejecting everything.
	var bo, be bytes.Buffer
	if c := run([]string{"backup", "--include-keys", "--help"}, &bo, &be); c == ExitUsage &&
		strings.Contains(be.String(), "flag provided but not defined") {
		t.Fatal("CONTROL FAILED: `backup` does not know --include-keys either")
	}
}

// TestSupportBundleCommand_UsageErrors covers the boundary conditions the
// rest of this binary's commands already have tests for.
func TestSupportBundleCommand_UsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"a positional argument", []string{"support-bundle", "unexpected.tar.gz"}, ExitUsage},
		{"an unknown output format", []string{"support-bundle", "-o", "yaml"}, ExitUsage},
		{"a config that does not exist", []string{"support-bundle", "--config", "/nonexistent/vnprox.toml"}, ExitError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tc.args, &stdout, &stderr); code != tc.want {
				t.Errorf("exit %d, want %d (stderr: %s)", code, tc.want, stderr.String())
			}
		})
	}
}

// TestKeyPathRefsFor_CoversEveryKeyFileClass keeps the CLI's class mapping
// aligned with internal/backup's declared inventory. A key-file class added
// to the inventory without a line in keyPathRefsFor would silently be
// probed with an empty path.
func TestKeyPathRefsFor_CoversEveryKeyFileClass(t *testing.T) {
	cfg := config.RecoveryConfig{
		Listen:                  "127.0.0.1:0",
		PVETokenFile:            "/etc/vnprox/keys/pve-token",
		MetricsKeyFile:          "/etc/vnprox/keys/metrics.key",
		BlueprintSigningKeyFile: "/etc/vnprox/keys/blueprint.key",
		OIDCClientSecretFile:    "/etc/vnprox/keys/oidc",
	}
	cfg.SessionKeyFile = "/etc/vnprox/keys/session.key"

	got := map[string]string{}
	for _, r := range keyPathRefsFor(cfg) {
		got[r.ClassID] = r.Path
	}
	for _, c := range backup.SecretClassesBy(backup.StorageKeyFile) {
		path, ok := got[c.ID]
		if !ok {
			t.Errorf("key-file class %q has no entry in keyPathRefsFor — the bundle would probe it "+
				"with an empty path and always report it missing", c.ID)
			continue
		}
		if path == "" {
			t.Errorf("key-file class %q maps to an empty path", c.ID)
		}
	}
	if len(got) < 5 {
		t.Errorf("keyPathRefsFor produced only %d refs", len(got))
	}
}

// ------------------------------------------------------------- helpers

// writeBundleLog writes a fixture daemon log with one credential and one
// ordinary line, so a test can assert both that the first is removed and
// that the second is not.
func writeBundleLog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vnprox.log")
	body := `time=2026-07-30T10:00:00Z level=INFO msg="CTLMARK-ordinary-log-line"` + "\n" +
		`time=2026-07-30T10:00:01Z level=DEBUG msg="pve request" Authorization: PVEAPIToken=CTLMARK-planted-token` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

func archivesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("a directory survived in the output directory: %s", e.Name())
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	return out
}

// archiveBytes returns every decompressed byte of every entry.
func archiveBytes(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gunzip %s: %v", path, err)
	}
	defer func() { _ = gz.Close() }()
	var out bytes.Buffer
	tr := tar.NewReader(gz)
	for {
		hdr, nextErr := tr.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			t.Fatalf("reading %s: %v", path, nextErr)
		}
		out.WriteString(hdr.Name)
		out.WriteByte('\n')
		if _, copyErr := io.Copy(&out, tr); copyErr != nil {
			t.Fatalf("reading entry %s: %v", hdr.Name, copyErr)
		}
	}
	return out.Bytes()
}
