package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestSnapshotTo_CapturesCommitsStillInTheWAL is the reason SnapshotTo
// exists at all, stated as a test.
//
// It is built to be non-vacuous in the one way that matters: it first
// proves the naive alternative (copying vnprox.db's bytes) genuinely LOSES
// data under the daemon's own WAL configuration, and only then proves
// SnapshotTo does not. If a future SQLite/driver change ever made the naive
// copy safe, the control assertion fails loudly rather than this test
// passing for the wrong reason.
func TestSnapshotTo_CapturesCommitsStillInTheWAL(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	src := filepath.Join(dir, "vnprox.db")

	db, err := Open(ctx, src)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Write a row and DO NOT close the database: in WAL mode this commit
	// lives in vnprox.db-wal, not in vnprox.db, until a checkpoint.
	if _, seedErr := db.Conn().ExecContext(ctx,
		`INSERT INTO kv (k, v) VALUES ('wal_probe', 'committed-but-in-the-wal')`); seedErr != nil {
		t.Fatalf("seeding: %v", seedErr)
	}

	// --- control: the naive file copy loses it --------------------------
	naive := filepath.Join(dir, "naive-copy.db")
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("reading %s: %v", src, err)
	}
	if err := os.WriteFile(naive, raw, 0o600); err != nil {
		t.Fatalf("writing naive copy: %v", err)
	}
	// The naive copy is a structurally VALID SQLite file — that is exactly
	// what makes it dangerous, and why the control asserts readability
	// before asserting the row is missing. "The copy is corrupt" and "the
	// copy is missing committed data" are different failures, and only the
	// second is the one this test is about.
	if _, err := InspectSchemaVersion(ctx, naive); err != nil {
		t.Fatalf("control failed: the naive byte copy is not even a readable SQLite file (%v) — "+
			"this test proves nothing about missing WAL commits until that is true", err)
	}
	if got, found := kvProbe(t, naive); found {
		t.Fatalf("control failed: a plain byte copy of the WAL-mode store already contained %q — "+
			"this test can no longer prove SnapshotTo is doing anything, fix the control before trusting it", got)
	}

	// --- SnapshotTo captures it ------------------------------------------
	snap := filepath.Join(dir, "snapshot.db")
	if err := SnapshotTo(ctx, src, snap); err != nil {
		t.Fatalf("SnapshotTo: %v", err)
	}
	got, found := kvProbe(t, snap)
	if !found {
		t.Fatal("SnapshotTo produced a copy missing a committed row that was still in the WAL — the snapshot is not consistent")
	}
	if got != "committed-but-in-the-wal" {
		t.Errorf("snapshot kv value = %q, want the committed value", got)
	}

	// A VACUUM INTO output is checkpointed: no sidecars should exist next
	// to it, or a restore would rename a stale WAL into place alongside it.
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(snap + suffix); err == nil {
			t.Errorf("SnapshotTo left a %s sidecar next to the snapshot", suffix)
		}
	}
	if info, err := os.Stat(snap); err != nil {
		t.Fatalf("stat snapshot: %v", err)
	} else if perm := info.Mode().Perm(); perm != dbFilePerm {
		t.Errorf("snapshot permissions = %04o, want %04o (a store copy is as sensitive as the store)", perm, dbFilePerm)
	}
}

// kvProbe opens a database file directly and looks for the probe row.
func kvProbe(t *testing.T, path string) (string, bool) {
	t.Helper()
	db, err := sql.Open("sqlite", rawDSN(path))
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer func() { _ = db.Close() }()
	var v string
	err = db.QueryRowContext(context.Background(), `SELECT v FROM kv WHERE k = 'wal_probe'`).Scan(&v)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false
	case err != nil:
		// A copy that is not even a readable database counts as "not found".
		return "", false
	}
	return v, true
}

// TestSnapshotTo_WorksWhileTheStoreIsBeingWritten covers the cron case: a
// backup is taken on a schedule, against a daemon that is doing its job.
func TestSnapshotTo_WorksWhileTheStoreIsBeingWritten(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	src := filepath.Join(dir, "vnprox.db")
	db, err := Open(ctx, src)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			_, _ = db.Conn().ExecContext(ctx,
				`INSERT INTO audit_log (at, username, action, result) VALUES (?, 'writer', 'probe', 'success')`, i)
		}
	}()

	snap := filepath.Join(dir, "snapshot.db")
	snapErr := SnapshotTo(ctx, src, snap)
	close(stop)
	wg.Wait()
	if snapErr != nil {
		t.Fatalf("SnapshotTo against a concurrently-written store: %v", snapErr)
	}
	// The snapshot must be a valid, readable database — the property that
	// makes it a backup rather than a torn file.
	if _, err := InspectSchemaVersion(ctx, snap); err != nil {
		t.Fatalf("snapshot taken during writes is not a readable store: %v", err)
	}
}

