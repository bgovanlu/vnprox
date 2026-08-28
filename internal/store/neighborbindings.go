// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

// NeighborBinding is one row of the neighbor_bindings table
// (docs/data-model.md, T-3905): one observed IP<->MAC binding *transition*
// on the local node — see 0050_neighbor_bindings.sql's header for why this
// is append-on-change rather than append-on-every-poll, and why node is
// always the local node.
type NeighborBinding struct {
	Node  string
	IP    string
	MAC   string
	Iface string
	State string
	// PrevMAC trails the plain strings: sql.NullString is 24 bytes (string
	// plus a bool) against a string's 16, so it belongs after them.
	PrevMAC sql.NullString

	ID int64
	At int64
}

// NeighborBindingRepo is the neighbor_bindings table repository.
type NeighborBindingRepo struct {
	db *DB
}

// NewNeighborBindingRepo constructs a NeighborBindingRepo.
func NewNeighborBindingRepo(db *DB) *NeighborBindingRepo { return &NeighborBindingRepo{db: db} }

// Insert records one binding transition. Like flow_samples (and unlike
// metric_samples' natural (ref, at) key), there is no dedup key: every
// transition internal/neighbor's HistoryRecorder decides to record is its
// own row.
func (r *NeighborBindingRepo) Insert(ctx context.Context, b NeighborBinding) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO neighbor_bindings (at, node, ip, mac, prev_mac, iface, state)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		b.At, b.Node, b.IP, b.MAC, b.PrevMAC, b.Iface, b.State,
	)
	if err != nil {
		return fmt.Errorf("store: inserting neighbor binding %s/%s: %w", b.Node, b.IP, err)
	}
	return nil
}

// LatestByIP returns, for every IP this node has ever recorded a binding
// for, that IP's most recently recorded row (by id, the same
// last-writer-wins tiebreaker flow_samples' Query uses for same-second
// rows). HistoryRecorder.Poll uses this once per tick to decide, for each
// currently-observed (ip, mac), whether it is unchanged (skip), a genuine
// transition (insert with prev_mac set), or a never-before-seen IP (insert
// with prev_mac NULL) — a single grouped query rather than one lookup per
// observed IP.
func (r *NeighborBindingRepo) LatestByIP(ctx context.Context, node string) (map[string]NeighborBinding, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT t1.id, t1.at, t1.node, t1.ip, t1.mac, t1.prev_mac, t1.iface, t1.state
		FROM neighbor_bindings t1
		INNER JOIN (
			SELECT ip, MAX(id) AS id FROM neighbor_bindings WHERE node = ? GROUP BY ip
		) t2 ON t1.id = t2.id`, node,
	)
	if err != nil {
		return nil, fmt.Errorf("store: reading latest neighbor bindings for %s: %w", node, err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string]NeighborBinding{}
	for rows.Next() {
		b, err := scanNeighborBinding(rows)
		if err != nil {
			return nil, err
		}
		out[b.IP] = b
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: reading latest neighbor bindings for %s: %w", node, err)
	}
	return out, nil
}

// NeighborBindingFilter narrows Query's result set. Every non-zero field is
// ANDed, mirroring FlowFilter's convention (docs/api.md's GET /flows): an
// unrecognized/unparsable filter value matches nothing rather than
// erroring.
type NeighborBindingFilter struct {
	Node   string
	IP     string
	MAC    string
	FromTs int64
	ToTs   int64
}

const defaultNeighborBindingPageLimit = 200

// Query returns one page of neighbor_bindings newest-first matching filter,
// per docs/api.md's `?limit=&cursor=` pagination convention — the same
// "<at>:<id>" keyset cursor scheme FlowSampleRepo.Query uses. An empty
// cursor starts from the newest row; nextCursor is empty once there is no
// further page.
func (r *NeighborBindingRepo) Query(ctx context.Context, filter NeighborBindingFilter, cursor string, limit int) ([]NeighborBinding, string, error) {
	if limit <= 0 {
		limit = defaultNeighborBindingPageLimit
	}

	query := `SELECT id, at, node, ip, mac, prev_mac, iface, state FROM neighbor_bindings WHERE 1=1`
	var args []any
	if filter.Node != "" {
		query += ` AND node = ?`
		args = append(args, filter.Node)
	}
	if filter.IP != "" {
		query += ` AND ip = ?`
		args = append(args, filter.IP)
	}
	if filter.MAC != "" {
		query += ` AND mac = ?`
		args = append(args, filter.MAC)
	}
	if filter.FromTs > 0 {
		query += ` AND at >= ?`
		args = append(args, filter.FromTs)
	}
	if filter.ToTs > 0 {
		query += ` AND at <= ?`
		args = append(args, filter.ToTs)
	}
	if cursor != "" {
		at, id, err := decodeNeighborBindingCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		query += ` AND (at < ? OR (at = ? AND id < ?))`
		args = append(args, at, at, id)
	}
	query += ` ORDER BY at DESC, id DESC LIMIT ?`
	args = append(args, limit+1)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("store: querying neighbor bindings: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var items []NeighborBinding
	for rows.Next() {
		b, err := scanNeighborBinding(rows)
		if err != nil {
			return nil, "", err
		}
		items = append(items, b)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("store: querying neighbor bindings: %w", err)
	}

	next := ""
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		next = encodeNeighborBindingCursor(last.At, last.ID)
	}
	return items, next, nil
}

// PruneOlderThan deletes rows with at < cutoff, returning the number
// removed — the retention-window half of the ring's bound.
func (r *NeighborBindingRepo) PruneOlderThan(ctx context.Context, cutoff int64) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM neighbor_bindings WHERE at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("store: pruning neighbor bindings older than %d: %w", cutoff, err)
	}
	return rowsAffected(res, "neighbor bindings")
}

// PruneToCap deletes the oldest rows until at most maxRows remain (by
// (at, id) descending — the same ordering Query returns), returning the
// number removed — the hard-row-cap half of the ring's bound. A maxRows <=
// 0 is a no-op (never interpreted as "delete everything"), mirroring
// FlowSampleRepo.PruneToCap exactly.
func (r *NeighborBindingRepo) PruneToCap(ctx context.Context, maxRows int64) (int64, error) {
	if maxRows <= 0 {
		return 0, nil
	}
	res, err := r.db.ExecContext(ctx, `
		DELETE FROM neighbor_bindings WHERE id IN (
			SELECT id FROM neighbor_bindings ORDER BY at DESC, id DESC LIMIT -1 OFFSET ?
		)`, maxRows)
	if err != nil {
		return 0, fmt.Errorf("store: pruning neighbor bindings to cap %d: %w", maxRows, err)
	}
	return rowsAffected(res, "neighbor bindings")
}

// CountSince returns how many genuine transitions (prev_mac IS NOT NULL —
// a (node, ip)'s first-ever row, recording a previously-unseen IP rather
// than a rebind, never counts as a transition) matching (node, ip or mac)
// were recorded at or after since — the flap-window count both directions
// of T-3905's flap detection query use. Exactly one of ip/mac should be
// non-empty; if both are set, both are ANDed (matches nothing useful, but
// never errors — same tolerant-filter posture as Query/
// NeighborBindingFilter).
func (r *NeighborBindingRepo) CountSince(ctx context.Context, node, ip, mac string, since int64) (int64, error) {
	query := `SELECT COUNT(*) FROM neighbor_bindings WHERE node = ? AND at >= ? AND prev_mac IS NOT NULL`
	args := []any{node, since}
	if ip != "" {
		query += ` AND ip = ?`
		args = append(args, ip)
	}
	if mac != "" {
		query += ` AND mac = ?`
		args = append(args, mac)
	}
	var n int64
	if err := r.db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: counting neighbor bindings since %d: %w", since, err)
	}
	return n, nil
}

// DistinctIPsSince returns the set of distinct IPs recorded for (node, mac)
// at or after since — the "one MAC claiming many IPs" flap direction, which
// needs the actual IP set (for the finding's Detail/Refs), not just a count.
func (r *NeighborBindingRepo) DistinctIPsSince(ctx context.Context, node, mac string, since int64) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT ip FROM neighbor_bindings WHERE node = ? AND mac = ? AND at >= ? ORDER BY ip`,
		node, mac, since,
	)
	if err != nil {
		return nil, fmt.Errorf("store: reading distinct IPs for %s/%s since %d: %w", node, mac, since, err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			return nil, fmt.Errorf("store: scanning distinct IP: %w", err)
		}
		out = append(out, ip)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: reading distinct IPs for %s/%s since %d: %w", node, mac, since, err)
	}
	return out, nil
}

