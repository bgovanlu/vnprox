// SPDX-License-Identifier: Apache-2.0

// GET /dashboard/tiles (T-3911, internal/api/dashboard.go): every enabled
// dashboardTile plugin's current tiles. Read-only, netRead-gated, no CSRF
// requirement (a read) — see docs/api.md's "Dashboard tiles" section.
import { apiFetch } from "./client";
import type { DashboardTilesResponse } from "./types";

export function fetchDashboardTiles(): Promise<DashboardTilesResponse> {
  return apiFetch<DashboardTilesResponse>("/dashboard/tiles");
}
