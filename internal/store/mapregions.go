// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// MapRegion is one row of the map_regions table (docs/data-model.md §2,
// T-2806): a labelled rectangle an operator drew on the topology canvas to
// group entities that belong together in their head but not in any PVE
// object ("vendor-managed, do not touch").
//
// X/Y/W/H are in the canvas's own graph coordinate space, so a region keeps
// its relationship to the entities it encloses under any pan/zoom. The row
// says nothing about any entity's configuration — it is app-owned map
// furniture, the same category `layouts` and `annotations` occupy.
type MapRegion struct {
	ID        string
	Label     string
	Color     string
	CreatedBy string
	X         float64
	Y         float64
	W         float64
	H         float64
	CreatedAt int64
	UpdatedAt int64
	// ExpiresAt is unix seconds, 0 = never. Judged at read time against an
	// injected clock, never here — see Annotation.ExpiresAt.
	ExpiresAt int64
}

// MapRegionRepo is the map_regions table repository.
type MapRegionRepo struct {
	db *DB
}

// NewMapRegionRepo constructs a MapRegionRepo.
func NewMapRegionRepo(db *DB) *MapRegionRepo { return &MapRegionRepo{db: db} }

const mapRegionColumns = `id, label, x, y, w, h, color, created_by, created_at, updated_at, expires_at`

// Insert creates a new region row (ID is caller-assigned, typically
// store.NewULID()).
func (r *MapRegionRepo) Insert(ctx context.Context, m MapRegion) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO map_regions (`+mapRegionColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.Label, m.X, m.Y, m.W, m.H, m.Color, m.CreatedBy, m.CreatedAt, m.UpdatedAt, m.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("store: inserting map region %s: %w", m.ID, err)
	}
	return nil
}

// Get returns one region by id, or ErrNotFound.
func (r *MapRegionRepo) Get(ctx context.Context, id string) (MapRegion, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+mapRegionColumns+` FROM map_regions WHERE id = ?`, id)
	m, err := scanMapRegion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return MapRegion{}, ErrNotFound
	}
	return m, err
}

// List returns every region, cluster/topology-wide, oldest first. Regions
// are shared team map furniture, so this is never scoped to one user, and
// expired rows are included — expiry is the caller's read-time judgement.
func (r *MapRegionRepo) List(ctx context.Context) ([]MapRegion, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+mapRegionColumns+` FROM map_regions ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: listing map regions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []MapRegion
	for rows.Next() {
		m, scanErr := scanMapRegion(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing map regions: %w", err)
	}
	return out, nil
}

// Delete removes a region by id; deleting an absent one is not an error
// (the same idempotent convention AnnotationRepo.Delete uses). This is the
// only path that removes a region row — nothing sweeps this table.
func (r *MapRegionRepo) Delete(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM map_regions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: deleting map region %s: %w", id, err)
	}
	return nil
}

func scanMapRegion(row rowScanner) (MapRegion, error) {
	var m MapRegion
	if err := row.Scan(&m.ID, &m.Label, &m.X, &m.Y, &m.W, &m.H, &m.Color,
		&m.CreatedBy, &m.CreatedAt, &m.UpdatedAt, &m.ExpiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MapRegion{}, err
		}
		return MapRegion{}, fmt.Errorf("store: scanning map region: %w", err)
	}
	return m, nil
}
