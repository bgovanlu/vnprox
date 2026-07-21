// Package failsim implements T-1604's failure-impact simulation core: a
// pure, static "what breaks if X dies?" analysis over a live inventory
// snapshot. Given a target entity (node, bond, uplink NIC, bridge, switch,
// or WireGuard tunnel), Simulate removes it from a snapshot copy of the
// graph and recomputes what loses connectivity — guests cut off from their
// uplink, SDN segments stranded, corosync quorum put at risk, Ceph
// public/cluster networks isolated, and management paths lost.
//
// # Honesty contract (binding, mirrors internal/sim)
//
// This card is soundness-critical: its verdict is the pre-flight check
// T-1103's maintenance-window scheduler consults before an *unattended*
// apply (see internal/change's ImpactPreflighter seam). A false "safe" here
// green-lights a change that can sever connectivity with no operator
// watching. So, exactly like internal/sim's "no silent approximation"
// contract (docs/features/firewall.md §5/§6, binding precedent), this
// package is false-negative-biased by construction:
//
//   - Where an impact dimension cannot actually be assessed — no corosync
//     config, no Ceph read model, no WireGuard tunnels, or an unresolvable
//     entity — the dimension is reported in Impact.NotEvaluated, never a
//     confident "no impact". A silently-omitted risk category is
//     indistinguishable from "checked and safe" to a caller; that is the one
//     failure mode this package must never ship with.
//   - The management-path dimension is computed by re-running the *shared*
//     resolver (internal/topology.ResolveMgmtPaths, via
//     change.DetectProtectedRoles) against the post-failure snapshot — the
//     same function GET /protected-interfaces/status and the T-703 interlock
//     use — never a parallel notion of "management connectivity" that could
//     silently disagree with the interlock.
//   - Quorum risk is computed from the corosync ring topology, not counted
//     by entity name: a removal that drops reachable quorum-voting nodes
//     below floor(N/2)+1 is quorumRisk regardless of labels.
//
// The grep-able dimension→evaluated|not-evaluated inventory lives in
// honesty.go (honestyInventory), mirroring internal/sim's own convention
// (T-503 AC3) so the code and the report's honesty table cannot drift.
//
// # Purity
//
// This package performs no I/O of its own — it calls only pure functions of
// the packages it reuses (topology.ResolvePhysicalPath /
// topology.ResolveMgmtPaths, change.DetectProtectedRoles, ceph.Project). It
// takes an inventory.Snapshot plus optional
// side-tables (corosync config, Ceph status, WireGuard tunnels) as plain
// values and returns computed structs. Impact and SPOFEntry are never
// persisted — they are pure functions of the live snapshot, the same "never
// a shadow copy of PVE state" rule every read model in this arc follows
// (docs/data-model.md).
package failsim
