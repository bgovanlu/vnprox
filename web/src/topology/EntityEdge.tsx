// Custom edge renderer: a smoothstep path colored/dashed by status, with
// its badges (VLAN ranges, enslavement active/backup, ...) rendered as a
// small label at the midpoint (docs/features/topology.md §1: "VLAN-aware
// bridges show trunked VID ranges as edge badges").
import { BaseEdge, EdgeLabelRenderer, getSmoothStepPath, type EdgeProps, type Edge } from "@xyflow/react";
import clsx from "clsx";
import type { EntityStatus } from "../api/types";

export interface EntityEdgeData extends Record<string, unknown> {
  status: EntityStatus;
  badges: string[];
  dimmed: boolean;
  highlighted: boolean;
}

export type EntityFlowEdge = Edge<EntityEdgeData, "entity">;

const STATUS_STROKE: Record<EntityStatus, string> = {
  ok: "#94a3b8",
  down: "#ef4444",
  degraded: "#f59e0b",
  unknown: "#94a3b8",
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

  return (
    <>
      <BaseEdge
        id={id}
        path={edgePath}
        markerEnd={markerEnd}
        style={{
          stroke: STATUS_STROKE[status],
          strokeWidth: highlighted ? 2.5 : 1.5,
          // drift = dashed outline (docs/features/topology.md §2), additive
          // to the existing "unknown"-status dashing — either condition
          // dashes the edge, independent of its status-driven color.
          strokeDasharray: status === "unknown" || badges.includes("drift") ? "4 3" : undefined,
          opacity: dimmed && !highlighted ? 0.15 : 1,
        }}
      />
      {badges.length > 0 && (
        <EdgeLabelRenderer>
          <div
            className={clsx(
              "pointer-events-none absolute rounded bg-white/90 px-1 text-[9px] text-slate-600 shadow-sm dark:bg-slate-900/90 dark:text-slate-300",
              dimmed && !highlighted && "opacity-15",
            )}
            style={{ transform: `translate(-50%, -50%) translate(${String(labelX)}px,${String(labelY)}px)` }}
          >
            {badges.join(" · ")}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  );
}
