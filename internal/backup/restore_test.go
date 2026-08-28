// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bgovanlu/vnprox/internal/store"
)

// takeBackup produces an archive of the fixture and returns its path.
func takeBackup(t *testing.T, f *fixture, includeKeys bool) *Result {
	t.Helper()
	res, err := Create(context.Background(), Options{
		ConfigPath: f.configPath, DBPath: f.dbPath, KeyPaths: f.keyPaths,
		OutDir: filepath.Join(t.TempDir(), "backups"),
		Node:   seededNode, ToolVersion: "test", Now: fixedNow,
		IncludeKeys: includeKeys,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return res
}

// storeFingerprint is the whole store file's digest plus a row sample —
// the two things a "the original is intact" assertion needs. Byte identity
// alone would be brittle (SQLite rewrites page headers on open); the digest
// is taken without ever opening the file, so it is exact.
func storeFingerprint(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------- AC4 (a)

// TestRestore_AC4_RefusesAgainstALiveDaemon covers both liveness signals
// with the REAL mechanisms — a real flock and a real bound TCP listener —
// rather than a stubbed check, because a stub would prove only that the
// stub was called.
//
// The listener case binds 127.0.0.1:0 and reads back the kernel-assigned
// port, so this package claims no fixed port and cannot collide with the
// e2e fleet or packaging tests (see the report's machine-sharing note).
func TestRestore_AC4_RefusesAgainstALiveDaemon(t *testing.T) {
	ctx := context.Background()

	t.Run("runtime lock held", func(t *testing.T) {
		f := newFixture(t)
		res := takeBackup(t, f, false)
		fingerprint := storeFingerprint(t, f.dbPath)

		lock, err := store.AcquireRuntimeLock(f.dbPath)
		if err != nil {
			t.Fatalf("AcquireRuntimeLock: %v", err)
		}
		defer func() { _ = lock.Release() }()

		_, err = Restore(ctx, RestoreOptions{
			ArchivePath: res.Path, DBPath: f.dbPath, ConfigPath: f.configPath,
			KeyDir: f.keyDir, Listen: f.listen, Now: fixedNow,
		})
		if !errors.Is(err, ErrDaemonRunning) {
			t.Fatalf("Restore error = %v, want ErrDaemonRunning", err)
		}
		if !strings.Contains(err.Error(), "systemctl stop vnprox") {
			t.Errorf("the refusal does not tell the operator how to proceed: %v", err)
		}
		if got := storeFingerprint(t, f.dbPath); got != fingerprint {
			t.Error("the store changed despite the refusal")
		}

		// And once the daemon stops, the same restore succeeds — proving
		// the refusal was about liveness and not about something else in
		// the archive.
		if err := lock.Release(); err != nil {
			t.Fatalf("Release: %v", err)
		}
		if _, err := Restore(ctx, RestoreOptions{
			ArchivePath: res.Path, DBPath: f.dbPath, ConfigPath: f.configPath,
			KeyDir: f.keyDir, Listen: f.listen, Now: fixedNow,
		}); err != nil {
			t.Fatalf("Restore after the lock was released: %v", err)
		}
	})

	t.Run("listen address bound by an older daemon that takes no lock", func(t *testing.T) {
		f := newFixture(t)
		res := takeBackup(t, f, false)
		fingerprint := storeFingerprint(t, f.dbPath)

		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("binding a probe listener: %v", err)
		}
		defer func() { _ = ln.Close() }()
		listen := ln.Addr().String()

		// No runtime lock is taken at all here: this is exactly the
		// pre-T-1901 daemon, which is the one most likely to be running
		// when someone reaches for a restore.
		if held, lockErr := store.RuntimeLockHeld(f.dbPath); lockErr != nil || held {
			t.Fatalf("precondition: the runtime lock must NOT be held for this case to test the listener probe (held=%v lockErr=%v)", held, lockErr)
		}

		_, err = Restore(ctx, RestoreOptions{
			ArchivePath: res.Path, DBPath: f.dbPath, ConfigPath: f.configPath,
			KeyDir: f.keyDir, Listen: listen, Now: fixedNow,
		})
		if !errors.Is(err, ErrDaemonRunning) {
			t.Fatalf("Restore error = %v, want ErrDaemonRunning", err)
		}
		if !strings.Contains(err.Error(), listen) {
			t.Errorf("the refusal does not name the address that is in use: %v", err)
		}
		if got := storeFingerprint(t, f.dbPath); got != fingerprint {
			t.Error("the store changed despite the refusal")
		}

		_ = ln.Close()
		if _, err := Restore(ctx, RestoreOptions{
			ArchivePath: res.Path, DBPath: f.dbPath, ConfigPath: f.configPath,
			KeyDir: f.keyDir, Listen: listen, Now: fixedNow,
		}); err != nil {
			t.Fatalf("Restore after the listener closed: %v", err)
		}
	})

	t.Run("dry run is refused too", func(t *testing.T) {
		// A --dry-run that reported "this would work" against a live daemon
		// would be actively misleading.
		f := newFixture(t)
		res := takeBackup(t, f, false)
		lock, err := store.AcquireRuntimeLock(f.dbPath)
		if err != nil {
			t.Fatalf("AcquireRuntimeLock: %v", err)
		}
		defer func() { _ = lock.Release() }()

		if _, err := Restore(ctx, RestoreOptions{
			ArchivePath: res.Path, DBPath: f.dbPath, ConfigPath: f.configPath,
			KeyDir: f.keyDir, Listen: f.listen, DryRun: true, Now: fixedNow,
		}); !errors.Is(err, ErrDaemonRunning) {
			t.Fatalf("dry-run error = %v, want ErrDaemonRunning", err)
		}
	})
}

// ---------------------------------------------------------------- AC4 (b)

// TestRestore_AC4_AtomicityUnderInjectedFailure is the other half of AC4:
// "a restore interrupted midway leaves the original store intact
// (atomicity, tested by injecting a failure)".
//
// Failures are injected at each of the three points where the consequences
// differ, and each case asserts the original store is byte-identical
// afterwards — not merely "openable", which a half-restored store also is.
func TestRestore_AC4_AtomicityUnderInjectedFailure(t *testing.T) {
	ctx := context.Background()
	injected := errors.New("injected mid-restore failure")

	cases := []struct {
		name  string
		hooks func() restoreHooks
		// asideExpected: whether a .pre-restore- copy should be left behind
		// (it should not: a rolled-back restore puts everything back).
		why string
	}{
		{
			name: "fails after staging, before the live store is touched",
			hooks: func() restoreHooks {
				return restoreHooks{afterStage: func() error { return injected }}
			},
			why: "the most common real failure: the archive validated and migrated, then something else went wrong",
		},
		{
			name: "fails between moving the live store aside and installing the new one",
			hooks: func() restoreHooks {
				return restoreHooks{afterMoveAside: func() error { return injected }}
			},
			why: "the only genuinely dangerous window in the whole operation; the rollback path is what closes it",
		},
		{
			name: "fails immediately before the swap",
			hooks: func() restoreHooks {
				return restoreHooks{beforeMoveAside: func() error { return injected }}
			},
			why: "boundary case: the decision phase has completed and the act phase has not begun",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			res := takeBackup(t, f, false)

			// Make the LIVE store differ from the archive, so "intact"
			// means "the live one", not "either of two identical files".
			liveDB, err := store.Open(ctx, f.dbPath)
			if err != nil {
				t.Fatalf("opening the live store: %v", err)
			}
			if _, seedErr := liveDB.Conn().ExecContext(ctx,
				`INSERT INTO annotations (id, ref, content, created_by, created_at, updated_at)
				 VALUES ('ann-live', 'node:pve1', 'ONLY IN THE LIVE STORE', 'root@pam', 1, 1)`); seedErr != nil {
				t.Fatalf("marking the live store: %v", seedErr)
			}
			if closeErr := liveDB.Close(); closeErr != nil {
				t.Fatalf("closing the live store: %v", closeErr)
			}
			fingerprint := storeFingerprint(t, f.dbPath)
			before := tableRowCounts(t, f.dbPath)

			opts := RestoreOptions{
				ArchivePath: res.Path, DBPath: f.dbPath, ConfigPath: f.configPath,
				KeyDir: f.keyDir, Listen: f.listen, Now: fixedNow,
			}
			opts.hooks = tc.hooks()
			_, err = Restore(ctx, opts)
			if !errors.Is(err, injected) {
				t.Fatalf("Restore error = %v, want the injected failure (%s)", err, tc.why)
			}

			// (1) byte-identical.
			if got := storeFingerprint(t, f.dbPath); got != fingerprint {
				t.Errorf("the live store was modified by a failed restore (%s)", tc.why)
			}
			// (2) still a working store with its live-only marker.
			after := tableRowCounts(t, f.dbPath)
			if len(after) != len(before) {
				t.Errorf("table count changed: %d -> %d", len(before), len(after))
			}
			db, err := sql.Open("sqlite", "file:"+f.dbPath)
			if err != nil {
				t.Fatalf("reopening: %v", err)
			}
			var content string
			err = db.QueryRow(`SELECT content FROM annotations WHERE id = 'ann-live'`).Scan(&content)
			_ = db.Close()
			if err != nil {
				t.Fatalf("the live-only row is gone after a failed restore: %v", err)
			}
			if content != "ONLY IN THE LIVE STORE" {
				t.Errorf("live-only row = %q, want the original", content)
			}
			// (3) no debris: no aside copy stranded, no staging directory
			// holding a plaintext store copy.
			dir := filepath.Dir(f.dbPath)
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("ReadDir: %v", err)
			}
			for _, e := range entries {
				if strings.Contains(e.Name(), ".pre-restore-") {
					t.Errorf("a failed restore stranded %q — the original was moved aside and not put back", e.Name())
				}
				if strings.HasPrefix(e.Name(), ".vnprox-restore-") {
					t.Errorf("a failed restore left the staging directory %q behind", e.Name())
				}
			}
		})
	}
}

