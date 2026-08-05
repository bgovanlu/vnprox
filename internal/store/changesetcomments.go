package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ChangesetComment is one row of the changeset_comments table (T-2003,
// docs/data-model.md §2). OpID is ” for a changeset-level comment, or the
// commented op's own stable Op.ID (internal/change/op.go) otherwise.
type ChangesetComment struct {
	ID          string
	ChangesetID string
	OpID        string
	Author      string
	Body        string
	CreatedAt   int64
}

// ChangesetCommentRepo is the changeset_comments table repository.
type ChangesetCommentRepo struct {
	db *DB
}

// NewChangesetCommentRepo constructs a ChangesetCommentRepo.
func NewChangesetCommentRepo(db *DB) *ChangesetCommentRepo { return &ChangesetCommentRepo{db: db} }

// Insert creates a new comment row (ID is caller-assigned, typically
// store.NewULID()).
func (r *ChangesetCommentRepo) Insert(ctx context.Context, c ChangesetComment) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO changeset_comments (id, changeset_id, op_id, author, body, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		c.ID, c.ChangesetID, c.OpID, c.Author, c.Body, c.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("store: inserting comment %s on changeset %s: %w", c.ID, c.ChangesetID, err)
	}
	return nil
}

// Get returns one comment by id, or ErrNotFound.
func (r *ChangesetCommentRepo) Get(ctx context.Context, id string) (ChangesetComment, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, changeset_id, op_id, author, body, created_at FROM changeset_comments WHERE id = ?`, id,
	)
	c, err := scanChangesetComment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ChangesetComment{}, ErrNotFound
	}
	return c, err
}

// ListForChangeset returns every comment on changesetID, oldest first (a
// review thread reads top-to-bottom in the order it was written).
func (r *ChangesetCommentRepo) ListForChangeset(ctx context.Context, changesetID string) ([]ChangesetComment, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, changeset_id, op_id, author, body, created_at FROM changeset_comments
		WHERE changeset_id = ? ORDER BY created_at ASC, id ASC`, changesetID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: listing comments for changeset %s: %w", changesetID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []ChangesetComment
	for rows.Next() {
		c, err := scanChangesetComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing comments for changeset %s: %w", changesetID, err)
	}
	return out, nil
}

// Delete removes a comment by id. Not an error to delete an already-absent
// one (mirrors AnnotationRepo.Delete's convention).
func (r *ChangesetCommentRepo) Delete(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM changeset_comments WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: deleting comment %s: %w", id, err)
	}
	return nil
}

// DeleteForOps removes every comment attached to one of opIDs within
// changesetID — the op-removal cleanup path (internal/change.Service.
// UpdateDraft): T-2003's card requires that deleting an op never silently
// orphans its comment, so a comment whose op no longer exists in a freshly
// PUT ops array is explicitly removed here (and the caller audits the
// count), rather than left referencing an id nothing will ever match again.
// Returns the number of rows removed.
func (r *ChangesetCommentRepo) DeleteForOps(ctx context.Context, changesetID string, opIDs []string) (int64, error) {
	if len(opIDs) == 0 {
		return 0, nil
	}
	placeholders := make([]string, len(opIDs))
	args := make([]any, 0, len(opIDs)+1)
	args = append(args, changesetID)
	for i, id := range opIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	query := fmt.Sprintf(`DELETE FROM changeset_comments WHERE changeset_id = ? AND op_id IN (%s)`, strings.Join(placeholders, ","))
	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("store: deleting orphaned comments for changeset %s: %w", changesetID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: deleting orphaned comments for changeset %s: %w", changesetID, err)
	}
	return n, nil
}

func scanChangesetComment(row rowScanner) (ChangesetComment, error) {
	var c ChangesetComment
	err := row.Scan(&c.ID, &c.ChangesetID, &c.OpID, &c.Author, &c.Body, &c.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ChangesetComment{}, err
		}
		return ChangesetComment{}, fmt.Errorf("store: scanning comment: %w", err)
	}
	return c, nil
}
