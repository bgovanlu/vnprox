// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// WanTarget is one row of the wan_targets table (docs/data-model.md §2,
// T-1405): an operator-configured reference target to probe for one node's
// one uplink. App-owned intent, per CLAUDE.md's storage rule — vnprox never
// invents this list, an operator explicitly configures it.
type WanTarget struct {
	Node      string
	Uplink    string
	Host      string
	ID        int64
	CreatedAt int64
}

// WanTargetRepo is the wan_targets table repository.
type WanTargetRepo struct {
	db *DB
}

// NewWanTargetRepo constructs a WanTargetRepo.
func NewWanTargetRepo(db *DB) *WanTargetRepo { return &WanTargetRepo{db: db} }

// ListByNode returns every configured target for node, ordered by uplink
// then host for deterministic responses.
func (r *WanTargetRepo) ListByNode(ctx context.Context, node string) ([]WanTarget, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, node, uplink, host, created_at FROM wan_targets
		WHERE node = ? ORDER BY uplink ASC, host ASC`, node)
	if err != nil {
		return nil, fmt.Errorf("store: querying wan targets for node %s: %w", node, err)
	}
	defer func() { _ = rows.Close() }()
	return scanWanTargets(rows)
}

// ReplaceForNode atomically replaces node's entire configured target list
// with targets — the same "delete then insert the new full set" replace
// semantics PUT /protected-interfaces uses for its own admin-configured
// set, appropriate here since a target list is small (a handful of hosts
// per uplink) and PUT /wan/targets is always a full-set replace, never a
// partial patch.
func (r *WanTargetRepo) ReplaceForNode(ctx context.Context, node string, targets []WanTarget, now int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: beginning wan targets replace: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM wan_targets WHERE node = ?`, node); err != nil {
		return fmt.Errorf("store: clearing existing wan targets for node %s: %w", node, err)
	}

	if len(targets) > 0 {
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO wan_targets (node, uplink, host, created_at) VALUES (?, ?, ?, ?)`)
		if err != nil {
			return fmt.Errorf("store: preparing wan target insert: %w", err)
		}
		defer func() { _ = stmt.Close() }()

		for _, t := range targets {
			if _, err := stmt.ExecContext(ctx, node, t.Uplink, t.Host, now); err != nil {
				return fmt.Errorf("store: inserting wan target: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: committing wan targets replace: %w", err)
	}
	return nil
}

func scanWanTargets(rows *sql.Rows) ([]WanTarget, error) {
	var out []WanTarget
	for rows.Next() {
		var t WanTarget
		if err := rows.Scan(&t.ID, &t.Node, &t.Uplink, &t.Host, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: scanning wan target: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating wan targets: %w", err)
	}
	return out, nil
}

// WanFabric is the internal/latmesh.Fabric value every WAN probe pair/
// sample uses — a plain package-level constant (not internal/latmesh.
// FabricCorosync/FabricGuest, which name this package's own two fabrics)
// since internal/latmesh.Fabric is just a string type, not a closed enum;
// see internal/wan's package doc comment for why WAN probing is modeled as
// a third, distinct fabric label on top of the exact same Pair/LinkHeat/
// Sample shapes rather than a fork of them.
const WanFabric = "wan"

// WanProbeSampleRepo is the wan_probe_samples table repository. It
// satisfies internal/latmesh.Ring structurally (its five methods have the
// identical signatures LatencySampleRepo's do, using the exact same
// LatencySample currency type) — literal reuse of T-1303's scheduler
// machinery (*latmesh.Service) against this task's own table/retention
// config, per T-1306's precedent for reusing internal/latmesh
// infrastructure rather than building a second scheduler. The one detail
// LatencySample can't carry (which of a node's, possibly several,
// multi-WAN uplinks a reading belongs to) is preserved anyway: this table's
// own `uplink` column is populated at insert time by parsing it back out of
// the sample's own LinkID (internal/latmesh.ComputeLinkID's stable
// "<fabric>[:<label>]|<from>-><to>" encoding — the label *is* the uplink
// name for a WAN pair, see internal/wan.TargetDiscoverer), so it is never
// lost even though the Ring interface's own return type has no field for
// it. internal/wan.Service.Export (AC4's "export path") reads the richer
// WanProbeSample shape (including Uplink) directly via QueryAll below,
// bypassing the narrower Ring interface.
type WanProbeSampleRepo struct {
	db *DB
}

// NewWanProbeSampleRepo constructs a WanProbeSampleRepo.
func NewWanProbeSampleRepo(db *DB) *WanProbeSampleRepo { return &WanProbeSampleRepo{db: db} }

// wanUplinkFromLinkID extracts a WAN Pair's uplink label back out of its
// LinkID ("wan:<uplink>|<fromNode>-><toNode>") — the inverse of
// internal/latmesh.ComputeLinkID(WanFabric, uplink, fromNode, toNode).
// Returns "" for a LinkID with no label segment (should not happen for a
// WAN pair, which always carries one — see TargetDiscoverer.Pairs — but
// this never panics on an unexpected shape).
func wanUplinkFromLinkID(linkID string) string {
	before, _, ok := strings.Cut(linkID, "|")
	if !ok {
		return ""
	}
	_, label, ok := strings.Cut(before, ":")
	if !ok {
		return ""
	}
	return label
}

// InsertBatch implements internal/latmesh.Ring.
func (r *WanProbeSampleRepo) InsertBatch(ctx context.Context, samples []LatencySample) error {
	if len(samples) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: beginning wan probe sample batch insert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO wan_probe_samples (link_id, from_node, uplink, to_node, at, rtt_ms, loss_pct)
		VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("store: preparing wan probe sample insert: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, s := range samples {
		uplink := wanUplinkFromLinkID(s.LinkID)
		if _, err := stmt.ExecContext(ctx, s.LinkID, s.FromNode, uplink, s.ToNode, s.At, s.RttMs, s.LossPct); err != nil {
			return fmt.Errorf("store: inserting wan probe sample: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: committing wan probe sample batch insert: %w", err)
	}
	return nil
}

// QueryRange implements internal/latmesh.Ring.
func (r *WanProbeSampleRepo) QueryRange(ctx context.Context, linkID string, fromTs, toTs int64) ([]LatencySample, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, link_id, from_node, to_node, at, rtt_ms, loss_pct
		FROM wan_probe_samples WHERE link_id = ? AND at BETWEEN ? AND ?
		ORDER BY at ASC`, linkID, fromTs, toTs)
	if err != nil {
		return nil, fmt.Errorf("store: querying wan probe samples for %s: %w", linkID, err)
	}
	defer func() { _ = rows.Close() }()
	return scanWanLatencySamples(rows)
}

