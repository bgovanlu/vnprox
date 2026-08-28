// SPDX-License-Identifier: Apache-2.0

// T-2403: the entity-history query hook, in its own module so
// EntityHistoryTab.tsx exports only a component (the react-refresh boundary
// rule).
import { useQuery } from "@tanstack/react-query";
import { fetchEntityHistory } from "../api/entityHistory";

export function useEntityHistoryQuery(entityRef: string, enabled: boolean) {
  return useQuery({
    queryKey: ["inventory", "history", entityRef],
    queryFn: () => fetchEntityHistory(entityRef),
    enabled: enabled && entityRef !== "",
    staleTime: 10_000,
  });
}
