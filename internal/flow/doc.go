// Package flow implements T-1002's flow ingestion engine: stdlib-only wire
// decoders for sFlow v5, NetFlow v5, NetFlow v9, and IPFIX
// (encoding/binary over a net.UDPConn — no third-party flow library, per
// CLAUDE.md's dependency rule), normalization into one Record shape with
// inventory-resolved srcRef/dstRef, and a bounded ring store.
//
// # Bound: this is not a flow warehouse
//
// Per docs/roadmap-next.md's carried-forward invariant ("vnprox never
// becomes a long-term flow/metric warehouse") and docs/features/
// monitoring.md §3 ("no packet capture, no flow sampling in v1" — promoted
// here in Phase 10): the flow_samples ring is bounded by TWO independent
// limits, whichever is smaller prunes first —
//
//   - a time window ([flows] retention_minutes, default 60 minutes), and
//   - a hard row cap ([flows] max_rows, default 2,000,000 rows),
//
// enforced on the same tick-based prune cadence internal/metrics'
// metric_samples ring already establishes (Store.RunPruneLoop). There is no
// "keep forever" mode and no export/archival path out of vnprox itself —
// Prometheus (T-1001) or a real flow collector/TSDB is the answer for
// anything longer than an hour or two of live traffic conversation history.
// GET /flows and GET /api/peer/flows only ever serve what's currently in
// this bounded ring.
//
// # Listeners are opt-in, per node, off by default
//
// Every protocol listener (listener.go) is disabled unless explicitly
// enabled in vnprox.toml's [flows] section on that specific node
// (sflow_enabled/netflow_enabled/ipfix_enabled) — matching T-1004's
// host-sampling opt-in convention and CLAUDE.md's "everything is
// cluster-aware" rule (a listener enabled on one node says nothing about
// any other node's config).
//
// # Defensive parsing
//
// Every decoder in this package treats its input as untrusted network
// bytes: a malformed or truncated datagram is skipped and counted (never
// panics, never blocks the listener goroutine) — the same convention
// internal/fwlog.ParseAll and internal/host.ParseDHCPLeases already
// establish for other externally-sourced, best-effort text/binary formats.
// See breader's doc comment for the bounds-checked reader every decoder is
// built on.
package flow
