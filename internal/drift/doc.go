// Package drift implements T-305's cross-node consistency engine
// (docs/features/topology.md §6): a set of pure check functions that run
// over an inventory.Snapshot and produce Findings, plus a Service that
// drives them on a periodic cycle over a live *inventory.Graph, exposes
// them for docs/api.md's `GET /drift`, and computes "fixing changeset" op
// patches for the subset of findings with a safe, computable fix
// (bridge-property harmonization and MTU alignment — the two families the
// task card names explicitly).
//
// The five documented check families (topology.md §6) each live in their
// own file:
//
//   - bridge.go: same-named bridge presence/VLAN-awareness/VID-set
//     divergence across nodes (checkBridgeDivergence).
//   - mtu.go: MTU consistency along an L2 path (NIC->bond->bridge) and
//     across same-named bridges cluster-wide (checkMTUConsistency).
//   - sdn.go: SDN zone node-membership vs. actual bridge realization
//     (checkSDNRealization).
//   - pending.go: pending-but-unapplied interfaces.new edits
//     (checkPendingInterfaces).
//   - filerun.go: interfaces file vs. runtime (netlink) divergence —
//     someone edited by hand or ran `ip` commands directly
//     (checkFileRuntimeDivergence).
//
// Every check function is a pure func(inventory.Snapshot) []Finding: no
// state, no I/O, deterministic given the same snapshot content (T-305
// acceptance criterion 5 — "no finding flapping" — falls straight out of
// that purity plus Finding.ID being a stable hash of the check's own
// identity, never a random/time-based value).
package drift
