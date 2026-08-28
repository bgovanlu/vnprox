// SPDX-License-Identifier: Apache-2.0

// T-3912's blast-radius lens: collapses the map to the subgraph a failure-
// impact simulation (internal/failsim.Impact, GET /failsim/spof-score) or a
// changeset-impact preview (internal/change's `GET /changesets/{id}/impact`)
// or a finding's `refs` names as affected, plus the *path* connecting those
// entities back to the thing that failed — "blast radius" means reachability,
// not just a set of endpoints.
//
// THIS MODULE COMPUTES NOTHING FAILSIM/CHANGE ALREADY COMPUTES. It is a pure
// view over an already-computed result: given the refs an Impact/
// ChangesetImpact/finding names, it resolves them against the CURRENTLY
// RENDERED map (whatever nodes/edges toFlowElements produced — post layer
// filter, post guest-group expansion) and finds the shortest connecting path
// through the live topology graph. Nothing here re-derives severity,
// reachability-at-failure-time, or any of failsim's dimensions.
//
// DEGRADATION IS THE POINT. An Impact/finding can be stale — computed a
// moment ago against a snapshot that has since changed, or naming an entity
// since removed. Every ref failsim/change/a finding names is resolved
// independently against the current map: a ref found on the map still
// focuses; a ref not found is reported (never silently dropped, never
// silently rendered as if present) and never blanks the rest of the lens.
// See `active` below — a request whose refs are ALL off-map degrades to
// "not active", which the caller must read as "show the whole map", never as
// "show nothing".
//
// WHY BFS OVER THE RENDERED GRAPH, NOT A NEW SERVER ROUTE. The path between
// an Impact's target and its affected entities is exactly the physical/SDN
// adjacency the topology map already draws — the same edges toFlowElements
// emits. Recomputing that server-side would be a second notion of
// "connected"; walking the client's own rendered edges keeps this lens
// honest about showing precisely what the map on screen can show.
//
// Kept framework-free (no React, no @xyflow/react types) so it is directly
// Vitest-able — the same split every other topology/*Overlay.ts module
// establishes.
import type { ChangesetImpact, FailsimImpact } from "../api/types";

/** Minimal node/edge shape this module needs — structurally compatible with
 * both `FlowNode`/`FlowEdge` (toFlowElements.ts's output, which is what
 * every call site actually has in hand) and a hand-built test fixture,
 * without importing @xyflow/react here. */
export interface FocusGraphNode {
  id: string;
}
export interface FocusGraphEdge {
  id: string;
  source: string;
  target: string;
}

/** Where a blast-radius request originated — surfaced in the banner text so
 * an operator always knows what they're looking at, never a bare "lens on". */
export type BlastRadiusOrigin = "failsim" | "changeset-impact" | "finding";

/** A resolved node's role in the focused subgraph. `target` is the entity
 * the Impact says failed/is being removed (failsim) or the changeset op's
 * own target (changeset-impact) — absent for a finding, which names no
 * single failure point. `affected` is a named-broken entity. `path` is a
 * hop the shortest-path walk passed through that the source didn't itself
 * name — included because blast radius is about reachability, not just
 * endpoints. */
export type BlastRadiusRole = "target" | "affected" | "path";

/** The input this lens computes over — already-computed refs from one of
 * three sources (see the request builders below), decoupled from any one
 * source's wire shape so `computeBlastRadiusFocus` never has to know which
 * endpoint produced them. */
export interface BlastRadiusRequest {
  origin: BlastRadiusOrigin;
  /** Human label for the banner ("Blast radius: removing bond0"). */
  label: string;
  /** The single entity that failed/is targeted, when the source names one. */
  target?: string;
  /** Every other entity the source names as affected (deduplicated by the
   * request builder, not by the caller). */
  affected: string[];
  /** Facts the source names that have no specific entity ref to attach to
   * (failsim's quorumRisk/cephRisk booleans, its notEvaluated dimensions, a
   * changeset's touchesMgmtPath/disruption) — never silently dropped, since
   * omitting them would understate the blast radius, but never turned into
   * a fabricated focus node either. */
  caveats: string[];
}

