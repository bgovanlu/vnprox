// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// KVRepo is the kv table repository: a small generic key/value store used
// for schema bookkeeping (the reserved "schema_version" key managed by
// migrate.go) and available to other packages for similar small persistent
// settings.
type KVRepo struct {
	db *DB
}

// NewKVRepo constructs a KVRepo.
func NewKVRepo(db *DB) *KVRepo { return &KVRepo{db: db} }

// Insert creates a new key. It fails if the key already exists; use Set to
// upsert.
func (r *KVRepo) Insert(ctx context.Context, k, v string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO kv (k, v) VALUES (?, ?)`, k, v)
	if err != nil {
		return fmt.Errorf("store: inserting kv key %q: %w", k, err)
	}
	return nil
}

// Get returns the value for k, or ErrNotFound.
func (r *KVRepo) Get(ctx context.Context, k string) (string, error) {
	var v string
	err := r.db.QueryRowContext(ctx, `SELECT v FROM kv WHERE k = ?`, k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("store: reading kv key %q: %w", k, err)
	}
	return v, nil
}

// List returns every key/value pair, ordered by key.
func (r *KVRepo) List(ctx context.Context) (map[string]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT k, v FROM kv ORDER BY k ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: listing kv: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("store: scanning kv row: %w", err)
		}
		out[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing kv: %w", err)
	}
	return out, nil
}

// Update overwrites the value of an existing key. It returns ErrNotFound if
// the key doesn't exist.
func (r *KVRepo) Update(ctx context.Context, k, v string) error {
	res, err := r.db.ExecContext(ctx, `UPDATE kv SET v = ? WHERE k = ?`, v, k)
	if err != nil {
		return fmt.Errorf("store: updating kv key %q: %w", k, err)
	}
	return checkRowAffected(res, "store: updating kv key %q", k)
}

// Set upserts k to v: insert if absent, otherwise overwrite.
func (r *KVRepo) Set(ctx context.Context, k, v string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO kv (k, v) VALUES (?, ?) ON CONFLICT (k) DO UPDATE SET v = excluded.v`, k, v)
	if err != nil {
		return fmt.Errorf("store: setting kv key %q: %w", k, err)
	}
	return nil
}

// Delete removes a key. It is not an error to delete an already-absent key.
func (r *KVRepo) Delete(ctx context.Context, k string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM kv WHERE k = ?`, k); err != nil {
		return fmt.Errorf("store: deleting kv key %q: %w", k, err)
	}
	return nil
}
