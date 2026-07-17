package store

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// FlowSample is one row of the flow_samples table (docs/data-model.md §2,
// T-1002): one normalized flow.Record, persisted for the bounded window
// internal/flow's package doc comment documents. This type intentionally
// mirrors internal/flow.Record field-for-field (internal/flow.Store
// converts between the two) rather than importing internal/flow directly —
// the same "internal/store never imports the packages that use it" layering
// every other repo in this file already follows (docs/architecture.md §2's
// package layout: internal/store sits below the packages built on it).
type FlowSample struct {
	Node   string
	SrcIP  string
	DstIP  string
	SrcRef string
	DstRef string
	Source string

	ID      int64
	At      int64
	Bytes   int64
	Packets int64

	SrcPort   int
	DstPort   int
	Proto     int
	VLAN      int
	IngressIf int
	EgressIf  int
}

// FlowSampleRepo is the flow_samples table repository.
type FlowSampleRepo struct {
	db *DB
}

// NewFlowSampleRepo constructs a FlowSampleRepo.
func NewFlowSampleRepo(db *DB) *FlowSampleRepo { return &FlowSampleRepo{db: db} }

// InsertBatch records samples in one transaction. A nil/empty slice is a
// no-op. Samples are immutable time-series points (like metric_samples;
// unlike metric_samples there is no natural (ref, at) dedup key — see the
// migration's doc comment — so every call always inserts len(samples) new
// rows).
func (r *FlowSampleRepo) InsertBatch(ctx context.Context, samples []FlowSample) error {
	if len(samples) == 0 {
		return nil
	}
	tx, err := r.db.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: beginning flow sample batch insert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO flow_samples
			(at, node, src_ip, dst_ip, src_port, dst_port, proto, bytes, packets, vlan, src_ref, dst_ref, ingress_if, egress_if, source)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("store: preparing flow sample insert: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, s := range samples {
		if _, err := stmt.ExecContext(ctx, s.At, s.Node, s.SrcIP, s.DstIP, s.SrcPort, s.DstPort, s.Proto,
			s.Bytes, s.Packets, s.VLAN, s.SrcRef, s.DstRef, s.IngressIf, s.EgressIf, s.Source,
		); err != nil {
			return fmt.Errorf("store: inserting flow sample: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: committing flow sample batch insert: %w", err)
	}
	return nil
}

// FlowFilter narrows FlowSampleRepo.Query's result set. Every non-zero
// field is ANDed, mirroring every other filter contract in this codebase
// (docs/api.md's GET /audit convention): an unrecognized/unparsable filter
// value (e.g. a malformed Subnet CIDR) matches nothing rather than
// erroring — the caller (internal/api/flows.go) is responsible for
// validating query params it wants to reject outright with a 400.
type FlowFilter struct {
	// Guest matches either SrcRef or DstRef exactly (an inventory Ref
	// string) — despite the name, this also matches a bridge/vnet-level ref
	// (see internal/flow.GraphResolver's doc comment on why a guest-nic-
	// level ref is not always resolvable).
	Guest  string
	Subnet string // CIDR; matches either SrcIP or DstIP within
	Source string
	VLAN   int
	Port   int // matches either SrcPort or DstPort
	Proto  int
	FromTs int64
	ToTs   int64
}

// defaultFlowPageLimit/flowSubnetScanMultiplier/maxFlowSubnetScanRows:
// Subnet containment cannot be expressed as a SQLite WHERE clause (IP
// addresses are stored as dotted-decimal/colon-hex TEXT, whose lexical
// order does not match numeric address order), so a Subnet-filtered query
// over-fetches a bounded scan window or ordinary rows and filters them in
// Go — see Query's doc comment for the resulting pagination contract.
const (
	defaultFlowPageLimit     = 200
	flowSubnetScanMultiplier = 8
	maxFlowSubnetScanRows    = 4000
)

// Query returns one page of flow_samples newest-first matching filter, per
// docs/api.md's `?limit=&cursor=` pagination convention (the same
// "<at>:<id>" keyset cursor scheme AuditRepo.ListPage/SnapshotRepo.ListPage
// use). An empty cursor starts from the newest row; nextCursor is empty
// once there is no further page.
//
// Subnet filtering note: every other filter is a plain SQL WHERE clause, so
// a page always returns exactly min(limit, available-matches-in-one-scan)
// items with no gaps. A Subnet filter instead scans up to
// flowSubnetScanMultiplier*limit (capped at maxFlowSubnetScanRows)
// underlying rows per call and returns whichever of those match — a page
// may therefore return fewer than limit items even when more matches exist
// further back in a very sparse subnet's history; callers should keep
// following nextCursor (as documented for every paginated route) until it
// is empty, exactly as they already must for an ordinary short final page.
func (r *FlowSampleRepo) Query(ctx context.Context, filter FlowFilter, cursor string, limit int) ([]FlowSample, string, error) {
	if limit <= 0 {
		limit = defaultFlowPageLimit
	}

	scanLimit := limit
	var subnetNet *net.IPNet
	if filter.Subnet != "" {
		_, ipnet, err := net.ParseCIDR(filter.Subnet)
		if err != nil {
			return nil, "", nil // unrecognized filter value: matches nothing, never a 400 (docs/api.md convention)
		}
		subnetNet = ipnet
		scanLimit = limit * flowSubnetScanMultiplier
		if scanLimit > maxFlowSubnetScanRows {
			scanLimit = maxFlowSubnetScanRows
		}
	}

	query := `SELECT id, at, node, src_ip, dst_ip, src_port, dst_port, proto, bytes, packets, vlan, src_ref, dst_ref, ingress_if, egress_if, source FROM flow_samples WHERE 1=1`
	var args []any
	if filter.Guest != "" {
		query += ` AND (src_ref = ? OR dst_ref = ?)`
		args = append(args, filter.Guest, filter.Guest)
	}
	if filter.Source != "" {
		query += ` AND source = ?`
		args = append(args, filter.Source)
	}
	if filter.VLAN != 0 {
		query += ` AND vlan = ?`
		args = append(args, filter.VLAN)
	}
	if filter.Port != 0 {
		query += ` AND (src_port = ? OR dst_port = ?)`
		args = append(args, filter.Port, filter.Port)
	}
	if filter.Proto != 0 {
		query += ` AND proto = ?`
		args = append(args, filter.Proto)
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
		at, id, err := decodeFlowCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		query += ` AND (at < ? OR (at = ? AND id < ?))`
		args = append(args, at, at, id)
	}
	query += ` ORDER BY at DESC, id DESC LIMIT ?`
	args = append(args, scanLimit+1)

	rows, err := r.db.sqlDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("store: querying flow samples: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var scanned []FlowSample
	for rows.Next() {
		s, err := scanFlowSample(rows)
		if err != nil {
			return nil, "", err
		}
		scanned = append(scanned, s)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("store: querying flow samples: %w", err)
	}

	hasMore := len(scanned) > scanLimit
	if hasMore {
		scanned = scanned[:scanLimit]
	}

	var items []FlowSample
	lastIdx := -1
	for i, s := range scanned {
		if subnetNet != nil {
			srcIn := subnetContains(subnetNet, s.SrcIP)
			dstIn := subnetContains(subnetNet, s.DstIP)
			if !srcIn && !dstIn {
				continue
			}
		}
		items = append(items, s)
		lastIdx = i
		if len(items) >= limit {
			break
		}
	}

	next := ""
	if lastIdx >= 0 && (lastIdx < len(scanned)-1 || hasMore) {
		next = encodeFlowCursor(scanned[lastIdx].At, scanned[lastIdx].ID)
	}
	return items, next, nil
}

func subnetContains(n *net.IPNet, ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	return n.Contains(ip)
}

// Count returns the total row count (test/observability helper — GET
// /flows itself never needs an unbounded count).
func (r *FlowSampleRepo) Count(ctx context.Context) (int64, error) {
	var n int64
	if err := r.db.sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM flow_samples`).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: counting flow samples: %w", err)
	}
	return n, nil
}

// PruneOlderThan deletes rows with at < cutoff, returning the number
// removed — the retention-window half of internal/flow's bound.
func (r *FlowSampleRepo) PruneOlderThan(ctx context.Context, cutoff int64) (int64, error) {
	res, err := r.db.sqlDB.ExecContext(ctx, `DELETE FROM flow_samples WHERE at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("store: pruning flow samples older than %d: %w", cutoff, err)
	}
	return rowsAffected(res, "flow samples")
}

// PruneToCap deletes the oldest rows until at most maxRows remain (by
// (at, id) descending — the same ordering Query returns), returning the
// number removed — the hard-row-cap half of internal/flow's bound. A
// maxRows <= 0 is a no-op (never interpreted as "delete everything").
func (r *FlowSampleRepo) PruneToCap(ctx context.Context, maxRows int64) (int64, error) {
	if maxRows <= 0 {
		return 0, nil
	}
	res, err := r.db.sqlDB.ExecContext(ctx, `
		DELETE FROM flow_samples WHERE id IN (
			SELECT id FROM flow_samples ORDER BY at DESC, id DESC LIMIT -1 OFFSET ?
		)`, maxRows)
	if err != nil {
		return 0, fmt.Errorf("store: pruning flow samples to cap %d: %w", maxRows, err)
	}
	return rowsAffected(res, "flow samples")
}

func rowsAffected(res sql.Result, what string) (int64, error) {
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: counting pruned %s: %w", what, err)
	}
	return n, nil
}

func encodeFlowCursor(at, id int64) string {
	return strconv.FormatInt(at, 10) + ":" + strconv.FormatInt(id, 10)
}

func decodeFlowCursor(cursor string) (int64, int64, error) {
	atStr, idStr, ok := strings.Cut(cursor, ":")
	if !ok {
		return 0, 0, fmt.Errorf("store: malformed flow cursor %q", cursor)
	}
	at, err := strconv.ParseInt(atStr, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("store: malformed flow cursor %q: %w", cursor, err)
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("store: malformed flow cursor %q: %w", cursor, err)
	}
	return at, id, nil
}

func scanFlowSample(row rowScanner) (FlowSample, error) {
	var s FlowSample
	err := row.Scan(&s.ID, &s.At, &s.Node, &s.SrcIP, &s.DstIP, &s.SrcPort, &s.DstPort, &s.Proto,
		&s.Bytes, &s.Packets, &s.VLAN, &s.SrcRef, &s.DstRef, &s.IngressIf, &s.EgressIf, &s.Source,
	)
	if err != nil {
		return FlowSample{}, fmt.Errorf("store: scanning flow sample: %w", err)
	}
	return s, nil
}
