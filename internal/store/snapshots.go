package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Snapshot is one row of the snapshots table (docs/data-model.md §2).
// Snapshots are immutable once written; there is no Update. FilesJSON holds
// `[{node,path,sha256}]` — the file *content* lives in the content-addressed
// `blobs` table (0002_snapshot_blobs.sql), keyed by each file's sha256, so
// identical content across snapshots is stored once (see BlobRepo).
type Snapshot struct {
	ID          string
	Kind        string
	FilesJSON   string
	ChangesetID sql.NullString
	Note        sql.NullString
	TakenAt     int64
}

// SnapshotFileRef is one row of the snapshot_files join table: the node/path
// this snapshot captured and the blob it resolves to.
type SnapshotFileRef struct {
	SnapshotID string
	Node       string
	Path       string
	SHA256     string
}

// SnapshotRepo is the snapshots table repository.
type SnapshotRepo struct {
	db *DB
}

// NewSnapshotRepo constructs a SnapshotRepo.
func NewSnapshotRepo(db *DB) *SnapshotRepo { return &SnapshotRepo{db: db} }

// Insert creates a new, immutable snapshot row.
func (r *SnapshotRepo) Insert(ctx context.Context, s Snapshot) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO snapshots (id, changeset_id, taken_at, kind, files_json, note)
		VALUES (?, ?, ?, ?, ?, ?)`,
		s.ID, s.ChangesetID, s.TakenAt, s.Kind, s.FilesJSON, s.Note,
	)
	if err != nil {
		return fmt.Errorf("store: inserting snapshot %s: %w", s.ID, err)
	}
	return nil
}

// InsertFiles records the (snapshot,node,path)->blob references for a
// snapshot already inserted via Insert, so retention pruning can find
// which blobs are still referenced without parsing files_json.
func (r *SnapshotRepo) InsertFiles(ctx context.Context, refs []SnapshotFileRef) error {
	if len(refs) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: beginning snapshot_files insert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO snapshot_files (snapshot_id, node, path, sha256) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("store: preparing snapshot_files insert: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, f := range refs {
		if _, err := stmt.ExecContext(ctx, f.SnapshotID, f.Node, f.Path, f.SHA256); err != nil {
			return fmt.Errorf("store: inserting snapshot_files row for snapshot %s: %w", f.SnapshotID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: committing snapshot_files insert: %w", err)
	}
	return nil
}

// Get returns the snapshot with the given id, or ErrNotFound.
func (r *SnapshotRepo) Get(ctx context.Context, id string) (Snapshot, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, changeset_id, taken_at, kind, files_json, note FROM snapshots WHERE id = ?`, id,
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
	query := `SELECT id, changeset_id, taken_at, kind, files_json, note FROM snapshots`
	args := []any{}
	if changesetID != "" {
		query += ` WHERE changeset_id = ?`
		args = append(args, changesetID)
	}
	query += ` ORDER BY taken_at DESC`

	rows, err := r.db.QueryContext(ctx, query, args...)
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

// ListPage returns one page of snapshots ordered newest-first, per
// docs/api.md's `?limit=&cursor=` pagination convention (audit/snapshots
// list endpoints). cursor is opaque to the caller: an empty string starts
// from the newest snapshot; the returned nextCursor (empty when there is no
// further page) is passed back verbatim to fetch the next page. Cursor
// encoding is a "<takenAt>:<id>" keyset token (stable under concurrent
// inserts, unlike an offset), where id breaks ties between snapshots taken
// in the same second.
func (r *SnapshotRepo) ListPage(ctx context.Context, cursor string, limit int) ([]Snapshot, string, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT id, changeset_id, taken_at, kind, files_json, note FROM snapshots`
	args := []any{}
	if cursor != "" {
		takenAt, id, err := decodeSnapshotCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		query += ` WHERE (taken_at < ?) OR (taken_at = ? AND id < ?)`
		args = append(args, takenAt, takenAt, id)
	}
	query += ` ORDER BY taken_at DESC, id DESC LIMIT ?`
	args = append(args, limit+1)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("store: listing snapshots page: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Snapshot
	for rows.Next() {
		s, err := scanSnapshot(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("store: listing snapshots page: %w", err)
	}

	next := ""
	if len(out) > limit {
		last := out[limit-1]
		next = encodeSnapshotCursor(last.TakenAt, last.ID)
		out = out[:limit]
	}
	return out, next, nil
}

func encodeSnapshotCursor(takenAt int64, id string) string {
	return strconv.FormatInt(takenAt, 10) + ":" + id
}

func decodeSnapshotCursor(cursor string) (int64, string, error) {
	takenAtStr, id, ok := strings.Cut(cursor, ":")
	if !ok || id == "" {
		return 0, "", fmt.Errorf("store: malformed snapshot cursor %q", cursor)
	}
	takenAt, err := strconv.ParseInt(takenAtStr, 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("store: malformed snapshot cursor %q: %w", cursor, err)
	}
	return takenAt, id, nil
}

// Prune deletes every snapshot row (and its snapshot_files) taken before
// cutoff, except a snapshot linked to a `committed` changeset taken on or
// after pinCutoff (docs/features/change-management.md §4's 7-day manual-
// rollback window: "committed-changeset snapshots pinned 7d minimum" per
// T-206's card, so a shorter configured retention can never delete a
// committed changeset's restore point before that window closes). It
// returns the number of snapshot rows deleted; callers should follow up
// with BlobRepo.PruneOrphans to reclaim the now-unreferenced blob storage.
func (r *SnapshotRepo) Prune(ctx context.Context, cutoff, pinCutoff int64) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: beginning snapshot prune: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
		SELECT s.id FROM snapshots s
		WHERE s.taken_at < ?
		AND NOT EXISTS (
			SELECT 1 FROM changesets c
			WHERE c.id = s.changeset_id AND c.status = 'committed' AND s.taken_at >= ?
		)`, cutoff, pinCutoff)
	if err != nil {
		return 0, fmt.Errorf("store: selecting expired snapshots: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("store: scanning expired snapshot id: %w", scanErr)
		}
		ids = append(ids, id)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		_ = rows.Close()
		return 0, fmt.Errorf("store: selecting expired snapshots: %w", rowsErr)
	}
	_ = rows.Close()

	if len(ids) == 0 {
		return 0, nil
	}

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	inClause := "(" + strings.Join(placeholders, ",") + ")"

	if _, delErr := tx.ExecContext(ctx, `DELETE FROM snapshot_files WHERE snapshot_id IN `+inClause, args...); delErr != nil {
		return 0, fmt.Errorf("store: deleting snapshot_files for pruned snapshots: %w", delErr)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM snapshots WHERE id IN `+inClause, args...)
	if err != nil {
		return 0, fmt.Errorf("store: deleting pruned snapshots: %w", err)
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return 0, fmt.Errorf("store: committing snapshot prune: %w", commitErr)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: reading snapshot prune count: %w", err)
	}
	return n, nil
}

func scanSnapshot(row rowScanner) (Snapshot, error) {
	var s Snapshot
	if err := row.Scan(&s.ID, &s.ChangesetID, &s.TakenAt, &s.Kind, &s.FilesJSON, &s.Note); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Snapshot{}, err
		}
		return Snapshot{}, fmt.Errorf("store: scanning snapshot: %w", err)
	}
	return s, nil
}
