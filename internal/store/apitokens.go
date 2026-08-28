// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// APIToken is one row of the api_tokens table (T-1104, docs/data-model.md
// new Tokens section): a vnprox-local, capability-scoped delegated
// credential a logged-in user mints (docs/security.md's Authentication
// section — explicitly not a second login/authentication path). TokenHash
// is the hex-encoded SHA-256 of the raw bearer token; the raw value itself
// is never persisted anywhere (POST /tokens reveals it exactly once, at
// creation). ScopesJSON is a JSON array of internal/auth.Cap strings.
type APIToken struct {
	ID         string
	Name       string
	TokenHash  string
	ScopesJSON string
	CreatedBy  string
	CreatedAt  int64
	LastUsedAt sql.NullInt64
	RevokedAt  sql.NullInt64
	// ExpiresAt (T-2903, 0048) is the unix second past which the bearer
	// path refuses this token like a revoked one. Invalid/NULL = no expiry
	// (every pre-0048 row, and explicitly non-expiring mints).
	ExpiresAt sql.NullInt64
}

// APITokenRepo is the api_tokens table repository.
type APITokenRepo struct {
	db *DB
}

// NewAPITokenRepo constructs an APITokenRepo.
func NewAPITokenRepo(db *DB) *APITokenRepo { return &APITokenRepo{db: db} }

// Create inserts a new token row.
func (r *APITokenRepo) Create(ctx context.Context, t APIToken) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO api_tokens (id, name, token_hash, scopes_json, created_by, created_at, last_used_at, revoked_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name, t.TokenHash, t.ScopesJSON, t.CreatedBy, t.CreatedAt, t.LastUsedAt, t.RevokedAt, t.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("store: creating api token %s: %w", t.ID, err)
	}
	return nil
}

// Get returns the token with the given id, or ErrNotFound.
func (r *APITokenRepo) Get(ctx context.Context, id string) (APIToken, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, name, token_hash, scopes_json, created_by, created_at, last_used_at, revoked_at, expires_at
		FROM api_tokens WHERE id = ?`, id,
	)
	t, err := scanAPIToken(row)
	if errors.Is(err, sql.ErrNoRows) {
		return APIToken{}, ErrNotFound
	}
	return t, err
}

// GetByHash returns the token whose token_hash matches hash, or
// ErrNotFound — the bearer-auth middleware's lookup path.
func (r *APITokenRepo) GetByHash(ctx context.Context, hash string) (APIToken, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, name, token_hash, scopes_json, created_by, created_at, last_used_at, revoked_at, expires_at
		FROM api_tokens WHERE token_hash = ?`, hash,
	)
	t, err := scanAPIToken(row)
	if errors.Is(err, sql.ErrNoRows) {
		return APIToken{}, ErrNotFound
	}
	return t, err
}

// List returns every token (including revoked ones — GET /tokens shows
// revocation status rather than hiding history), newest-first.
func (r *APITokenRepo) List(ctx context.Context) ([]APIToken, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, token_hash, scopes_json, created_by, created_at, last_used_at, revoked_at, expires_at
		FROM api_tokens ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("store: listing api tokens: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []APIToken
	for rows.Next() {
		t, err := scanAPIToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing api tokens: %w", err)
	}
	return out, nil
}

// Revoke sets revoked_at for id, if not already revoked. Returns
// ErrNotFound if no such token exists.
func (r *APITokenRepo) Revoke(ctx context.Context, id string, now int64) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, now, id)
	if err != nil {
		return fmt.Errorf("store: revoking api token %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: revoking api token %s: %w", id, err)
	}
	if n == 0 {
		// Either the id doesn't exist, or it was already revoked; tell them
		// apart so the handler can 404 a genuinely-unknown id while treating
		// a double-revoke as a no-op success.
		if _, err := r.Get(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// UpdateLastUsed stamps last_used_at for id. Best-effort from the caller's
// perspective (internal/auth's bearer middleware logs, never fails the
// request, if this errors) — see that package's doc comment.
func (r *APITokenRepo) UpdateLastUsed(ctx context.Context, id string, now int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, now, id)
	if err != nil {
		return fmt.Errorf("store: updating api token %s last_used_at: %w", id, err)
	}
	return nil
}

// Upsert inserts t, or fully overwrites the existing row with the same id —
// the id-preserving write T-1704's HA replication uses to mirror the active's
// api_tokens onto the standby verbatim (including revoked_at, so a revocation
// on the active propagates on the next replication pass). Tokens persist only
// a hash, never a plaintext secret, so no cipher is involved in replicating
// them.
func (r *APITokenRepo) Upsert(ctx context.Context, t APIToken) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO api_tokens (id, name, token_hash, scopes_json, created_by, created_at, last_used_at, revoked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			name         = excluded.name,
			token_hash   = excluded.token_hash,
			scopes_json  = excluded.scopes_json,
			created_by   = excluded.created_by,
			created_at   = excluded.created_at,
			last_used_at = excluded.last_used_at,
			revoked_at   = excluded.revoked_at`,
		t.ID, t.Name, t.TokenHash, t.ScopesJSON, t.CreatedBy, t.CreatedAt, t.LastUsedAt, t.RevokedAt,
	)
	if err != nil {
		return fmt.Errorf("store: upserting api token %s: %w", t.ID, err)
	}
	return nil
}

func scanAPIToken(row rowScanner) (APIToken, error) {
	var t APIToken
	err := row.Scan(&t.ID, &t.Name, &t.TokenHash, &t.ScopesJSON, &t.CreatedBy, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt, &t.ExpiresAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return APIToken{}, err
		}
		return APIToken{}, fmt.Errorf("store: scanning api token: %w", err)
	}
	return t, nil
}
