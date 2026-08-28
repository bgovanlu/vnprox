// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/backup"
	"github.com/bgovanlu/vnprox/internal/config"
	"github.com/bgovanlu/vnprox/internal/store"
)

// seedBackupFixture builds a minimal but real installation on disk: a
// migrated store with a row in it, a config pointing at it, and a key
// directory. No daemon anywhere — `backup`/`restore` are members of this
// binary's daemon-independent family, so a test that needed one would be
// testing the wrong thing.
func seedBackupFixture(t *testing.T) (configPath, dbPath, keyDir string) {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()

	dbPath = filepath.Join(dir, "var", "vnprox.db")
	keyDir = filepath.Join(dir, "etc", "keys")
	configPath = filepath.Join(dir, "etc", "vnprox.toml")
	for _, d := range []string{filepath.Dir(dbPath), keyDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	db, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if _, err := db.Conn().ExecContext(ctx,
		`INSERT INTO kv (k, v) VALUES ('install_id', 'ctl-fixture')`); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}

	sessionKey := filepath.Join(keyDir, "session.key")
	if err := os.WriteFile(sessionKey, []byte("CTLKEY-session-0123456789abcdef!"), 0o600); err != nil {
		t.Fatalf("writing session key: %v", err)
	}
	if err := os.WriteFile(filepath.Join(keyDir, "pve-token"), []byte("CTLMARK-pve-token"), 0o600); err != nil {
		t.Fatalf("writing pve token: %v", err)
	}

	cfg := "[server]\nlisten = \"127.0.0.1:0\"\n\n" +
		"[storage]\ndb_path = \"" + dbPath + "\"\nsession_key_file = \"" + sessionKey + "\"\n\n" +
		"[pve]\ntoken_file = \"" + filepath.Join(keyDir, "pve-token") + "\"\n\n" +
		"[metrics]\nkey_file = \"" + filepath.Join(keyDir, "metrics.key") + "\"\n\n" +
		"[blueprint]\nsigning_key_file = \"" + filepath.Join(keyDir, "blueprint-signing.key") + "\"\n"
	if err := os.WriteFile(configPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return configPath, dbPath, keyDir
}

// TestBackupCommand_RoundTripThroughTheCLI drives the two commands exactly
// as an operator would, through run(), asserting the exit codes and the
// documented human output — not the library, which has its own tests.
func TestBackupCommand_RoundTripThroughTheCLI(t *testing.T) {
	configPath, dbPath, _ := seedBackupFixture(t)
	outDir := filepath.Join(t.TempDir(), "backups")

	var stdout, stderr bytes.Buffer
	code := run([]string{"backup", "--config", configPath, "--out-dir", outDir}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("backup exit = %d (want %d), stderr: %s", code, ExitSuccess, stderr.String())
	}
	if !strings.Contains(stdout.String(), "No key material included") {
		t.Errorf("a default backup does not tell the operator it excluded key material:\n%s", stdout.String())
	}

	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("backup produced %d files, want 1: %v", len(entries), entries)
	}
	archive := filepath.Join(outDir, entries[0].Name())
	if strings.Contains(entries[0].Name(), "with-keys") {
		t.Errorf("a default backup is named %q", entries[0].Name())
	}

	// --dry-run first: it must not change anything.
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"restore", "--config", configPath, "--dry-run", archive}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("restore --dry-run exit = %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Would restore") || !strings.Contains(stdout.String(), "Nothing was changed") {
		t.Errorf("--dry-run output does not read as a dry run:\n%s", stdout.String())
	}

	// Wipe and restore for real.
	for _, s := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(dbPath + s)
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"restore", "--config", configPath, archive}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("restore exit = %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Restored") {
		t.Errorf("restore output:\n%s", stdout.String())
	}

	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("opening the restored store: %v", err)
	}
	defer func() { _ = db.Close() }()
	var v string
	if err := db.Conn().QueryRow(`SELECT v FROM kv WHERE k = 'install_id'`).Scan(&v); err != nil {
		t.Fatalf("reading the restored row: %v", err)
	}
	if v != "ctl-fixture" {
		t.Errorf("restored install_id = %q, want ctl-fixture", v)
	}
}

