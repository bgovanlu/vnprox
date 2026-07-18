package store

import (
	"context"
	"database/sql"
	"fmt"
)

// LatencySample is one row of the latency_samples table (docs/data-model.md
// §2, T-1303): one internal/latmesh.Sample probe reading, persisted for the
// bounded window internal/latmesh's package doc comment documents.
type LatencySample struct {
	LinkID   string
	Fabric   string
	FromNode string
	ToNode   string
	ID       int64
	At       int64
	RttMs    float64
	LossPct  float64
}

// LatencySampleRepo is the latency_samples table repository.
type LatencySampleRepo struct {
	db *DB
}

// NewLatencySampleRepo constructs a LatencySampleRepo.
func NewLatencySampleRepo(db *DB) *LatencySampleRepo { return &LatencySampleRepo{db: db} }

// InsertBatch records samples in one transaction — mirrors
// FlowSampleRepo.InsertBatch exactly (one probe tick's worth of readings
// across every pair a node discovered is exactly the same shape as one
// decoded flow-record batch). A nil/empty slice is a no-op.
func (r *LatencySampleRepo) InsertBatch(ctx context.Context, samples []LatencySample) error {
	if len(samples) == 0 {
		return nil
	}
	tx, err := r.db.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: beginning latency sample batch insert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO latency_samples (link_id, fabric, from_node, to_node, at, rtt_ms, loss_pct)
		VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("store: preparing latency sample insert: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, s := range samples {
		if _, err := stmt.ExecContext(ctx, s.LinkID, s.Fabric, s.FromNode, s.ToNode, s.At, s.RttMs, s.LossPct); err != nil {
			return fmt.Errorf("store: inserting latency sample: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: committing latency sample batch insert: %w", err)
	}
	return nil
}

// QueryRange returns linkID's samples with at in [fromTs, toTs] (either
// bound 0/max-int64 disables that side, matching GET /metrics/history's
// convention), ascending by at — the shape both Service.History and
// Service.rollingStats/Baseline read from.
func (r *LatencySampleRepo) QueryRange(ctx context.Context, linkID string, fromTs, toTs int64) ([]LatencySample, error) {
	rows, err := r.db.sqlDB.QueryContext(ctx, `
		SELECT id, link_id, fabric, from_node, to_node, at, rtt_ms, loss_pct
		FROM latency_samples WHERE link_id = ? AND at BETWEEN ? AND ?
		ORDER BY at ASC`, linkID, fromTs, toTs)
	if err != nil {
		return nil, fmt.Errorf("store: querying latency samples for %s: %w", linkID, err)
	}
	defer func() { _ = rows.Close() }()
	return scanLatencySamples(rows)
}

// LatestPerLink returns the single most recent sample for every distinct
// link_id currently in the table — GET /latmesh/heatmap's "current value"
// half (Service.Heatmap pairs this with a rolling-window QueryRange call
// per link for the "rolling" half).
func (r *LatencySampleRepo) LatestPerLink(ctx context.Context) ([]LatencySample, error) {
	rows, err := r.db.sqlDB.QueryContext(ctx, `
		SELECT s.id, s.link_id, s.fabric, s.from_node, s.to_node, s.at, s.rtt_ms, s.loss_pct
		FROM latency_samples s
		INNER JOIN (
			SELECT link_id, MAX(at) AS max_at FROM latency_samples GROUP BY link_id
		) latest ON s.link_id = latest.link_id AND s.at = latest.max_at
		ORDER BY s.link_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: querying latest-per-link latency samples: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanLatencySamples(rows)
}

func scanLatencySamples(rows *sql.Rows) ([]LatencySample, error) {
	var out []LatencySample
	for rows.Next() {
		var s LatencySample
		if err := rows.Scan(&s.ID, &s.LinkID, &s.Fabric, &s.FromNode, &s.ToNode, &s.At, &s.RttMs, &s.LossPct); err != nil {
			return nil, fmt.Errorf("store: scanning latency sample: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating latency samples: %w", err)
	}
	return out, nil
}

// PruneOlderThan deletes rows with at < cutoff, returning the number
// removed — the retention-window half of internal/latmesh's bound (mirrors
// FlowSampleRepo.PruneOlderThan exactly).
func (r *LatencySampleRepo) PruneOlderThan(ctx context.Context, cutoff int64) (int64, error) {
	res, err := r.db.sqlDB.ExecContext(ctx, `DELETE FROM latency_samples WHERE at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("store: pruning latency samples older than %d: %w", cutoff, err)
	}
	return rowsAffected(res, "latency samples")
}

// PruneToCap deletes the oldest rows (by (at, id) ascending) until at most
// maxRows remain, returning the number removed — the hard-row-cap half of
// internal/latmesh's bound (mirrors FlowSampleRepo.PruneToCap exactly). A
// maxRows <= 0 is a no-op (never interpreted as "delete everything").
func (r *LatencySampleRepo) PruneToCap(ctx context.Context, maxRows int64) (int64, error) {
	if maxRows <= 0 {
		return 0, nil
	}
	res, err := r.db.sqlDB.ExecContext(ctx, `
		DELETE FROM latency_samples WHERE id IN (
			SELECT id FROM latency_samples ORDER BY at DESC, id DESC LIMIT -1 OFFSET ?
		)`, maxRows)
	if err != nil {
		return 0, fmt.Errorf("store: pruning latency samples to cap %d: %w", maxRows, err)
	}
	return rowsAffected(res, "latency samples")
}

// Count returns the total row count (test/diagnostic helper, mirrors
// FlowSampleRepo.Count).
func (r *LatencySampleRepo) Count(ctx context.Context) (int64, error) {
	var n int64
	if err := r.db.sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM latency_samples`).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: counting latency samples: %w", err)
	}
	return n, nil
}
