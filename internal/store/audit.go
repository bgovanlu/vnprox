package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// AuditEntry is one row of the audit_log table (docs/data-model.md §2).
// Every mutation attempt (including denied and rolled-back) is recorded
// here; per docs/security.md "Audit", entries are append-only at the API
// layer, so this repository intentionally has no Update or Delete.
type AuditEntry struct {
	Username    string
	Action      string
	Result      string
	// ClusterID (T-1201) tags which attached cluster the audited action
	// targeted; '' is the implicit default/local cluster, so every
	// pre-federation row keeps its meaning. GET /audit's cluster-dimension
	// fan-out (docs/architecture §7) reads this.
	ClusterID   string
	Target      sql.NullString
	ChangesetID sql.NullString
	DetailJSON  sql.NullString
	ID          int64
	At          int64
}

// AuditRepo is the audit_log table repository.
type AuditRepo struct {
	db       *DB
	onAppend func(AuditEntry)
}

// NewAuditRepo constructs an AuditRepo.
func NewAuditRepo(db *DB) *AuditRepo { return &AuditRepo{db: db} }

// SetOnAppend registers fn to be called (synchronously, after the insert
// commits) with every entry Append writes, id included — T-1104's
// `audit.appended` WS/webhook event producer hook. cmd/vnproxd wires this
// once at composition root to marshal and broadcast the event; it is the
// single place that watches every mutation attempt this daemon's audit log
// records (append-only, docs/security.md's Audit section — "every
// mutation attempt, including denied and rolled-back"), so no call site
// that already calls Append needs to separately know about the events
// stream. fn is invoked from whichever goroutine called Append — it must
// not block or panic; a nil fn (the default) means "no hook", matching
// every other optional-callback convention in this codebase (e.g.
// internal/drift.Config.OnChange).
func (r *AuditRepo) SetOnAppend(fn func(AuditEntry)) { r.onAppend = fn }

