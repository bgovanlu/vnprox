// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bgovanlu/vnprox/internal/store"
)

// seedDisasterFixture builds everything AC4's daemon-down scenario needs
// with NO daemon anywhere: a real SQLite store file seeded directly through
// internal/store (exactly what a stopped daemon leaves behind), a config
// file pointing at it, and a fake local interfaces file. Returns the config
// path, the interfaces path, and the ids of the seeded snapshot + changeset.
func seedDisasterFixture(t *testing.T, changesetStatus string) (configPath, ifacesPath, snapshotID, changesetID string) {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()

	dbPath := filepath.Join(dir, "vnprox.db")
	db, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	changesetID = store.NewULID()
	if insertErr := store.NewChangesetRepo(db).Insert(ctx, store.Changeset{
		ID: changesetID, Author: "root@pam", Status: changesetStatus, OpsJSON: "[]",
		CreatedAt: 100, UpdatedAt: 100,
		ConfirmDeadline: sql.NullInt64{Int64: time.Now().Add(time.Hour).Unix(), Valid: true},
	}); insertErr != nil {
		t.Fatalf("seed changeset: %v", insertErr)
	}

	// The pre-apply state the snapshot preserves (what restore must bring back).
	preContent := "auto lo\niface lo inet loopback\n# pre-apply state\n"
	blobHash, err := store.NewBlobRepo(db).Put(ctx, preContent)
	if err != nil {
		t.Fatalf("seed blob: %v", err)
	}
	snapshotID = store.NewULID()
	filesJSON, _ := json.Marshal([]snapshotFileEntry{{Node: "pve1", Path: "/etc/network/interfaces", SHA256: blobHash}})
	snapRepo := store.NewSnapshotRepo(db)
	if insertErr := snapRepo.Insert(ctx, store.Snapshot{
		ID: snapshotID, ChangesetID: sql.NullString{String: changesetID, Valid: true},
		TakenAt: 100, Kind: "pre", FilesJSON: string(filesJSON),
	}); insertErr != nil {
		t.Fatalf("seed snapshot: %v", insertErr)
	}
	if filesErr := snapRepo.InsertFiles(ctx, []store.SnapshotFileRef{
		{SnapshotID: snapshotID, Node: "pve1", Path: "/etc/network/interfaces", SHA256: blobHash},
	}); filesErr != nil {
		t.Fatalf("seed snapshot_files: %v", filesErr)
	}

	// A minimal but valid config pointing at the DB (config.Load requires
	// resolvable TLS paths; reuse the repo's checked-in dev certs).
	certPath, err := filepath.Abs("../../testdata/certs/dev-cert.pem")
	if err != nil {
		t.Fatalf("abs cert path: %v", err)
	}
	keyPath, err := filepath.Abs("../../testdata/certs/dev-key.pem")
	if err != nil {
		t.Fatalf("abs key path: %v", err)
	}
	configPath = filepath.Join(dir, "vnprox.toml")
	cfg := fmt.Sprintf("[server]\nlisten = \"127.0.0.1:8007\"\ntls_cert = %q\ntls_key = %q\n\n[storage]\ndb_path = %q\nsession_key_file = %q\n",
		certPath, keyPath, dbPath, filepath.Join(dir, "session.key"))
	if err := os.WriteFile(configPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// The "live" local interfaces file (the bad state to recover from).
	ifacesPath = filepath.Join(dir, "interfaces")
	if err := os.WriteFile(ifacesPath, []byte("auto lo\niface lo inet loopback\n# BAD applied state\n"), 0o644); err != nil {
		t.Fatalf("write interfaces: %v", err)
	}
	return configPath, ifacesPath, snapshotID, changesetID
}

// testEnv returns a cliEnv pointing at the fixture's temp paths, pretending
// to be root on host pve1, with an ifreload that records its invocations.
func testEnv(ifacesPath string, reloads *int) *cliEnv {
	return &cliEnv{
		geteuid:  func() int { return 0 },
		hostname: func() (string, error) { return "pve1", nil },
		ifreload: func(context.Context) error {
			*reloads++
			return nil
		},
		interfacesPath: ifacesPath,
		now:            time.Now,
	}
}

// AC4: `vnproxctl snapshots restore` works with the daemon stopped except
// for its DB — direct DB read + blob decompress + file write + (fake)
// ifreload exec, no HTTP anywhere.
func TestSnapshotsRestore_DaemonDown(t *testing.T) {
	configPath, ifacesPath, snapshotID, _ := seedDisasterFixture(t, "committed")
	reloads := 0
	env := testEnv(ifacesPath, &reloads)

	var stdout, stderr bytes.Buffer
	code := runSnapshotsEnv(env, []string{"restore", "--config", configPath, snapshotID}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}

	got, err := os.ReadFile(ifacesPath)
	if err != nil {
		t.Fatalf("reading restored file: %v", err)
	}
	if !strings.Contains(string(got), "# pre-apply state") || strings.Contains(string(got), "BAD applied state") {
		t.Fatalf("restored content = %q, want the pre-apply state", got)
	}
	if reloads != 1 {
		t.Fatalf("ifreload invocations = %d, want 1", reloads)
	}

	// A timestamped backup of the bad state was left behind.
	matches, _ := filepath.Glob(ifacesPath + ".vnprox-backup-*")
	if len(matches) != 1 {
		t.Fatalf("backup files = %v, want exactly one", matches)
	}
	backup, _ := os.ReadFile(matches[0])
	if !strings.Contains(string(backup), "BAD applied state") {
		t.Fatalf("backup content = %q, want the pre-restore state", backup)
	}

	// The action was audited directly in the DB, attributed to the CLI actor.
	assertCLIAudit(t, configPath, "snapshot.restore.cli", "success")
}

func TestSnapshotsRestore_RefusesNonRoot(t *testing.T) {
	configPath, ifacesPath, snapshotID, _ := seedDisasterFixture(t, "committed")
	reloads := 0
	env := testEnv(ifacesPath, &reloads)
	env.geteuid = func() int { return 1000 }

	var stdout, stderr bytes.Buffer
	code := runSnapshotsEnv(env, []string{"restore", "--config", configPath, snapshotID}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "root") {
		t.Fatalf("stderr = %q, want a root-required message", stderr.String())
	}
	if reloads != 0 {
		t.Fatal("ifreload ran despite the root refusal")
	}
}

func TestSnapshotsRestore_UnknownNode(t *testing.T) {
	configPath, ifacesPath, snapshotID, _ := seedDisasterFixture(t, "committed")
	reloads := 0
	env := testEnv(ifacesPath, &reloads)
	env.hostname = func() (string, error) { return "not-in-snapshot", nil }

	var stdout, stderr bytes.Buffer
	code := runSnapshotsEnv(env, []string{"restore", "--config", configPath, snapshotID}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "--node") {
		t.Fatalf("stderr = %q, want a hint at --node", stderr.String())
	}

	// With --node picking a captured node explicitly, it succeeds.
	stdout.Reset()
	stderr.Reset()
	code = runSnapshotsEnv(env, []string{"restore", "--config", configPath, "--node", "pve1", snapshotID}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code with --node = %d, want 0 (stderr: %s)", code, stderr.String())
	}
}

func TestSnapshotsRestore_ReloadFailureRestoresBackup(t *testing.T) {
	configPath, ifacesPath, snapshotID, _ := seedDisasterFixture(t, "committed")
	calls := 0
	env := &cliEnv{
		geteuid:  func() int { return 0 },
		hostname: func() (string, error) { return "pve1", nil },
		ifreload: func(context.Context) error {
			calls++
			if calls == 1 {
				return fmt.Errorf("boom")
			}
			return nil // the restore-backup re-reload succeeds
		},
		interfacesPath: ifacesPath,
		now:            time.Now,
	}

	var stdout, stderr bytes.Buffer
	code := runSnapshotsEnv(env, []string{"restore", "--config", configPath, snapshotID}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	got, _ := os.ReadFile(ifacesPath)
	if !strings.Contains(string(got), "BAD applied state") {
		t.Fatalf("file after failed reload = %q, want the original (bad) state put back", got)
	}
	if calls != 2 {
		t.Fatalf("ifreload calls = %d, want 2 (fail, then restore re-reload)", calls)
	}
}

func TestSnapshotsList_DaemonDown(t *testing.T) {
	configPath, _, snapshotID, changesetID := seedDisasterFixture(t, "committed")

	var stdout, stderr bytes.Buffer
	code := runSnapshotsList([]string{"--config", configPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{snapshotID, changesetID, "pre", "pve1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list output missing %q:\n%s", want, out)
		}
	}
}

// AC4 (rollback-now): an awaiting_confirm changeset whose daemon died is
// rolled back from its pre-apply snapshot and marked rolled_back directly
// in the DB, so a restarted daemon won't re-arm its timer.
func TestRollbackNow_AwaitingConfirm_DaemonDown(t *testing.T) {
	configPath, ifacesPath, _, changesetID := seedDisasterFixture(t, "awaiting_confirm")
	reloads := 0
	env := testEnv(ifacesPath, &reloads)

	var stdout, stderr bytes.Buffer
	code := runRollbackNowEnv(env, []string{"--config", configPath, changesetID}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}

	got, _ := os.ReadFile(ifacesPath)
	if !strings.Contains(string(got), "# pre-apply state") {
		t.Fatalf("file after rollback-now = %q, want the pre-apply state", got)
	}
	if reloads != 1 {
		t.Fatalf("ifreload invocations = %d, want 1", reloads)
	}

	// Status marked terminal, deadline cleared.
	ctx := context.Background()
	db, err := openStore(ctx, configPath)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer func() { _ = db.Close() }()
	cs, err := store.NewChangesetRepo(db).Get(ctx, changesetID)
	if err != nil {
		t.Fatalf("Get changeset: %v", err)
	}
	if cs.Status != "rolled_back" {
		t.Fatalf("status = %q, want rolled_back", cs.Status)
	}
	if cs.ConfirmDeadline.Valid {
		t.Fatal("confirm_deadline not cleared")
	}
}

func TestRollbackNow_CommittedRefusedWithHint(t *testing.T) {
	configPath, ifacesPath, _, changesetID := seedDisasterFixture(t, "committed")
	reloads := 0
	env := testEnv(ifacesPath, &reloads)

	var stdout, stderr bytes.Buffer
	code := runRollbackNowEnv(env, []string{"--config", configPath, changesetID}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "snapshots restore") {
		t.Fatalf("stderr = %q, want a pointer at snapshots restore", stderr.String())
	}
	if reloads != 0 {
		t.Fatal("ifreload ran for a refused rollback")
	}
}

// assertCLIAudit opens the fixture DB and asserts one audit row with the
// given action/result attributed to the CLI actor exists.
func assertCLIAudit(t *testing.T, configPath, action, result string) {
	t.Helper()
	ctx := context.Background()
	db, err := openStore(ctx, configPath)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer func() { _ = db.Close() }()
	entries, err := store.NewAuditRepo(db).List(ctx, "", 0)
	if err != nil {
		t.Fatalf("audit List: %v", err)
	}
	for _, e := range entries {
		if e.Action == action && e.Result == result && e.Username == cliActor {
			return
		}
	}
	t.Fatalf("no audit row action=%s result=%s username=%s in %+v", action, result, cliActor, entries)
}