// TestBackupCommand_IncludeKeysWarnsBeforeItWrites: the warning is the
// safety control, so it has to reach stderr on the path an automation
// caller actually takes (--yes), not only the interactive one.
func TestBackupCommand_IncludeKeysWarnsBeforeItWrites(t *testing.T) {
	configPath, _, _ := seedBackupFixture(t)
	outDir := filepath.Join(t.TempDir(), "backups")

	var stdout, stderr bytes.Buffer
	code := run([]string{"backup", "--config", configPath, "--out-dir", outDir, "--include-keys", "--yes"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("backup --include-keys exit = %d, stderr: %s", code, stderr.String())
	}

	warning := stderr.String()
	if !strings.Contains(warning, "COMPLETE COMPROMISE") {
		t.Errorf("--include-keys did not print the compromise warning:\n%s", warning)
	}
	for _, c := range backup.SecretClassesBy(backup.StorageKeyFile) {
		if !strings.Contains(warning, c.Name) {
			t.Errorf("the warning does not name the %s", c.Name)
		}
	}
	if !strings.Contains(stdout.String(), "CONTAINS KEY MATERIAL") {
		t.Errorf("the success output does not restate what was written:\n%s", stdout.String())
	}

	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), "-with-keys.tar.gz") {
		t.Fatalf("archive is named %v, want a -with-keys suffix", entries)
	}
	info, err := os.Stat(filepath.Join(outDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("key-bearing archive mode = %04o, want 0600", perm)
	}
}

