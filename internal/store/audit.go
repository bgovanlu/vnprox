package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// AuditEntry is one row of the audit_log table (docs/data-model.md §2).
// Every mutation attempt (including denied and rolled-back) is recorded
// here; per docs/security.md "Audit", entries are append-only at the API
// layer, so this repository intentionally has no Update or Delete.
type AuditEntry struct {
	Username string
	Action   string
	Result   string
	// ClusterID (T-1201) tags which attached cluster the audited action
	// targeted; '' is the implicit default/local cluster, so every
	// pre-federation row keeps its meaning. GET /audit's cluster-dimension
	// fan-out (docs/architecture §7) reads this.
	ClusterID string
	// IP (T-2902) is the requesting client's source IP, stamped into the
	// request context by internal/api's audit-IP middleware and copied here
	// by every append site — the field docs/security.md's Audit section
	// always claimed. '' means "no HTTP client behind this row" (pre-0047
	// rows, confirm-timer rollbacks, system actions), per 0047_audit_ip.sql.
	IP          string
	Target      sql.NullString
	ChangesetID sql.NullString
	DetailJSON  sql.NullString
	ID          int64
	At          int64
}

// auditIPKey carries the requesting client's IP through a request context
// to whatever append site eventually writes the audit row — internal/api
// sets it once per request, so neither the change engine nor auth needs an
// extra parameter on every call path in between (T-2902).
type auditIPKey struct{}

// WithAuditClientIP returns ctx carrying ip for AuditClientIPFromContext.
func WithAuditClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, auditIPKey{}, ip)
}

