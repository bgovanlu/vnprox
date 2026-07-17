package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Annotation is one row of the annotations table (docs/data-model.md §2,
// T-907): a free-text sticky note pinned to a map entity's Ref. Shared
// across every user (not keyed by username — see the migration's doc
// comment); CreatedBy records the author for display only.
type Annotation struct {
	ID        string
	Ref       string
	Content   string
	CreatedBy string
	CreatedAt int64
	UpdatedAt int64
}

// AnnotationRepo is the annotations table repository.
type AnnotationRepo struct {
	db *DB
}

// NewAnnotationRepo constructs an AnnotationRepo.
func NewAnnotationRepo(db *DB) *AnnotationRepo { return &AnnotationRepo{db: db} }

// Insert creates a new annotation row (ID is caller-assigned, typically
// store.NewULID()).
func (r *AnnotationRepo) Insert(ctx context.Context, a Annotation) error {
	_, err := r.db.sqlDB.ExecContext(ctx, `
		INSERT INTO annotations (id, ref, content, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		a.ID, a.Ref, a.Content, a.CreatedBy, a.CreatedAt, a.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("store: inserting annotation %s: %w", a.ID, err)
	}
	return nil
}

// Get returns one annotation by id, or ErrNotFound.
func (r *AnnotationRepo) Get(ctx context.Context, id string) (Annotation, error) {
	row := r.db.sqlDB.QueryRowContext(ctx, `
		SELECT id, ref, content, created_by, created_at, updated_at FROM annotations WHERE id = ?`, id,
	)
	a, err := scanAnnotation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Annotation{}, ErrNotFound
	}
	return a, err
}

// List returns every annotation, cluster/topology-wide, ordered by
// created_at ascending (oldest note on an entity first). Annotations are
// a shared team scratchpad (docs/data-model.md §2), so this is never
// scoped to one user.
func (r *AnnotationRepo) List(ctx context.Context) ([]Annotation, error) {
	rows, err := r.db.sqlDB.QueryContext(ctx, `
		SELECT id, ref, content, created_by, created_at, updated_at FROM annotations ORDER BY created_at ASC, id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("store: listing annotations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Annotation
	for rows.Next() {
		a, err := scanAnnotation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing annotations: %w", err)
	}
	return out, nil
}

// Delete removes an annotation by id. It is not an error to delete an
// already-absent one (mirrors LayoutRepo.Delete/BlueprintRepo.Delete's
// convention).
func (r *AnnotationRepo) Delete(ctx context.Context, id string) error {
	if _, err := r.db.sqlDB.ExecContext(ctx, `DELETE FROM annotations WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: deleting annotation %s: %w", id, err)
	}
	return nil
}

func scanAnnotation(row rowScanner) (Annotation, error) {
	var a Annotation
	if err := row.Scan(&a.ID, &a.Ref, &a.Content, &a.CreatedBy, &a.CreatedAt, &a.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Annotation{}, err
		}
		return Annotation{}, fmt.Errorf("store: scanning annotation: %w", err)
	}
	return a, nil
}