// TestRestoreCommand_RefusesAgainstALiveDaemon exercises the refusal
// through the CLI, including the exit code a cron wrapper would branch on.
func TestRestoreCommand_RefusesAgainstALiveDaemon(t *testing.T) {
	configPath, dbPath, _ := seedBackupFixture(t)
	outDir := filepath.Join(t.TempDir(), "backups")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"backup", "--config", configPath, "--out-dir", outDir}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("backup: %s", stderr.String())
	}
	entries, _ := os.ReadDir(outDir)
	archive := filepath.Join(outDir, entries[0].Name())

	lock, err := store.AcquireRuntimeLock(dbPath)
	if err != nil {
		t.Fatalf("AcquireRuntimeLock: %v", err)
	}
	defer func() { _ = lock.Release() }()

	stdout.Reset()
	stderr.Reset()
	code := run([]string{"restore", "--config", configPath, archive}, &stdout, &stderr)
	if code != ExitError {
		t.Fatalf("restore against a live daemon exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stderr.String(), "daemon is running") {
		t.Errorf("stderr does not explain the refusal:\n%s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("a refused restore wrote to stdout: %q", stdout.String())
	}
}

// TestBackupCommand_JSONOutput: the `-o json` contract every command in
// this binary carries (T-1105 AC4), extended to the two new ones.
func TestBackupCommand_JSONOutput(t *testing.T) {
	configPath, _, _ := seedBackupFixture(t)
	outDir := filepath.Join(t.TempDir(), "backups")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"backup", "--config", configPath, "--out-dir", outDir, "-o", "json"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("backup -o json: %s", stderr.String())
	}
	//nolint:govet // fieldalignment: wire DTO mirroring the command's JSON output.
	var got struct {
		Path                string   `json:"path"`
		Bytes               int64    `json:"bytes"`
		SchemaVersion       int      `json:"schemaVersion"`
		IncludesKeyMaterial bool     `json:"includesKeyMaterial"`
		SecretClasses       []string `json:"secretClasses"`
		Entries             int      `json:"entries"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("backup -o json produced non-JSON: %v\n%s", err, stdout.String())
	}
	if got.Path == "" || got.Bytes == 0 || got.SchemaVersion == 0 || got.Entries == 0 {
		t.Errorf("backup -o json is missing fields: %+v", got)
	}
	if got.IncludesKeyMaterial || len(got.SecretClasses) != 0 {
		t.Errorf("a default backup reports key material in JSON: %+v", got)
	}
	assertDocumentedJSON(t, "backup", stdout.Bytes())

	stdout.Reset()
	if code := run([]string{"restore", "--config", configPath, "--dry-run", "-o", "json", got.Path}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("restore -o json: %s", stderr.String())
	}
	var plan backup.Plan
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
		t.Fatalf("restore -o json produced non-JSON: %v\n%s", err, stdout.String())
	}
	if plan.Applied {
		t.Error("a --dry-run reported applied: true")
	}
	if len(plan.Notes) == 0 {
		t.Error("the JSON plan carries no notes")
	}
	assertDocumentedJSON(t, "restore", stdout.Bytes())
}

// TestBackupRestoreCommands_UsageErrors pins the ExitUsage boundary, so a
// script that mistypes a flag gets 2 rather than 1 (and never a partial
// action).
func TestBackupRestoreCommands_UsageErrors(t *testing.T) {
	configPath, _, _ := seedBackupFixture(t)
	cases := []struct {
		name string
		args []string
	}{
		{"restore with no archive", []string{"restore", "--config", configPath}},
		{"restore with two archives", []string{"restore", "--config", configPath, "a.tar.gz", "b.tar.gz"}},
		{"backup with a stray argument", []string{"backup", "--config", configPath, "somewhere.tar.gz"}},
		{"bad -o value", []string{"backup", "--config", configPath, "-o", "yaml"}},
		{"unknown flag", []string{"backup", "--nope"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tc.args, &stdout, &stderr); code != ExitUsage {
				t.Errorf("exit = %d, want %d (stderr: %s)", code, ExitUsage, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Errorf("a usage error wrote to stdout: %q", stdout.String())
			}
		})
	}
}

// TestRestoreCommand_RejectsAMaliciousArchive: AC5's rejection reaches the
// operator as a clear error and a non-zero exit, not a stack trace.
func TestRestoreCommand_RejectsAMaliciousArchive(t *testing.T) {
	configPath, dbPath, _ := seedBackupFixture(t)
	before, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("reading the store: %v", err)
	}

	bogus := filepath.Join(t.TempDir(), "not-a-backup.tar.gz")
	if writeErr := os.WriteFile(bogus, []byte("PK\x03\x04 definitely not a vnprox backup"), 0o600); writeErr != nil {
		t.Fatalf("writing: %v", writeErr)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"restore", "--config", configPath, bogus}, &stdout, &stderr); code != ExitError {
		t.Fatalf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stderr.String(), "malformed archive") {
		t.Errorf("stderr does not name the problem:\n%s", stderr.String())
	}
	after, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("reading the store: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the store changed after a rejected archive")
	}
}

// TestUsage_MentionsBackupAndRestore: `vnproxctl --help` is where an
// operator at 3 a.m. finds out these exist at all.
func TestUsage_MentionsBackupAndRestore(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--help"}, &stdout, &stderr); code != ExitSuccess {
		t.Fatalf("--help exit = %d", code)
	}
	help := stdout.String()
	for _, want := range []string{
		"vnproxctl backup", "vnproxctl restore <archive>",
		"--include-keys", "--dry-run", "--keep",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("--help never mentions %q", want)
		}
	}
}

// TestKeyPathsFor_ComesFromTheConfigNotAHardcodedList: an install that
// relocated its keys must still be backed up correctly, and one that never
// configured OIDC must not have a phantom path in the list.
func TestKeyPathsFor_ComesFromTheConfigNotAHardcodedList(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "vnprox.toml")
	cfg := "[storage]\nsession_key_file = \"/srv/secrets/vnprox-session.key\"\n\n" +
		"[pve]\ntoken_file = \"/srv/secrets/pve\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	cfgLoaded, err := config.LoadRecoveryOnly(cfgPath, discardLogger())
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	paths := keyPathsFor(cfgLoaded)
	joined := strings.Join(paths, ",")
	if !strings.Contains(joined, "/srv/secrets/vnprox-session.key") {
		t.Errorf("the relocated session key is not collected: %v", paths)
	}
	if !strings.Contains(joined, "/srv/secrets/pve") {
		t.Errorf("the relocated PVE token is not collected: %v", paths)
	}
	for _, p := range paths {
		if p == "" {
			t.Error("keyPathsFor emitted an empty path")
		}
	}
	if strings.Contains(joined, "oidc") {
		t.Errorf("an install with no [oidc] section still lists an OIDC secret: %v", paths)
	}
}

// TestAddrInUseProbeUsesNoFixedPort is a machine-sharing guard: this
// package's tests must never bind a fixed port (three collisions have
// already cost this project time — T-1807-bug-01). Binding :0 lets the
// kernel choose.
func TestAddrInUseProbeUsesNoFixedPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("binding: %v", err)
	}
	addr := ln.Addr().String()
	defer func() { _ = ln.Close() }()

	check := backup.DaemonLiveness(filepath.Join(t.TempDir(), "vnprox.db"), addr)
	if err := check(); err == nil {
		t.Error("the liveness check did not notice a bound listener")
	}
	_ = ln.Close()
	if err := check(); err != nil {
		t.Errorf("the liveness check still reports a daemon after the listener closed: %v", err)
	}
}
