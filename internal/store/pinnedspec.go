package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// PinnedSpec is the pinned_spec table's one possible row (T-1102,
// 0012_pinned_spec.sql): the declarative cluster network spec (internal/spec,
// T-1101) an operator has pinned as the GitOps reconciler's desired state.
// App-owned data, never a shadow copy of PVE config — Content is whatever
// YAML document was pinned, byte-for-byte, exactly as internal/drift's
// spec_drift check reads it back for reconciliation.
type PinnedSpec struct {
	Content  string
	PinnedBy string
	PinnedAt int64
}

// PinnedSpecRepo is the pinned_spec table repository. There is at most one
// row (the singleton the table's CHECK constraint enforces) — Set upserts
// it, Clear removes it, Get returns ErrNotFound when nothing is pinned.
type PinnedSpecRepo struct {
	db *DB
}

// NewPinnedSpecRepo constructs a PinnedSpecRepo.
func NewPinnedSpecRepo(db *DB) *PinnedSpecRepo { return &PinnedSpecRepo{db: db} }

// Get returns the current pin, or ErrNotFound if nothing is pinned.
func (r *PinnedSpecRepo) Get(ctx context.Context) (PinnedSpec, error) {
	var ps PinnedSpec
	err := r.db.QueryRowContext(ctx,
		`SELECT content, pinned_by, pinned_at FROM pinned_spec WHERE id = 1`,
	).Scan(&ps.Content, &ps.PinnedBy, &ps.PinnedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PinnedSpec{}, ErrNotFound
	}
	if err != nil {
		return PinnedSpec{}, fmt.Errorf("store: reading pinned spec: %w", err)
	}
	return ps, nil
}

// Set upserts the singleton pin row — pin, if unset, or replace, if already
// pinned (POST /spec/pin re-pins in place rather than requiring an explicit
// unpin first).
func (r *PinnedSpecRepo) Set(ctx context.Context, ps PinnedSpec) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO pinned_spec (id, content, pinned_by, pinned_at) VALUES (1, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			content    = excluded.content,
			pinned_by  = excluded.pinned_by,
			pinned_at  = excluded.pinned_at`,
		ps.Content, ps.PinnedBy, ps.PinnedAt,
	)
	if err != nil {
		return fmt.Errorf("store: setting pinned spec: %w", err)
	}
	return nil
}

// Clear removes the pin. Not an error to clear when nothing is pinned.
func (r *PinnedSpecRepo) Clear(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM pinned_spec WHERE id = 1`); err != nil {
		return fmt.Errorf("store: clearing pinned spec: %w", err)
	}
	return nil
}
