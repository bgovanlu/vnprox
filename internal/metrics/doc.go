// Package metrics implements interface counters, rate computation, and
// history rings (docs/features/monitoring.md §1-2, docs/architecture.md's
// MON component). Sampler is the entry point: it is fed raw counter
// snapshots by internal/collect's host poll loop (local node directly, peer
// nodes via internal/peer — collect.Config.OnStats, piggybacked on the
// existing 5s host-loop cadence rather than a second poll loop or a second
// peer transport), computes live per-second rates and utilization,
// broadcasts docs/api.md's `metrics.sample` WS event to subscribed clients,
// and persists a 30s-downsampled counter ring to SQLite for
// GET /metrics/history.
package metrics
