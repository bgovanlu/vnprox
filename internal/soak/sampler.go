package soak

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Metric names produced by the built-in samplers. Exported because a
// Policy's PerMetric map addresses them by name and a typo there silently
// falls back to Policy.Default — which would be a tolerance loosened by
// accident.
const (
	MetricGoroutines = "goroutines"
	MetricHeapBytes  = "heap_bytes"
	MetricRSSBytes   = "rss_bytes"
	MetricOpenFDs    = "open_fds"
)

// Sampler reads one metric. Name is the series key (and what a failure
// message names); Unit is a short noun for the message ("goroutines",
// "bytes", "rows", "fds").
//
// A Sampler must be cheap: Run calls every sampler on every tick, and a
// sampler that takes seconds distorts the interval it is being measured on.
type Sampler interface {
	Name() string
	Unit() string
	Sample(ctx context.Context) (float64, error)
}

type funcSampler struct {
	fn   func(ctx context.Context) (float64, error)
	name string
	unit string
}

func (s funcSampler) Name() string { return s.name }
func (s funcSampler) Unit() string { return s.unit }
func (s funcSampler) Sample(ctx context.Context) (float64, error) {
	return s.fn(ctx)
}

// SamplerFunc builds a Sampler from a closure — the seam tests use to feed
// a deliberately leaking or deliberately flat-but-high series through the
// real Run/Analyze path without a real daemon.
func SamplerFunc(name, unit string, fn func(ctx context.Context) (float64, error)) Sampler {
	return funcSampler{name: name, unit: unit, fn: fn}
}

// restingReads/restingGap define the "resting value" reduction Goroutines
// and OpenFDs use: take several readings a few milliseconds apart and keep
// the minimum.
//
// The point is to measure the daemon at rest rather than mid-request. A
// single reading catches whatever handler goroutines and client sockets
// happen to be in flight at that instant, which adds several units of
// jitter that is pure sampling artefact — and over a short window, jitter
// is what a least-squares fit turns into a spurious slope. A leaked
// goroutine or a leaked descriptor never goes away, so it survives the
// minimum; a transient one does not. This costs ~100ms per sample against a
// sample interval measured in seconds.
const (
	restingReads = 9
	restingGap   = 40 * time.Millisecond
)

func restingMin(ctx context.Context, read func() (float64, error)) (float64, error) {
	best, err := read()
	if err != nil {
		return 0, err
	}
	for range restingReads - 1 {
		select {
		case <-ctx.Done():
			return best, nil
		case <-time.After(restingGap):
		}
		v, err := read()
		if err != nil {
			return 0, err
		}
		best = min(best, v)
	}
	return best, nil
}

// Goroutines samples the resting runtime.NumGoroutine() of the current
// process (see restingReads).
//
// In-process by construction: the gate runs in the same process as the
// daemon it measures (see cmd/vnproxd/soak_test.go), so this counts the
// harness's own goroutines too. That is harmless for a *trend* gate — the
// harness's goroutine count is constant, so it contributes a constant
// offset and zero slope — and it is what makes a leak in an actor visible
// without a debug endpoint or a second process.
func Goroutines() Sampler {
	return SamplerFunc(MetricGoroutines, "goroutines", func(ctx context.Context) (float64, error) {
		return restingMin(ctx, func() (float64, error) { return float64(runtime.NumGoroutine()), nil })
	})
}

// Heap samples the live heap (runtime.MemStats.HeapAlloc), forcing a GC
// first when forceGC is set.
//
// Forcing a GC is the difference between measuring a leak and measuring the
// GC's sawtooth. HeapAlloc without a preceding collection includes every
// object that is already garbage but not yet swept, which swings by tens of
// megabytes between samples for reasons that have nothing to do with
// retention. After a forced GC, HeapAlloc is (approximately) the live set —
// the number that only grows if something is genuinely holding on. The cost
// is a real GC per sample, which is why the sample interval is seconds, not
// milliseconds.
func Heap(forceGC bool) Sampler {
	return SamplerFunc(MetricHeapBytes, "bytes", func(context.Context) (float64, error) {
		if forceGC {
			runtime.GC()
		}
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return float64(ms.HeapAlloc), nil
	})
}

