// TanStack Query hooks for T-501's firewall read views. Read-only (no WS
// bridge yet — firewall config changes infrequently and T-502 owns writes;
// a future task can add a `firewall.changed` bridge the same way
// drift/queries.ts's useDriftWsBridge does, if that turns out to matter).
import { useQuery } from "@tanstack/react-query";
import {
  fetchClusterRuleset,
  fetchFirewallObjects,
  fetchGuestRuleset,
  fetchGuestRulesets,
  fetchNodeRuleset,
  fetchNodeRulesets,
} from "../api/firewall";

export function useClusterRulesetQuery() {
  return useQuery({
    queryKey: ["firewall", "ruleset", "cluster"],
    queryFn: fetchClusterRuleset,
    staleTime: 15_000,
  });
}

export function useNodeRulesetsQuery() {
  return useQuery({
    queryKey: ["firewall", "rulesets", "node"],
    queryFn: fetchNodeRulesets,
    staleTime: 15_000,
  });
}

export function useNodeRulesetQuery(node: string | undefined) {
  return useQuery({
    queryKey: ["firewall", "ruleset", "node", node],
    queryFn: () => fetchNodeRuleset(node ?? ""),
    enabled: node !== undefined,
    staleTime: 15_000,
  });
}

export function useGuestRulesetsQuery() {
  return useQuery({
    queryKey: ["firewall", "rulesets", "guest"],
    queryFn: fetchGuestRulesets,
    staleTime: 15_000,
  });
}

export function useGuestRulesetQuery(ref: string | undefined) {
  return useQuery({
    queryKey: ["firewall", "ruleset", "guest", ref],
    queryFn: () => fetchGuestRuleset(ref ?? ""),
    enabled: ref !== undefined,
    staleTime: 15_000,
  });
}

export function useFirewallObjectsQuery() {
  return useQuery({
    queryKey: ["firewall", "objects"],
    queryFn: fetchFirewallObjects,
    staleTime: 15_000,
  });
}
