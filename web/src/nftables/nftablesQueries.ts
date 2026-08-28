// SPDX-License-Identifier: Apache-2.0

// TanStack Query hooks for T-3904's compiled-ruleset inspector
// (docs/development.md: "server state via TanStack Query only — no fetch
// in components"). On-demand (refetched on node change, no
// `refetchInterval`), matching web/src/routeexplorer/routeQueries.ts'
// precedent: a compiled firewall ruleset changes on configuration events,
// not continuously, so this is not a live-polling view the way Conntrack/
// Flow explorers are.
import { useQuery } from "@tanstack/react-query";
import { fetchCompiledRuleset } from "../api/nftables";
import type { NftRulesetResponse } from "../api/types";

export function compiledRulesetQueryKey(node: string) {
  return ["firewall", "compiled", node] as const;
}

/** GET /firewall/compiled?node= — enabled only once a node is selected,
 * the same "no meaningful un-selected state once the node list resolves"
 * reasoning useRouteSnapshotQuery documents. */
export function useCompiledRulesetQuery(node: string, enabled = true) {
  return useQuery<NftRulesetResponse>({
    queryKey: compiledRulesetQueryKey(node),
    queryFn: () => fetchCompiledRuleset(node),
    enabled: enabled && node !== "",
  });
}