export interface BlastRadiusFocus {
  request: BlastRadiusRequest;
  /** False when NONE of the request's refs (target + affected) resolve
   * against the current map — the degradation case. A caller must treat
   * `active: false` as "show the whole map, unfocused", never as "show
   * nothing": see this file's doc comment. */
  active: boolean;
  /** Every node id to keep at full strength: target + on-map affected +
   * whatever path hops connect them. Empty when `active` is false. */
  focusNodeIds: ReadonlySet<string>;
  /** Every edge id (toFlowElements.ts's `edgeId` convention) on a connecting
   * path. Empty when `active` is false, or when every on-map ref is already
   * mutually unreachable (still a valid, non-degraded result — see
   * `unreachable`). */
  focusEdgeIds: ReadonlySet<string>;
  /** Role of each id in `focusNodeIds` — for the corner-badge glyph. */
  roles: ReadonlyMap<string, BlastRadiusRole>;
  /** Requested refs (target + affected, deduplicated) that resolved to a
   * node on the current map. */
  onMapRefs: readonly string[];
  /** Requested refs that did NOT resolve to a node on the current map — the
   * stale/removed-entity case. Always reported, never dropped. */
  offMapRefs: readonly string[];
  /** On-map refs with no path back to the anchor in the current graph (a
   * genuinely disconnected component) — still included in `focusNodeIds`
   * (an operator should still see them), just with no connecting edge. */
  unreachableRefs: readonly string[];
  /** Total node count the current map is rendering — the denominator for
   * the "showing N of M" summary. */
  totalNodeCount: number;
}

function dedupe(refs: readonly (string | undefined)[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const r of refs) {
    if (r === undefined || r === "" || seen.has(r)) continue;
    seen.add(r);
    out.push(r);
  }
  return out;
}

/** `inventory.Ref.String()` for a bare Node entity is `node:<name>:<name>`
 * (`internal/failsim/impact.go`'s own `nodeRef` helper; `internal/inventory/
 * ref.go`'s `KindNode = "node"` constant) — the conversion failsim's
 * `mgmtPathLoss` (plain node names) and a changeset impact's `nodes` (same
 * convention, docs/api.md's `GET /changesets/{id}/impact`) both need to
 * become a focusable map id.
 *
 * THIS ID IS NEVER ACTUALLY RENDERED ON THE MAP. `internal/topology.Project`
 * deliberately never emits a `KindNode` topology node (`topology/
 * nodeAnchor.ts`'s doc comment — the exact defect T-1402 fixed for the
 * Latency/MTU/WireGuard overlays, which resolved against this same
 * nonexistent id before that fix). A caller MUST run every request this
 * builds (and a failsim `target` that itself names a whole node — failsim
 * can simulate removing one) through `resolveNodeAnchorsInRequest` with
 * TopologyPage's own `nodeIdForName` resolver before calling
 * `computeBlastRadiusFocus`, or every node-loss ref will show as
 * permanently off-map — never as a bug in `computeBlastRadiusFocus` itself,
 * which correctly reports "not found" for an id that, by construction,
 * never exists in `GET /topology`. */
export function nodeRefForName(name: string): string {
  return `node:${name}:${name}`;
}

/** The `<name>` half of a `nodeRefForName` ref, or undefined for any ref
 * that isn't one (every other Kind's `Ref.String()` also uses `kind:node:
 * id`, but only `KindNode`'s own convention repeats the node name as both
 * segments — this checks the literal prefix, not just ':' counting). */
function nodeNameFromNodeRef(ref: string): string | undefined {
  if (!ref.startsWith("node:")) return undefined;
  const rest = ref.slice("node:".length);
  const sep = rest.indexOf(":");
  return sep === -1 ? undefined : rest.slice(0, sep);
}

/** Resolves every `node:<name>:<name>` ref in a request (a whole-node
 * failsim `target`, or a `mgmtPathLoss`/changeset-`nodes` entry folded into
 * `affected`) to that node's actual rendered map anchor — see
 * `nodeRefForName`'s doc comment for why this step cannot be skipped.
 * `anchorForNodeName` is `topology/nodeAnchor.ts`'s
 * `buildNodeAnchorResolver` output, already computed by every call site
 * that renders a map (TopologyPage.tsx's `nodeIdForName`) — this function
 * takes the resolver, not the raw node list, so it never has to know
 * nodeAnchor.ts's own tie-breaking rule. A name with no rendered anchor
 * (returns undefined — e.g. a node with no physical/L2 entity, or a name
 * failsim/change named that no longer exists) is left as the unresolved
 * `node:<name>:<name>` ref, which then correctly reports off-map rather
 * than throwing or silently vanishing. Non-node refs pass through
 * unchanged. */
