// TanStack Query hooks for the SDN cockpit (docs/development.md's
// TypeScript standards: "server state via TanStack Query only — no fetch
// in components").
import { useQuery } from "@tanstack/react-query";
import { fetchDhcpView } from "../api/dhcp";
import { fetchEvpnStatus } from "../api/evpn";
import { fetchSdnTree } from "../api/sdn";
import type { DhcpView, EvpnStatus, SdnTree } from "../api/types";

export const SDN_QUERY_KEY = ["sdn"] as const;

export function useSdnQuery() {
  return useQuery<SdnTree>({
    queryKey: SDN_QUERY_KEY,
    queryFn: fetchSdnTree,
    staleTime: 15_000,
  });
}

export const EVPN_STATUS_QUERY_KEY = ["sdn", "evpn", "status"] as const;

// T-404: flapping-session detection (docs/features/sdn.md §3) needs
// repeated observations over time — internal/evpn.Service accumulates its
// rolling per-session state history across calls to GET
// /sdn/evpn/status, so this hook refetches on an interval (not just on
// mount) for a flap finding to become and stay accurate, same as it would
// for any other live-status view. `enabled` lets EvpnView's caller (the
// SDN cockpit's tab switcher) avoid firing this query at all while the
// EVPN tab isn't the active one.
export function useEvpnStatusQuery(options?: { enabled?: boolean }) {
  return useQuery<EvpnStatus>({
    queryKey: EVPN_STATUS_QUERY_KEY,
    queryFn: fetchEvpnStatus,
    staleTime: 5_000,
    refetchInterval: 10_000,
    enabled: options?.enabled ?? true,
  });
}

export const DHCP_VIEW_QUERY_KEY = (zone?: string) => ["sdn", "dhcp", zone ?? ""] as const;

// T-406: "a live leases view (parsed per-node via peer API)... live-ish 30s
// refresh" (docs/features/sdn.md §5, this task's card) — dnsmasq leases
// change independently of any changeset apply, so this needs its own
// polling interval rather than relying on WS invalidation the way
// changeset-driven data does. `enabled` mirrors useEvpnStatusQuery's own
// "only poll while this tab is active" convention.
export function useDhcpViewQuery(zone?: string, options?: { enabled?: boolean }) {
  return useQuery<DhcpView>({
    queryKey: DHCP_VIEW_QUERY_KEY(zone),
    queryFn: () => fetchDhcpView(zone),
    staleTime: 15_000,
    refetchInterval: 30_000,
    enabled: options?.enabled ?? true,
  });
}
