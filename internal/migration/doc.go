// SPDX-License-Identifier: Apache-2.0

// Package migration implements T-1507's migration network planner: a
// purely advisory, read-only pre-flight check for live migrations and
// evacuations — bandwidth headroom on the migration network versus a
// guest's configured RAM size and a best-effort dirty-rate estimate,
// warning before a Friday-night evacuation saturates a shared link.
//
// # Advisory only — the product's core safety guarantee this task cannot
// weaken
//
// Plan never triggers, blocks, or otherwise participates in an actual
// migration. It has no dependency on (and, by construction, no way to
// call) any PVE migration-start/evacuate endpoint: the only PVE-facing
// interface this package defines, GuestConfigReader, exposes exactly one
// read-only method (GetGuestConfig) — there is no method on any type in
// this package whose call graph can reach a mutating PVE call. See
// plan_test.go's regression test for the mechanical proof.
//
// # "The migration network" — a proxy, not a live PVE reader
//
// No live reader of PVE's own datacenter.cfg `migration: network=...`
// exists anywhere in this codebase (internal/flow/classify.go's own doc
// comment documents the identical gap for T-1504's classifier; internal/
// latmesh's package doc comment documents it again for T-1303's fabric
// discovery — see planning/reports/needs-hardware-validation.md's T-1303
// entry). Absent that reader, this package resolves "the migration
// network" from two proxies, both flagged in every Assessment.Caveats
// entry that uses them:
//
//   - Capacity (capacity.go): the highest-capacity bridge the source and
//     target node carry in common (internal/xnode.BridgesByName, the exact
//     grouping T-1303's own guest-fabric discoverer already uses to find
//     shared L2 segments), summing that bridge's member PhysNic/Bond
//     SpeedMbps on each node and taking the lesser of the two nodes'
//     figures. This is the same bridge migration traffic actually rides
//     when — the common case — no dedicated migration network is
//     configured. A node pair sharing no such bridge falls back to
//     DefaultAssumedCapacityMbps (1000, a conservative single-GbE-link
//     assumption), always flagged.
//   - Congestion (mesh.go): T-1303's latency mesh has no distinct
//     migration fabric either (only "corosync" and "guest" — see the gap
//     above), so this package reads the corosync fabric's rolling
//     loss/RTT first — the literal risk this card exists to warn about,
//     "a Friday-night evacuation saturates the corosync link" — falling
//     back to the guest fabric otherwise, and derates the resolved
//     capacity by the observed loss percentage.
//
// # Dirty-rate estimate — always best-effort
//
// This arc has no live guest instrumentation (no dirty-bitmap read, no
// QMP query-migrate telemetry). The dirty-rate figure Plan computes is a
// fixed, documented fraction of the guest's own configured RAM size
// (Config.DirtyRateFraction) — a heuristic proxy standing in for real
// measurement, not a measurement itself. Assessment.BestEffort is
// therefore unconditionally true on every Assessment this package ever
// returns, regardless of how confidently the rest of the inputs resolved.
//
// # Verdict stability — the pinned interface Phase 16/T-1103 depend on
//
// Assessment's shape ({headroomMbps, estimatedTransferSec, verdict,
// bestEffort, caveats}) is the stable contract docs/api.md's Migration
// planner section pins: Phase 16's T-1604 failure-impact simulator and,
// through it, the already-shipped T-1103 maintenance scheduler consume it
// directly. Every field always has a value (Caveats is an empty, never
// nil, slice when there is nothing to flag) — no field is ever omitted,
// so a consumer can deserialize into a fixed struct without an existence
// check. estimatedTransferSec is the one documented sentinel: -1 when
// headroomMbps is 0 and no finite transfer-time estimate is possible.
package migration
