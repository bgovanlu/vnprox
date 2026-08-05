package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Layout is one row of the layouts table (docs/data-model.md §2): a named,
// per-user saved UI layout (e.g. topology canvas positions).
type Layout struct {
	Username   string
	Name       string
	LayoutJSON string
	UpdatedAt  int64
}

// LayoutRepo is the layouts table repository.
type LayoutRepo struct {
	db *DB
}

// NewLayoutRepo constructs a LayoutRepo.
func NewLayoutRepo(db *DB) *LayoutRepo { return &LayoutRepo{db: db} }

// Insert creates a new layout row. It fails if (username, name) already
// exists; use Update to modify an existing layout.
func (r *LayoutRepo) Insert(ctx context.Context, l Layout) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO layouts (username, name, layout_json, updated_at) VALUES (?, ?, ?, ?)`,
		l.Username, l.Name, l.LayoutJSON, l.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("store: inserting layout %s/%s: %w", l.Username, l.Name, err)
	}
	return nil
}

// Get returns the named layout for username, or ErrNotFound.
func (r *LayoutRepo) Get(ctx context.Context, username, name string) (Layout, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT username, name, layout_json, updated_at FROM layouts WHERE username = ? AND name = ?`,
		username, name,
	)
	l, err := scanLayout(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Layout{}, ErrNotFound
	}
	return l, err
}

// List returns all layouts saved by username, ordered by name.
func (r *LayoutRepo) List(ctx context.Context, username string) ([]Layout, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT username, name, layout_json, updated_at FROM layouts WHERE username = ? ORDER BY name ASC`,
		username,
	)
	if err != nil {
		return nil, fmt.Errorf("store: listing layouts for %s: %w", username, err)
	}
	defer func() { _ = rows.Close() }()

	var out []Layout
	for rows.Next() {
		l, err := scanLayout(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing layouts for %s: %w", username, err)
	}
	return out, nil
}

// Update overwrites the layout_json/updated_at of an existing (username,
// name) layout. It returns ErrNotFound if the layout doesn't exist.
func (r *LayoutRepo) Update(ctx context.Context, l Layout) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE layouts SET layout_json = ?, updated_at = ? WHERE username = ? AND name = ?`,
		l.LayoutJSON, l.UpdatedAt, l.Username, l.Name,
	)
	if err != nil {
		return fmt.Errorf("store: updating layout %s/%s: %w", l.Username, l.Name, err)
	}
	return checkRowAffected(res, "store: updating layout %s/%s", l.Username, l.Name)
}

// Put upserts a layout: insert if absent, otherwise overwrite. This is the
// convenience most callers (e.g. the "save layout" API handler) want, since
// UI layout saves are naturally idempotent.
func (r *LayoutRepo) Put(ctx context.Context, l Layout) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO layouts (username, name, layout_json, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (username, name) DO UPDATE SET layout_json = excluded.layout_json, updated_at = excluded.updated_at`,
		l.Username, l.Name, l.LayoutJSON, l.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("store: upserting layout %s/%s: %w", l.Username, l.Name, err)
	}
	return nil
}

// Delete removes a saved layout. It is not an error to delete an
// already-absent layout.
func (r *LayoutRepo) Delete(ctx context.Context, username, name string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM layouts WHERE username = ? AND name = ?`, username, name); err != nil {
		return fmt.Errorf("store: deleting layout %s/%s: %w", username, name, err)
	}
	return nil
}

func scanLayout(row rowScanner) (Layout, error) {
	var l Layout
	if err := row.Scan(&l.Username, &l.Name, &l.LayoutJSON, &l.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Layout{}, err
		}
		return Layout{}, fmt.Errorf("store: scanning layout: %w", err)
	}
	return l, nil
}
