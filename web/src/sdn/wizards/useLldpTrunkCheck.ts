// SPDX-License-Identifier: Apache-2.0

// Data wiring for the VLAN zone wizard's LLDP trunk cross-check
// (lldpTrunkCheck.ts has the pure logic). Reuses the already-cached
// GET /topology response (no extra fetch for the graph itself) and issues
// one GET /inventory/{ref} per candidate LLDP neighbor — there are
// typically only a handful (one or two per member node's uplink), not a
// cluster-wide fetch.
import { useQuery } from "@tanstack/react-query";
import { readNumberList, readString } from "../../api/entityFields";
import { fetchInventoryDetail } from "../../api/topology";
import { useTopologyQuery } from "../../topology/queries";
import {
  bridgePhysNicRefs,
  checkVlanTrunk,
  findBridgeRef,
  lldpNeighborRefsForPhysNics,
  type LldpNeighborTrunkInfo,
  type TrunkWarning,
} from "./lldpTrunkCheck";

export interface LldpTrunkCheckResult {
  /** True once the neighbor set is known and every detail fetch settled. */
  ready: boolean;
  /** True iff at least one LLDP neighbor was found for the bridge's
   * physical ports — the wizard only renders a check result (ok/warning)
   * when this is true; docs/features/sdn.md §2's "when available" is the
   * reason this is a distinct flag from `warnings.length === 0`. */
  hasData: boolean;
  warnings: TrunkWarning[];
}

const EMPTY: LldpTrunkCheckResult = { ready: false, hasData: false, warnings: [] };

/** bridgeName + memberNodes + vid describe the VLAN zone wizard's current
 * form state; vid <= 0 disables the check entirely (nothing to check
 * against yet). */
export function useLldpTrunkCheck(bridgeName: string, memberNodes: readonly string[], vid: number): LldpTrunkCheckResult {
  const { data: topology } = useTopologyQuery();

  const neighborRefs = (() => {
    if (!topology || !bridgeName || memberNodes.length === 0) return [];
    const out = new Set<string>();
    for (const node of memberNodes) {
      const bridgeRef = findBridgeRef(topology.nodes, node, bridgeName);
      if (!bridgeRef) continue;
      const physNics = bridgePhysNicRefs(topology.edges, bridgeRef);
      for (const ref of lldpNeighborRefsForPhysNics(topology.edges, physNics)) out.add(ref);
    }
    return Array.from(out).sort();
  })();

  const enabled = vid > 0 && neighborRefs.length > 0;
  const { data, isFetched } = useQuery<LldpNeighborTrunkInfo[]>({
    queryKey: ["sdn-wizard-lldp-trunk-check", neighborRefs],
    queryFn: async () => {
      const details = await Promise.all(neighborRefs.map((ref) => fetchInventoryDetail(ref)));
      return details.map((d) => ({
        ref: d.ref,
        // Go field names, Go types — see api/entityFields.ts. These three
        // were read as `chassisName`/`portId`/comma-joined `taggedVlans`,
        // none of which the API sends, so every neighbour reported an empty
        // trunk and the wizard warned that the chosen VID was not trunked
        // anywhere, naming a blank switch and port (T-2108).
        chassisName: readString(d.fields, "ChassisName") ?? "",
        portId: readString(d.fields, "PortID") ?? "",
        taggedVlans: readNumberList(d.fields, "TaggedVLANs"),
      }));
    },
    enabled,
    staleTime: 15_000,
  });

  if (vid <= 0) return EMPTY;
  if (neighborRefs.length === 0) {
    return { ready: true, hasData: false, warnings: [] };
  }
  if (!isFetched || !data) {
    return { ready: false, hasData: true, warnings: [] };
  }
  return { ready: true, hasData: true, warnings: checkVlanTrunk(data, vid) };
}
