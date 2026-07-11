// Firewall log viewer API calls (docs/api.md's `GET /firewall/log`;
// internal/api/fwlog.go). T-505.
import { apiFetch } from "./client";
import type { FwLogPage } from "./types";

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