// RSS samples resident set size in bytes from /proc/<pid>/status. pid 0
// means the current process.
//
// RSS is included alongside the heap because the two catch different
// things: a leak of Go objects moves the heap, while a leak of goroutine
// stacks, mmap'd SQLite pages, or cgo/off-heap allocations moves only RSS.
func RSS(pid int) Sampler {
	return SamplerFunc(MetricRSSBytes, "bytes", func(context.Context) (float64, error) {
		kb, err := readVmRSSKB(procPath(pid, "status"))
		if err != nil {
			return 0, err
		}
		return float64(kb) * 1024, nil
	})
}

// OpenFDs samples the resting number of open file descriptors from
// /proc/<pid>/fd (see restingReads). pid 0 means the current process. This
// is the sampler that catches a response body never closed or a listener
// never shut down — neither of which necessarily shows up in the heap.
func OpenFDs(pid int) Sampler {
	dir := procPath(pid, "fd")
	return SamplerFunc(MetricOpenFDs, "fds", func(ctx context.Context) (float64, error) {
		return restingMin(ctx, func() (float64, error) {
			entries, err := os.ReadDir(dir)
			if err != nil {
				return 0, fmt.Errorf("counting open fds: %w", err)
			}
			return float64(len(entries)), nil
		})
	})
}

func procPath(pid int, leaf string) string {
	if pid <= 0 {
		return "/proc/self/" + leaf
	}
	return "/proc/" + strconv.Itoa(pid) + "/" + leaf
}

func readVmRSSKB(path string) (int64, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a /proc path this package builds itself, not caller input
	if err != nil {
		return 0, fmt.Errorf("reading %s: %w", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, fmt.Errorf("parsing VmRSS from %s: malformed line %q", path, line)
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parsing VmRSS from %s: %w", path, err)
		}
		return kb, nil
	}
	return 0, fmt.Errorf("no VmRSS line in %s", path)
}

// ProcSamplersAvailable reports whether this host exposes the /proc entries
// RSS and OpenFDs read. Linux-only by design (vnprox ships to Proxmox VE);
// a caller on another platform should skip rather than fail.
func ProcSamplersAvailable() bool {
	if _, err := os.Stat("/proc/self/status"); err != nil {
		return false
	}
	_, err := os.Stat("/proc/self/fd")
	return err == nil
}

// TableSamplers builds one row-count sampler per user table in db, named
// TablePrefix+<table>.
//
// Every table is enumerated from sqlite_master rather than listed here on
// purpose. The failure this gate exists to catch is a table somebody added
// and forgot to prune, and a hand-maintained list would, by construction,
// not contain it — the new table would be exactly the one not watched. A
// table whose growth during a run is legitimate (a retention ring filling
// up toward a window far longer than the run) is handled where it belongs,
// as a stated per-table tolerance in the Policy, not by omitting it here.
//
// SQLite's own internal tables (sqlite_*) and the schema-migration ledger
// are excluded: they are not app-owned state.
func TableSamplers(ctx context.Context, db *sql.DB) ([]Sampler, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("listing tables for row-count samplers: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scanning table name: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing tables for row-count samplers: %w", err)
	}
	sort.Strings(names)

	samplers := make([]Sampler, 0, len(names))
	for _, name := range names {
		if !safeTableName(name) {
			// Unreachable for this repo's schema (migrations create plain
			// identifiers), but a row count is built by string concatenation
			// below — SQLite has no bind parameter for an identifier — so the
			// name is validated rather than trusted.
			continue
		}
		query := `SELECT count(*) FROM "` + name + `"`
		samplers = append(samplers, SamplerFunc(TablePrefix+name, "rows", func(ctx context.Context) (float64, error) {
			var n int64
			if err := db.QueryRowContext(ctx, query).Scan(&n); err != nil {
				return 0, fmt.Errorf("counting rows in %s: %w", name, err)
			}
			return float64(n), nil
		}))
	}
	return samplers, nil
}

// safeTableName accepts only plain SQL identifiers (letters, digits,
// underscore, not starting with a digit).
func safeTableName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}
