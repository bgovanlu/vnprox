// SPDX-License-Identifier: Apache-2.0

// Pure derivation of the SDN vnet-delete flow's reattachment candidate list
// from an already-fetched topology — the SDN cockpit's own counterpart of
// changesets/entityCandidates.ts's reattachTargets, framework-free and
// directly Vitest-able.
export interface TopologyNodeLike {
  id: string;
  kind: string;
}

/** Every other cluster VNet a guest could reattach to (excluding the one
 * being deleted) — VNets are cluster-scoped, so unlike a bridge reattach
 * target this never needs to be node-filtered. Bridges are intentionally
 * not offered here (out of scope for T-402's vnet-delete flow — flagged in
 * the report); a guest that needs to move to a plain bridge instead can
 * still be reattached via the topology map's own bridge delete/edit flow. */
export function vnetReattachTargets(topologyNodes: TopologyNodeLike[], excludeVnetId: string): string[] {
  return topologyNodes
    .filter((n) => n.kind === "sdn-vnet" && n.id !== `sdn-vnet::${excludeVnetId}`)
    .map((n) => {
      const bare = n.id.slice(n.id.lastIndexOf(":") + 1);
      const slash = bare.lastIndexOf("/");
      return slash === -1 ? bare : bare.slice(slash + 1);
    });
}
