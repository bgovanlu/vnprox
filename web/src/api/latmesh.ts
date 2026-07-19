// Latency mesh API calls (docs/api.md's Latency mesh section, T-1303;
// internal/api/latmesh.go's GET /latmesh/heatmap, GET /latmesh/history).
// Node-local only (no cluster fan-out) — see that section's own doc
// comment for why.
import { apiFetch } from "./client";
import type { LatMeshHeatmap, LatMeshHistory, LatMeshLink, LatMeshSample } from "./types";

/** GET /latmesh/heatmap — current + rolling per-link status for every link
 * this node originates. */
export function fetchLatMeshHeatmap(): Promise<LatMeshLink[]> {
  return apiFetch<LatMeshHeatmap>("/latmesh/heatmap").then((r) => r.items);
}

/** GET /latmesh/history?linkId=&fromTs=&toTs= — one link's raw sample
 * history (the inspector sparkline's data source). fromTs/toTs are unix
 * seconds; both optional. */
export function fetchLatMeshHistory(linkId: string, fromTs?: number, toTs?: number): Promise<LatMeshSample[]> {
  const params = new URLSearchParams({ linkId });
  if (fromTs !== undefined) params.set("fromTs", String(fromTs));
  if (toTs !== undefined) params.set("toTs", String(toTs));
  return apiFetch<LatMeshHistory>(`/latmesh/history?${params.toString()}`).then((r) => r.items);
}
