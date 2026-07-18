// Package capture implements T-1301's distributed packet-capture engine:
// server-side capture orchestration on the local node or any peer, behind a
// dedicated `capture` capability (internal/auth), with server-enforced,
// un-overridable caps and audited start/stop.
//
// # The trust boundary this package owns
//
// Every capture consumer (T-1302's UX, T-1307's diagnosis ladder, a future
// MCP surface) inherits the decisions made here — so they are deliberately
// conservative and cannot be relaxed from the client side:
//
//   - A capture session is gated on the `capture` capability alone
//     (auth.CapCapture), which is strictly stronger than `netWrite`
//     (docs/security.md's Authorization section). Holding netRead/netWrite
//     is never sufficient.
//   - Every session has a hard time cap, size cap, and packet-count cap
//     enforced by the server itself (Coordinator.clampCaps against the
//     configured ceilings). Whichever limit is hit first stops the capture.
//     No request field or filter construction can raise a cap past its
//     configured ceiling: a request may only ask for a *lower* value. Each
//     capturing node re-clamps independently against its own config, so a
//     peer never trusts a coordinator's arithmetic.
//   - Captured payload bytes are written only to one bounded per-session
//     file on the capturing node (<root>/<sessionId>.pcap). Nothing —
//     no payload byte, no decoded field — is ever copied into SQLite, the
//     audit log, or any other store. capture_sessions holds app-owned
//     intent + accounting only (byte/packet counts, status, file path).
//     An auto-purge sweep (Coordinator.Sweep / RunSweepLoop) deletes files
//     past their retention age on the same tick-cadence pattern
//     internal/metrics' ring pruning uses, including files orphaned by a
//     daemon restart mid-capture.
//   - Both start and stop write an audit row (capture.start / capture.stop)
//     recording actor, target Ref, the resolved filter, and the effective
//     caps in `detail`.
//
// # The BPF filter is validated before any capture process is invoked
//
// Coordinator.Start validates the submitted filter (bpf.go) — rejecting
// shell-unsafe or oversized filters, and filters for a target that cannot
// be scoped to a concrete capture interface — before it ever calls the
// capture Agent. A rejected filter never reaches a capture process.
//
// # Multi-point coordination
//
// A single request may name ≥2 targets on different nodes; the Coordinator
// starts one session per node correlated under one group id, so the same
// flow captured on two nodes can be matched up later (T-1302 consumes the
// pairing). Stopping the group stops every member.
//
// This package has no libpcap dependency (CLAUDE.md's stdlib-first rule):
// the real, on-hardware capture Agent (a `tcpdump`/AF_PACKET binding) is a
// needs-hardware-validation item; development and tests run against
// internal/capturemock's scripted agent, which writes a real classic-pcap
// file the same decoders consume.
package capture
