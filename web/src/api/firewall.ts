// Firewall read API calls (docs/api.md §"Firewall, SDN, IPAM"'s
// `/firewall/rulesets?scope=` and `/firewall/objects`; the exact contract
// is internal/fw + internal/api/firewall.go). Read-only for T-501 — T-502
// adds the fw.* changeset ops this module will grow write helpers for.
import { apiFetch } from "./client";
import type {
  FirewallEffectsResponse,
  FirewallObjectsResponse,
  GroupRulesetResponse,
  GuestRulesetResponse,
  RulesetListResponse,
  RulesetView,
} from "./types";

/** GET /firewall/rulesets?scope=cluster — the single datacenter-wide
 * ruleset. Rejects (ApiError, 404) if the cluster firewall config has not
 * been observed by this daemon's collector yet. */
export function fetchClusterRuleset(): Promise<RulesetView> {
  return apiFetch<RulesetView>("/firewall/rulesets?scope=cluster");
}

/** GET /firewall/rulesets?scope=node[&node=]. With `node`, that node's
 * ruleset; without it, every observed node's ruleset (hierarchy nav). */
export function fetchNodeRuleset(node: string): Promise<RulesetView> {
  return apiFetch<RulesetView>(`/firewall/rulesets?scope=node&node=${encodeURIComponent(node)}`);
}

export function fetchNodeRulesets(): Promise<RulesetListResponse> {
  return apiFetch<RulesetListResponse>("/firewall/rulesets?scope=node");
}

/** GET /firewall/rulesets?scope=guest&ref=... — that guest's raw ruleset
 * plus its resolved (group-expanded) evaluation order in one payload. */
export function fetchGuestRuleset(ref: string): Promise<GuestRulesetResponse> {
  return apiFetch<GuestRulesetResponse>(`/firewall/rulesets?scope=guest&ref=${encodeURIComponent(ref)}`);
}

export function fetchGuestRulesets(): Promise<RulesetListResponse> {
  return apiFetch<RulesetListResponse>("/firewall/rulesets?scope=guest");
}

/** GET /firewall/rulesets?scope=group&name=... — that security group's own
 * rule list, for the group inspector surface (T-2002). */
export function fetchGroupRuleset(name: string): Promise<GroupRulesetResponse> {
  return apiFetch<GroupRulesetResponse>(`/firewall/rulesets?scope=group&name=${encodeURIComponent(name)}`);
}

/** GET /firewall/rulesets?scope=vnet&ref=... — that vnet's raw forward-chain
 * ruleset (T-3103). Addressed by `ref` (an sdn-vnet Ref), the same
 * convention scope=guest uses — a vnet ruleset's id is a "<zone>/<vnet>"
 * composite, not a plain name a `?vnet=`-style query param could carry
 * unambiguously the way `?node=` does. No resolved view in the response
 * (unlike scope=guest): this scope has no hardware-confirmed cluster+group
 * cascade model, so the server serves the raw ruleset only. */
export function fetchVnetRuleset(ref: string): Promise<RulesetView> {
  return apiFetch<RulesetView>(`/firewall/rulesets?scope=vnet&ref=${encodeURIComponent(ref)}`);
}

export function fetchVnetRulesets(): Promise<RulesetListResponse> {
  return apiFetch<RulesetListResponse>("/firewall/rulesets?scope=vnet");
}

/** GET /firewall/objects — every alias/ipset/security-group visible
 * anywhere, each with its usage count, plus the built-in macro catalog. */
export function fetchFirewallObjects(): Promise<FirewallObjectsResponse> {
  return apiFetch<FirewallObjectsResponse>("/firewall/objects");
}

/** GET /firewall/effects?group= — T-502 acceptance criterion 4's rule-
 * effects preview: every guest a security group's own rules actually
 * reach. */
export function fetchFirewallEffects(group: string): Promise<FirewallEffectsResponse> {
  return apiFetch<FirewallEffectsResponse>(`/firewall/effects?group=${encodeURIComponent(group)}`);
}
