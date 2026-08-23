// Package drift implements T-305's cross-node consistency engine
// (docs/features/topology.md §6): a set of pure check functions that run
// over an inventory.Snapshot and produce Findings, plus a Service that
// drives them on a periodic cycle over a live *inventory.Graph, exposes
// them for docs/api.md's `GET /drift`, and computes "fixing changeset" op
// patches for the subset of findings with a safe, computable fix
// (bridge-property harmonization and MTU alignment — the two families the
// task card names explicitly).
//
// The six documented check families (topology.md §6) each live in their
// own file:
//
//   - bridge.go: same-named bridge presence/VLAN-awareness/VID-set
//     divergence across nodes (checkBridgeDivergence).
//   - mtu.go: MTU consistency along an L2 path (NIC->bond->bridge) and
//     across same-named bridges cluster-wide (checkMTUConsistency).
//   - sdn.go: SDN zone node-membership vs. actual bridge realization
//     (checkSDNRealization), plus a zone's live PVE-reported per-node
//     realization status (checkSDNZoneStatus, T-3701) — two distinct
//     signals sharing one file; see sdn.go's own doc comment for why.
//   - pending.go: pending-but-unapplied interfaces.new edits
//     (checkPendingInterfaces).
//   - filerun.go: interfaces file vs. runtime (netlink) divergence —
//     someone edited by hand or ran `ip` commands directly
//     (checkFileRuntimeDivergence).
//   - sriov.go (T-1506): an already-diverged live SR-IOV VF whose VLAN/
//     spoof-check setting no longer matches its PF's bridge's own
//     VLAN-awareness/VID-set policy (checkVFSpoofcheckMismatch) — the
//     identical comparison internal/change's changeset-validate-time check
//     reuses for a *staged* vf.provision op.
//
// Every check function is a pure func(inventory.Snapshot) []Finding: no
// state, no I/O, deterministic given the same snapshot content (T-305
// acceptance criterion 5 — "no finding flapping" — falls straight out of
// that purity plus Finding.ID being a stable hash of the check's own
// identity, never a random/time-based value).
//
// T-1102 adds a sixth, additional family — spec_drift (specdrift.go): live
// state vs. a *pinned* declarative spec (internal/spec, T-1101), the GitOps
// reconciler's own reference. Unlike the five above it is not a pure
// func(Snapshot) — it also depends on the current pin (Service.pins, a
// PinProvider), so it lives as a Service method combined into Findings'
// output rather than a checkFuncs entry. Detection-only, exactly like every
// other family: a "create fixing changeset" action (POST /drift/{id}/fix)
// exists where the reconcile ops are safely computable (spec.Import,
// reused verbatim, never reimplemented), never auto-applied.
package drift
