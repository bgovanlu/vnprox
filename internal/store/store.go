// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"sync"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// DB is an opened, migrated app store database.
type DB struct {
	sqlDB *sql.DB
	obs   QueryObserver
	path  string
	obsMu sync.RWMutex
}

// dbFilePerm is the file mode enforced on the SQLite database file and its
// WAL/SHM sidecars, per docs/security.md "Host footprint": "SQLite DB and
// key files are root:root 0600". The sqlite driver itself creates these
// files honoring the process umask (typically 0644 under the common
// 022 umask) — group/world-readable, which would let any other local user
// read session/audit/changeset data straight off disk. Open enforces the
// documented mode explicitly rather than relying on umask (T-604 hardening
// pass; see store_test.go's TestOpen_EnforcesFilePermissions).
const dbFilePerm = 0o600

// Open opens (creating if necessary) the SQLite database at path, enables
// WAL journaling, foreign key enforcement, and a busy_timeout so concurrent
// writers retry inside SQLite instead of returning SQLITE_BUSY to callers,
// and applies any pending embedded migrations.
//
// Open returns ErrSchemaTooNew if the database's stored schema version is
// newer than this build's embedded migrations understand.
func Open(ctx context.Context, path string) (*DB, error) {
	dsn := "file:" + url.PathEscape(path) +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=synchronous(NORMAL)"

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: opening database %s: %w", path, err)
	}

	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("store: connecting to database %s: %w", path, err)
	}

	if err := migrate(ctx, sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}

	if err := enforceDBFilePerms(path); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}

	return &DB{sqlDB: sqlDB, path: path}, nil
}

// enforceDBFilePerms chmods the main database file and its WAL/SHM sidecars
// (if present — WAL mode creates them lazily on first write, which migrate
// above guarantees has happened at least once) to dbFilePerm. Called after
// every Open so a pre-existing file with looser permissions (e.g. one
// created before this hardening landed) is also corrected in place, not
// just newly-created ones.
func enforceDBFilePerms(path string) error {
	if err := os.Chmod(path, dbFilePerm); err != nil {
		return fmt.Errorf("store: setting permissions on %s: %w", path, err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		sidecar := path + suffix
		if _, statErr := os.Stat(sidecar); statErr != nil {
			continue // not present yet; nothing to fix.
		}
		if err := os.Chmod(sidecar, dbFilePerm); err != nil {
			return fmt.Errorf("store: setting permissions on %s: %w", sidecar, err)
		}
	}
	return nil
}

// Close closes the underlying database connection pool.
func (d *DB) Close() error {
	if err := d.sqlDB.Close(); err != nil {
		return fmt.Errorf("store: closing database: %w", err)
	}
	return nil
}

// Conn exposes the underlying *sql.DB for callers (e.g. tests) that need
// direct access; repositories should generally be constructed instead.
func (d *DB) Conn() *sql.DB { return d.sqlDB }
