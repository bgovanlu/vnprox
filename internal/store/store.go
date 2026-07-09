package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// DB is an opened, migrated app store database.
type DB struct {
	sqlDB *sql.DB
}

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

	return &DB{sqlDB: sqlDB}, nil
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
