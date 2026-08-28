// SPDX-License-Identifier: Apache-2.0

// tcmirror.go implements T-4014's tc_mirror_sessions storage
// (docs/data-model.md §2, migration 0053_tc_mirror_sessions.sql). App-owned
// intent + accounting only per CLAUDE.md's storage rule — the live tc/
// clsact/mirred state on the node stays authoritative and is never
// shadow-copied here; internal/tcmirror.RenderTC/RenderTCTeardown
// re-derive the on-node invocation from a row's own fields every time it
// is (re)applied or torn down. Every row is written only by the change
// engine's tc.mirror.* apply/rollback executor (cmd/vnproxd's
// hostTcMirrorGateway) or the daemon's own unattended expiry sweep
// (internal/change/tcmirror_expiry.go) — there is no third mutation path.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// TcMirrorSessionStatus is the lifecycle state of one tc_mirror_sessions
// row.
type TcMirrorSessionStatus string

const (
	// TcMirrorSessionActive: the session's tc state is live on its node.
	TcMirrorSessionActive TcMirrorSessionStatus = "active"
	// TcMirrorSessionExpired: RunTcMirrorSweep tore the session down
	// because its max-duration deadline passed — the "must stop itself"
	// bound, never an operator action.
	TcMirrorSessionExpired TcMirrorSessionStatus = "expired"
	// TcMirrorSessionStopped: an operator explicitly deleted the session
	// (tc.mirror.delete) before its deadline.
	TcMirrorSessionStopped TcMirrorSessionStatus = "stopped"
)

// TcMirrorSession is one row of the tc_mirror_sessions table.
type TcMirrorSession struct {
	MaxMbit        *int
	StoppedAt      *int64
	ID             string
	Node           string
	SourceIface    string
	DestIface      string
	Status         TcMirrorSessionStatus
	CreatedBy      string
	MaxDurationSec int
	StartedAt      int64
	ExpiresAt      int64
}

// TcMirrorSessionRepo is the tc_mirror_sessions table repository.
type TcMirrorSessionRepo struct {
	db *DB
}

// NewTcMirrorSessionRepo constructs a TcMirrorSessionRepo.
func NewTcMirrorSessionRepo(db *DB) *TcMirrorSessionRepo { return &TcMirrorSessionRepo{db: db} }

// Insert creates a new tc_mirror_sessions row (ID is caller-assigned,
// typically the changeset op's own target id).
func (r *TcMirrorSessionRepo) Insert(ctx context.Context, s TcMirrorSession) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO tc_mirror_sessions
			(id, node, source_iface, dest_iface, max_mbit, max_duration_sec, status, created_by, started_at, expires_at, stopped_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Node, s.SourceIface, s.DestIface, intPtrToNull(s.MaxMbit), s.MaxDurationSec, string(s.Status),
		s.CreatedBy, s.StartedAt, s.ExpiresAt, int64PtrToNull(s.StoppedAt),
	)
	if err != nil {
		return fmt.Errorf("store: inserting tc mirror session %s: %w", s.ID, err)
	}
	return nil
}

