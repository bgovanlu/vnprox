package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Blueprint is one row of the blueprints table (T-603, docs/features/
// blueprints.md §1): a saved, user-authored (or captured, or
// starter-copied-to-edit) parameterized topology template. BlueprintJSON
// is the full internal/blueprint.Blueprint value, serialized — this
// package deliberately does not import internal/blueprint (it would
// invert that package's own store dependency direction, the same
// concrete-repo-type convention internal/change's Config already uses for
// *store.ChangesetRepo/*store.SnapshotRepo); callers decode/encode it.
type Blueprint struct {
	ID            string
	Name          string
	BlueprintJSON string
	CreatedBy     string
	CreatedAt     int64
	UpdatedAt     int64
}

// BlueprintRepo is the blueprints table repository.
type BlueprintRepo struct {
	db *DB
}

// NewBlueprintRepo constructs a BlueprintRepo.
func NewBlueprintRepo(db *DB) *BlueprintRepo { return &BlueprintRepo{db: db} }

// List returns every saved blueprint, ordered by name then id for a
// stable listing.
func (r *BlueprintRepo) List(ctx context.Context) ([]Blueprint, error) {
	rows, err := r.db.sqlDB.QueryContext(ctx, `
		SELECT id, name, blueprint_json, created_by, created_at, updated_at
		FROM blueprints ORDER BY name ASC, id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("store: listing blueprints: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Blueprint
	for rows.Next() {
		b, err := scanBlueprint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing blueprints: %w", err)
	}
	return out, nil
}

// Get returns one saved blueprint by id, or ErrNotFound.
func (r *BlueprintRepo) Get(ctx context.Context, id string) (Blueprint, error) {
	row := r.db.sqlDB.QueryRowContext(ctx, `
		SELECT id, name, blueprint_json, created_by, created_at, updated_at
		FROM blueprints WHERE id = ?`, id,
	)
	b, err := scanBlueprint(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Blueprint{}, ErrNotFound
	}
	return b, err
}

// Put upserts a blueprint (insert if absent, overwrite if present) — the
// save path is idempotent by id, mirroring LayoutRepo.Put's convention.
func (r *BlueprintRepo) Put(ctx context.Context, b Blueprint) error {
	_, err := r.db.sqlDB.ExecContext(ctx, `
		INSERT INTO blueprints (id, name, blueprint_json, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			name = excluded.name, blueprint_json = excluded.blueprint_json, updated_at = excluded.updated_at`,
		b.ID, b.Name, b.BlueprintJSON, b.CreatedBy, b.CreatedAt, b.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("store: upserting blueprint %s: %w", b.ID, err)
	}
	return nil
}

// Delete removes a saved blueprint. It is not an error to delete an
// already-absent one.
func (r *BlueprintRepo) Delete(ctx context.Context, id string) error {
	if _, err := r.db.sqlDB.ExecContext(ctx, `DELETE FROM blueprints WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: deleting blueprint %s: %w", id, err)
	}
	return nil
}

func scanBlueprint(row rowScanner) (Blueprint, error) {
	var b Blueprint
	if err := row.Scan(&b.ID, &b.Name, &b.BlueprintJSON, &b.CreatedBy, &b.CreatedAt, &b.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Blueprint{}, err
		}
		return Blueprint{}, fmt.Errorf("store: scanning blueprint: %w", err)
	}
	return b, nil
}