// TestRestore_AC4_TheAtomicityTestIsNotVacuous: the same fixture, the same
// call, no injected failure — the restore MUST succeed and MUST replace the
// live-only row. Without this, all three cases above could be passing
// because Restore never gets anywhere near the store.
func TestRestore_AC4_TheAtomicityTestIsNotVacuous(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	res := takeBackup(t, f, false)

	liveDB, err := store.Open(ctx, f.dbPath)
	if err != nil {
		t.Fatalf("opening the live store: %v", err)
	}
	if _, seedErr := liveDB.Conn().ExecContext(ctx,
		`INSERT INTO annotations (id, ref, content, created_by, created_at, updated_at)
		 VALUES ('ann-live', 'node:pve1', 'ONLY IN THE LIVE STORE', 'root@pam', 1, 1)`); seedErr != nil {
		t.Fatalf("marking the live store: %v", seedErr)
	}
	_ = liveDB.Close()
	fingerprint := storeFingerprint(t, f.dbPath)

	plan, err := Restore(ctx, RestoreOptions{
		ArchivePath: res.Path, DBPath: f.dbPath, ConfigPath: f.configPath,
		KeyDir: f.keyDir, Listen: f.listen, Now: fixedNow,
	})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := storeFingerprint(t, f.dbPath); got == fingerprint {
		t.Fatal("a successful restore left the store byte-identical — it did nothing, so the atomicity cases prove nothing")
	}
	db, err := sql.Open("sqlite", "file:"+f.dbPath)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	var n int
	if queryErr := db.QueryRow(`SELECT count(*) FROM annotations WHERE id = 'ann-live'`).Scan(&n); queryErr != nil {
		t.Fatalf("query: %v", queryErr)
	}
	_ = db.Close()
	if n != 0 {
		t.Error("the restore did not replace the live store: the live-only row is still there")
	}

	// The previous store is kept, at the documented path, and is still a
	// readable store — that is the operator's undo.
	if _, statErr := os.Stat(plan.PreRestorePath); statErr != nil {
		t.Fatalf("the previous store was not kept at %s: %v", plan.PreRestorePath, statErr)
	}
	prev, err := sql.Open("sqlite", "file:"+plan.PreRestorePath)
	if err != nil {
		t.Fatalf("opening the kept previous store: %v", err)
	}
	defer func() { _ = prev.Close() }()
	if err := prev.QueryRow(`SELECT count(*) FROM annotations WHERE id = 'ann-live'`).Scan(&n); err != nil {
		t.Fatalf("the kept previous store is unreadable: %v", err)
	}
	if n != 1 {
		t.Error("the kept previous store does not contain the live-only row")
	}
	// No stale WAL from the old store next to the restored one.
	for _, s := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(f.dbPath + s); err == nil {
			t.Errorf("a %s sidecar survived the swap next to the restored store", s)
		}
	}
}

