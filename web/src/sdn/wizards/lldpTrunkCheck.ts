// Pure graph-traversal + check logic for the VLAN zone wizard's LLDP trunk
// cross-check (docs/features/sdn.md §2: "validates the physical path
// actually trunks the chosen VIDs (cross-checks LLDP VLAN info when
// available)"). Framework-free (no React, no fetch) so it's exhaustively
// Vitest-able — see lldpTrunkCheck.test.ts. Data fetching lives in
// useLldpTrunkCheck.ts, which calls these functions against the already-
// cached topology query plus a handful of GET /inventory/{ref} detail
// fetches (LLDP-neighbor VLAN membership isn't in the topology response
// itself — see web/src/topology/projection.ts's own doc comment on this
// exact gap).
import type { TopologyEdge, TopologyNode } from "../../api/types";

/** Edge kinds mirror internal/inventory/link.go's EdgeKind constants
 * exactly (this module reads the same string values the backend emits). */
const EDGE_PORT_OF = "port-of";
const EDGE_ENSLAVED_BY = "enslaved-by";
const EDGE_LLDP_ADJACENT = "lldp-adjacent";

/** Resolves an existing bridge's physical NICs: direct ports (physnic
 * `port-of` the bridge) plus, for any bond port, that bond's own slave
 * physnics (`enslaved-by` the bond) — a VLAN-aware trunk bridge is
 * typically carried by a bonded pair, not a bare NIC, so this needs to see
 * through one level of bonding to reach the physnics LLDP actually
 * observes neighbors on. */
export function bridgePhysNicRefs(edges: TopologyEdge[], bridgeRef: string): string[] {
  const ports = edges.filter((e) => e.kind === EDGE_PORT_OF && e.to === bridgeRef).map((e) => e.from);
  const out = new Set<string>();
  for (const port of ports) {
    if (port.startsWith("physnic:")) {
      out.add(port);
      continue;
    }
    // Assume anything else port-of a bridge is a bond (or an already
    // resolved non-NIC port this check has nothing useful to say about,
    // e.g. a VLAN sub-interface trunk member) — resolve its slaves.
    const slaves = edges.filter((e) => e.kind === EDGE_ENSLAVED_BY && e.to === port).map((e) => e.from);
    for (const s of slaves) out.add(s);
  }
  return Array.from(out);
}

/** Resolves the LLDP neighbor refs adjacent to any of physNicRefs. */
export function lldpNeighborRefsForPhysNics(edges: TopologyEdge[], physNicRefs: readonly string[]): string[] {
  const nicSet = new Set(physNicRefs);
  const out = new Set<string>();
  for (const e of edges) {
    if (e.kind === EDGE_LLDP_ADJACENT && nicSet.has(e.from)) out.add(e.to);
  }
  return Array.from(out);
}

/** Finds the topology node id (== inventory Ref string) for an existing
 * bridge named `bridgeName` on `node`, or undefined if no such bridge is
 * in the currently-loaded topology (e.g. a name typo, or a bridge that
 * genuinely doesn't exist on that node — the wizard's own referential
 * validation catches that separately at draft time). */
export function findBridgeRef(nodes: TopologyNode[], node: string, bridgeName: string): string | undefined {
  return nodes.find((n) => n.kind === "bridge" && n.nodeGroup === node && n.label === bridgeName)?.id;
}

/** One LLDP neighbor's trunk-relevant fields, already extracted from its
 * GET /inventory/{ref} detail (internal/inventory.LldpNeighbor's
 * fieldMap: `chassisName`, `portId`, `taggedVlans` — a comma-joined int
 * list, "" when the switch reports no tagged VLANs at all). */
export interface LldpNeighborTrunkInfo {
  ref: string;
  chassisName: string;
  portId: string;
  taggedVlans: number[];
}

/** Parses internal/inventory.LldpNeighbor.fieldMap's `taggedVlans` string
 * ("100,200,300", or "" for none) into a number list. */
export function parseTaggedVlans(field: string | undefined): number[] {
  if (!field) return [];
  return field
    .split(",")
    .map((s) => Number(s.trim()))
    .filter((n) => Number.isFinite(n) && n > 0);
}

export interface TrunkWarning {
  neighborRef: string;
  chassisName: string;
  portId: string;
}

/** The cross-check itself: which neighbors' advertised trunk does *not*
 * include `vid`. Deliberately returns one warning per affected neighbor
 * (not deduplicated by port) so a redundant pair of uplinks to two
 * different switches both surface their own, separately actionable
 * warning — T-403 acceptance criterion 2: "inline warning naming the
 * port." Returns [] (not a false "all good") when neighbors is empty —
 * callers must treat "no LLDP data" as "unchecked", not "checked and
 * clean" (see useLldpTrunkCheck's `hasData` flag). */
export function checkVlanTrunk(neighbors: readonly LldpNeighborTrunkInfo[], vid: number): TrunkWarning[] {
  return neighbors
    .filter((n) => !n.taggedVlans.includes(vid))
    .map((n) => ({ neighborRef: n.ref, chassisName: n.chassisName, portId: n.portId }));
}
