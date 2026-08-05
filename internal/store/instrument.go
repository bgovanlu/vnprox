package store

// instrument.go implements T-1903's store self-observability: a query-
// duration observer and pull-model reads of the store's on-disk size and
// schema state (docs/features/monitoring.md §9,
// internal/api/metrics_exporter.go's StoreInfoProvider).
//
// Design: every repository in this package (ChangesetRepo, AuditRepo, ...)
// already calls its shared *DB's sqlDB field directly (`r.db.sqlDB.
// ExecContext(...)`, etc.) rather than through a wrapper — adding timing to
// each of that package's ~200 individual call sites would be both a much
// larger diff and a much larger risk surface for this task. Instead, *DB
// itself grows ExecContext/QueryContext/QueryRowContext/BeginTx methods
// with the exact same signatures as *sql.DB's own, timing around a
// pass-through call to d.sqlDB's real method — and every repository's call
// sites were mechanically changed from `r.db.sqlDB.Foo(` to `r.db.Foo(`.
// This is a pure timing wrapper: the underlying *sql.DB, driver, and query
// text are completely unchanged, so behavior is identical to before this
// task for every caller that doesn't care about the new
// vnprox_store_query_duration_seconds series.
//
// op labels are a SQL statement's leading verb only (queryOp below) — never
// the query text itself, which is a much larger and less useful label
// (this package's ~200 call sites carry ~200 distinct literal query
// strings) for what is meant to be a coarse "is the store slow right now"
// signal, not per-statement profiling (which the audit trail / a real
// profiler is better suited for anyway).
import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"
)

// QueryObserver is called after every ExecContext/QueryContext/
// QueryRowContext/BeginTx call *DB makes, with the statement's op label
// (queryOp) and its duration. QueryRowContext cannot report its own error
// synchronously (database/sql defers it until Scan) so it always reports
// err as nil — the duration is still meaningful (the query itself already
// ran by the time QueryRowContext returns).
type QueryObserver func(op string, dur time.Duration, err error)

// SetQueryObserver installs obs as d's query-duration observer (nil clears
// it — the default, "not observed", matching every other Config/wiring
// field in this codebase's nil-safe-optional-dependency convention).
// cmd/vnproxd calls this once at startup with a closure into the daemon's
// internal/metrics.Registry; every other production and test caller of
// store.Open is unaffected.
func (d *DB) SetQueryObserver(obs QueryObserver) {
	d.obsMu.Lock()
	defer d.obsMu.Unlock()
	d.obs = obs
}

func (d *DB) observe(op string, start time.Time, err error) {
	d.obsMu.RLock()
	obs := d.obs
	d.obsMu.RUnlock()
	if obs != nil {
		obs(op, time.Since(start), err)
	}
}

// queryOp reduces a SQL statement to its leading verb, lowercased —
// docs/features/monitoring.md §9's closed, six-value label vocabulary for
// vnprox_store_query_duration_seconds' "op" label.
func queryOp(query string) string {
	trimmed := strings.TrimSpace(query)
	end := strings.IndexAny(trimmed, " \t\n")
	verb := trimmed
	if end >= 0 {
		verb = trimmed[:end]
	}
	switch strings.ToUpper(verb) {
	case "SELECT":
		return "select"
	case "INSERT":
		return "insert"
	case "UPDATE":
		return "update"
	case "DELETE":
		return "delete"
	default:
		return "other"
	}
}

// ExecContext times and delegates to the underlying *sql.DB's ExecContext —
// see this file's doc comment.
func (d *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	start := time.Now()
	res, err := d.sqlDB.ExecContext(ctx, query, args...)
	d.observe(queryOp(query), start, err)
	return res, err
}

// QueryContext times and delegates to the underlying *sql.DB's QueryContext.
func (d *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	start := time.Now()
	rows, err := d.sqlDB.QueryContext(ctx, query, args...)
	d.observe(queryOp(query), start, err)
	return rows, err
}

// QueryRowContext times and delegates to the underlying *sql.DB's
// QueryRowContext. Its error is not visible here (database/sql defers a
// *sql.Row's error until Scan) — the recorded observation always carries a
// nil error; the duration is still accurate, since the query itself
// executes synchronously inside this call.
func (d *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	start := time.Now()
	row := d.sqlDB.QueryRowContext(ctx, query, args...)
	d.observe(queryOp(query), start, nil)
	return row
}

// BeginTx times and delegates to the underlying *sql.DB's BeginTx, reported
// under the op label "tx". Statements issued against the returned *sql.Tx
// are not individually observed (they bypass *DB entirely) — this
// measures only the transaction's begin/commit-visible-effects boundary as
// seen by *DB's own caller, not each statement inside it.
func (d *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	start := time.Now()
	tx, err := d.sqlDB.BeginTx(ctx, opts)
	d.observe("tx", start, err)
	return tx, err
}

// SizeBytes returns d's current on-disk footprint: the main database file
// plus its WAL/SHM sidecars (present only once WAL mode has written to
// them at least once), summed. T-1903's GET /metrics
// vnprox_store_size_bytes gauge (api.StoreInfoProvider).
func (d *DB) SizeBytes() (int64, error) {
	var total int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Stat(d.path + suffix)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, fmt.Errorf("store: stat %s%s: %w", d.path, suffix, err)
		}
		total += info.Size()
	}
	return total, nil
}

// SchemaVersion returns d's currently-applied schema version and the
// highest version this binary's embedded migrations know about ("latest").
// T-1903's GET /metrics vnprox_store_schema_version/
// vnprox_store_schema_migration_pending gauges (api.StoreInfoProvider);
// also the seam T-1904's `vnproxctl doctor` schema-vs-binary check is
// expected to reuse rather than re-deriving.
func (d *DB) SchemaVersion(ctx context.Context) (current, latest int, err error) {
	current, err = currentSchemaVersion(ctx, d.sqlDB)
	if err != nil {
		return 0, 0, fmt.Errorf("store: reading current schema version: %w", err)
	}
	migrations, err := loadMigrations()
	if err != nil {
		return 0, 0, fmt.Errorf("store: loading embedded migrations: %w", err)
	}
	return current, latestSchemaVersion(migrations), nil
}
