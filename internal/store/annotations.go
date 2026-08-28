// SPDX-License-Identifier: Apache-2.0

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
	// ExpiresAt (T-2806, migration 0045) is the optional self-destruct
	// instant in unix seconds; 0 means "never expires", which is what
	// every note predating that migration reads as.
	//
	// This repo NEVER filters on it: expiry is a read-time judgement the
	// caller makes against its own injected clock (internal/annotate),
	// exactly as EntityLockRepo leaves entity_locks.expires_at to
	// internal/presence. Deciding it here, in SQL, against a
	// database-side clock would put a second clock in the system.
	ExpiresAt int64
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
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO annotations (id, ref, content, created_by, created_at, updated_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Ref, a.Content, a.CreatedBy, a.CreatedAt, a.UpdatedAt, a.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("store: inserting annotation %s: %w", a.ID, err)
	}
	return nil
}

// Get returns one annotation by id, or ErrNotFound.
func (r *AnnotationRepo) Get(ctx context.Context, id string) (Annotation, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, ref, content, created_by, created_at, updated_at, expires_at FROM annotations WHERE id = ?`, id,
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
//
// Expired notes are included: see Annotation.ExpiresAt for why expiry is
// never judged here.
func (r *AnnotationRepo) List(ctx context.Context) ([]Annotation, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, ref, content, created_by, created_at, updated_at, expires_at FROM annotations ORDER BY created_at ASC, id ASC`,
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
//
// This is the ONLY path that removes an annotation row, and it is reached
// only from an explicit operator "unpin this note" action naming a single
// id. Nothing sweeps this table: no retention job, no expiry sweep, and
// nothing keyed on the annotated entity still existing. T-2806 AC2 is why
// — a note on a deleted entity may be the only surviving record of why
// that entity was removed, so it is retained and reported as orphaned
// rather than cascaded away.
func (r *AnnotationRepo) Delete(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM annotations WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: deleting annotation %s: %w", id, err)
	}
	return nil
}

func scanAnnotation(row rowScanner) (Annotation, error) {
	var a Annotation
	if err := row.Scan(&a.ID, &a.Ref, &a.Content, &a.CreatedBy, &a.CreatedAt, &a.UpdatedAt, &a.ExpiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Annotation{}, err
		}
		return Annotation{}, fmt.Errorf("store: scanning annotation: %w", err)
	}
	return a, nil
}
