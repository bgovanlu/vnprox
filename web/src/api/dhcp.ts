// DHCP reservations/leases API calls (docs/features/sdn.md §5;
// internal/api/dhcp.go's GET /sdn/dhcp).
import { apiFetch } from "./client";
import type { DhcpView } from "./types";

/** GET /sdn/dhcp[?zone=]: every DHCP-enabled subnet's static reservations
 * (IPAM allocations bound to a MAC) plus live dnsmasq leases, optionally
 * scoped to one zone. */
export function fetchDhcpView(zone?: string): Promise<DhcpView> {
  const qs = zone ? `?zone=${encodeURIComponent(zone)}` : "";
  return apiFetch<DhcpView>(`/sdn/dhcp${qs}`);
}
