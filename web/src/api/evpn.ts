// EVPN/BGP observability API calls (docs/features/sdn.md §3;
// internal/api/evpn.go's GET /sdn/evpn/status).
import { apiFetch } from "./client";
import type { EvpnStatus } from "./types";

/** GET /sdn/evpn/status: cluster-wide FRR/BGP peering state, EVPN VNI
 * list, exit-node health, and flapping-session findings. */
export function fetchEvpnStatus(): Promise<EvpnStatus> {
  return apiFetch<EvpnStatus>("/sdn/evpn/status");
}
