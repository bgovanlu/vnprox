// SDN cockpit API calls (docs/features/sdn.md; internal/api/sdn.go's
// GET /sdn).
import { apiFetch } from "./client";
import type { SdnTree } from "./types";

/** GET /sdn: the zone -> vnet -> subnet tree, per-node apply/health status,
 * and the staged-vs-running pending diff. */
export function fetchSdnTree(): Promise<SdnTree> {
  return apiFetch<SdnTree>("/sdn");
}
