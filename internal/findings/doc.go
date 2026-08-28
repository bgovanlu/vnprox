// SPDX-License-Identifier: Apache-2.0

// Package findings implements T-602's unified findings stream
// (docs/features/monitoring.md §5): one Finding shape and one Engine that
// composes four producers —
//
//   - drift (T-305, internal/drift): cross-node consistency checks, wired
//     through unchanged (adapt_drift.go adapts drift.Finding -> Finding).
//   - lldp (T-302, internal/topology's VLAN cross-check): wired through
//     unchanged (adapt_lldp.go adapts topology.VlanFinding -> Finding,
//     dropping the "everything matches" info-level entries — those are a
//     standing display, not an actionable finding).
//   - ipam (T-405): deferred. IPAMProvider is the seam T-405's own
//     conflict/finding producer will satisfy once that task lands (still a
//     stub package as of this task — see the T-602 completion report for
//     the concurrency note); Engine simply contributes zero IPAM findings
//     while Config.IPAM is nil.
//   - health (this task, health_*.go): the seven §5 checks not already
//     covered by drift's MTU-consistency family (which already implements
//     "MTU path mismatch" and flows through via the drift adapter):
//     interface error/drop rate thresholds, bond slave down, STP topology
//     change bursts, bridge with no carrier uplink, dnsmasq/frr service
//     down, and stale (>1h) interfaces.new.
//
// Unlike internal/drift's check functions (pure functions of an
// inventory.Snapshot), several health checks are inherently stateful across
// poll cycles — a threshold breach must persist for several consecutive
// observations before it fires (hysteresis, AC3), and "stale interfaces.new"
// needs to remember when a pending edit was first observed. That state
// lives on Engine (hysteresis.go's debouncer, plus small per-check
// first-seen maps), guarded by one mutex; every check function itself still
// takes its inputs as plain values/interfaces so it stays unit-testable
// without constructing a whole Engine.
//
// internal/drift is deliberately NOT refactored, renamed, or otherwise
// changed by this package: its Finding type, Service, RunLoop, GET /drift,
// and POST /drift/{id}/fix all keep working exactly as T-305 left them.
// This keeps the diff small and, more importantly, avoids breaking
// concurrently-developed code in other in-flight worktrees that already
// depends on drift's existing shape. See the T-602 completion report for
// the full "why a new package instead of renaming internal/drift" rationale.
package findings