// ---------------------------------------------------------------- AC3 (b)

// TestRestore_RefusesADowngrade is the second half of AC3. The forward
// direction (backup at N, restore into N+k) lives in
// internal/store/backup_upgrade_test.go against T-1807's fixture corpus;
// this is the direction that must be refused.
func TestRestore_RefusesADowngrade(t *testing.T) {
	ctx := context.Background()

	latest, err := store.LatestSchemaVersion()
	if err != nil {
		t.Fatalf("LatestSchemaVersion: %v", err)
	}

	t.Run("archive from a newer build", func(t *testing.T) {
		f := newFixture(t)
		// Fake "a newer vnprox already migrated this store" by bumping the
		// recorded schema version past what this build knows.
		bumpSchemaVersion(t, f.dbPath, latest+5)
		res := takeBackup(t, f, false)
		if res.Manifest.SchemaVersion != latest+5 {
			t.Fatalf("manifest schema version = %d, want %d", res.Manifest.SchemaVersion, latest+5)
		}

		target := newFixture(t)
		fingerprint := storeFingerprint(t, target.dbPath)
		_, err := Restore(ctx, RestoreOptions{
			ArchivePath: res.Path, DBPath: target.dbPath, ConfigPath: target.configPath,
			KeyDir: target.keyDir, Listen: target.listen, Now: fixedNow,
		})
		if !errors.Is(err, ErrSchemaDowngrade) {
			t.Fatalf("Restore error = %v, want ErrSchemaDowngrade", err)
		}
		if !strings.Contains(err.Error(), fmt.Sprintf("%d", latest)) {
			t.Errorf("the refusal does not name what this build understands: %v", err)
		}
		if got := storeFingerprint(t, target.dbPath); got != fingerprint {
			t.Error("the target store was modified by a refused downgrade")
		}
		// Nothing was staged, either: the refusal happens from the
		// manifest, before extraction.
		assertNoRestoreDebris(t, filepath.Dir(target.dbPath))
	})

	t.Run("manifest lies about the schema version", func(t *testing.T) {
		// The manifest is inside the archive, so it is exactly as
		// untrusted as the rest. An edited manifest claiming a restorable
		// version must not smuggle a newer store past the check.
		f := newFixture(t)
		bumpSchemaVersion(t, f.dbPath, latest+5)
		res := takeBackup(t, f, false)
		forged := forgeManifest(t, res.Path, func(m *Manifest) { m.SchemaVersion = 1 })

		target := newFixture(t)
		fingerprint := storeFingerprint(t, target.dbPath)
		_, err := Restore(ctx, RestoreOptions{
			ArchivePath: forged, DBPath: target.dbPath, ConfigPath: target.configPath,
			KeyDir: target.keyDir, Listen: target.listen, Now: fixedNow,
		})
		if !errors.Is(err, ErrSchemaMismatch) {
			t.Fatalf("Restore error = %v, want ErrSchemaMismatch — a forged manifest walked a newer store past the downgrade check", err)
		}
		if got := storeFingerprint(t, target.dbPath); got != fingerprint {
			t.Error("the target store was modified by a refused restore")
		}
		assertNoRestoreDebris(t, filepath.Dir(target.dbPath))
	})

	t.Run("same version restores fine", func(t *testing.T) {
		// The control: without this, both refusals above could be passing
		// because restore refuses everything.
		f := newFixture(t)
		res := takeBackup(t, f, false)
		target := newFixture(t)
		if _, err := Restore(ctx, RestoreOptions{
			ArchivePath: res.Path, DBPath: target.dbPath, ConfigPath: target.configPath,
			KeyDir: target.keyDir, Listen: target.listen, Now: fixedNow,
		}); err != nil {
			t.Fatalf("a same-version restore was refused: %v", err)
		}
	})
}

