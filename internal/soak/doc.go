// SPDX-License-Identifier: Apache-2.0

// Package soak is T-2504's resource-leak gate: the machinery that samples a
// running daemon's resource usage on a fixed interval and fails on a
// *trend*, not on a threshold.
//
// The failure modes this package exists to catch — a goroutine leaked per
// collection cycle, a table nobody prunes, a slow leak in a ring buffer —
// are invisible to unit tests because they need time, and invisible to a
// threshold alarm because at any single instant the absolute number looks
// fine. What distinguishes them is that the number keeps going *up*. So the
// verdict here is the least-squares slope of each metric over the second
// half of the run (Analyze): a high-but-flat value passes, a low-but-rising
// value fails, and the failure names the metric that rose.
//
// Why the second half: every daemon has a warm-up. Collectors fill an
// inventory graph, SQLite grows its page cache, the Go heap reaches its
// steady-state working set. Regressing over the whole run would score that
// one-time climb as a leak. Regressing over the second half only asks the
// question that matters — "having warmed up, is it *still* climbing?"
//
// Layout:
//
//   - trend.go    — the least-squares slope itself (stdlib arithmetic, no
//     statistics dependency).
//   - analyze.go  — Series/Policy/Report: slope vs. per-metric tolerance,
//     windowed to the second half.
//   - sampler.go  — the samplers: goroutines, live heap, RSS, open file
//     descriptors, and one row-count sampler per SQLite table.
//   - run.go      — the sample/churn loop that produces a Result.
//   - artifact.go — the sample series and verdict written out, so a failure
//     is diagnosable without a re-run.
//
// This package knows nothing about vnproxd. The churn generator, the daemon
// boot, and the tolerances that gate a real run live in the caller
// (cmd/vnproxd/soak_test.go, driven by `make soak`).
package soak
