// SPDX-License-Identifier: Apache-2.0

// Route explorer API calls (T-3903, docs/api.md's "Route explorer"
// section; internal/api/route.go). Read-only — no mutation call exists in
// this file, on purpose: this page never stages, validates, or applies a
// route/rule change.
import { apiFetch } from "./client";
import type { RouteLookupResult, RouteNodesResponse, RouteSnapshot } from "./types";

/** GET /route/nodes: every node a Snapshot/Lookup call can currently
 * target — the local node (if known) plus every reachable peer. */
export function fetchRouteNodes(): Promise<RouteNodesResponse> {
  return apiFetch<RouteNodesResponse>("/route/nodes");
}

/** GET /route/snapshot?node=: one node's full kernel FIB + policy rules +
 * FRR RIB (when the node runs FRR). node="" (or omitted) asks for the
 * local node. */
export function fetchRouteSnapshot(node: string): Promise<RouteSnapshot> {
  const qs = node ? `?node=${encodeURIComponent(node)}` : "";
  return apiFetch<RouteSnapshot>(`/route/snapshot${qs}`);
}

/** GET /route/lookup?node=&dst=&iface=: T-3903's core operator question,
 * "which path would this address take." dst is a plain IP address, not a
 * CIDR. iface is an optional device hint disambiguating a genuine tie
 * (most commonly an IPv6 link-local destination on-link via more than one
 * interface at once). */
export function fetchRouteLookup(node: string, dst: string, iface?: string): Promise<RouteLookupResult> {
  const params = new URLSearchParams();
  if (node) params.set("node", node);
  params.set("dst", dst);
  if (iface) params.set("iface", iface);
  return apiFetch<RouteLookupResult>(`/route/lookup?${params.toString()}`);
}
