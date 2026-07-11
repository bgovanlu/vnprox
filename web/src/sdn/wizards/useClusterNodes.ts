// The distinct cluster node names currently visible in the topology —
// mirrors web/src/topology/TopologyPage.tsx's own `NewEntityMenu` node-list
// derivation exactly (the only other place in this codebase needs "every
// node name", and there's no dedicated node-list API to call instead).
import { useMemo } from "react";
import { useTopologyQuery } from "../../topology/queries";

export function useClusterNodes(): string[] {
  const { data: topology } = useTopologyQuery();
  return useMemo(
    () => Array.from(new Set((topology?.nodes ?? []).map((n) => n.nodeGroup).filter(Boolean))).sort(),
    [topology],
  );
}
