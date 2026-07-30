// Federation global-read API calls (T-1202; docs/api.md §Federation). Backed
// by internal/federation.Aggregator via internal/api/federationtopo.go. These
// aggregate reads across attached clusters; they never mutate a cluster's
// config.
import { apiFetch, ApiError } from "./client";
import type { TopologyResponse } from "./types";

/** One attached cluster's capsule-level rollup for the global map — the
 * shape internal/federation.ClusterSummary serializes. */
export interface ClusterSummary {
  clusterId: string;
  clusterName: string;
  reachable: boolean;
  nodes: number;
  nodesOnline: number;
  guests: number;
  findings: number;
  drift: boolean;
}

/** GET /federation/topology response: one summary per attached cluster
 * (including the unreachable ones, tagged reachable:false), plus the
 * standard partial/failedClusters failure-isolation envelope. */
export interface FederationTopologyResponse {
  clusters: ClusterSummary[];
  failedClusters?: string[];
  partial?: boolean;
}

/** One cluster-namespaced global-search hit — a single-cluster SearchResult
 * plus the clusterId/clusterName the palette groups by. */
export interface FederationSearchHit {
  clusterId: string;
  clusterName: string;
  ref: string;
  kind: string;
  label: string;
  node: string;
  matchedField: string;
}

export interface FederationSearchResponse {
  results: FederationSearchHit[];
  failedClusters?: string[];
  partial?: boolean;
}

/** GET /federation/topology — per-cluster capsule summaries. A 404 (the
 * route isn't mounted because no federation is wired) is normalized to an
 * empty attachment set, so single-cluster deployments never see an error:
 * the global-view gate simply falls through to the ordinary topology page. */
export async function fetchFederationTopology(): Promise<FederationTopologyResponse> {
  try {
    return await apiFetch<FederationTopologyResponse>("/federation/topology");
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      return { clusters: [] };
    }
    throw err;
  }
}

/** GET /federation/topology/clusters/{id} — one attached cluster's full
 * projected topology, fetched lazily on drill-down. Same contract as
 * GET /topology so the existing single-cluster canvas renders it unchanged. */
export function fetchFederationClusterTopology(id: string): Promise<TopologyResponse> {
  return apiFetch<TopologyResponse>(`/federation/topology/clusters/${encodeURIComponent(id)}`);
}

/** One attached cluster's registry entry — GET /federation/clusters' item
 * shape (internal/api's federationClusterResponse). The credential is never
 * part of it.
 *
 * `wgTunnelId` is the cluster's *effective* WireGuard tunnel linkage and
 * `wgTunnelSource` says where it came from: "explicit" (an operator set it
 * through PUT /federation/clusters/{id}) or "peer" (derived from a WireGuard
 * peer tagged with this cluster — what the connect-clusters wizard stages).
 * Both are absent on a cluster that isn't tunnel-linked. */
export interface FederationCluster {
  id: string;
  name: string;
  apiUrl: string;
  status: string;
  addedBy: string;
  addedAt: number;
  wgTunnelId?: string;
  wgTunnelSource?: "explicit" | "peer";
}

export interface FederationClustersResponse {
  items: FederationCluster[];
}

/** GET /federation/clusters — the attached-cluster registry. Like the other
 * federation reads here, a 404 (federation isn't wired on this daemon) is
 * normalized to an empty list rather than an error, so a single-cluster
 * deployment just sees "no attached clusters" wherever this is offered. */
export async function fetchFederationClusters(): Promise<FederationCluster[]> {
  try {
    const res = await apiFetch<FederationClustersResponse>("/federation/clusters");
    return res.items;
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      return [];
    }
    throw err;
  }
}

/** GET /federation/search?q= — cluster-namespaced global entity search.
 * Returns an empty set (no request) for a blank query, mirroring
 * searchInventory's own "nothing typed yet" short-circuit. A 404 (federation
 * not wired) is likewise normalized to empty. */
export async function fetchFederationSearch(q: string): Promise<FederationSearchResponse> {
  const trimmed = q.trim();
  if (!trimmed) {
    return { results: [] };
  }
  try {
    return await apiFetch<FederationSearchResponse>(`/federation/search?q=${encodeURIComponent(trimmed)}`);
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      return { results: [] };
    }
    throw err;
  }
}
