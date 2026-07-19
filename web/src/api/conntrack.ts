// Conntrack explorer API calls (docs/api.md's `GET /conntrack`;
// internal/api/conntrack.go, T-1305). Read-only — this task adds no
// mutation route anywhere: there is no flush/delete-entry call in this
// file, on purpose (docs/features's "read-only this arc" contract).
import { apiFetch } from "./client";
import type { ConntrackPage } from "./types";

/** Filter params GET /conntrack accepts — every field optional and ANDed
 * together server-side, mirroring GET /flows'/GET /audit's convention (an
 * unrecognized/unparsable value matches nothing, never a 400 — except a
 * malformed `port`, which 400s the same way GET /flows' own `port` does).
 * `guest` is an inventory Guest ref (e.g. "guest:pve1:104"), resolved
 * server-side against every enrichment source vnprox currently has
 * evidence from (never a client-side IP guess). */
export interface ConntrackFilter {
  node?: string;
  guest?: string;
  srcIp?: string;
  dstIp?: string;
  port?: number;
  state?: string;
}

/** GET /conntrack?node=&guest=&srcIp=&dstIp=&port=&state= — a live,
 * cluster-fanned-out conntrack/NAT table read. */
export function fetchConntrack(filter: ConntrackFilter = {}): Promise<ConntrackPage> {
  const params = new URLSearchParams();
  if (filter.node) params.set("node", filter.node);
  if (filter.guest) params.set("guest", filter.guest);
  if (filter.srcIp) params.set("srcIp", filter.srcIp);
  if (filter.dstIp) params.set("dstIp", filter.dstIp);
  if (filter.port !== undefined) params.set("port", String(filter.port));
  if (filter.state) params.set("state", filter.state);
  const qs = params.toString();
  return apiFetch<ConntrackPage>(`/conntrack${qs ? `?${qs}` : ""}`);
}
