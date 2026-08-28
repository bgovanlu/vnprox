// SPDX-License-Identifier: Apache-2.0

import { useMemo } from "react";
import { useQueries } from "@tanstack/react-query";
import { fetchInventoryDetail } from "../api/topology";
import { useTopologyQuery } from "../topology/queries";
import { expandGuestGroup } from "../topology/expand";
import { guestGroupPillIds, guestNicRowsFromExpansion, guestNicRowsFromTopology, type GuestNicRow } from "./guestNics";

/** The full cluster-wide guest NIC list (docs/features/change-management.md
 * §6), combining whatever guest-nic nodes are already un-collapsed in the
 * cached topology with an expansion of every collapsed "guest-group" pill.
 * Reuses the same GET /topology cache useTopologyQuery already populated —
 * no dedicated backend route exists for "list every guest NIC" (see this
 * task's report), so this composes it client-side from what T-107 already
 * fetches. */
export function useAllGuestNicsQuery(): { rows: GuestNicRow[]; isLoading: boolean } {
  const { data: topology, isLoading: topologyLoading } = useTopologyQuery();
  const pillIds = topology ? guestGroupPillIds(topology) : [];

  const expansions = useQueries({
    queries: pillIds.map((id) => ({
      queryKey: ["guest-group-expand", id],
      queryFn: () => expandGuestGroup(id, { fetchDetail: fetchInventoryDetail }),
      enabled: topology !== undefined,
      staleTime: 15_000,
    })),
  });

  const rows = useMemo(() => {
    if (!topology) return [];
    const direct = guestNicRowsFromTopology(topology);
    const expanded = expansions.flatMap((q) => (q.data ? guestNicRowsFromExpansion(q.data.nodes, q.data.edges) : []));
    return [...direct, ...expanded];
    // expansions' identity changes every render (useQueries returns fresh
    // array instances); keying off its serialized data instead avoids an
    // infinite recompute loop while still reacting to actual data changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [topology, expansions.map((q) => q.dataUpdatedAt).join(",")]);

  return { rows, isLoading: topologyLoading || expansions.some((q) => q.isLoading) };
}
