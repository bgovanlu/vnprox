// Pure status-painting logic for the SDN cockpit tree, shared (in spirit —
// the actual color classes live in EntityNode.tsx/EntityEdge.tsx, which
// this module doesn't import to keep it framework-free and Vitest-cheap)
// with the topology map's own painting rules, so a zone with an error node
// paints amber/red *consistently* in both places (T-401 acceptance
// criterion 4). The backend's authoritative version of this same logic is
// internal/topology/project.go's sdnZoneStatus — kept in sync by hand,
// same as this file's EntityStatus values matching api/types.ts's.
import type { EntityStatus, SdnNodeStatus } from "../api/types";

/** Maps one PVE per-node SDN status ("ok"|"pending"|"error", plus the
 * vnprox-synthesized "unknown" — internal/pve.ReconcileSDNZoneStatus's doc
 * comment: a declared member node PVE had nothing to report for at all,
 * confirmed live on a real two-node cluster,
 * planning/reports/evidence/pve-9.2.4-cluster-vnprox-dev.txt) onto the
 * shared EntityStatus vocabulary the map already paints with: error -> down
 * (red), pending -> degraded (amber), ok/"" -> ok, "unknown" or anything
 * else unrecognized -> unknown (gray, "we don't know" — never silently ok). */
export function sdnNodeEntityStatus(status: string): EntityStatus {
  const normalized = status.trim().toLowerCase();
  switch (normalized) {
    case "":
    case "ok":
      return "ok";
    case "error":
      return "down";
    case "pending":
      return "degraded";
    default:
      return "unknown";
  }
}

/** Worst-of a zone's per-node statuses, mirroring
 * internal/topology/project.go's sdnZoneStatus exactly: "error" (down) wins
 * over "pending" (degraded), which wins over "unknown" (a node PVE had
 * nothing to report for is not the same fact as that node being healthy —
 * it must move the aggregate off "ok", just less urgently than a confirmed
 * problem), which wins over "ok"; no reported status at all is "unknown". */
export function sdnZoneEntityStatus(nodeStatus: readonly SdnNodeStatus[]): EntityStatus {
  if (nodeStatus.length === 0) return "unknown";
  let worst: EntityStatus = "ok";
  for (const ns of nodeStatus) {
    const s = sdnNodeEntityStatus(ns.status);
    if (s === "down") return "down";
    if (s === "degraded") worst = "degraded";
    else if (s === "unknown" && worst === "ok") worst = "unknown";
  }
  return worst;
}
