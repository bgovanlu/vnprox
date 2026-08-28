// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

// openTestDB opens a fresh database in a per-test temp directory and
// registers cleanup.
func openTestDB(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vnprox.db")
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return db
}

func TestOpen_CreatesAllTables(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	wantTables := []string{"sessions", "changesets", "snapshots", "audit_log", "layouts", "metric_samples", "kv"}
	for _, table := range wantTables {
		var name string
		err := db.sqlDB.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q: %v", table, err)
		}
	}
}

func TestOpen_PragmasApplied(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	var journalMode string
	if err := db.sqlDB.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want wal", journalMode)
	}

	var fk int
	if err := db.sqlDB.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}

	var busyTimeout int
	if err := db.sqlDB.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatalf("PRAGMA busy_timeout: %v", err)
	}
	if busyTimeout <= 0 {
		t.Errorf("busy_timeout = %d, want > 0", busyTimeout)
	}
}

func TestOpen_ReopenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vnprox.db")
	ctx := context.Background()

	db1, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	// Insert a row so we can prove the reopen didn't wipe/recreate data.
	if setErr := NewKVRepo(db1).Set(ctx, "hello", "world"); setErr != nil {
		t.Fatalf("Set: %v", setErr)
	}
	if closeErr := db1.Close(); closeErr != nil {
		t.Fatalf("first Close: %v", closeErr)
	}

	db2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("second Open (reopen): %v", err)
	}
	defer func() { _ = db2.Close() }()

	v, err := NewKVRepo(db2).Get(ctx, "hello")
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if v != "world" {
		t.Errorf("value after reopen = %q, want %q", v, "world")
	}

	version, err := currentSchemaVersion(ctx, db2.sqlDB)
	if err != nil {
		t.Fatalf("currentSchemaVersion: %v", err)
	}
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if want := latestSchemaVersion(migrations); version != want {
		t.Errorf("schema version after reopen = %d, want %d", version, want)
	}
}

// TestOpen_EnforcesFilePermissions is the automated check for docs/
// security.md's "Host footprint" claim ("SQLite DB and key files are
// root:root 0600"): the sqlite driver itself creates the main DB file (and
// its WAL/SHM sidecars, once something has written under WAL mode) honoring
// the process umask, which under the common 022 default yields
// group/world-readable 0644 — this test pins Open's explicit
// enforceDBFilePerms correction instead of merely assuming the claim holds
// (T-604 acceptance criterion 1: "each claim gets an automated test").
func TestOpen_EnforcesFilePermissions(t *testing.T) {
	// A permissive umask is the adversarial case docs/security.md's claim
	// actually needs to survive: prove Open corrects the mode even when the
	// ambient umask would otherwise leave the file world-readable.
	oldUmask := syscall.Umask(0o022)
	defer syscall.Umask(oldUmask)

	path := filepath.Join(t.TempDir(), "vnprox.db")
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s): %v", path, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("db file perm = %o, want 0600", perm)
	}

	// WAL mode (enabled by Open) creates -wal/-shm sidecars lazily on first
	// write; migrate() guarantees at least one write has already happened,
	// so both must exist and be corrected too.
	for _, suffix := range []string{"-wal", "-shm"} {
		sidecar := path + suffix
		sidecarInfo, statErr := os.Stat(sidecar)
		if statErr != nil {
			t.Errorf("Stat(%s): %v (expected WAL sidecar to exist)", sidecar, statErr)
			continue
		}
		if perm := sidecarInfo.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s perm = %o, want 0600", sidecar, perm)
		}
	}
}

// TestOpen_CorrectsPreExistingLoosePermissions proves the fix isn't merely
// "new files get the right mode" — reopening a database file that already
// has looser permissions (e.g. one created before this hardening landed, or
// restored from a backup with different ambient umask) is also corrected in
// place, not left alone.
func TestOpen_CorrectsPreExistingLoosePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vnprox.db")

	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if closeErr := db.Close(); closeErr != nil {
		t.Fatalf("Close: %v", closeErr)
	}

	if chmodErr := os.Chmod(path, 0o644); chmodErr != nil {
		t.Fatalf("Chmod to simulate a loosely-permissioned pre-existing file: %v", chmodErr)
	}

	db2, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = db2.Close() }()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("db file perm after reopen = %o, want 0600 (pre-existing loose permission should be corrected)", perm)
	}
}

func TestOpen_RefusesNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vnprox.db")
	ctx := context.Background()

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	future := latestSchemaVersion(migrations) + 1
	if setErr := NewKVRepo(db).Set(ctx, schemaVersionKey, strconv.Itoa(future)); setErr != nil {
		t.Fatalf("hand-writing future schema_version: %v", setErr)
	}
	if closeErr := db.Close(); closeErr != nil {
		t.Fatalf("Close: %v", closeErr)
	}

	_, err = Open(ctx, path)
	if err == nil {
		t.Fatal("Open with a future schema_version: got nil error, want ErrSchemaTooNew")
	}
	if !errors.Is(err, ErrSchemaTooNew) {
		t.Errorf("Open with a future schema_version: got %v, want ErrSchemaTooNew", err)
	}
}
