// Pure derivation of the cluster-wide guest NIC list from an already-
// fetched TopologyResponse (docs/features/change-management.md §6: "List +
// map views support: reattach ... Bulk mode: select N guests (filter by
// current bridge/VLAN/node)"). Framework-free, directly Vitest-able.
//
// GET /topology collapses a (node, attachment-target) group's guest NICs
// into one "guest-group" pill once the count exceeds
// internal/topology/collapse.go's DefaultCollapseThreshold — small clusters
// (e.g. the three-node-vlan pvemock fixture, 2 guests total) never trigger
// this, so their guest NICs are already individual nodes in the response;
// larger ones need each pill expanded (topology/expand.ts, reused here) to
// get the full list this page needs.
import type { TopologyEdge, TopologyNode, TopologyResponse } from "../api/types";
import { refId, refNode } from "../changesets/opSummary";

export interface GuestNicRow {
  ref: string;
  label: string;
  node: string;
  /** The bridge/VNet ref this NIC is attached to, if resolved. */
  bridgeOrVnet?: string;
  vid?: number;
  linkDown: boolean;
  /** This NIC's MAC address, from the topology node's "mac=" badge
   * (internal/topology/project.go's badgesOf — T-406). Undefined for a
   * NIC with no known MAC. */
  mac?: string;
}

function attachedTargetFor(edges: TopologyEdge[], nicRef: string): string | undefined {
  return edges.find((e) => e.kind === "attached-to" && e.from === nicRef)?.to;
}

function vidFromBadges(badges: string[]): number | undefined {
  const b = badges.find((x) => x.startsWith("vid="));
  if (!b) return undefined;
  const n = Number(b.slice("vid=".length));
  return Number.isFinite(n) ? n : undefined;
}

function macFromBadges(badges: string[]): string | undefined {
  const b = badges.find((x) => x.startsWith("mac="));
  return b ? b.slice("mac=".length) : undefined;
}

/** Rows for every guest NIC already present as an individual node in
 * `topology` (i.e. not currently collapsed into a "guest-group" pill). */
export function guestNicRowsFromTopology(topology: TopologyResponse): GuestNicRow[] {
  return topology.nodes
    .filter((n) => n.kind === "guest-nic")
    .map((n) => ({
      ref: n.id,
      label: n.label,
      node: n.nodeGroup || refNode(n.id),
      bridgeOrVnet: attachedTargetFor(topology.edges, n.id),
      vid: vidFromBadges(n.badges),
      linkDown: n.badges.includes("link-down"),
      mac: macFromBadges(n.badges),
    }));
}

/** Every currently-collapsed "guest-group" pill id in `topology` — callers
 * expand each of these (topology/expand.ts's expandGuestGroup) to fold
 * their members into the full row list. */
export function guestGroupPillIds(topology: TopologyResponse): string[] {
  return topology.nodes.filter((n) => n.kind === "guest-group").map((n) => n.id);
}

/** Converts one expanded pill's synthesized nodes/edges (from
 * topology/expand.ts's expandGuestGroup) into GuestNicRows, same shape as
 * guestNicRowsFromTopology so callers can simply concatenate. */
export function guestNicRowsFromExpansion(nodes: TopologyNode[], edges: TopologyEdge[]): GuestNicRow[] {
  return guestNicRowsFromTopology({ nodes, edges, layers: [], generatedAt: 0 });
}

export interface GuestNicFilter {
  node?: string;
  bridgeOrVnet?: string;
  vid?: number;
}

export function filterGuestNicRows(rows: GuestNicRow[], filter: GuestNicFilter): GuestNicRow[] {
  return rows.filter((r) => {
    if (filter.node && r.node !== filter.node) return false;
    if (filter.bridgeOrVnet && r.bridgeOrVnet !== filter.bridgeOrVnet) return false;
    if (filter.vid !== undefined && r.vid !== filter.vid) return false;
    return true;
  });
}

/** For display: the bare bridge/VNet name from a full Ref string, or a
 * placeholder when unattached. */
export function targetLabel(ref: string | undefined): string {
  return ref ? refId(ref) : "(unattached)";
}
