// SPDX-License-Identifier: Apache-2.0

// TanStack Query hooks for T-3911's composable dashboard: the per-user
// saved tile layout (reserved `layouts` name "dashboard", the same
// per-user mechanism web/src/topology/queries.ts's useLayoutQuery /
// web/src/topology/savedViewsQueries.ts already use for the topology
// canvas and named saved views) and the plugin-provided tile list
// (GET /dashboard/tiles).
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch, ApiError } from "../api/client";
import { fetchDashboardTiles } from "../api/dashboardTiles";
import { saveLayout } from "../api/layouts";
import type { DashboardLayoutPayload, DashboardTile } from "../api/types";
import { defaultDashboardLayout, isDashboardLayoutPayload } from "./dashboardLayout";

export const DASHBOARD_LAYOUT_NAME = "dashboard";
export const DASHBOARD_LAYOUT_QUERY_KEY = ["layouts", DASHBOARD_LAYOUT_NAME] as const;
export const DASHBOARD_TILES_QUERY_KEY = ["dashboard", "tiles"] as const;

/** GET /layouts/{name}, typed `layout: unknown` (not api/layouts.ts's
 * TopologyLayoutPayload-typed fetchLayout) for the same reason
 * savedViewsQueries.ts's fetchRawLayout is: this reserved name's blob is a
 * different shape, and trusting fetchLayout's narrower type here would be
 * an unchecked cast. Mirrors that function's shape exactly. */
async function fetchRawDashboardLayout(): Promise<{ name: string; layout: unknown; updatedAt: number }> {
  return apiFetch(`/layouts/${encodeURIComponent(DASHBOARD_LAYOUT_NAME)}`);
}

/** The saved tile layout, or the built-in default for a user who has never
 * customised their dashboard (no `layouts/dashboard` row yet — a 404,
 * exactly like useLayoutQuery's own 404 handling). A row that fails the
 * isDashboardLayoutPayload guard (corrupted, or written by something else
 * out of band) degrades the same way, rather than surfacing a parse error
 * on the one page every operator lands on first. */
export function useDashboardLayoutQuery() {
  return useQuery<DashboardLayoutPayload>({
    queryKey: DASHBOARD_LAYOUT_QUERY_KEY,
    queryFn: async () => {
      try {
        const res = await fetchRawDashboardLayout();
        return isDashboardLayoutPayload(res.layout) ? res.layout : defaultDashboardLayout();
      } catch (err) {
        if (err instanceof ApiError && err.status === 404) {
          return defaultDashboardLayout();
        }
        throw err;
      }
    },
    staleTime: Infinity,
    retry: false,
  });
}

export function useSaveDashboardLayoutMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (payload: DashboardLayoutPayload) => saveLayout(DASHBOARD_LAYOUT_NAME, payload),
    onSuccess: (_res, payload) => {
      queryClient.setQueryData<DashboardLayoutPayload>(DASHBOARD_LAYOUT_QUERY_KEY, payload);
    },
  });
}

/** GET /dashboard/tiles — every enabled dashboardTile plugin's current
 * tiles. A short staleTime (not Infinity, unlike the layout query): unlike
 * a user's own saved arrangement, which only changes when they act on it,
 * a plugin's tiles are live data a plugin author expects to actually
 * refresh (docs/plugins/dashboard-tile.md: "Called on every dashboard
 * render"). */
export function useDashboardPluginTilesQuery() {
  return useQuery<DashboardTile[]>({
    queryKey: DASHBOARD_TILES_QUERY_KEY,
    queryFn: async () => {
      const res = await fetchDashboardTiles();
      return res.items;
    },
    staleTime: 15_000,
  });
}
