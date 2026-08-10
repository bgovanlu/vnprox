//go:build soakleak

// soakleak.go holds T-2504's deliberate resource-leak fixtures. It is
// compiled ONLY under the `soakleak` build tag, which nothing in this
// repository's build, test, lint, package, or release path ever sets — see
// soakleak_off.go for the no-op halves that every real build gets instead.
//
// Why a gate ships with fixtures that break it (planning/
// implementation-plan-adopted.md, "Every guard ships with a fixture that
// makes it fire"): a leak gate with only a passing fixture is worse than no
// gate, because it is trusted. These three fixtures are the evidence that
// `make soak` detects something no other test in this repository detects.
// Two of them must fail the gate; one of them must NOT.
//
//	make soak LEAK=goroutine   AC1 — one goroutine leaked per PVE collection
//	                           cycle. Must FAIL, naming "goroutines".
//	make soak LEAK=table       AC2 — a table nobody prunes, growing every
//	                           second. Must FAIL, naming the table.
//	make soak LEAK=flat        AC3 — a large, one-time allocation of
//	                           goroutines and heap, held for the daemon's
//	                           lifetime. Must PASS: the gate measures slope,
//	                           and a high-but-flat value is not a leak.
//
// The mode is chosen by the VNPROX_SOAK_LEAK environment variable rather
// than by three separate build tags, so that "run the entire existing test
// suite against the leaky build" (AC2's second half) is one command per
// mode instead of one build matrix. An unset variable under this tag means
// no leak; an unrecognized one refuses to start the daemon rather than
// silently running clean, because a typo that quietly disables the fixture
// would turn this file's own verification into a lie.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/bgovanlu/vnprox/internal/store"
)

// LeakTableName is the table the `table` fixture grows without bound. It is
// created by the fixture itself rather than by a migration, which is
// exactly the shape of the defect this gate exists to catch: a table that
// appears in the schema and that nobody wrote a prune loop for. The soak
// gate enumerates tables from sqlite_master, so it watches this one without
// anybody adding it to a list.
const LeakTableName = "soak_leak_unbounded"

// leakEnvVar selects which fixture is active.
const leakEnvVar = "VNPROX_SOAK_LEAK"

// Fixture intensities. Each is chosen to be unmistakable against the
// tolerances cmd/vnproxd/soak_test.go gates on, without being so violent
// that it exhausts the machine before the run finishes.
const (
	// leakTableRowsPerTick x one tick per leakTableInterval = 300 rows/min.
	leakTableRowsPerTick = 5
	leakTableInterval    = time.Second
	// flatGoroutines is well above the daemon's own steady-state count, so
	// a gate that secretly looked at the absolute value would fail AC3.
	flatGoroutines = 500
	// flatHeapBytes is likewise far above the daemon's live heap.
	flatHeapBytes = 64 << 20
)

// soakLeakPollHook wraps the collector's OnPoll hook. In `goroutine` mode
// it spawns one goroutine per *PVE* collection cycle that blocks until the
// process exits — the textbook "a goroutine per cycle" leak, at the
// collector's own 10s cadence (6/min), deliberately slow enough that only a
// trend gate would ever see it.
func soakLeakPollHook(next func(source, node string, dur time.Duration, err error)) func(source, node string, dur time.Duration, err error) {
	if leakMode() != "goroutine" {
		return next
	}
	return func(source, node string, dur time.Duration, err error) {
		if next != nil {
			next(source, node, dur, err)
		}
		if source != "pve" {
			return
		}
		go func() {
			// No shutdown path, on purpose. This is the fixture.
			select {}
		}()
	}
}

// soakLeakActors returns the run-group actors the selected fixture needs.
//
// Every actor here honours the runGroup contract documented on runGroup.run:
// it blocks for the daemon's lifetime and returns nil on context
// cancellation. An actor that returned early would cancel every other actor
// and shut the daemon down at startup, and the soak would measure nothing.
// The one deliberate exception is an unrecognized mode, which returns an
// error immediately and *should* stop the daemon.
func soakLeakActors(db *store.DB, logger *slog.Logger) []actor {
	mode := leakMode()
	switch mode {
	case "":
		return nil
	case "goroutine":
		// Handled entirely by soakLeakPollHook above; no actor needed.
		logger.Warn("soak-leak fixture ACTIVE: leaking one goroutine per PVE collection cycle (T-2504 AC1)")
		return nil
	case "table":
		logger.Warn("soak-leak fixture ACTIVE: growing an unpruned table (T-2504 AC2)",
			"table", LeakTableName, "rows_per_tick", leakTableRowsPerTick, "tick", leakTableInterval)
		return []actor{unboundedTableActor(db, logger)}
	case "flat":
		logger.Warn("soak-leak fixture ACTIVE: allocating once and holding (T-2504 AC3 — this one must PASS)",
			"goroutines", flatGoroutines, "heap_bytes", flatHeapBytes)
		return []actor{flatAllocationActor(logger)}
	default:
		return []actor{func(context.Context) error {
			return fmt.Errorf("%s=%q is not a known soak-leak fixture (want one of: goroutine, table, flat)", leakEnvVar, mode)
		}}
	}
}

func leakMode() string { return os.Getenv(leakEnvVar) }

// unboundedTableActor is AC2: rows in, never out. No prune loop, no
// retention, no cap — the exact omission that has bitten this project
// before (audit phase-0 F-01, metric_samples growing forever until
// RunPruneLoop was wired).
func unboundedTableActor(db *store.DB, logger *slog.Logger) actor {
	return func(ctx context.Context) error {
		if _, err := db.ExecContext(ctx,
			`CREATE TABLE IF NOT EXISTS `+LeakTableName+` (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				created_at TEXT NOT NULL,
				payload TEXT NOT NULL
			)`); err != nil {
			return fmt.Errorf("creating the %s leak fixture table: %w", LeakTableName, err)
		}

		ticker := time.NewTicker(leakTableInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil // the runGroup contract: nil on cancellation
			case <-ticker.C:
			}
			for range leakTableRowsPerTick {
				if _, err := db.ExecContext(ctx,
					`INSERT INTO `+LeakTableName+` (created_at, payload) VALUES (?, ?)`,
					time.Now().UTC().Format(time.RFC3339Nano),
					"soak leak fixture row — T-2504 AC2"); err != nil {
					if ctx.Err() != nil {
						return nil
					}
					logger.Error("soak-leak fixture: insert failed", "table", LeakTableName, "error", err)
					break
				}
			}
		}
	}
}

// flatAllocationActor is AC3: allocate a lot, once, at startup, and hold it
// for the daemon's lifetime. Goroutine count and heap both sit far above
// their normal values and both stay perfectly flat, so a gate that measured
// absolute values would fail this and a gate that measures slope must pass
// it.
func flatAllocationActor(logger *slog.Logger) actor {
	return func(ctx context.Context) error {
		slab := make([]byte, flatHeapBytes)
		for i := range slab {
			// Touched so the pages are really resident, not just reserved:
			// an untouched allocation would not move RSS.
			slab[i] = byte(i)
		}

		var wg sync.WaitGroup
		for range flatGoroutines {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-ctx.Done()
			}()
		}
		logger.Warn("soak-leak fixture: flat allocation in place", "goroutines_now", runtime.NumGoroutine())

		<-ctx.Done()
		wg.Wait()
		runtime.KeepAlive(slab)
		return nil // the runGroup contract: nil on cancellation
	}
}
