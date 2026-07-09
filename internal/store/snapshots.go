package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Snapshot is one row of the snapshots table (docs/data-model.md §2).
// Snapshots are immutable once written; there is no Update.
type Snapshot struct {
	ID          string
	Kind        string
	FilesJSON   string
	ChangesetID sql.NullString
	TakenAt     int64
}

// SnapshotRepo is the snapshots table repository.
type SnapshotRepo struct {
	db *DB
}

// NewSnapshotRepo constructs a SnapshotRepo.
func NewSnapshotRepo(db *DB) *SnapshotRepo { return &SnapshotRepo{db: db} }

// Insert creates a new, immutable snapshot row.
func (r *SnapshotRepo) Insert(ctx context.Context, s Snapshot) error {
	_, err := r.db.sqlDB.ExecContext(ctx, `
		INSERT INTO snapshots (id, changeset_id, taken_at, kind, files_json)
		VALUES (?, ?, ?, ?, ?)`,
		s.ID, s.ChangesetID, s.TakenAt, s.Kind, s.FilesJSON,
	)
	if err != nil {
		return fmt.Errorf("store: inserting snapshot %s: %w", s.ID, err)
	}
	return nil
}

// Get returns the snapshot with the given id, or ErrNotFound.
func (r *SnapshotRepo) Get(ctx context.Context, id string) (Snapshot, error) {
	row := r.db.sqlDB.QueryRowContext(ctx, `
		SELECT id, changeset_id, taken_at, kind, files_json FROM snapshots WHERE id = ?`, id,
	)
	s, err := scanSnapshot(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, ErrNotFound
	}
	return s, err
}

// List returns snapshots ordered by taken_at descending, optionally
// filtered to a single changeset. Pass an empty changesetID to list all.
func (r *SnapshotRepo) List(ctx context.Context, changesetID string) ([]Snapshot, error) {
	query := `SELECT id, changeset_id, taken_at, kind, files_json FROM snapshots`
	args := []any{}
	if changesetID != "" {
		query += ` WHERE changeset_id = ?`
		args = append(args, changesetID)
	}
	query += ` ORDER BY taken_at DESC`

	rows, err := r.db.sqlDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: listing snapshots: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Snapshot
	for rows.Next() {
		s, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing snapshots: %w", err)
	}
	return out, nil
}

func scanSnapshot(row rowScanner) (Snapshot, error) {
	var s Snapshot
	if err := row.Scan(&s.ID, &s.ChangesetID, &s.TakenAt, &s.Kind, &s.FilesJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Snapshot{}, err
		}
		return Snapshot{}, fmt.Errorf("store: scanning snapshot: %w", err)
	}
	return s, nil
}