export function resolveNodeAnchorsInRequest(
  request: BlastRadiusRequest,
  anchorForNodeName: (nodeName: string) => string | undefined,
): BlastRadiusRequest {
  const resolve = (ref: string): string => {
    const name = nodeNameFromNodeRef(ref);
    if (name === undefined) return ref;
    return anchorForNodeName(name) ?? ref;
  };
  return {
    ...request,
    target: request.target !== undefined ? resolve(request.target) : undefined,
    affected: request.affected.map(resolve),
  };
}

/** Builds a request from one `internal/failsim.Impact` (`FailsimImpact` on
 * the wire) — the shape `GET /failsim/spof-score`'s `SpofEntry.impact`
 * already carries, and the only failsim surface currently rendered in the
 * UI (SpofPanel.tsx). `disconnectedGuests`/`strandedVlans` are ref strings
 * already; `mgmtPathLoss` is node NAMES (FailsimImpact's own doc comment),
 * converted via `nodeRefForName`. `quorumRisk`/`cephRisk`/`notEvaluated`
 * name no specific entity, so they become caveats, not focus nodes. */
export function blastRadiusRequestFromFailsimImpact(impact: FailsimImpact): BlastRadiusRequest {
  const caveats: string[] = [];
  if (impact.quorumRisk) caveats.push("puts corosync quorum at risk");
  if (impact.cephRisk) caveats.push("isolates a Ceph network");
  if (impact.notEvaluated.length > 0) {
    caveats.push(`not evaluated: ${impact.notEvaluated.join(", ")}`);
  }
  return {
    origin: "failsim",
    label: `Blast radius: removing ${impact.target}`,
    target: impact.target,
    affected: dedupe([...impact.disconnectedGuests, ...impact.strandedVlans, ...impact.mgmtPathLoss.map(nodeRefForName)]),
    caveats,
  };
}

/** Builds a request from `GET /changesets/{id}/impact`'s `ChangesetImpact` —
 * checked against `internal/change`'s impact endpoint per this task's card
 * ("a second, already-shipped impact source"). Unlike failsim's Impact,
 * a changeset can carry several ops with several distinct targets: the
 * first op target (deterministic — array order, the order the server would
 * apply them in) anchors the lens as `target`; every other op target joins
 * `affected` alongside the touched nodes/carriers/guests, so nothing named
 * by the impact is dropped even though there is no single "the thing that
 * died" the way failsim has one. */
export function blastRadiusRequestFromChangesetImpact(changesetId: string, impact: ChangesetImpact): BlastRadiusRequest {
  const opTargets = dedupe(impact.ops.map((o) => o.target));
  const [primaryTarget, ...restTargets] = opTargets;
  const caveats: string[] = [];
  if (impact.touchesMgmtPath) caveats.push("touches the management path");
  if (impact.disruption !== "none") caveats.push(`overall disruption: ${impact.disruption}`);
  return {
    origin: "changeset-impact",
    label: `Blast radius: changeset ${changesetId}`,
    target: primaryTarget,
    affected: dedupe([...restTargets, ...impact.nodes.map(nodeRefForName), ...impact.carriers, ...impact.guests.map((g) => g.ref)]),
    caveats,
  };
}

/** Builds a request from a finding's own `refs` — no single failure point
 * (a finding names a set of related entities, not a removal), so `target`
 * is left undefined; `computeBlastRadiusFocus` picks an anchor from the
 * on-map refs itself. */
export function blastRadiusRequestFromFindingRefs(refs: readonly string[], label: string): BlastRadiusRequest {
  return { origin: "finding", label, affected: dedupe(refs), caveats: [] };
}

function buildAdjacency(edges: readonly FocusGraphEdge[]): Map<string, { to: string; edgeId: string }[]> {
  const adj = new Map<string, { to: string; edgeId: string }[]>();
  const add = (a: string, b: string, id: string): void => {
    let list = adj.get(a);
    if (!list) {
      list = [];
      adj.set(a, list);
    }
    list.push({ to: b, edgeId: id });
  };
  for (const e of edges) {
    add(e.source, e.target, e.id);
    add(e.target, e.source, e.id);
  }
  return adj;
}

/** Unweighted BFS shortest-path tree from `start`, restricted to nodes in
 * `validIds` (the current map's on-screen node set) — a path may only walk
 * through entities actually rendered, never through something filtered out
 * or off-map. */
