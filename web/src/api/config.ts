import { apiFetch } from "./client";
import type { InstanceConfigResponse } from "./types";

/** GET /config — the daemon's non-secret operational configuration (Settings
 * page Instance section). */
export function fetchInstanceConfig(): Promise<InstanceConfigResponse> {
  return apiFetch<InstanceConfigResponse>("/config");
}