// TestRestore_RefusesASupportBundle: T-1902's bundle shares this archive
// format with a deliberately redacted payload. Restoring one would install
// an incomplete store.
func TestRestore_RefusesASupportBundle(t *testing.T) {
	f := newFixture(t)
	res := takeBackup(t, f, false)
	bundle := forgeManifest(t, res.Path, func(m *Manifest) { m.Kind = KindSupportBundle })

	target := newFixture(t)
	fingerprint := storeFingerprint(t, target.dbPath)
	_, err := Restore(context.Background(), RestoreOptions{
		ArchivePath: bundle, DBPath: target.dbPath, ConfigPath: target.configPath,
		KeyDir: target.keyDir, Listen: target.listen, Now: fixedNow,
	})
	if !errors.Is(err, ErrWrongKind) {
		t.Fatalf("Restore error = %v, want ErrWrongKind", err)
	}
	if got := storeFingerprint(t, target.dbPath); got != fingerprint {
		t.Error("the target store was modified")
	}
}

// ---------------------------------------------------------------- dry run

// TestRestore_DryRunChangesNothingAndMatchesTheRealRun. `--dry-run` exists
// so an operator can decide; a dry run that described something other than
// what happens would be worse than none.
func TestRestore_DryRunChangesNothingAndMatchesTheRealRun(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	res := takeBackup(t, f, true)

	target := newFixture(t)
	before := listDirTree(t, target.dir)

	opts := RestoreOptions{
		ArchivePath: res.Path, DBPath: target.dbPath, ConfigPath: target.configPath,
		KeyDir: target.keyDir, Listen: target.listen, Now: fixedNow,
		RestoreConfig: true, RestoreKeys: true,
	}
	dryOpts := opts
	dryOpts.DryRun = true
	dry, err := Restore(ctx, dryOpts)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if dry.Applied {
		t.Error("a dry run reported Applied = true")
	}
	if after := listDirTree(t, target.dir); after != before {
		t.Errorf("the dry run changed the filesystem:\n%s\n---\n%s", before, after)
	}

	realRun, err := Restore(ctx, opts)
	if err != nil {
		t.Fatalf("real run: %v", err)
	}
	if !realRun.Applied {
		t.Error("the real run reported Applied = false")
	}

	// Everything the dry run promised, the real run did.
	if dry.StorePath != realRun.StorePath || dry.SchemaFrom != realRun.SchemaFrom || dry.SchemaTo != realRun.SchemaTo {
		t.Errorf("dry run plan differs from the real one:\n dry: %+v\nreal: %+v", dry, realRun)
	}
	if strings.Join(dry.KeyPaths, ",") != strings.Join(realRun.KeyPaths, ",") {
		t.Errorf("dry run key paths %v != real %v", dry.KeyPaths, realRun.KeyPaths)
	}
	if strings.Join(dry.Notes, "|") != strings.Join(realRun.Notes, "|") {
		t.Error("dry run notes differ from the real run's")
	}
	for _, p := range realRun.KeyPaths {
		info, err := os.Stat(p)
		if err != nil {
			t.Errorf("--restore-keys did not install %s: %v", p, err)
			continue
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("restored key %s has mode %04o, want 0600", p, perm)
		}
	}
	// The overwritten config and keys were moved aside, not destroyed.
	if _, err := os.Stat(target.configPath + ".pre-restore-" + "20260304-050607"); err != nil {
		t.Errorf("the previous config was not kept: %v", err)
	}
}

