package store

// compact.go implements T-1905's compaction path: reclaiming the disk space
// retention pruning frees up, without blocking the daemon's reads.
//
// Why not a plain VACUUM: SQLite's ordinary VACUUM command rebuilds the
// whole database file inside one exclusive-lock transaction, which blocks
// every other reader and writer for its full duration — directly the wrong
// shape for a live daemon (T-1905 AC3: "the daemon serves reads
// throughout"). VACUUM INTO (internal/store.SnapshotTo, T-1901) avoids that
// by writing a fresh copy elsewhere, but swapping that copy in for the live
// file while cmd/vnproxd's *sql.DB connection pool still holds the old file
// open is unsafe: existing pooled connections keep their file descriptors
// on the old (renamed-away) inode, so writes after a live swap would land
// somewhere nothing ever reads again. Restoring a compacted copy into place
// is therefore restore's job (internal/backup, T-1901), which already
// refuses to run against a live daemon for exactly this reason — compaction
// must not require that.
//
// The mechanism that actually fits "reclaim space, keep serving reads" is
// SQLite's own incremental auto-vacuum: once a database is in
// auto_vacuum=INCREMENTAL mode, `PRAGMA incremental_vacuum(N)` moves up to
// N pages from the free list to the end of the file and truncates them off
// — an ordinary write transaction through the *same* connection pool
// everything else already uses. Under WAL mode (internal/store.Open's
// journal_mode(WAL)) a writer never blocks a reader: every concurrent
// SELECT continues against its own snapshot for the whole call. This is
// the identical non-blocking property T-1901's SnapshotTo/VACUUM INTO
// already leans on, just applied to shrinking the live file instead of
// writing a side copy of it.
//
// The one genuinely expensive step is switching an EXISTING database
// (created before this task, so auto_vacuum=NONE, the SQLite default) into
// incremental mode: that requires one full VACUUM to physically reorganize
// the file, which DOES take the exclusive lock for its duration —
// unavoidable, since it is rebuilding the whole file layout. That is why
// EnsureIncrementalVacuum is a separate, explicit, one-time step
// (cmd/vnproxd calls it once at startup, before the daemon starts serving,
// the same timing class as schema migrations) rather than something
// RunCompactionLoop ever does implicitly on a live daemon. A brand-new
// store (an empty file) converts near-instantly; an existing large store's
// first startup after upgrading past this task pays that cost once —
// documented in docs/deployment.md's sizing section so it is not a
// surprise.
import (
	"context"
	"errors"
	"fmt"
	"time"
)

// DefaultCompactionMaxPages bounds how many pages a single Compact call
// reclaims via PRAGMA incremental_vacuum — kept modest (2,000 pages; 8MB at
// SQLite's default 4KB page size) so each call is a short, ordinary write
// transaction rather than a long one that could meaningfully compete with
// other writers. RunCompactionLoop calls Compact repeatedly on its own
// schedule, so a large backlog of freed pages is reclaimed over several
// ticks rather than in one long call.
const DefaultCompactionMaxPages = 2000

// DefaultCompactionInterval is how often RunCompactionLoop reclaims a
// batch. Coarser than the retention prune loops that feed it freed pages
// (snapshotRetentionInterval/metricPruneInterval etc. in cmd/vnproxd) —
// compaction is a housekeeping pass over whatever those already freed, not
// something that needs to race them.
const DefaultCompactionInterval = 6 * time.Hour

// autoVacuumIncremental is PRAGMA auto_vacuum's integer value for
// "incremental" mode (SQLite's own vocabulary: 0=NONE, 1=FULL,
// 2=INCREMENTAL).
const autoVacuumIncremental = 2