function shortestPathTree(
  start: string,
  validIds: ReadonlySet<string>,
  adjacency: ReadonlyMap<string, { to: string; edgeId: string }[]>,
): { parent: Map<string, string>; parentEdge: Map<string, string> } {
  const parent = new Map<string, string>();
  const parentEdge = new Map<string, string>();
  const visited = new Set<string>([start]);
  // A plain `for...of` over `queue` is safe here even though the loop body
  // pushes onto it: the array iterator re-checks `queue.length` on every
  // step, so newly enqueued nodes are visited in the same pass — the usual
  // BFS-via-array-as-queue idiom, without an index variable.
  const queue: string[] = [start];
  for (const cur of queue) {
    for (const { to, edgeId } of adjacency.get(cur) ?? []) {
      if (!validIds.has(to) || visited.has(to)) continue;
      visited.add(to);
      parent.set(to, cur);
      parentEdge.set(to, edgeId);
      queue.push(to);
    }
  }
  return { parent, parentEdge };
}

/** Resolves a `BlastRadiusRequest` against the current map, per this file's
 * doc comment. `nodeIds` and `edges` are whatever `toFlowElements.ts`
 * produced for the currently active layers/expand/VLAN-filter state — the
 * exact set the operator is looking at right now, not the raw server
 * payload. */
export function computeBlastRadiusFocus(
  nodeIds: ReadonlySet<string> | readonly string[],
  edges: readonly FocusGraphEdge[],
  request: BlastRadiusRequest,
): BlastRadiusFocus {
  const onMapIds = nodeIds instanceof Set ? nodeIds : new Set(nodeIds);
  const wanted = dedupe([request.target, ...request.affected]);

  const onMapRefs: string[] = [];
  const offMapRefs: string[] = [];
  for (const ref of wanted) {
    (onMapIds.has(ref) ? onMapRefs : offMapRefs).push(ref);
  }

  if (onMapRefs.length === 0) {
    return {
      request,
      active: false,
      focusNodeIds: new Set(),
      focusEdgeIds: new Set(),
      roles: new Map(),
      onMapRefs,
      offMapRefs,
      unreachableRefs: [],
      totalNodeCount: onMapIds.size,
    };
  }

  const roles = new Map<string, BlastRadiusRole>();
  if (request.target !== undefined && onMapIds.has(request.target)) roles.set(request.target, "target");
  for (const ref of request.affected) {
    if (onMapIds.has(ref) && !roles.has(ref)) roles.set(ref, "affected");
  }

  const anchor = onMapRefs.find((r) => r === request.target) ?? onMapRefs[0];
  const focusNodeIds = new Set<string>(onMapRefs);
  const focusEdgeIds = new Set<string>();
  const unreachableRefs: string[] = [];

  if (anchor !== undefined) {
    const adjacency = buildAdjacency(edges);
    const { parent, parentEdge } = shortestPathTree(anchor, onMapIds, adjacency);
    for (const ref of onMapRefs) {
      if (ref === anchor) continue;
      if (!parent.has(ref)) {
        unreachableRefs.push(ref);
        continue;
      }
      let cur: string = ref;
      while (cur !== anchor) {
        const edge = parentEdge.get(cur);
        if (edge !== undefined) focusEdgeIds.add(edge);
        focusNodeIds.add(cur);
        const p = parent.get(cur);
        if (p === undefined) break;
        focusNodeIds.add(p);
        cur = p;
      }
    }
  }

  for (const id of focusNodeIds) {
    if (!roles.has(id)) roles.set(id, "path");
  }

  return {
    request,
    active: true,
    focusNodeIds,
    focusEdgeIds,
    roles,
    onMapRefs,
    offMapRefs,
    unreachableRefs,
    totalNodeCount: onMapIds.size,
  };
}

/** The real-DOM summary line the banner renders — WCAG: the "showing N of M"
 * count must exist as text, never conveyed only by which nodes look dimmed.
 * Mirrors summarizeDiffOverlay/summarizeRecencyOverlay's exact shape. */
export function summarizeBlastRadiusFocus(focus: BlastRadiusFocus): string {
  if (!focus.active) {
    return focus.offMapRefs.length > 0
      ? `None of the ${String(focus.offMapRefs.length)} named entities are on the current map — showing the full map instead.`
      : "Nothing to focus on.";
  }
  const parts = [`Showing ${String(focus.focusNodeIds.size)} of ${String(focus.totalNodeCount)} entities`];
  if (focus.offMapRefs.length > 0) {
    parts.push(`${String(focus.offMapRefs.length)} named ${focus.offMapRefs.length === 1 ? "entity is" : "entities are"} not on the current map`);
  }
  if (focus.unreachableRefs.length > 0) {
    parts.push(`${String(focus.unreachableRefs.length)} not connected to the rest in the current view`);
  }
  return `${parts.join(" · ")}.`;
}
