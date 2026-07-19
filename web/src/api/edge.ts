// Edge & NAT cockpit API calls (docs/api.md's "Edge & NAT cockpit" section;
// internal/api/edge.go, T-1403). Read-only — this file adds no mutation
// call: every nat.*/route.static.* write flows through the ordinary
// changeset draft/apply calls in ./changesets, never a dedicated call here.
import { apiFetch } from "./client";
import type { EdgeNATView, EdgeRoutesView } from "./types";

/** GET /edge/routes — per-node default route(s) + additional/policy static
 * routes. */
export function fetchEdgeRoutes(): Promise<EdgeRoutesView> {
  return apiFetch<EdgeRoutesView>("/edge/routes");
}

/** GET /edge/nat — PVE-host masquerade/port-forward rules + PVE SDN
 * simple-zone NAT, with port-forward targets correlated to known guests. */
export function fetchEdgeNAT(): Promise<EdgeNATView> {
  return apiFetch<EdgeNATView>("/edge/nat");
}