// TestRestore_RefusesRestoreKeysOnAKeylessArchive: asking for something the
// archive cannot provide must be an error, not a silent no-op that leaves
// an operator believing their keys came back.
func TestRestore_RefusesRestoreKeysOnAKeylessArchive(t *testing.T) {
	f := newFixture(t)
	res := takeBackup(t, f, false)
	target := newFixture(t)

	_, err := Restore(context.Background(), RestoreOptions{
		ArchivePath: res.Path, DBPath: target.dbPath, ConfigPath: target.configPath,
		KeyDir: target.keyDir, Listen: target.listen, Now: fixedNow, RestoreKeys: true,
	})
	if err == nil || !strings.Contains(err.Error(), "--include-keys") {
		t.Fatalf("Restore error = %v, want a refusal naming --include-keys", err)
	}
}

// TestRestore_NotesDescribeWhatDoesAndDoesNotCarryOver is the
// restore-to-different-hardware story, asserted rather than only
// documented: the notes are generated from the archive, so they cannot go
// stale relative to what was actually restored.
func TestRestore_NotesDescribeWhatDoesAndDoesNotCarryOver(t *testing.T) {
	ctx := context.Background()

	t.Run("archive without keys", func(t *testing.T) {
		f := newFixture(t)
		res := takeBackup(t, f, false)
		target := newFixture(t)
		plan, err := Restore(ctx, RestoreOptions{
			ArchivePath: res.Path, DBPath: target.dbPath, ConfigPath: target.configPath,
			KeyDir: target.keyDir, Listen: target.listen, DryRun: true, Now: fixedNow,
		})
		if err != nil {
			t.Fatalf("Restore: %v", err)
		}
		joined := strings.Join(plan.Notes, "\n")
		for _, want := range []string{
			"no key material", "must be re-entered",
			"peer cluster secret", "/etc/pve", "hostname",
			"--restore-config",
		} {
			if !strings.Contains(joined, want) {
				t.Errorf("the restore notes never mention %q:\n%s", want, joined)
			}
		}
	})

	t.Run("archive with keys, keys not installed", func(t *testing.T) {
		f := newFixture(t)
		res := takeBackup(t, f, true)
		target := newFixture(t)
		plan, err := Restore(ctx, RestoreOptions{
			ArchivePath: res.Path, DBPath: target.dbPath, ConfigPath: target.configPath,
			KeyDir: target.keyDir, Listen: target.listen, DryRun: true, Now: fixedNow,
		})
		if err != nil {
			t.Fatalf("Restore: %v", err)
		}
		joined := strings.Join(plan.Notes, "\n")
		if !strings.Contains(joined, "--restore-keys was NOT given") {
			t.Errorf("the notes do not warn that the sealed columns will not decrypt:\n%s", joined)
		}
	})
}

