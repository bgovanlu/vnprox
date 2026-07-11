// Data wiring for the VXLAN wizard's peer address auto-suggest
// (peerSuggest.ts has the pure address parsing). For each member node,
// tries that node's own bridge/VLAN interfaces (in the order the topology
// lists them) until one reports an address, reusing the already-cached
// GET /topology response plus a handful of GET /inventory/{ref} detail
// fetches — no new backend route.
import { useQuery } from "@tanstack/react-query";
import { fetchInventoryDetail } from "../../api/topology";
import { useTopologyQuery } from "../../topology/queries";
import { firstHostAddress } from "./peerSuggest";

const ADDRESSABLE_KINDS = new Set(["bridge", "ovs-bridge", "vlan"]);

export interface SuggestedPeers {
  ready: boolean;
  /** node name -> suggested underlay address (undefined if none found). */
  byNode: Record<string, string | undefined>;
}

export function useSuggestedPeers(memberNodes: readonly string[]): SuggestedPeers {
  const { data: topology } = useTopologyQuery();

  const candidatesByNode = (() => {
    const out: Record<string, string[]> = {};
    if (!topology) return out;
    for (const node of memberNodes) {
      out[node] = topology.nodes
        .filter((n) => n.nodeGroup === node && ADDRESSABLE_KINDS.has(n.kind))
        .map((n) => n.id);
    }
    return out;
  })();

  const allCandidateRefs = Object.values(candidatesByNode).flat().sort();
  const enabled = memberNodes.length > 0 && allCandidateRefs.length > 0;

  const { data, isFetched } = useQuery<Record<string, string | undefined>>({
    queryKey: ["sdn-wizard-suggested-peers", memberNodes.slice().sort(), allCandidateRefs],
    queryFn: async () => {
      const out: Record<string, string | undefined> = {};
      for (const node of memberNodes) {
        let found: string | undefined;
        for (const ref of candidatesByNode[node] ?? []) {
          if (found) break;
          // Sequential, not Promise.all: stops at the first candidate that
          // actually has an address, so a node with 3 bridges only ever
          // costs 1 request in the common case (first bridge is the
          // management/addressed one).
          const detail = await fetchInventoryDetail(ref);
          const addr = firstHostAddress(typeof detail.fields.addresses === "string" ? detail.fields.addresses : undefined);
          if (addr) found = addr;
        }
        out[node] = found;
      }
      return out;
    },
    enabled,
    staleTime: 15_000,
  });

  if (!enabled) return { ready: true, byNode: {} };
  return { ready: isFetched, byNode: data ?? {} };
}