func TestInspectSchemaVersion(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	latest, err := LatestSchemaVersion()
	if err != nil {
		t.Fatalf("LatestSchemaVersion: %v", err)
	}
	if latest < 33 {
		t.Fatalf("LatestSchemaVersion = %d, which is lower than the 33 migrations already shipped — loadMigrations is broken", latest)
	}

	// A migrated store reports latest, and inspecting does NOT migrate.
	migrated := filepath.Join(dir, "migrated.db")
	db, err := Open(ctx, migrated)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = db.Close()
	if v, err := InspectSchemaVersion(ctx, migrated); err != nil || v != latest {
		t.Fatalf("InspectSchemaVersion(migrated) = %d, %v; want %d, nil", v, err, latest)
	}

	// A frozen store reports its frozen version and stays frozen — the
	// property restore depends on when it decides whether an archive is a
	// downgrade.
	frozen := filepath.Join(dir, "frozen.db")
	fdb := openFrozenAtPath(t, frozen, 5)
	_ = fdb.Close()
	if v, err := InspectSchemaVersion(ctx, frozen); err != nil || v != 5 {
		t.Fatalf("InspectSchemaVersion(frozen at 5) = %d, %v; want 5, nil", v, err)
	}
	if v, err := InspectSchemaVersion(ctx, frozen); err != nil || v != 5 {
		t.Fatalf("InspectSchemaVersion mutated the file: second read = %d, %v; want 5, nil", v, err)
	}

	// A file that is not a database at all is an error, never a silent 0 —
	// otherwise an attacker-supplied blob would look like "a fresh store".
	garbage := filepath.Join(dir, "garbage.db")
	if err := os.WriteFile(garbage, []byte("this is not a sqlite database, not even slightly"), 0o600); err != nil {
		t.Fatalf("writing garbage: %v", err)
	}
	if v, err := InspectSchemaVersion(ctx, garbage); err == nil {
		t.Fatalf("InspectSchemaVersion(garbage) = %d, nil; want an error", v)
	}

	// A missing file is an error, not a zero.
	if _, err := InspectSchemaVersion(ctx, filepath.Join(dir, "nope.db")); err == nil {
		t.Fatal("InspectSchemaVersion on a missing file returned no error")
	}
}

// TestRuntimeLock_DetectsAHolder is the mechanism `vnproxctl restore`'s
// refusal rests on. flock associates a lock with the open file
// *description*, not the process, so a second descriptor on the same file —
// even in this same test binary — is genuinely blocked; that is what makes
// this testable in-process without spawning a daemon.
func TestRuntimeLock_DetectsAHolder(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "vnprox.db")

	// Nothing has run: no lock file, so not held — and asking must not
	// create one (a probe that leaves state behind is a probe that changes
	// what the next probe sees).
	held, err := RuntimeLockHeld(dbPath)
	if err != nil {
		t.Fatalf("RuntimeLockHeld: %v", err)
	}
	if held {
		t.Fatal("RuntimeLockHeld reported a holder before any lock was ever taken")
	}
	if _, statErr := os.Stat(RuntimeLockPath(dbPath)); statErr == nil {
		t.Fatal("RuntimeLockHeld created the lock file as a side effect of asking")
	}

	lock, err := AcquireRuntimeLock(dbPath)
	if err != nil {
		t.Fatalf("AcquireRuntimeLock: %v", err)
	}
	held, err = RuntimeLockHeld(dbPath)
	if err != nil {
		t.Fatalf("RuntimeLockHeld while held: %v", err)
	}
	if !held {
		t.Fatal("RuntimeLockHeld = false while the lock is held — a restore would proceed against a live daemon")
	}

	// A second acquisition is refused with the sentinel, so two daemons
	// cannot both believe they own the store.
	if _, reErr := AcquireRuntimeLock(dbPath); !errors.Is(reErr, ErrRuntimeLockHeld) {
		t.Fatalf("second AcquireRuntimeLock error = %v, want ErrRuntimeLockHeld", reErr)
	}

	if releaseErr := lock.Release(); releaseErr != nil {
		t.Fatalf("Release: %v", releaseErr)
	}
	held, err = RuntimeLockHeld(dbPath)
	if err != nil {
		t.Fatalf("RuntimeLockHeld after release: %v", err)
	}
	if held {
		t.Fatal("the lock is still reported held after Release — a stopped daemon would permanently block recovery")
	}
	// And it can be re-acquired: the file surviving on disk is not a stale
	// lock.
	relock, err := AcquireRuntimeLock(dbPath)
	if err != nil {
		t.Fatalf("re-acquiring after release: %v", err)
	}
	_ = relock.Release()

	if info, err := os.Stat(RuntimeLockPath(dbPath)); err != nil {
		t.Fatalf("stat lock file: %v", err)
	} else if perm := info.Mode().Perm(); perm != runtimeLockPerm {
		t.Errorf("lock file permissions = %04o, want %04o", perm, runtimeLockPerm)
	}
}
