// Federation global-read API calls (T-1202; docs/api.md §Federation), plus
// the T-1201 cluster-registry CRUD (T-2001: the registry has had full
// backend routes and no UI since T-1201 — this file's create/update/delete
// functions are that UI's data layer). Reads are backed by
// internal/federation.Aggregator via internal/api/federationtopo.go and
// never mutate a cluster's config; the registry CRUD below only ever
// adds/edits/removes vnprox's own local registration row for an attached
// cluster — see internal/api/federation.go's own doc comment for the same
// distinction on the server side.
import { apiFetch, ApiError } from "./client";
import { readCsrfCookie } from "./auth";
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

/** The credential material an attach/edit carries — exactly one of the two
 * shapes internal/api/federation.go's federationCredentialRequest accepts:
 * `{kind:"ticket", username, password, realm?}` or `{kind:"token", token}`.
 * Write-only end to end: never returned by any route, never held onto by
 * this client past the request that sends it (see FederationClusters.tsx's
 * own doc comment on why the form never rehydrates it from a fetched
 * cluster). */
export interface FederationCredentialRequest {
  kind: "ticket" | "token";
  username?: string;
  password?: string;
  realm?: string;
  token?: string;
}

/** POST /federation/clusters' body. */
export interface FederationClusterCreateRequest {
  name: string;
  apiUrl: string;
  credential: FederationCredentialRequest;
}

/** PUT /federation/clusters/{id}'s body. `credential` omitted leaves the
 * stored one untouched (a rename must not force re-entering the token).
 * `wgTunnelId` follows the identical omit-leaves-unchanged convention:
 * omit the field to leave the tunnel linkage untouched, or send `""`
 * explicitly to clear the *explicit* override — which, per docs/api.md's
 * T-1407 note, does not necessarily unlink a cluster that still has a
 * WireGuard peer tagged with it (that link is then reported as
 * `wgTunnelSource: "peer"` instead). */
export interface FederationClusterUpdateRequest {
  name: string;
  apiUrl: string;
  credential?: FederationCredentialRequest;
  wgTunnelId?: string;
}

/** POST /federation/clusters — attach a cluster. */
export function createFederationCluster(req: FederationClusterCreateRequest): Promise<FederationCluster> {
  return apiFetch<FederationCluster>("/federation/clusters", { method: "POST", json: req, csrfToken: readCsrfCookie() });
}

/** PUT /federation/clusters/{id} — edit name/apiUrl, optionally re-credential,
 * optionally set/clear the explicit wgTunnelId override. */
export function updateFederationCluster(id: string, req: FederationClusterUpdateRequest): Promise<FederationCluster> {
  return apiFetch<FederationCluster>(`/federation/clusters/${encodeURIComponent(id)}`, {
    method: "PUT",
    json: req,
    csrfToken: readCsrfCookie(),
  });
}

/** DELETE /federation/clusters/{id} — detach a cluster; resolves whether or
 * not it previously existed (the backend's delete is idempotent, per
 * docs/api.md). */
export async function deleteFederationCluster(id: string): Promise<void> {
  await apiFetch(`/federation/clusters/${encodeURIComponent(id)}`, { method: "DELETE", csrfToken: readCsrfCookie() });
}
