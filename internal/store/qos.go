// qos.go implements T-1505's qos_shapes storage (docs/data-model.md §2,
// migration 0020_qos.sql). App-owned intent only per CLAUDE.md's storage
// rule — the live tc/HTB state on the node stays authoritative and is
// never shadow-copied here; internal/qos.RenderTC re-derives the on-node
// invocation from this row's own fields every time it is (re)applied.
// Every row is written only by the change engine's qos.shape.*
// apply/rollback executor (cmd/vnproxd's hostQosGateway) — there is no
// second mutation path.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// QosShape is one row of the qos_shapes table.
type QosShape struct {
	ID        string
	Node      string
	Bridge    string
	MatchCIDR string
	MatchVlan *int
	CeilMbit  *int
	Priority  *int
	CreatedBy string
	RateMbit  int
	CreatedAt int64
	UpdatedAt int64
}

// QosShapeRepo is the qos_shapes table repository.
type QosShapeRepo struct {
	db *DB
}

// NewQosShapeRepo constructs a QosShapeRepo.
func NewQosShapeRepo(db *DB) *QosShapeRepo { return &QosShapeRepo{db: db} }

func intPtrToNull(p *int) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*p), Valid: true}
}

func nullToIntPtr(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

// Insert creates a new qos_shapes row (ID is caller-assigned, typically the
// changeset op's own target id).
func (r *QosShapeRepo) Insert(ctx context.Context, s QosShape) error {
	_, err := r.db.sqlDB.ExecContext(ctx, `
		INSERT INTO qos_shapes
			(id, node, bridge, match_cidr, match_vlan, rate_mbit, ceil_mbit, priority, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Node, s.Bridge, s.MatchCIDR, intPtrToNull(s.MatchVlan), s.RateMbit, intPtrToNull(s.CeilMbit), intPtrToNull(s.Priority),
		s.CreatedBy, s.CreatedAt, s.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("store: inserting qos shape %s: %w", s.ID, err)
	}
	return nil
}

// Get returns one shape by id, or ErrNotFound.
func (r *QosShapeRepo) Get(ctx context.Context, id string) (QosShape, error) {
	row := r.db.sqlDB.QueryRowContext(ctx, `
		SELECT id, node, bridge, match_cidr, match_vlan, rate_mbit, ceil_mbit, priority, created_by, created_at, updated_at
		FROM qos_shapes WHERE id = ?`, id)
	s, err := scanQosShape(row)
	if errors.Is(err, sql.ErrNoRows) {
		return QosShape{}, ErrNotFound
	}
	return s, err
}

// List returns every shape, or every shape on one node when node is
// non-empty, ordered by node then bridge then id for a stable listing.
func (r *QosShapeRepo) List(ctx context.Context, node string) ([]QosShape, error) {
	q := `SELECT id, node, bridge, match_cidr, match_vlan, rate_mbit, ceil_mbit, priority, created_by, created_at, updated_at
		FROM qos_shapes`
	var args []any
	if node != "" {
		q += ` WHERE node = ?`
		args = append(args, node)
	}
	q += ` ORDER BY node ASC, bridge ASC, id ASC`
	rows, err := r.db.sqlDB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: listing qos shapes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []QosShape
	for rows.Next() {
		s, err := scanQosShape(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listing qos shapes: %w", err)
	}
	return out, nil
}

// Update overwrites an existing shape's mutable fields (never its id/node —
// those are the row's identity). Returns ErrNotFound if the shape doesn't
// exist.
func (r *QosShapeRepo) Update(ctx context.Context, s QosShape) error {
	res, err := r.db.sqlDB.ExecContext(ctx, `
		UPDATE qos_shapes SET
			bridge = ?, match_cidr = ?, match_vlan = ?, rate_mbit = ?, ceil_mbit = ?, priority = ?, updated_at = ?
		WHERE id = ?`,
		s.Bridge, s.MatchCIDR, intPtrToNull(s.MatchVlan), s.RateMbit, intPtrToNull(s.CeilMbit), intPtrToNull(s.Priority), s.UpdatedAt, s.ID,
	)
	if err != nil {
		return fmt.Errorf("store: updating qos shape %s: %w", s.ID, err)
	}
	return checkRowAffected(res, "store: updating qos shape %s", s.ID)
}

// Delete removes a shape by id. Not an error to delete an already-absent
// one — rollback of a create must converge even if a prior step already
// removed it.
func (r *QosShapeRepo) Delete(ctx context.Context, id string) error {
	if _, err := r.db.sqlDB.ExecContext(ctx, `DELETE FROM qos_shapes WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: deleting qos shape %s: %w", id, err)
	}
	return nil
}

func scanQosShape(row rowScanner) (QosShape, error) {
	var s QosShape
	var matchVlan, ceilMbit, priority sql.NullInt64
	if err := row.Scan(&s.ID, &s.Node, &s.Bridge, &s.MatchCIDR, &matchVlan, &s.RateMbit, &ceilMbit, &priority, &s.CreatedBy, &s.CreatedAt, &s.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return QosShape{}, err
		}
		return QosShape{}, fmt.Errorf("store: scanning qos shape: %w", err)
	}
	s.MatchVlan = nullToIntPtr(matchVlan)
	s.CeilMbit = nullToIntPtr(ceilMbit)
	s.Priority = nullToIntPtr(priority)
	return s, nil
}
