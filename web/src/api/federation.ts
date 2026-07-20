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
