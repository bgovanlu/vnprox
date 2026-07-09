// Guest-group pill expand logic.
//
// internal/topology/collapse.go drops collapsed guests' individual node
// entries entirely from GET /topology's response — they are not merely
// hidden, they are absent (confirmed by reading collapse.go: `filtered`
// never includes a node whose id was in `collapsedNicIDs`). So expanding a
// "guest-group:<node>:<targetRef>" pill client-side cannot just reveal
// already-fetched data; per this task's own instructions, that meant
// checking whether a follow-up API call was needed, and if so, whether one
// exists.
//
// It turns out one does, without any backend change: internal/topology/
// detail.go's Detail() (GET /inventory/{ref}) builds its `related` list
// from `snap.EdgesOf(ref)` — the live inventory graph — not from the
// collapsed/projected Topology. So GET /inventory/{targetRef} (the bridge
// or VNet a guest-group pill attaches to) still lists *every* guest NIC
// attached to it, collapsed or not. Expansion is therefore: fetch the
// target's detail, filter `related` down to the guest NICs belonging to
// this pill's node, then fetch each one's own detail (already-normalized
// label/fields) to synthesize a node + attachment edge for it.
import type { EntityDetail, TopologyEdge, TopologyNode } from "../api/types";
import { parseGuestGroupId } from "./projection";

/** A ref string's node segment, per inventory.Ref's "kind:node:id" encoding
 * (ParseRef splits on only the first two ':', so this must too — a plain
 * `.split(":")[1]` breaks the moment an id contains ':'). */
function refNode(ref: string): string {
  const firstColon = ref.indexOf(":");
  if (firstColon === -1) return "";
  const secondColon = ref.indexOf(":", firstColon + 1);
  if (secondColon === -1) return "";
  return ref.slice(firstColon + 1, secondColon);
}

function refKind(ref: string): string {
  const firstColon = ref.indexOf(":");
  return firstColon === -1 ? ref : ref.slice(0, firstColon);
}

/** Given the collapsed group's target entity detail, returns the guest NIC
 * refs this specific pill represents: edges where the guest NIC is the
 * "attached-to" source (`direction === "from"`, `edgeKind === "attached-to"`)
 * and the NIC's own node matches the pill's node (a cluster-scoped VNet's
 * `related` list can include NICs from *other* nodes too, which belong to a
 * different pill). */
export function guestNicRefsForGroup(targetDetail: EntityDetail, groupNode: string): string[] {
  return targetDetail.related
    .filter((r) => r.edgeKind === "attached-to" && r.direction === "from" && refKind(r.ref) === "guest-nic")
    .map((r) => r.ref)
    .filter((ref) => refNode(ref) === groupNode);
}

/** Synthesizes a rendered TopologyNode + attachment TopologyEdge for one
 * expanded guest NIC, from its own /inventory/{ref} detail — the same shape
 * internal/topology/project.go would have produced had it not been
 * collapsed. `targetRef` is the guest-group pill's attachment target
 * (bridge/VNet); `vid` comes from the NIC's own `fields.effectiveVid`. */
export function nicDetailToTopologyElements(
  nicDetail: EntityDetail,
  targetRef: string,
): { node: TopologyNode; edge: TopologyEdge } {
  const effectiveVid = typeof nicDetail.fields.effectiveVid === "number" ? nicDetail.fields.effectiveVid : 0;
  const linkDown = nicDetail.fields.linkDown === true;
  const badges: string[] = [];
  if (effectiveVid !== 0) badges.push(`vid=${String(effectiveVid)}`);
  if (linkDown) badges.push("link-down");

  const node: TopologyNode = {
    id: nicDetail.ref,
    kind: nicDetail.kind,
    label: nicDetail.label,
    layer: "guest",
    nodeGroup: nicDetail.node,
    status: linkDown ? "down" : "ok",
    badges,
  };
  const edge: TopologyEdge = {
    from: nicDetail.ref,
    to: targetRef,
    kind: "attached-to",
    status: node.status,
    badges,
  };
  return { node, edge };
}

/** True error type distinguishing "not a guest-group id at all" (caller
 * bug) from ordinary fetch failures, which callers surface as a toast. */
export class InvalidGuestGroupIdError extends Error {
  constructor(id: string) {
    super(`not a guest-group id: ${id}`);
    this.name = "InvalidGuestGroupIdError";
  }
}

export interface ExpandGuestGroupDeps {
  fetchDetail: (ref: string) => Promise<EntityDetail>;
}

/** Expands one guest-group pill: resolves its member guest NIC refs via the
 * target's detail, then fetches each NIC's own detail (in parallel) to
 * synthesize its node + edge. Throws InvalidGuestGroupIdError for a
 * malformed id; individual per-NIC fetch failures are collected rather than
 * failing the whole expansion, so one bad ref doesn't hide the rest. */
export async function expandGuestGroup(
  groupId: string,
  deps: ExpandGuestGroupDeps,
): Promise<{ nodes: TopologyNode[]; edges: TopologyEdge[] }> {
  const parsed = parseGuestGroupId(groupId);
  if (!parsed) {
    throw new InvalidGuestGroupIdError(groupId);
  }

  const targetDetail = await deps.fetchDetail(parsed.targetRef);
  const nicRefs = guestNicRefsForGroup(targetDetail, parsed.node);

  const settled = await Promise.allSettled(nicRefs.map((ref) => deps.fetchDetail(ref)));
  const nodes: TopologyNode[] = [];
  const edges: TopologyEdge[] = [];
  for (const result of settled) {
    if (result.status !== "fulfilled") continue;
    const { node, edge } = nicDetailToTopologyElements(result.value, parsed.targetRef);
    nodes.push(node);
    edges.push(edge);
  }
  return { nodes, edges };
}
