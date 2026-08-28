// SPDX-License-Identifier: Apache-2.0

// Flow explorer + map-painting API calls (docs/api.md's `GET /flows`;
// internal/api/flows.go, T-1002). Read-only — T-1003 adds no backend
// surface of its own.
import { apiFetch } from "./client";
import type { FlowsPage } from "./types";

/** Filter params GET /flows accepts — every field optional and ANDed
 * together server-side (mirrors GET /audit's/GET /firewall/log's
 * convention: an unrecognized/unparsable value matches nothing, never a
 * 400). `guest` matches either srcRef or dstRef exactly (despite the name,
 * it's an inventory Ref — always a Bridge or SdnVnet ref per FlowRecord's
 * doc comment, never a real guest ref). `protocol` accepts either a name
 * (tcp/udp/icmp/icmpv6/sctp) or a raw IP protocol number. */
export interface FlowsFilter {
  guest?: string;
  vlan?: number;
  subnet?: string;
  port?: number;
  protocol?: string;
  fromTs?: number;
  toTs?: number;
  limit?: number;
  cursor?: string;
}

/** GET /flows?guest=&vlan=&subnet=&port=&protocol=&fromTs=&toTs=&limit=&cursor=
 * — paginated, cluster-wide flow record query. */
export function fetchFlows(filter: FlowsFilter = {}): Promise<FlowsPage> {
  const params = new URLSearchParams();
  if (filter.guest) params.set("guest", filter.guest);
  if (filter.vlan !== undefined) params.set("vlan", String(filter.vlan));
  if (filter.subnet) params.set("subnet", filter.subnet);
  if (filter.port !== undefined) params.set("port", String(filter.port));
  if (filter.protocol) params.set("protocol", filter.protocol);
  if (filter.fromTs !== undefined) params.set("fromTs", String(filter.fromTs));
  if (filter.toTs !== undefined) params.set("toTs", String(filter.toTs));
  if (filter.limit) params.set("limit", String(filter.limit));
  if (filter.cursor) params.set("cursor", filter.cursor);
  const qs = params.toString();
  return apiFetch<FlowsPage>(`/flows${qs ? `?${qs}` : ""}`);
}
