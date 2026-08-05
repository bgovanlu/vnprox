// TanStack Query hooks for T-501's firewall read views. Read-only (no WS
// bridge yet — firewall config changes infrequently and T-502 owns writes;
// a future task can add a `firewall.changed` bridge the same way
// drift/queries.ts's useDriftWsBridge does, if that turns out to matter).
import { useQuery } from "@tanstack/react-query";
import {
  fetchClusterRuleset,
  fetchFirewallEffects,
  fetchFirewallObjects,
  fetchGroupRuleset,
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

/** T-2002: a security group's own rule list, for the group inspector.
 * `name` undefined disables the query (nothing to inspect yet). */
export function useGroupRulesetQuery(name: string | undefined) {
  return useQuery({
    queryKey: ["firewall", "ruleset", "group", name],
    queryFn: () => fetchGroupRuleset(name ?? ""),
    enabled: name !== undefined && name !== "",
    staleTime: 15_000,
  });
}

/** T-502 acceptance criterion 4: the rule-effects preview for a security-
 * group reference. `group` undefined disables the query (e.g. the builder
 * row's direction isn't "group" yet). */
export function useFirewallEffectsQuery(group: string | undefined) {
  return useQuery({
    queryKey: ["firewall", "effects", group],
    queryFn: () => fetchFirewallEffects(group ?? ""),
    enabled: group !== undefined && group !== "",
    staleTime: 15_000,
  });
}