// ---------------------------------------------------------------- helpers

func bumpSchemaVersion(t *testing.T, path string, to int) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`UPDATE kv SET v = ? WHERE k = 'schema_version'`, fmt.Sprintf("%d", to)); err != nil {
		t.Fatalf("bumping schema version: %v", err)
	}
}

// forgeManifest rewrites an archive's manifest, re-signing nothing (there
// is nothing to re-sign) but keeping the entry digests correct — i.e. the
// most capable attacker the format contemplates: one who can edit the
// header at will.
func forgeManifest(t *testing.T, src string, mutate func(*Manifest)) string {
	t.Helper()
	m, err := InspectArchive(src, DefaultLimits())
	if err != nil {
		t.Fatalf("inspecting %s: %v", src, err)
	}
	// Extract, mutate, rewrite.
	stage := t.TempDir()
	f, err := os.Open(src)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := Extract(f, stage, DefaultLimits()); err != nil {
		_ = f.Close()
		t.Fatalf("extract: %v", err)
	}
	_ = f.Close()

	mutate(m)
	dest := filepath.Join(t.TempDir(), "forged.tar.gz")
	if _, err := Write(dest, *m, stage); err != nil {
		t.Fatalf("writing forged archive: %v", err)
	}
	return dest
}

func assertNoRestoreDebris(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", dir, err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".vnprox-restore-") || strings.Contains(e.Name(), ".pre-restore-") {
			t.Errorf("a refused restore left %q behind in %s", e.Name(), dir)
		}
	}
}

func listDirTree(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s %d %04o\n", p, info.Size(), info.Mode().Perm())
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return b.String()
}
