// SPDX-License-Identifier: Apache-2.0

// Package whatif implements T-4103's capacity planner: "add N guests of
// profile X" evaluated simultaneously against bandwidth headroom
// (internal/capacity), IPAM exhaustion (internal/ipam), and failure impact
// (internal/failsim) — one combined verdict naming which of the three binds
// first, rather than three numbers a caller must reconcile.
//
// None of the three composed engines does what-if evaluation on its own —
// internal/capacity trends *observed* history forward, internal/ipam and
// internal/failsim are read/observe today (see this task's report for the
// evidence). Evaluate is genuinely new composition, not a wrapper around an
// existing what-if primitive: it drives internal/capacity.WhatIf (which
// itself reuses Forecast's linear-fit/threshold math against a synthetic,
// guest-count-indexed load series), computes IPAM pool exhaustion directly
// from internal/ipam's exact allocation counts, and calls
// internal/failsim.Simulate twice — once over the live snapshot, once over
// a snapshot augmented with N synthetic guest/NIC entities — diffing the two
// Impacts rather than re-deriving failsim's connectivity/quorum/Ceph/
// management-path math.
//
// # Honesty
//
// Two of the three axes answer fundamentally different kinds of question,
// and the Verdict says so rather than presenting three interchangeable
// numbers:
//
//   - Capacity is an ESTIMATE. It is derived from the most recently observed
//     daily capacity rollup, which is itself summarized from
//     internal/store's metric_samples ring — bounded to
//     store.MetricRetention (24h) before it is rolled up into the
//     longer-retained (but still bounded, capacity/doc.go) daily aggregate.
//     A projection built on top of that inherits its nature as an estimate,
//     not a measurement of the future; CapacityAxis.Basis states this
//     plainly and CapacityAxis.Estimated is always true.
//   - IPAM is EXACT. Pool total/allocated counts are live, precise integers
//     from internal/ipam — the exhaustion count is arithmetic, not a
//     projection, and IPAMAxis.Estimated is always false.
//   - Failsim impact is a deterministic graph computation over the *current*
//     live topology (never persisted, never a trend) — reusing failsim's own
//     false-negative-biased honesty contract (a dimension failsim itself
//     could not evaluate is surfaced, never silently folded into "safe").
//
// A signal this package cannot evaluate (no capacity rollup history yet, no
// IPAM pool resolved for the profile's attachment, failsim can't resolve the
// synthetic guest's attachment) degrades to AxisStatusUnavailable — never
// silently treated as unconstrained, and never allowed to win the "binding
// constraint" comparison (see Evaluate's doc comment).
//
// # Purity
//
// Evaluate takes already-resolved inputs (a link's daily Aggregate history,
// the target ipam.Subnet(s), a live inventory.Snapshot plus failsim's
// optional side-tables) and returns a computed Verdict; it performs no I/O
// and persists nothing (Verdict is evaluate-and-discard, matching
// capacity/doc.go's "not a warehouse" stance — there is no new "what if"
// state to retain). Gathering those inputs from PVE/the store is the
// concern of a composition-root adapter (cmd/vnproxd), the same seam every
// other package in this arc uses to stay unit-testable against fakes.
package whatif
