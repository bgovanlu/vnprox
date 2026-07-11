// Deep-link construction from a simulator deny verdict's blocking rule to
// its editor (T-504 AC1: "one click lands in the rule editor with the rule
// focused"). Deliberately mirrors web/src/fwlog/deeplink.ts's
// ruleDeepLinkPath contract exactly (same query param names: scope, ref,
// pos, origin, group) rather than inventing a second convention — both
// T-505's log viewer and this task's simulator link into the same
// `/firewall` consuming side (FirewallPage.tsx's `focusRule` reading,
// added by this task).
import type { SimBlockingRule } from "../api/types";

export function blockingRuleDeepLinkPath(rule: SimBlockingRule): string {
  const params = new URLSearchParams({
    scope: "guest",
    ref: rule.rulesetRef,
    pos: String(rule.pos),
    origin: rule.origin,
  });
  if (rule.groupName) {
    params.set("group", rule.groupName);
  }
  return `/firewall?${params.toString()}`;
}
