package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// GuestInteriorToggleRepo is the guest_interior_toggles table repository
// (T-1304): one row per guest that has ever had its interior-inspector
// opt-in flipped, keyed by the guest's Ref string.
type GuestInteriorToggleRepo struct {
	db *DB
}

// NewGuestInteriorToggleRepo constructs a GuestInteriorToggleRepo.
func NewGuestInteriorToggleRepo(db *DB) *GuestInteriorToggleRepo {
	return &GuestInteriorToggleRepo{db: db}
}

// Get returns whether ref has opted in to the interior inspector. A guest
// with no row at all (never toggled) is false — off by default, matching
// docs/api.md's Guest interior section, not an error.
func (r *GuestInteriorToggleRepo) Get(ctx context.Context, ref string) (bool, error) {
	row := r.db.sqlDB.QueryRowContext(ctx, `SELECT enabled FROM guest_interior_toggles WHERE ref = ?`, ref)
	var enabled int
	err := row.Scan(&enabled)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("store: getting guest interior toggle for %s: %w", ref, err)
	}
	return enabled != 0, nil
}

// Set upserts ref's opt-in state.
func (r *GuestInteriorToggleRepo) Set(ctx context.Context, ref string, enabled bool, updatedBy string, at int64) error {
	e := 0
	if enabled {
		e = 1
	}
	_, err := r.db.sqlDB.ExecContext(ctx, `
		INSERT INTO guest_interior_toggles (ref, enabled, updated_by, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (ref) DO UPDATE SET enabled = excluded.enabled, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
		ref, e, updatedBy, at,
	)
	if err != nil {
		return fmt.Errorf("store: setting guest interior toggle for %s: %w", ref, err)
	}
	return nil
}