// LatestPerLink implements internal/latmesh.Ring.
func (r *WanProbeSampleRepo) LatestPerLink(ctx context.Context) ([]LatencySample, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT s.id, s.link_id, s.from_node, s.to_node, s.at, s.rtt_ms, s.loss_pct
		FROM wan_probe_samples s
		INNER JOIN (
			SELECT link_id, MAX(id) AS max_id FROM wan_probe_samples
			WHERE (link_id, at) IN (
				SELECT link_id, MAX(at) FROM wan_probe_samples GROUP BY link_id
			)
			GROUP BY link_id
		) latest ON s.id = latest.max_id
		ORDER BY s.link_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: querying latest-per-link wan probe samples: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanWanLatencySamples(rows)
}

// PruneOlderThan implements internal/latmesh.Ring.
func (r *WanProbeSampleRepo) PruneOlderThan(ctx context.Context, cutoff int64) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM wan_probe_samples WHERE at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("store: pruning wan probe samples older than %d: %w", cutoff, err)
	}
	return rowsAffected(res, "wan probe samples")
}

// PruneToCap implements internal/latmesh.Ring.
func (r *WanProbeSampleRepo) PruneToCap(ctx context.Context, maxRows int64) (int64, error) {
	if maxRows <= 0 {
		return 0, nil
	}
	res, err := r.db.ExecContext(ctx, `
		DELETE FROM wan_probe_samples WHERE id IN (
			SELECT id FROM wan_probe_samples ORDER BY at DESC, id DESC LIMIT -1 OFFSET ?
		)`, maxRows)
	if err != nil {
		return 0, fmt.Errorf("store: pruning wan probe samples to cap %d: %w", maxRows, err)
	}
	return rowsAffected(res, "wan probe samples")
}

// Count returns the total row count (test/diagnostic helper, mirrors
// LatencySampleRepo.Count).
func (r *WanProbeSampleRepo) Count(ctx context.Context) (int64, error) {
	var n int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM wan_probe_samples`).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: counting wan probe samples: %w", err)
	}
	return n, nil
}

// QueryAll returns every currently-retained sample (already bounded by the
// ring's own prune loop), newest first, capped at limit — T-1405 AC4's
// "exposes an export path". limit <= 0 defaults to 10,000 so a caller can
// never accidentally request an unbounded dump. Unlike the Ring-interface
// methods above, this returns the richer WanProbeSample shape (Uplink
// included, read straight off the column rather than re-derived) since
// callers of the export path (internal/wan.Service.Export) want it without
// re-parsing every LinkID themselves.
func (r *WanProbeSampleRepo) QueryAll(ctx context.Context, limit int64) ([]WanProbeSample, error) {
	if limit <= 0 {
		limit = 10_000
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, link_id, from_node, uplink, to_node, at, rtt_ms, loss_pct
		FROM wan_probe_samples ORDER BY at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: querying all wan probe samples: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []WanProbeSample
	for rows.Next() {
		var s WanProbeSample
		if err := rows.Scan(&s.ID, &s.LinkID, &s.FromNode, &s.Uplink, &s.ToNode, &s.At, &s.RttMs, &s.LossPct); err != nil {
			return nil, fmt.Errorf("store: scanning wan probe sample: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating wan probe samples: %w", err)
	}
	return out, nil
}

// WanProbeSample is one row of the wan_probe_samples table, carrying the
// Uplink column QueryAll's export path needs (see its doc comment) —
// QueryRange/LatestPerLink above intentionally return the narrower, Ring-
// interface-compatible LatencySample instead.
type WanProbeSample struct {
	LinkID   string
	FromNode string
	Uplink   string
	ToNode   string
	ID       int64
	At       int64
	RttMs    float64
	LossPct  float64
}

func scanWanLatencySamples(rows *sql.Rows) ([]LatencySample, error) {
	var out []LatencySample
	for rows.Next() {
		var s LatencySample
		if err := rows.Scan(&s.ID, &s.LinkID, &s.FromNode, &s.ToNode, &s.At, &s.RttMs, &s.LossPct); err != nil {
			return nil, fmt.Errorf("store: scanning wan probe sample: %w", err)
		}
		s.Fabric = WanFabric
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating wan probe samples: %w", err)
	}
	return out, nil
}