// EnsureIncrementalVacuum switches db onto SQLite's incremental auto-vacuum
// mode if it is not there already, so Compact's PRAGMA incremental_vacuum
// calls have a free list to reclaim from. Idempotent and safe to call on
// every daemon startup: a database already in incremental mode (every
// store this function has previously converted, and any store created
// after this task ships, since auto_vacuum is a persistent per-file
// property recorded in the SQLite header) is detected via `PRAGMA
// auto_vacuum` and left untouched — converted reports false and took is
// zero.
//
// See this file's package doc comment for why the one-time conversion (a
// full VACUUM) is deliberately not folded into Compact/RunCompactionLoop:
// it is the one part of this file that DOES briefly hold the exclusive
// lock, so callers must run it before the daemon starts serving, never
// from a periodic loop against a live store.
func EnsureIncrementalVacuum(ctx context.Context, db *DB) (converted bool, took time.Duration, err error) {
	mode, err := autoVacuumMode(ctx, db)
	if err != nil {
		return false, 0, err
	}
	if mode == autoVacuumIncremental {
		return false, 0, nil
	}

	start := time.Now()
	if _, err := db.ExecContext(ctx, `PRAGMA auto_vacuum = INCREMENTAL`); err != nil {
		return false, 0, fmt.Errorf("store: setting auto_vacuum=incremental: %w", err)
	}
	// The mode change above only takes effect once the file is physically
	// rebuilt — SQLite's own documented requirement for changing
	// auto_vacuum on a non-empty database.
	if _, err := db.ExecContext(ctx, `VACUUM`); err != nil {
		return false, 0, fmt.Errorf("store: vacuuming to apply incremental auto-vacuum: %w", err)
	}
	return true, time.Since(start), nil
}

// Compact reclaims up to maxPages (DefaultCompactionMaxPages if <= 0) of
// freed space from db via `PRAGMA incremental_vacuum(N)`, then checkpoints
// and truncates the WAL (`PRAGMA wal_checkpoint(TRUNCATE)`) so the freed
// space is reflected in SizeBytes's main-file measurement rather than
// sitting invisibly in an un-checkpointed -wal sidecar. It returns the
// number of bytes SizeBytes reports as reclaimed (never negative — a
// concurrent writer growing the WAL between the two measurements is
// reported as 0 reclaimed, not a negative number).
//
// If db has not yet been converted via EnsureIncrementalVacuum, this is a
// documented, cheap no-op (auto_vacuum=NONE has no incremental free list to
// walk) rather than an error — RunCompactionLoop degrades quietly on a
// store this daemon has not yet had the chance to convert, exactly like
// every other optional-precondition seam in this codebase (e.g.
// MetricsProvider/PeerTrustProvider's nil-provider skip in
// internal/findings).
//
// Each PRAGMA call here is one ordinary write transaction through db's own
// connection pool; under WAL mode a concurrent reader always proceeds
// against its own snapshot and is never blocked by it (T-1905 AC3 — see
// this file's package doc comment for the full reasoning).
func Compact(ctx context.Context, db *DB, maxPages int) (freedBytes int64, err error) {
	if maxPages <= 0 {
		maxPages = DefaultCompactionMaxPages
	}
	mode, err := autoVacuumMode(ctx, db)
	if err != nil {
		return 0, err
	}
	if mode != autoVacuumIncremental {
		return 0, nil
	}

	before, err := db.SizeBytes()
	if err != nil {
		return 0, fmt.Errorf("store: measuring size before compaction: %w", err)
	}
	if _, err = db.ExecContext(ctx, fmt.Sprintf(`PRAGMA incremental_vacuum(%d)`, maxPages)); err != nil {
		return 0, fmt.Errorf("store: running incremental_vacuum: %w", err)
	}
	if _, err = db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return 0, fmt.Errorf("store: checkpointing after compaction: %w", err)
	}
	after, err := db.SizeBytes()
	if err != nil {
		return 0, fmt.Errorf("store: measuring size after compaction: %w", err)
	}
	if before > after {
		return before - after, nil
	}
	return 0, nil
}

// RunCompactionLoop runs Compact every interval (DefaultCompactionInterval
// if <= 0) until ctx is cancelled, logging failures via logFn (nil
// discards them) rather than stopping the loop — mirrors
// RunSnapshotRetentionLoop's contract (func(ctx context.Context) error,
// suitable for cmd/vnproxd's runGroup).
func RunCompactionLoop(ctx context.Context, db *DB, interval time.Duration, maxPages int, logFn func(err error)) error {
	if interval <= 0 {
		interval = DefaultCompactionInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := Compact(ctx, db, maxPages); err != nil && logFn != nil &&
				!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				logFn(fmt.Errorf("store: compaction: %w", err))
			}
		}
	}
}

func autoVacuumMode(ctx context.Context, db *DB) (int, error) {
	var mode int
	if err := db.QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		return 0, fmt.Errorf("store: reading auto_vacuum mode: %w", err)
	}
	return mode, nil
}
