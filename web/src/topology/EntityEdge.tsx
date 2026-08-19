// Custom edge renderer: a smoothstep path colored/dashed by status, with
// its badges (VLAN ranges, enslavement active/backup, ...) rendered as a
// small label at the midpoint (docs/features/topology.md §1: "VLAN-aware
// bridges show trunked VID ranges as edge badges").
import { BaseEdge, EdgeLabelRenderer, getSmoothStepPath, type EdgeProps, type Edge } from "@xyflow/react";
import clsx from "clsx";
import type { EntityStatus, FindingBadge, SimVerdict } from "../api/types";
import { findingChipText, hasOpenFinding, parseFindingBadge } from "./findingBadges";
import { trafficEdgeStyle } from "./trafficMode";

export interface EntityEdgeData extends Record<string, unknown> {
  status: EntityStatus;
  badges: string[];
  /** T-3501, mirrors TopologyEdge.findings — see its doc comment
   * (api/types.ts). No producer currently names an edge in a finding's refs
   * (see findingBadges.ts's back-compat note), so this is typically empty. */
  findings?: FindingBadge[];
  dimmed: boolean;
  highlighted: boolean;
  /** "Traffic" paint mode (docs/features/monitoring.md §1): when true, the
   * edge's stroke color/width come from utilizationPct instead of status,
   * via trafficMode.ts's shared heat mapping. */
  trafficMode?: boolean;
  /** This edge's resolved link utilization % (see
   * trafficMode.ts's resolveEdgeUtilizationRef) — undefined means "no live
   * data yet for either endpoint", rendered as idle rather than blank. */
  utilizationPct?: number;
  /** Path simulator overlay (T-504, see toFlowElements.ts's `pathHighlight`
   * param) — undefined leaves this edge's normal status coloring alone;
   * otherwise overrides the stroke color with the verdict's color. */
  simVerdict?: SimVerdict;
}

export type EntityFlowEdge = Edge<EntityEdgeData, "entity">;

const STATUS_STROKE: Record<EntityStatus, string> = {
  ok: "#94a3b8",
  down: "#ef4444",
  degraded: "#f59e0b",
  unknown: "#94a3b8",
};

// Same verdict palette as EntityNode.tsx's SIM_RING_CLASS, in stroke-color
// form (allow=emerald, deny=red, unreachable=amber, indeterminate=violet).
const SIM_STROKE: Record<SimVerdict, string> = {
  allow: "#10b981",
  deny: "#ef4444",
  unreachable: "#f59e0b",
  indeterminate: "#8b5cf6",
};

export function EntityEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  data,
  markerEnd,
}: EdgeProps<EntityFlowEdge>) {
  const [edgePath, labelX, labelY] = getSmoothStepPath({
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
  });

  const status = data?.status ?? "ok";
  const dimmed = data?.dimmed ?? false;
  const highlighted = data?.highlighted ?? false;
  const badges = data?.badges ?? [];
  const trafficMode = data?.trafficMode ?? false;
  const traffic = trafficEdgeStyle(data?.utilizationPct);
  const simVerdict = data?.simVerdict;
  // T-3501: the legacy bare "drift" token is dropped from the printed label
  // (it stays on the wire, and still counts for the dashing decision above,
  // via hasOpenFinding — but is never itself displayed text any more) and a
  // "finding:<source>:<severity>" token renders as its source name.
  const labelParts = badges
    .filter((b) => b !== "drift")
    .map((b) => {
      const parsed = parseFindingBadge(b);
      return parsed ? findingChipText(parsed) : b;
    });

  return (
    <>
      <BaseEdge
        id={id}
        path={edgePath}
        markerEnd={markerEnd}
        style={{
          stroke: simVerdict ? SIM_STROKE[simVerdict] : trafficMode ? traffic.stroke : STATUS_STROKE[status],
          strokeWidth: simVerdict ? 3.5 : trafficMode ? traffic.strokeWidth : highlighted ? 2.5 : 1.5,
          // An open finding = dashed outline (docs/features/topology.md
          // §2), additive to the existing "unknown"-status dashing — either
          // condition dashes the edge, independent of its status-driven
          // color. A path-simulator overlay (T-504) takes over the stroke
          // entirely. hasOpenFinding (T-3501) is source-agnostic, matching
          // the pre-T-3501 bare "drift" check exactly — see
          // findingBadges.ts's doc comment for why the legacy token still
          // counts (wgEdgeStatus.ts's client-synthesized WG endpoint-drift
          // edge badge has no richer form yet).
          strokeDasharray:
            !simVerdict && !trafficMode && (status === "unknown" || hasOpenFinding(badges)) ? "4 3" : undefined,
          opacity: dimmed && !highlighted ? 0.15 : 1,
        }}
      />
      {labelParts.length > 0 && (
        <EdgeLabelRenderer>
          <div
            className={clsx(
              "pointer-events-none absolute rounded bg-white/90 px-1 text-[9px] text-slate-600 shadow-sm dark:bg-slate-900/90 dark:text-slate-300",
              dimmed && !highlighted && "opacity-15",
            )}
            style={{ transform: `translate(-50%, -50%) translate(${String(labelX)}px,${String(labelY)}px)` }}
          >
            {labelParts.join(" · ")}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  );
}
