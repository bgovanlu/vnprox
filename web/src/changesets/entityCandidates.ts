// Pure derivation of editor candidate lists (bridge port picker, bond slave
// picker, VLAN parent picker) from an already-fetched TopologyResponse —
// no extra network round trip for the common case, since the map's own
// data already names every physnic/bond/bridge/vlan per node and which
// edges already enslave them. Framework-free, directly Vitest-able.
import type { TopologyEdge, TopologyNode, TopologyResponse } from "../api/types";
import { refId } from "./opSummary";

const ENSLAVEMENT_EDGE_KINDS = new Set(["enslaved-by", "port-of", "tagged-on"]);

/** Maps an entity ref to the name of the bond/bridge/parent it's already
 * "used by" (the other end of an enslaved-by/port-of/tagged-on edge),
 * across the whole topology — the conflict hint every port/slave picker
 * shows so a user understands why picking an already-used interface will
 * fail validation. */
export function enslavementMap(topology: TopologyResponse): Map<string, string> {
  const out = new Map<string, string>();
  for (const e of topology.edges) {
    if (ENSLAVEMENT_EDGE_KINDS.has(e.kind)) {
      out.set(e.from, refId(e.to));
    }
  }
  return out;
}

function nodesOnNodeOfKinds(topology: TopologyResponse, node: string, kinds: string[]): TopologyNode[] {
  return topology.nodes.filter((n) => n.nodeGroup === node && kinds.includes(n.kind));
}

export interface CandidateNode {
  ref: string;
  name: string;
  label: string;
  status: TopologyNode["status"];
  alreadyEnslaved?: string;
}

/** Candidates for a bridge's port picker: physnics, bonds, and VLAN
 * sub-interfaces on `node` — anything interfaces(5) allows as a bridge
 * port — excluding the bridge itself when editing one (`excludeRef`). */
export function bridgePortCandidates(topology: TopologyResponse, node: string, excludeRef?: string): CandidateNode[] {
  const already = enslavementMap(topology);
  return nodesOnNodeOfKinds(topology, node, ["physnic", "bond", "ovs-bond", "vlan"])
    .filter((n) => n.id !== excludeRef)
    .map((n) => ({ ref: n.id, name: refId(n.id), label: n.label, status: n.status, alreadyEnslaved: already.get(n.id) }));
}

/** Candidates for a bond's slave picker: physnics only. */
export function bondSlaveCandidates(topology: TopologyResponse, node: string): CandidateNode[] {
  const already = enslavementMap(topology);
  return nodesOnNodeOfKinds(topology, node, ["physnic"]).map((n) => ({
    ref: n.id,
    name: refId(n.id),
    label: n.label,
    status: n.status,
    alreadyEnslaved: already.get(n.id),
  }));
}

/** Candidates for a VLAN's parent picker: physnics, bonds, and bridges on
 * `node` (interfaces(5) allows a VLAN tagged on any of these). */
export function vlanParentCandidates(topology: TopologyResponse, node: string): string[] {
  return nodesOnNodeOfKinds(topology, node, ["physnic", "bond", "ovs-bond", "bridge", "ovs-bridge"]).map((n) => refId(n.id));
}

/** Every bridge/VNet a guest could reattach to, cluster-wide (bridges are
 * per-node; VNets are cluster-scoped and shown once) — for the bridge
 * delete-with-reattach flow's target picker. `excludeRef` omits the bridge
 * being deleted. */
export function reattachTargets(topology: TopologyResponse, node: string, excludeRef: string): string[] {
  const bridges = nodesOnNodeOfKinds(topology, node, ["bridge", "ovs-bridge"])
    .filter((n) => n.id !== excludeRef)
    .map((n) => refId(n.id));
  // A VNet's Ref id is zone-qualified ("vlanz/vnet100"), but a guest net
  // config references the bare vnet id ("vnet100") — take the last path
  // segment (guest.nic.update's BridgeOrVnet is a name, not a Ref).
  const vnets = topology.nodes
    .filter((n) => n.kind === "sdn-vnet")
    .map((n) => {
      const rid = refId(n.id);
      const slash = rid.lastIndexOf("/");
      return slash === -1 ? rid : rid.slice(slash + 1);
    });
  return [...new Set([...bridges, ...vnets])];
}

export interface AttachedGuestNic {
  ref: string;
  label: string;
}

/** Every guest NIC currently attached to `bridgeRef` (docs/features/
 * change-management.md §5's delete-with-reattach flow needs this list to
 * generate the reattachment ops). Reads `attached-to` edges pointing at the
 * bridge; NB the raw GET /topology response collapses guest NICs into
 * per-bridge pills by default (internal/topology/collapse.go) — callers
 * that want the *individual* NICs (not just a pill count) should pass in a
 * topology snapshot with the relevant guest-group(s) already expanded (see
 * topology/expand.ts), same as the map's own expand-on-click affordance. */
export function attachedGuestNics(edges: TopologyEdge[], nodesById: Map<string, TopologyNode>, bridgeRef: string): AttachedGuestNic[] {
  return edges
    .filter((e) => e.kind === "attached-to" && e.to === bridgeRef)
    .map((e) => ({ ref: e.from, label: nodesById.get(e.from)?.label ?? refId(e.from) }));
}
