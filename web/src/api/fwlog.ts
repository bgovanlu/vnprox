// SPDX-License-Identifier: Apache-2.0

// Firewall log viewer + analytics API calls (docs/api.md's `GET
// /firewall/log` and `GET /firewall/analytics`; internal/api/fwlog.go).
// T-505 / T-1006.
import { apiFetch } from "./client";
import type { FwAnalyticsResponse, FwLogPage } from "./types";

/** Filter params GET /firewall/log accepts — every field optional and
 * ANDed together server-side (mirrors GET /audit's convention). */
export interface FwLogFilter {
  node?: string;
  vmid?: number;
  direction?: "in" | "out";
  action?: string;
  limit?: number;
}

/** GET /firewall/log?node=&vmid=&direction=&action=&limit= — the initial
 * "tail" view / a filtered re-query, served from the daemon's shared,
 * cluster-merged log buffer. */
export function fetchFwLog(filter: FwLogFilter = {}): Promise<FwLogPage> {
  const params = new URLSearchParams();
  if (filter.node) params.set("node", filter.node);
  if (filter.vmid) params.set("vmid", String(filter.vmid));
  if (filter.direction) params.set("direction", filter.direction);
  if (filter.action) params.set("action", filter.action);
  if (filter.limit) params.set("limit", String(filter.limit));
  const qs = params.toString();
  return apiFetch<FwLogPage>(`/firewall/log${qs ? `?${qs}` : ""}`);
}

/** Filter/window params GET /firewall/analytics accepts (T-1006). `scope`
 * only ever narrows by `"guest"` today (mirroring GET /firewall/rulesets'
 * own scope convention) — `ref` is required whenever `scope` is set. */
export interface FwAnalyticsFilter {
  scope?: "guest";
  ref?: string;
  windowHours?: number;
}

/** GET /firewall/analytics?scope=&ref=&windowHours= — per-rule hit counts,
 * top-N blocked sources/destinations, and the unused-rule report, all
 * aggregated over the same shared log buffer GET /firewall/log serves. */
export function fetchFwAnalytics(filter: FwAnalyticsFilter = {}): Promise<FwAnalyticsResponse> {
  const params = new URLSearchParams();
  if (filter.scope) params.set("scope", filter.scope);
  if (filter.ref) params.set("ref", filter.ref);
  if (filter.windowHours) params.set("windowHours", String(filter.windowHours));
  const qs = params.toString();
  return apiFetch<FwAnalyticsResponse>(`/firewall/analytics${qs ? `?${qs}` : ""}`);
}
