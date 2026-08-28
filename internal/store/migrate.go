// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// schemaVersionKey is the reserved kv row that tracks which migrations have
// been applied to a given database file.
const schemaVersionKey = "schema_version"

// migration is one forward-only, numbered migration script.
type migration struct {
	name    string
	sql     string
	version int
}

// loadMigrations reads every embedded migrations/NNNN_*.sql file and returns
// them sorted by version ascending. The numeric prefix (e.g. "0001") is the
// migration's version number; there must be no gaps or duplicates.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("store: reading embedded migrations: %w", err)
	}

	migrations := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("store: migration file %q does not match NNNN_name.sql", entry.Name())
		}
		version, err := strconv.Atoi(prefix)
		if err != nil {
			return nil, fmt.Errorf("store: migration file %q has a non-numeric version prefix: %w", entry.Name(), err)
		}
		data, err := migrationsFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("store: reading migration %q: %w", entry.Name(), err)
		}
		migrations = append(migrations, migration{version: version, name: entry.Name(), sql: string(data)})
	}

	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })

	for i, m := range migrations {
		if i > 0 && m.version == migrations[i-1].version {
			return nil, fmt.Errorf("store: duplicate migration version %d (%q and %q)", m.version, migrations[i-1].name, m.name)
		}
	}

	return migrations, nil
}

// latestSchemaVersion returns the highest version among the embedded
// migrations, i.e. the schema version this build of vnproxd understands.
func latestSchemaVersion(migrations []migration) int {
	latest := 0
	for _, m := range migrations {
		if m.version > latest {
			latest = m.version
		}
	}
	return latest
}

// currentSchemaVersion inspects db (which may be a brand-new, empty file) and
// returns the schema version already applied, or 0 if the kv table doesn't
// exist yet (nothing has ever been applied).
func currentSchemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var kvExists int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'kv'`,
	).Scan(&kvExists)
	if err != nil {
		return 0, fmt.Errorf("store: checking for kv table: %w", err)
	}
	if kvExists == 0 {
		return 0, nil
	}

	var raw string
	err = db.QueryRowContext(ctx, `SELECT v FROM kv WHERE k = ?`, schemaVersionKey).Scan(&raw)
	switch {
	case err == sql.ErrNoRows:
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("store: reading schema_version: %w", err)
	}

	version, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("store: schema_version %q is not an integer: %w", raw, err)
	}
	return version, nil
}

// migrate brings db up to the latest embedded schema version, applying each
// pending migration in its own transaction and recording progress in the kv
// table as it goes. It refuses to proceed (ErrSchemaTooNew) if the database
// already has a schema version newer than this build understands — that
// happens when an older vnproxd binary is pointed at a database a newer
// build already migrated.
func migrate(ctx context.Context, db *sql.DB) error {
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	latest := latestSchemaVersion(migrations)

	current, err := currentSchemaVersion(ctx, db)
	if err != nil {
		return err
	}
	if current > latest {
		return fmt.Errorf("%w: database is at schema version %d, this build only understands up to %d", ErrSchemaTooNew, current, latest)
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		if err := applyMigration(ctx, db, m); err != nil {
			return fmt.Errorf("store: applying migration %s: %w", m.name, err)
		}
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, m migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("executing script: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO kv (k, v) VALUES (?, ?) ON CONFLICT (k) DO UPDATE SET v = excluded.v`,
		schemaVersionKey, strconv.Itoa(m.version),
	); err != nil {
		return fmt.Errorf("recording schema_version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing: %w", err)
	}
	return nil
}