// Get returns one session by id, or ErrNotFound.
func (r *TcMirrorSessionRepo) Get(ctx context.Context, id string) (TcMirrorSession, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, node, source_iface, dest_iface, max_mbit, max_duration_sec, status, created_by, started_at, expires_at, stopped_at
		FROM tc_mirror_sessions WHERE id = ?`, id)
	s, err := scanTcMirrorSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return TcMirrorSession{}, ErrNotFound
	}
	return s, err
}

// ActiveByNode returns every currently-active session on node (or every
// node, when node is ""), ordered by id for a stable listing — used both
// by validate_safety.go's concurrent-session/bandwidth cap check and by
// the API surface's session listing.
func (r *TcMirrorSessionRepo) ActiveByNode(ctx context.Context, node string) ([]TcMirrorSession, error) {
	q := `SELECT id, node, source_iface, dest_iface, max_mbit, max_duration_sec, status, created_by, started_at, expires_at, stopped_at
		FROM tc_mirror_sessions WHERE status = ?`
	args := []any{string(TcMirrorSessionActive)}
	if node != "" {
		q += ` AND node = ?`
		args = append(args, node)
	}
	q += ` ORDER BY id ASC`
	return r.queryList(ctx, q, args...)
}

// DueForExpiry returns every active session whose expires_at is at or
// before now — RunTcMirrorSweep's own read, called on every tick
// (including the eager startup tick, so a session that expired while the
// daemon was down is caught immediately rather than left running).
func (r *TcMirrorSessionRepo) DueForExpiry(ctx context.Context, now int64) ([]TcMirrorSession, error) {
	q := `SELECT id, node, source_iface, dest_iface, max_mbit, max_duration_sec, status, created_by, started_at, expires_at, stopped_at
		FROM tc_mirror_sessions WHERE status = ? AND expires_at <= ? ORDER BY id ASC`
	return r.queryList(ctx, q, string(TcMirrorSessionActive), now)
}

func (r *TcMirrorSessionRepo) queryList(ctx context.Context, q string, args ...any) ([]TcMirrorSession, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: listing tc mirror sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []TcMirrorSession
	for rows.Next() {
		s, err := scanTcMirrorSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing tc mirror sessions: %w", err)
	}
	return out, nil
}

// SetStatus transitions id to status (expired/stopped), stamping stoppedAt.
// Not an error if id is already absent — rollback/expiry of an already-
// removed session must converge, not fail (mirrors QosShapeRepo.Delete's
// identical idempotency rule).
func (r *TcMirrorSessionRepo) SetStatus(ctx context.Context, id string, status TcMirrorSessionStatus, stoppedAt int64) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE tc_mirror_sessions SET status = ?, stopped_at = ? WHERE id = ?`,
		string(status), stoppedAt, id,
	)
	if err != nil {
		return fmt.Errorf("store: setting tc mirror session %s status: %w", id, err)
	}
	return nil
}

// UpdateDuration re-arms an active session's bound (tc.mirror.update's
// only effect — see internal/tcmirror's doc comment on why an update never
// re-renders tc state).
func (r *TcMirrorSessionRepo) UpdateDuration(ctx context.Context, id string, maxDurationSec int, expiresAt int64) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE tc_mirror_sessions SET max_duration_sec = ?, expires_at = ? WHERE id = ? AND status = ?`,
		maxDurationSec, expiresAt, id, string(TcMirrorSessionActive),
	)
	if err != nil {
		return fmt.Errorf("store: updating tc mirror session %s duration: %w", id, err)
	}
	return checkRowAffected(res, "store: updating tc mirror session %s duration", id)
}

// Delete removes a session row outright — used only by rollback of a
// tc.mirror.create that never confirmed (undoing the row the same as
// undoing the tc state itself). Not an error to delete an already-absent
// row.
func (r *TcMirrorSessionRepo) Delete(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM tc_mirror_sessions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: deleting tc mirror session %s: %w", id, err)
	}
	return nil
}

func int64PtrToNull(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}

func nullToInt64Ptr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func scanTcMirrorSession(row rowScanner) (TcMirrorSession, error) {
	var s TcMirrorSession
	var maxMbit sql.NullInt64
	var stoppedAt sql.NullInt64
	var status string
	if err := row.Scan(&s.ID, &s.Node, &s.SourceIface, &s.DestIface, &maxMbit, &s.MaxDurationSec, &status,
		&s.CreatedBy, &s.StartedAt, &s.ExpiresAt, &stoppedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TcMirrorSession{}, err
		}
		return TcMirrorSession{}, fmt.Errorf("store: scanning tc mirror session: %w", err)
	}
	s.MaxMbit = nullToIntPtr(maxMbit)
	s.Status = TcMirrorSessionStatus(status)
	s.StoppedAt = nullToInt64Ptr(stoppedAt)
	return s, nil
}
