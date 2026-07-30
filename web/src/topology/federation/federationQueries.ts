// TanStack Query hooks for T-1202's global (cross-cluster) surfaces: the
// per-cluster capsule summary, one cluster's lazy drill-down topology, and
// the cluster-namespaced global search. All three are read-only aggregates
// (docs/api.md §Federation) and tolerate a single unreachable cluster via
// the response's partial/failedClusters envelope.
import { useQuery } from "@tanstack/react-query";
import {
  fetchFederationClusterTopology,
  fetchFederationClusters,
  fetchFederationSearch,
  fetchFederationTopology,
  type FederationCluster,
  type FederationSearchResponse,
  type FederationTopologyResponse,
} from "../../api/federation";
import type { TopologyResponse } from "../../api/types";

export const FEDERATION_TOPOLOGY_QUERY_KEY = ["federation", "topology"] as const;
export const FEDERATION_CLUSTERS_QUERY_KEY = ["federation", "clusters"] as const;
export const federationClusterTopologyKey = (id: string) => ["federation", "topology", "cluster", id] as const;
export const federationSearchKey = (q: string) => ["federation", "search", q] as const;

/** GET /federation/topology — the capsule summaries. Enabled always (cheap;
 * a 404 is normalized to zero clusters so single-cluster deployments never
 * error), so the global-view gate and the palette both key their
 * "federation is active" decision off one shared, cached response. */
export function useFederationTopologyQuery(enabled = true) {
  return useQuery<FederationTopologyResponse>({
    queryKey: FEDERATION_TOPOLOGY_QUERY_KEY,
    queryFn: fetchFederationTopology,
    staleTime: 15_000,
    enabled,
    retry: false,
  });
}

/** GET /federation/clusters — the attached-cluster registry (names + their
 * effective WireGuard tunnel linkage), for surfaces that need to *name* a
 * cluster rather than summarize it: the connect-clusters wizard's optional
 * "this peer is cluster X" tagging. `retry: false` plus the 404-to-empty
 * normalization in the fetcher means a daemon with no federation wired, or a
 * session without netRead, degrades to "no attached clusters" instead of an
 * error state. */
export function useFederationClustersQuery(enabled = true) {
  return useQuery<FederationCluster[]>({
    queryKey: FEDERATION_CLUSTERS_QUERY_KEY,
    queryFn: fetchFederationClusters,
    staleTime: 30_000,
    enabled,
    retry: false,
  });
}

/** Whether the global (multi-cluster) surfaces should engage: federation is
 * invisible until a *second* cluster is attached (T-1202: "a single attached
 * cluster renders its existing topology unchanged"). */
export function federationIsActive(resp: FederationTopologyResponse | undefined): boolean {
  return (resp?.clusters.length ?? 0) >= 2;
}

/** GET /federation/topology/clusters/{id} — one cluster's full topology,
 * fetched only when a capsule has been drilled into (enabled iff clusterId
 * is set). Same TopologyResponse shape the single-cluster canvas already
 * renders. */
export function useFederationClusterTopologyQuery(clusterId: string | undefined) {
  return useQuery<TopologyResponse>({
    queryKey: federationClusterTopologyKey(clusterId ?? ""),
    queryFn: () => fetchFederationClusterTopology(clusterId ?? ""),
    enabled: clusterId !== undefined && clusterId !== "",
    staleTime: 15_000,
  });
}

/** GET /federation/search?q= — cluster-namespaced global search, enabled
 * only when federation is active and a query is typed. */
export function useFederationSearchQuery(query: string, active: boolean) {
  const trimmed = query.trim();
  return useQuery<FederationSearchResponse>({
    queryKey: federationSearchKey(trimmed),
    queryFn: () => fetchFederationSearch(trimmed),
    enabled: active && trimmed.length > 0,
    staleTime: 5_000,
    retry: false,
  });
}
