package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Changeset is one row of the changesets table (docs/data-model.md §2). Its
// id is a ULID (see NewULID) so changesets sort lexicographically by
// creation order.
type Changeset struct {
	ID              string
	Title           string
	Author          string
	Status          string // draft|validated|applying|awaiting_confirm|committed|rolled_back|failed|discarded
	ClusterID       string // T-1201: the cluster this changeset is scoped to; '' = implicit default/local cluster
	OpsJSON         string
	FindingsJSON    sql.NullString
	PlanJSON        sql.NullString
	ApplyLogJSON    sql.NullString
	ConfirmDeadline sql.NullInt64
	CreatedAt       int64
	UpdatedAt       int64
}

// ChangesetRepo is the changesets table repository.
type ChangesetRepo struct {
	db *DB
}

// NewChangesetRepo constructs a ChangesetRepo.
func NewChangesetRepo(db *DB) *ChangesetRepo { return &ChangesetRepo{db: db} }

// Insert creates a new changeset row, typically in "draft" status.
func (r *ChangesetRepo) Insert(ctx context.Context, c Changeset) error {
	_, err := r.db.sqlDB.ExecContext(ctx, `
		INSERT INTO changesets (id, title, author, status, cluster_id, ops_json, findings_json, plan_json, apply_log_json, confirm_deadline, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.Title, c.Author, c.Status, c.ClusterID, c.OpsJSON, c.FindingsJSON, c.PlanJSON, c.ApplyLogJSON, c.ConfirmDeadline, c.CreatedAt, c.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("store: inserting changeset %s: %w", c.ID, err)
	}
	return nil
}

// Get returns the changeset with the given id, or ErrNotFound.
func (r *ChangesetRepo) Get(ctx context.Context, id string) (Changeset, error) {
	row := r.db.sqlDB.QueryRowContext(ctx, `
		SELECT id, title, author, status, cluster_id, ops_json, findings_json, plan_json, apply_log_json, confirm_deadline, created_at, updated_at
		FROM changesets WHERE id = ?`, id,
	)
	c, err := scanChangeset(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Changeset{}, ErrNotFound
	}
	return c, err
}

// List returns changesets ordered by created_at descending (newest first),
// optionally filtered to a single status. Pass an empty status to list all.
func (r *ChangesetRepo) List(ctx context.Context, status string) ([]Changeset, error) {
	query := `SELECT id, title, author, status, cluster_id, ops_json, findings_json, plan_json, apply_log_json, confirm_deadline, created_at, updated_at
		FROM changesets`
	args := []any{}
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC`

	rows, err := r.db.sqlDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: listing changesets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Changeset
	for rows.Next() {
		c, err := scanChangeset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing changesets: %w", err)
	}
	return out, nil
}

// Update rewrites the mutable fields of an existing changeset (status, ops,
// findings, plan, apply log, confirm deadline, updated_at) as it moves
// through stage -> validate -> diff -> apply -> confirm/rollback. It returns
// ErrNotFound if the changeset doesn't exist.
func (r *ChangesetRepo) Update(ctx context.Context, c Changeset) error {
	res, err := r.db.sqlDB.ExecContext(ctx, `
		UPDATE changesets
		SET title = ?, status = ?, ops_json = ?, findings_json = ?, plan_json = ?, apply_log_json = ?, confirm_deadline = ?, updated_at = ?
		WHERE id = ?`,
		c.Title, c.Status, c.OpsJSON, c.FindingsJSON, c.PlanJSON, c.ApplyLogJSON, c.ConfirmDeadline, c.UpdatedAt, c.ID,
	)
	if err != nil {
		return fmt.Errorf("store: updating changeset %s: %w", c.ID, err)
	}
	return checkRowAffected(res, "store: updating changeset %s", c.ID)
}

func scanChangeset(row rowScanner) (Changeset, error) {
	var c Changeset
	err := row.Scan(&c.ID, &c.Title, &c.Author, &c.Status, &c.ClusterID, &c.OpsJSON, &c.FindingsJSON, &c.PlanJSON, &c.ApplyLogJSON, &c.ConfirmDeadline, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Changeset{}, err
		}
		return Changeset{}, fmt.Errorf("store: scanning changeset: %w", err)
	}
	return c, nil
}
