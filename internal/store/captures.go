package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// CaptureCaps mirrors internal/capture.Caps field-for-field (the effective,
// server-clamped cap set persisted as caps_json). This package keeps its own
// copy rather than importing internal/capture, the same "internal/store
// never imports the packages that use it" layering internal/store/flows.go
// already follows.
type CaptureCaps struct {
	MaxDurationSec int   `json:"maxDurationSec"`
	MaxBytes       int64 `json:"maxBytes"`
	MaxPackets     int64 `json:"maxPackets"`
	RetentionHours int   `json:"retentionHours"`
}

// CaptureSession is one row of the capture_sessions table (docs/data-model.md
// §2, T-1301) — app-owned intent + accounting only, never payload bytes.
type CaptureSession struct {
	ID        string
	GroupID   string
	TargetRef string
	Node      string
	Filter    string
	Status    string
	StartedBy string
	FilePath  string
	Nodes     []string
	Caps      CaptureCaps
	StartedAt int64
	StoppedAt int64
	FileBytes int64
	Packets   int64
}

// CaptureRepo is the capture_sessions table repository.
type CaptureRepo struct {
	db *DB
}

// NewCaptureRepo constructs a CaptureRepo.
func NewCaptureRepo(db *DB) *CaptureRepo { return &CaptureRepo{db: db} }

// Upsert inserts or replaces a capture session row by id.
func (r *CaptureRepo) Upsert(ctx context.Context, s CaptureSession) error {
	nodesJSON, err := json.Marshal(s.Nodes)
	if err != nil {
		return fmt.Errorf("store: encoding capture session nodes: %w", err)
	}
	capsJSON, err := json.Marshal(s.Caps)
	if err != nil {
		return fmt.Errorf("store: encoding capture session caps: %w", err)
	}
	_, err = r.db.sqlDB.ExecContext(ctx, `
		INSERT INTO capture_sessions
			(id, group_id, target_ref, node, nodes_json, filter, caps_json, status, started_by, started_at, stopped_at, file_path, file_bytes, packets)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			group_id=excluded.group_id, target_ref=excluded.target_ref, node=excluded.node,
			nodes_json=excluded.nodes_json, filter=excluded.filter, caps_json=excluded.caps_json,
			status=excluded.status, started_by=excluded.started_by, started_at=excluded.started_at,
			stopped_at=excluded.stopped_at, file_path=excluded.file_path, file_bytes=excluded.file_bytes,
			packets=excluded.packets`,
		s.ID, s.GroupID, s.TargetRef, s.Node, string(nodesJSON), s.Filter, string(capsJSON),
		s.Status, s.StartedBy, s.StartedAt, s.StoppedAt, s.FilePath, s.FileBytes, s.Packets,
	)
	if err != nil {
		return fmt.Errorf("store: upserting capture session %s: %w", s.ID, err)
	}
	return nil
}

const captureCols = `id, group_id, target_ref, node, nodes_json, filter, caps_json, status, started_by, started_at, stopped_at, file_path, file_bytes, packets`

// Get returns one capture session by id, or ErrNotFound.
func (r *CaptureRepo) Get(ctx context.Context, id string) (CaptureSession, error) {
	row := r.db.sqlDB.QueryRowContext(ctx, `SELECT `+captureCols+` FROM capture_sessions WHERE id = ?`, id)
	s, err := scanCaptureSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CaptureSession{}, ErrNotFound
	}
	return s, err
}

// ByGroup returns every session sharing group_id, oldest first.
func (r *CaptureRepo) ByGroup(ctx context.Context, groupID string) ([]CaptureSession, error) {
	return r.query(ctx, `SELECT `+captureCols+` FROM capture_sessions WHERE group_id = ? ORDER BY started_at ASC, id ASC`, groupID)
}

// List returns every capture session, newest first.
func (r *CaptureRepo) List(ctx context.Context) ([]CaptureSession, error) {
	return r.query(ctx, `SELECT `+captureCols+` FROM capture_sessions ORDER BY started_at DESC, id DESC`)
}

// ListGroups returns the distinct group ids, newest first.
func (r *CaptureRepo) ListGroups(ctx context.Context) ([]string, error) {
	rows, err := r.db.sqlDB.QueryContext(ctx, `SELECT group_id, MAX(started_at) AS m FROM capture_sessions GROUP BY group_id ORDER BY m DESC`)
	if err != nil {
		return nil, fmt.Errorf("store: listing capture groups: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var g string
		var m int64
		if err := rows.Scan(&g, &m); err != nil {
			return nil, fmt.Errorf("store: scanning capture group: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// Delete removes a capture session row by id (used when its file is gone and
// the row is no longer needed; the sweep itself keeps rows and marks them
// purged, so this is for explicit cleanup only).
func (r *CaptureRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.sqlDB.ExecContext(ctx, `DELETE FROM capture_sessions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: deleting capture session %s: %w", id, err)
	}
	return nil
}

func (r *CaptureRepo) query(ctx context.Context, q string, args ...any) ([]CaptureSession, error) {
	rows, err := r.db.sqlDB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: querying capture sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []CaptureSession
	for rows.Next() {
		s, err := scanCaptureSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func scanCaptureSession(row rowScanner) (CaptureSession, error) {
	var s CaptureSession
	var nodesJSON, capsJSON string
	err := row.Scan(&s.ID, &s.GroupID, &s.TargetRef, &s.Node, &nodesJSON, &s.Filter, &capsJSON,
		&s.Status, &s.StartedBy, &s.StartedAt, &s.StoppedAt, &s.FilePath, &s.FileBytes, &s.Packets)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CaptureSession{}, err
		}
		return CaptureSession{}, fmt.Errorf("store: scanning capture session: %w", err)
	}
	if nodesJSON != "" {
		if err := json.Unmarshal([]byte(nodesJSON), &s.Nodes); err != nil {
			return CaptureSession{}, fmt.Errorf("store: decoding capture session nodes: %w", err)
		}
	}
	if err := json.Unmarshal([]byte(capsJSON), &s.Caps); err != nil {
		return CaptureSession{}, fmt.Errorf("store: decoding capture session caps: %w", err)
	}
	return s, nil
}