// AuditClientIPFromContext returns the client IP WithAuditClientIP stored,
// or "" — the "no HTTP client" value AuditEntry.IP documents.
func AuditClientIPFromContext(ctx context.Context) string {
	ip, _ := ctx.Value(auditIPKey{}).(string)
	return ip
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
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO audit_log (at, username, action, target, changeset_id, result, detail_json, cluster_id, ip)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.At, e.Username, e.Action, e.Target, e.ChangesetID, e.Result, e.DetailJSON, e.ClusterID, e.IP,
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
	row := r.db.QueryRowContext(ctx, `
		SELECT id, at, username, action, target, changeset_id, result, detail_json, cluster_id, ip
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
	query := `SELECT id, at, username, action, target, changeset_id, result, detail_json, cluster_id, ip FROM audit_log`
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

	rows, err := r.db.QueryContext(ctx, query, args...)
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
	query := `SELECT id, at, username, action, target, changeset_id, result, detail_json, cluster_id, ip FROM audit_log WHERE 1=1`
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

	rows, err := r.db.QueryContext(ctx, query, args...)
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
	query := `SELECT id, at, username, action, target, changeset_id, result, detail_json, cluster_id, ip FROM audit_log WHERE action IN (` +
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

	rows, err := r.db.QueryContext(ctx, query, args...)
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

// UpsertReplicated inserts an audit row with its ORIGINAL id preserved, doing
// nothing if a row with that id already exists — the id-preserving,
// append-only write T-1704's HA replication uses to mirror the active's audit
// log onto the standby. The audit log is immutable (docs/security.md's Audit
// section — no Update/Delete), so a re-replicated row is a no-op rather than
// an overwrite; the standby's copy therefore converges to the active's without
// ever mutating or re-ordering an already-present entry. It deliberately does
// NOT fire the onAppend hook (that hook is T-1104's live event producer for
// this daemon's OWN mutations — a standby must not re-emit the active's events
// as if it had just performed them).
func (r *AuditRepo) UpsertReplicated(ctx context.Context, e AuditEntry) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO audit_log (id, at, username, action, target, changeset_id, result, detail_json, cluster_id, ip)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO NOTHING`,
		e.ID, e.At, e.Username, e.Action, e.Target, e.ChangesetID, e.Result, e.DetailJSON, e.ClusterID, e.IP,
	)
	if err != nil {
		return fmt.Errorf("store: upserting replicated audit entry %d: %w", e.ID, err)
	}
	return nil
}

// MaxAuditID returns the highest audit_log id present, or 0 if the log is
// empty — T-1704's HA replication reads it to request only rows the standby
// has not yet seen (an incremental audit cursor), rather than re-shipping the
// whole log every pass.
func (r *AuditRepo) MaxAuditID(ctx context.Context) (int64, error) {
	var max sql.NullInt64
	err := r.db.QueryRowContext(ctx, `SELECT MAX(id) FROM audit_log`).Scan(&max)
	if err != nil {
		return 0, fmt.Errorf("store: reading max audit id: %w", err)
	}
	if !max.Valid {
		return 0, nil
	}
	return max.Int64, nil
}

// ListSince returns audit rows with id greater than sinceID, oldest id first,
// capped at limit (limit <= 0 means the default page size) — T-1704's HA
// replication feed. Ordered by id ascending so the standby applies them in the
// same order the active wrote them.
func (r *AuditRepo) ListSince(ctx context.Context, sinceID int64, limit int) ([]AuditEntry, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, at, username, action, target, changeset_id, result, detail_json, cluster_id, ip
		FROM audit_log WHERE id > ? ORDER BY id ASC LIMIT ?`, sinceID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("store: listing audit entries since %d: %w", sinceID, err)
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
		return nil, fmt.Errorf("store: listing audit entries since %d: %w", sinceID, err)
	}
	return out, nil
}

// DefaultAuditRetentionDays is T-1905's documented audit_log age cap
// ([retention] audit_keep_days). Every other bounded table in this arc is a
// short operational ring (metric_samples 24h, flow/latency/wan samples
// 60m) or a downsampled long-horizon rollup (capacity_aggregates ~13
// months); audit_log is neither — it is the compliance/forensic record of
// "who did what to the network, and was it allowed" (docs/security.md's
// Audit section: "every mutation attempt, including denied and rolled
// back"), the exact artifact an operator reaches for after an incident or
// a compliance review, often long after the changeset itself is history.
//
// 730 days (2 years) is chosen deliberately, not guessed: common
// compliance regimes vnprox deployments plausibly fall under ask for
// 1 year of change-control history as a floor (SOC 2, PCI-DSS), some ask
// for longer; 2 years gives an operator margin over an annual audit cycle
// without treating the table as a literal forever-warehouse, which is
// exactly the unbounded-growth failure mode this whole card exists to
// close (a full root filesystem is a worse audit story than a pruned
// year-three row). Unlike RetentionConfig's snapshot fields, there is no
// separate "pin" floor here: audit rows carry no rollback dependency (see
// SnapshotRepo.Prune's AC2 guardrail for the table that does), so age
// alone decides. An operator under a longer regulatory retention
// requirement configures a larger audit_keep_days; there is no "0 =
// forever" escape hatch (config.Validate requires a positive value,
// matching RetentionConfig's existing snapshot fields) — an unbounded
// table is exactly what this card was written to prevent, so "keep
// forever" is expressed by configuring a very large number, not by
// disabling the ceiling.
const DefaultAuditRetentionDays = 730

// Prune deletes audit_log rows older than the given cutoff (unix seconds),
// returning the number of rows removed. Callers/tests should compute
// cutoff themselves; PruneRetention wraps this with the configured
// audit_keep_days window and wall-clock time. There is no in-flight/pin
// guardrail here (contrast SnapshotRepo.Prune) — an audit row is a
// historical record of an already-attempted action, never something a live
// rollback reads back from, so age alone is safe to prune on.
func (r *AuditRepo) Prune(ctx context.Context, cutoff int64) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM audit_log WHERE at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("store: pruning audit_log older than %d: %w", cutoff, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: counting pruned audit_log rows: %w", err)
	}
	return n, nil
}

// PruneRetention deletes audit_log rows older than keepDays (falling back
// to DefaultAuditRetentionDays if keepDays <= 0), measured from now.
func (r *AuditRepo) PruneRetention(ctx context.Context, now time.Time, keepDays int) (int64, error) {
	if keepDays <= 0 {
		keepDays = DefaultAuditRetentionDays
	}
	cutoff := now.AddDate(0, 0, -keepDays).Unix()
	return r.Prune(ctx, cutoff)
}

// RunPruneLoop runs PruneRetention every interval until ctx is cancelled,
// logging failures via logFn (nil discards them) rather than stopping the
// loop, matching MetricSampleRepo.RunPruneLoop's contract
// (func(ctx context.Context) error, suitable for cmd/vnproxd's runGroup).
func (r *AuditRepo) RunPruneLoop(ctx context.Context, interval time.Duration, keepDays int, logFn func(err error)) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			if _, err := r.PruneRetention(ctx, now, keepDays); err != nil && logFn != nil &&
				!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				logFn(fmt.Errorf("store: pruning audit_log: %w", err))
			}
		}
	}
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
	err := row.Scan(&e.ID, &e.At, &e.Username, &e.Action, &e.Target, &e.ChangesetID, &e.Result, &e.DetailJSON, &e.ClusterID, &e.IP)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AuditEntry{}, err
		}
		return AuditEntry{}, fmt.Errorf("store: scanning audit entry: %w", err)
	}
	return e, nil
}
