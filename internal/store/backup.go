// SPDX-License-Identifier: Apache-2.0

// backup.go holds the three store-level primitives T-1901's backup and
// restore path needs and nothing else in the codebase had:
//
//  1. a *consistent* copy of a live SQLite database (SnapshotTo) — copying
//     vnprox.db with cp/tar while the daemon runs is not a backup, because
//     WAL mode leaves committed transactions in the -wal sidecar and a
//     naive file copy can capture a torn page set;
//  2. reading a database's schema version *without* migrating it
//     (InspectSchemaVersion) — Open always migrates, which is exactly what
//     a restore must not do until it has decided the archive is safe; and
//  3. the schema version this build understands (LatestSchemaVersion), so
//     restore can refuse a store from a newer binary (the downgrade
//     direction migrate() already refuses via ErrSchemaTooNew) *before*
//     going anywhere near the target file.
//
// All three deliberately avoid Open: a backup must still work against a
// database an older binary cannot migrate (that is precisely when you most
// want a backup), and a restore must be able to inspect an untrusted file
// without mutating it.

package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

// LatestSchemaVersion reports the highest embedded migration version, i.e.
// the schema version this build of vnprox understands. A store recorded at
// a higher version than this was written by a newer build and must not be
// restored into this one (migrate() would refuse with ErrSchemaTooNew, but
// only after the file was already in place — restore checks this first).
func LatestSchemaVersion() (int, error) {
	migrations, err := loadMigrations()
	if err != nil {
		return 0, err
	}
	return latestSchemaVersion(migrations), nil
}

// rawDSN builds the DSN used by the two functions below: the same pragmas
// Open uses minus anything that would migrate or rewrite the file.
func rawDSN(path string, extra ...string) string {
	dsn := "file:" + url.PathEscape(path) + "?_pragma=busy_timeout(5000)"
	for _, e := range extra {
		dsn += "&" + e
	}
	return dsn
}

// InspectReadOnly opens the SQLite database at path with query_only(1) —
// no migration, no write of any kind — and hands the connection to fn.
//
// It is the generalisation of InspectSchemaVersion below, extracted for
// T-1902: a support bundle has to read *derived facts* out of a store it
// must not touch (row counts, the last N changesets, finding history) and
// it may well be pointed at a store this binary is too old, or too new, to
// migrate — a failed migration being one of the exact situations a support
// bundle exists to diagnose. Open() would migrate it; this cannot.
//
// query_only(1) is the load-bearing pragma: any statement fn issues that
// would write returns an error from SQLite rather than silently mutating a
// database nobody has decided to trust yet.
func InspectReadOnly(ctx context.Context, path string, fn func(*sql.DB) error) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("store: inspecting %s: %w", path, err)
	}
	db, err := sql.Open("sqlite", rawDSN(path, "_pragma=query_only(1)"))
	if err != nil {
		return fmt.Errorf("store: opening %s for inspection: %w", path, err)
	}
	defer func() { _ = db.Close() }()

	if pingErr := db.PingContext(ctx); pingErr != nil {
		return fmt.Errorf("store: opening %s for inspection: %w", path, pingErr)
	}
	return fn(db)
}

// InspectSchemaVersion opens the SQLite database at path read-only and
// returns the schema version recorded in its kv table, WITHOUT applying any
// migration. It returns 0 for a database that has never had a migration
// applied (a brand-new/empty file), matching currentSchemaVersion.
//
// This is the check restore runs against an extracted, still-untrusted
// store copy: it must be able to say "this database is from a newer build"
// without becoming the thing that migrates it.
func InspectSchemaVersion(ctx context.Context, path string) (int, error) {
	var version int
	err := InspectReadOnly(ctx, path, func(db *sql.DB) error {
		v, err := currentSchemaVersion(ctx, db)
		if err != nil {
			// currentSchemaVersion's first statement reads sqlite_master,
			// so a file that is not a SQLite database at all lands here
			// rather than being mistaken for an empty one.
			return err
		}
		version = v
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("store: inspecting schema version of %s: %w", path, err)
	}
	return version, nil
}

// SnapshotTo writes a consistent, self-contained copy of the database at
// srcPath to destPath using SQLite's `VACUUM INTO`.
//
// Why not copy the file: the daemon runs in WAL mode, so at any instant
// some committed transactions live only in `vnprox.db-wal`. `cp vnprox.db`
// therefore captures a database missing its most recent commits, and
// copying the three files separately can capture them from different
// instants. `VACUUM INTO` runs inside a read transaction, so what lands at
// destPath is the database exactly as of one consistent point in time,
// already checkpointed (no -wal/-shm sidecars) and defragmented — a single
// file that is safe to put in an archive.
//
// It deliberately does not call Open: a backup must still be takeable from
// a database whose schema this binary is too old to migrate.
//
// destPath must not exist; SQLite refuses to overwrite (a property this
// relies on rather than checking separately).
func SnapshotTo(ctx context.Context, srcPath, destPath string) error {
	if _, err := os.Stat(srcPath); err != nil {
		return fmt.Errorf("store: snapshotting %s: %w", srcPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o700); err != nil {
		return fmt.Errorf("store: creating snapshot directory for %s: %w", destPath, err)
	}

	db, err := sql.Open("sqlite", rawDSN(srcPath))
	if err != nil {
		return fmt.Errorf("store: opening %s for snapshot: %w", srcPath, err)
	}
	defer func() { _ = db.Close() }()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("store: opening %s for snapshot: %w", srcPath, err)
	}

	// VACUUM INTO's filename is an ordinary SQL expression, so it binds a
	// parameter — no string interpolation of a path into SQL anywhere.
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, destPath); err != nil {
		return fmt.Errorf("store: snapshotting %s to %s: %w", srcPath, destPath, err)
	}
	// The snapshot inherits whatever mode the sqlite driver created it
	// with (umask-dependent); a store copy is as sensitive as the store.
	if err := os.Chmod(destPath, dbFilePerm); err != nil {
		return fmt.Errorf("store: setting permissions on %s: %w", destPath, err)
	}
	return nil
}