// CandidateIPsSince returns every distinct IP with at least one transition
// recorded on node at or after since — the IP-flap direction's candidate
// set: an IP with only one transition in the window can never cross the
// threshold, so the flap check only needs to run CountSince against IPs
// this query names, not every IP the node has ever seen.
func (r *NeighborBindingRepo) CandidateIPsSince(ctx context.Context, node string, since int64) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT ip FROM neighbor_bindings WHERE node = ? AND at >= ? ORDER BY ip`, node, since,
	)
	if err != nil {
		return nil, fmt.Errorf("store: reading candidate IPs for %s since %d: %w", node, since, err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			return nil, fmt.Errorf("store: scanning candidate IP: %w", err)
		}
		out = append(out, ip)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: reading candidate IPs for %s since %d: %w", node, since, err)
	}
	return out, nil
}

// CandidateMACsSince returns every distinct MAC with at least one
// transition recorded on node at or after since — the "one MAC claiming
// many IPs" direction's candidate set, mirroring CandidateIPsSince.
func (r *NeighborBindingRepo) CandidateMACsSince(ctx context.Context, node string, since int64) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT mac FROM neighbor_bindings WHERE node = ? AND at >= ? ORDER BY mac`, node, since,
	)
	if err != nil {
		return nil, fmt.Errorf("store: reading candidate MACs for %s since %d: %w", node, since, err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var mac string
		if err := rows.Scan(&mac); err != nil {
			return nil, fmt.Errorf("store: scanning candidate MAC: %w", err)
		}
		out = append(out, mac)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: reading candidate MACs for %s since %d: %w", node, since, err)
	}
	return out, nil
}

func encodeNeighborBindingCursor(at, id int64) string {
	return strconv.FormatInt(at, 10) + ":" + strconv.FormatInt(id, 10)
}

func decodeNeighborBindingCursor(cursor string) (int64, int64, error) {
	atStr, idStr, ok := strings.Cut(cursor, ":")
	if !ok {
		return 0, 0, fmt.Errorf("store: malformed neighbor binding cursor %q", cursor)
	}
	at, err := strconv.ParseInt(atStr, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("store: malformed neighbor binding cursor %q: %w", cursor, err)
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("store: malformed neighbor binding cursor %q: %w", cursor, err)
	}
	return at, id, nil
}

func scanNeighborBinding(row rowScanner) (NeighborBinding, error) {
	var b NeighborBinding
	err := row.Scan(&b.ID, &b.At, &b.Node, &b.IP, &b.MAC, &b.PrevMAC, &b.Iface, &b.State)
	if err != nil {
		return NeighborBinding{}, fmt.Errorf("store: scanning neighbor binding: %w", err)
	}
	return b, nil
}