// Append inserts a new audit entry and returns its assigned id.
func (r *AuditRepo) Append(ctx context.Context, e AuditEntry) (int64, error) {
	res, err := r.db.sqlDB.ExecContext(ctx, `
		INSERT INTO audit_log (at, username, action, target, changeset_id, result, detail_json, cluster_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.At, e.Username, e.Action, e.Target, e.ChangesetID, e.Result, e.DetailJSON, e.ClusterID,
	)
	if err != nil {
		return 0, fmt.Errorf("store: appending audit entry: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: reading audit entry id: %w", err)
	}
	if r.onAppend != nil {
		e.ID = id
		r.onAppend(e)
	}
	return id, nil
}

// Get returns the audit entry with the given id, or ErrNotFound.
func (r *AuditRepo) Get(ctx context.Context, id int64) (AuditEntry, error) {
	row := r.db.sqlDB.QueryRowContext(ctx, `
		SELECT id, at, username, action, target, changeset_id, result, detail_json, cluster_id
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
	query := `SELECT id, at, username, action, target, changeset_id, result, detail_json, cluster_id FROM audit_log`
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

// AuditFilter narrows AuditRepo.ListPage's result set (docs/features/
// change-management.md §8: "Filterable table (user, date range, target,
// result)"). Zero-value fields impose no constraint; From/To are unix
// seconds, inclusive, with 0 meaning "unbounded" on that side.
type AuditFilter struct {
	User        string
	Action      string
	Target      string
	Result      string
	ChangesetID string
	// ClusterID (T-1201) narrows to a single cluster's audit rows — the
	// per-cluster slice internal/federation.Aggregator fans out over for
	// GET /audit's cluster dimension. Empty imposes no constraint.
	ClusterID string
	From      int64
	To        int64
}

// ListPage returns one page of audit entries newest-first matching filter,
// per docs/api.md's `?limit=&cursor=` pagination convention. cursor is
// opaque (an "<at>:<id>" keyset token, see SnapshotRepo.ListPage's identical
// scheme); an empty string starts from the newest entry, and the returned
// nextCursor is empty once there is no further page.
func (r *AuditRepo) ListPage(ctx context.Context, filter AuditFilter, cursor string, limit int) ([]AuditEntry, string, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT id, at, username, action, target, changeset_id, result, detail_json, cluster_id FROM audit_log WHERE 1=1`
	var args []any
	if filter.User != "" {
		query += ` AND username = ?`
		args = append(args, filter.User)
	}
	if filter.Action != "" {
		query += ` AND action = ?`
		args = append(args, filter.Action)
	}
	if filter.Target != "" {
		query += ` AND target = ?`
		args = append(args, filter.Target)
	}
	if filter.Result != "" {
		query += ` AND result = ?`
		args = append(args, filter.Result)
	}
	if filter.ChangesetID != "" {
		query += ` AND changeset_id = ?`
		args = append(args, filter.ChangesetID)
	}
	if filter.ClusterID != "" {
		query += ` AND cluster_id = ?`
		args = append(args, filter.ClusterID)
	}
	if filter.From > 0 {
		query += ` AND at >= ?`
		args = append(args, filter.From)
	}
	if filter.To > 0 {
		query += ` AND at <= ?`
		args = append(args, filter.To)
	}
	if cursor != "" {
		at, id, err := decodeAuditCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		query += ` AND (at < ? OR (at = ? AND id < ?))`
		args = append(args, at, at, id)
	}
	query += ` ORDER BY at DESC, id DESC LIMIT ?`
	args = append(args, limit+1)

	rows, err := r.db.sqlDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("store: listing audit page: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []AuditEntry
	for rows.Next() {
		e, err := scanAuditEntry(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("store: listing audit page: %w", err)
	}

	next := ""
	if len(out) > limit {
		last := out[limit-1]
		next = encodeAuditCursor(last.At, last.ID)
		out = out[:limit]
	}
	return out, next, nil
}

// ChangesetLifecycleActions is the T-205 apply-engine lifecycle action set
// docs/api.md's Audit section documents (changeset.apply/confirm/rollback/
// timer_rearm/recover/safety_override) — the exact filter GET
// /history/events (internal/api/history.go, T-1007) narrows audit_log to
// for its changeset-marker half of the merged timeline feed.
var ChangesetLifecycleActions = []string{
	"changeset.apply",
	"changeset.confirm",
	"changeset.rollback",
	"changeset.timer_rearm",
	"changeset.recover",
	"changeset.safety_override",
}

// ListActionsInRange returns audit_log rows whose action is one of actions
// and whose at falls within [from, to] (0 on either side means "unbounded
// on that side"), ordered by at ascending — GET /history/events' (T-1007)
// changeset-lifecycle input. Unlike ListPage's newest-first cursor
// pagination, this route has no pagination contract of its own (its task
// card only names fromTs/toTs), so this method returns the whole matching
// set for the requested range. An empty actions slice returns (nil, nil)
// rather than every row — a caller should never rely on that as "no
// filter", it is simply the "nothing was asked for" case.
func (r *AuditRepo) ListActionsInRange(ctx context.Context, actions []string, from, to int64) ([]AuditEntry, error) {
	if len(actions) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(actions))
	args := make([]any, 0, len(actions)+2)
	for i, a := range actions {
		placeholders[i] = "?"
		args = append(args, a)
	}
	query := `SELECT id, at, username, action, target, changeset_id, result, detail_json, cluster_id FROM audit_log WHERE action IN (` +
		strings.Join(placeholders, ",") + `)`
	if from > 0 {
		query += ` AND at >= ?`
		args = append(args, from)
	}
	if to > 0 {
		query += ` AND at <= ?`
		args = append(args, to)
	}
	query += ` ORDER BY at ASC, id ASC`

	rows, err := r.db.sqlDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: listing audit entries by action in range: %w", err)
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
		return nil, fmt.Errorf("store: listing audit entries by action in range: %w", err)
	}
	return out, nil
}

func encodeAuditCursor(at, id int64) string {
	return strconv.FormatInt(at, 10) + ":" + strconv.FormatInt(id, 10)
}

func decodeAuditCursor(cursor string) (int64, int64, error) {
	atStr, idStr, ok := strings.Cut(cursor, ":")
	if !ok {
		return 0, 0, fmt.Errorf("store: malformed audit cursor %q", cursor)
	}
	at, err := strconv.ParseInt(atStr, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("store: malformed audit cursor %q: %w", cursor, err)
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("store: malformed audit cursor %q: %w", cursor, err)
	}
	return at, id, nil
}

func scanAuditEntry(row rowScanner) (AuditEntry, error) {
	var e AuditEntry
	err := row.Scan(&e.ID, &e.At, &e.Username, &e.Action, &e.Target, &e.ChangesetID, &e.Result, &e.DetailJSON, &e.ClusterID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AuditEntry{}, err
		}
		return AuditEntry{}, fmt.Errorf("store: scanning audit entry: %w", err)
	}
	return e, nil
}
