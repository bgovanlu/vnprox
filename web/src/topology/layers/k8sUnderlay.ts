// T-1502: "why is this pod unreachable" — traces a correlated k8s node's
// PVE guest down through the real topology graph: node-guest -> its
// attached bridge -> that bridge's bond, the underlay half nobody else
// shows alongside the pod itself (this task's card, verbatim: "pod ->
// node-guest -> bridge -> bond, in one panel"). Reuses the exact edge-kind
// vocabulary projection.ts's computeHoverHighlight already reads
// (internal/inventory/link.go's EdgeKind constants: "attached-to" for a
// guest NIC's bridge attachment, "port-of"/"enslaved-by" for a bridge
// port's own upstream bond) rather than a new traversal primitive — a
// small, purpose-built ordered walk (computeHoverHighlight itself returns
// an unordered highlight *set*, not the ordered hop list a drilldown panel
// needs to render as a chain).
//
// A guest with multiple NICs produces one path per NIC (a pod's node-guest
// can reasonably have more than one attachment) — never merged into a
// single, potentially misleading chain.
import type { TopologyEdge, TopologyNode } from "../../api/types";

export interface UnderlayHop {
  id: string;
  kind: string;
  label: string;
}

export interface UnderlayPath {
  /** The guest-nic entity this path starts from (after the guest itself). */
  nicId: string;
  /** Ordered [guest, guest-nic, bridge, bond?] — bond is present only when
   * the bridge's own port-of/enslaved-by edges name one (a bridge sitting
   * directly on a physical NIC with no bond has a 3-hop path). */
  hops: UnderlayHop[];
}

const BOND_KINDS = new Set(["bond", "ovs-bond"]);

function toHop(n: TopologyNode): UnderlayHop {
  return { id: n.id, kind: n.kind, label: n.label };
}

/** Parses a guest ref ("guest:<node>:<vmid>") into its node/vmid segments,
 * mirroring inventory.Ref.String()'s "kind:node:id" layout as an opaque
 * split only (see k8sOverlay.ts's pveNodeFromGuestRef doc comment for the
 * same convention). */
function parseGuestRef(ref: string): { node: string; vmid: string } | undefined {
  const parts = ref.split(":");
  if (parts.length < 3 || parts[0] !== "guest") return undefined;
  return { node: parts[1] ?? "", vmid: parts[2] ?? "" };
}

/** Computes every underlay path from guestRef's own guest-nic entities
 * down to their bridge (and that bridge's bond, if any) — pure, over an
 * already-fetched topology's nodes/edges, so it's directly Vitest-able
 * without a live GET /topology round trip. Returns an empty array (never
 * a guessed hop) when guestRef doesn't parse, or when the guest currently
 * has no rendered guest-nic entity at all (e.g. filtered out, or the
 * collector hasn't produced it yet). */
export function computePodUnderlayChain(
  nodes: readonly TopologyNode[],
  edges: readonly TopologyEdge[],
  guestRef: string,
): UnderlayPath[] {
  const parsed = parseGuestRef(guestRef);
  if (!parsed) return [];

  const byId = new Map(nodes.map((n) => [n.id, n]));
  const guestNode = byId.get(guestRef);
  const guestHop: UnderlayHop = guestNode ? toHop(guestNode) : { id: guestRef, kind: "guest", label: guestRef };

  const nicPrefix = `guest-nic:${parsed.node}:${parsed.vmid}/`;
  const nics = nodes.filter((n) => n.id.startsWith(nicPrefix));

  const paths: UnderlayPath[] = [];
  for (const nic of nics) {
    const hops: UnderlayHop[] = [guestHop, toHop(nic)];

    const attach = edges.find((e) => e.kind === "attached-to" && e.from === nic.id);
    const bridge = attach ? byId.get(attach.to) : undefined;
    if (bridge) {
      hops.push(toHop(bridge));

      const port = edges.find(
        (e) =>
          (e.kind === "port-of" || e.kind === "enslaved-by") &&
          e.to === bridge.id &&
          BOND_KINDS.has(byId.get(e.from)?.kind ?? ""),
      );
      const bond = port ? byId.get(port.from) : undefined;
      if (bond) hops.push(toHop(bond));
    }

    paths.push({ nicId: nic.id, hops });
  }

  return paths;
}
