package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// AuditEntry is one row of the audit_log table (docs/data-model.md §2).
// Every mutation attempt (including denied and rolled-back) is recorded
// here; per docs/security.md "Audit", entries are append-only at the API
// layer, so this repository intentionally has no Update or Delete.
type AuditEntry struct {
	Username    string
	Action      string
	Result      string
	Target      sql.NullString
	ChangesetID sql.NullString
	DetailJSON  sql.NullString
	ID          int64
	At          int64
}

// AuditRepo is the audit_log table repository.
type AuditRepo struct {
	db *DB
}

// NewAuditRepo constructs an AuditRepo.
func NewAuditRepo(db *DB) *AuditRepo { return &AuditRepo{db: db} }

// Append inserts a new audit entry and returns its assigned id.
func (r *AuditRepo) Append(ctx context.Context, e AuditEntry) (int64, error) {
	res, err := r.db.sqlDB.ExecContext(ctx, `
		INSERT INTO audit_log (at, username, action, target, changeset_id, result, detail_json)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.At, e.Username, e.Action, e.Target, e.ChangesetID, e.Result, e.DetailJSON,
	)
	if err != nil {
		return 0, fmt.Errorf("store: appending audit entry: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: reading audit entry id: %w", err)
	}
	return id, nil
}

// Get returns the audit entry with the given id, or ErrNotFound.
func (r *AuditRepo) Get(ctx context.Context, id int64) (AuditEntry, error) {
	row := r.db.sqlDB.QueryRowContext(ctx, `
		SELECT id, at, username, action, target, changeset_id, result, detail_json
		FROM audit_log WHERE id = ?`, id,
	)
	e, err := scanAuditEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AuditEntry{}, ErrNotFound
	}
	return e, err
}

// List returns audit entries ordered by at descending (newest first),
// optionally filtered to a single changeset. Pass an empty changesetID to
// list all. limit <= 0 means "no limit".
func (r *AuditRepo) List(ctx context.Context, changesetID string, limit int) ([]AuditEntry, error) {
	query := `SELECT id, at, username, action, target, changeset_id, result, detail_json FROM audit_log`
	args := []any{}
	if changesetID != "" {
		query += ` WHERE changeset_id = ?`
		args = append(args, changesetID)
	}
	query += ` ORDER BY at DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := r.db.sqlDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: listing audit entries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []AuditEntry
	for rows.Next() {
		e, err := scanAuditEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing audit entries: %w", err)
	}
	return out, nil
}

func scanAuditEntry(row rowScanner) (AuditEntry, error) {
	var e AuditEntry
	err := row.Scan(&e.ID, &e.At, &e.Username, &e.Action, &e.Target, &e.ChangesetID, &e.Result, &e.DetailJSON)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AuditEntry{}, err
		}
		return AuditEntry{}, fmt.Errorf("store: scanning audit entry: %w", err)
	}
	return e, nil
}
