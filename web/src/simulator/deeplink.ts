// Deep-link construction from a simulator deny verdict's blocking rule to
// its editor (T-504 AC1: "one click lands in the rule editor with the rule
// focused"). Deliberately mirrors web/src/fwlog/deeplink.ts's
// ruleDeepLinkPath contract exactly (same query param names: scope, ref,
// pos, origin, group) rather than inventing a second convention — both
// T-505's log viewer and this task's simulator link into the same
// `/firewall` consuming side (FirewallPage.tsx's `focusRule` reading,
// added by this task).
//
// The `ref` query param is always the GUEST's own ref (`guest:<node>:
// <vmid>`), derived from the resolved endpoint at the blocking rule's
// enforcement point — never `SimBlockingRule.rulesetRef` directly. Two
// reasons, both load-bearing (found while integration-testing this task's
// own E2E fixture against the real engine, see planning/reports/T-504.md):
//
//  1. `rulesetRef` is only populated by internal/sim/firewall.go's
//     `ruleRef` for `origin: cluster`/`origin: group` (it points at the
//     *cluster* ruleset's own ref in those cases) — for `origin: guest`,
//     the single most common deny case ("this guest's own rule blocked
//     it"), `rulesetRef` is left as the empty string. Using it directly
//     would silently produce a dead link (`ref=`) for exactly the
//     scenario this task's AC1 demonstrates. Not fixed in internal/sim
//     itself per this task's "pure consumer" constraint; flagged in the
//     report as a backend follow-up.
//  2. Even where `rulesetRef` IS populated (cluster/group origin), it
//     names the *cluster* ruleset — but T-505's own established deep-link
//     convention (FwLogRuleRef: `{guestRef, origin, groupName?, pos}`)
//     always targets the *guest*'s resolved view regardless of a rule's
//     true origin (a cluster-origin rule still appears there, labeled by
//     its OriginBadge) — the more useful landing spot, since it shows the
//     rule in the context of the actual guest being blocked. Matching that
//     exact convention here means both producers land on the same kind of
//     target and FirewallPage's one `focusRule` consumer works for either.
//
// `ResolvedEndpoint.guest` (see api/types.ts) is exactly this guest ref
// string (internal/sim/endpoint.go: `Guest: nic.Guest.String()`) — always
// present for whichever endpoint a blockingRule's enforcementPoint names,
// since firewall enforcement only ever happens on a resolved guest's own
// chain (an IP/external endpoint can never itself be an enforcement point).
import type { SimBlockingRule, SimResolvedEndpoint } from "../api/types";

/** The guest ref (`guest:<node>:<vmid>`) whose chain produced `blockingRule`
 * — the deep link's target — or undefined in the (should-never-happen)
 * case where the named endpoint somehow carries no guest ref, so callers
 * can degrade honestly (no link) rather than build a dead one. */
export function blockingRuleGuestRef(
  blockingRule: SimBlockingRule,
  src: SimResolvedEndpoint,
  dst: SimResolvedEndpoint,
): string | undefined {
  const endpoint = blockingRule.enforcementPoint === "source-guest-out" ? src : dst;
  return endpoint.guest;
}

export function blockingRuleDeepLinkPath(rule: SimBlockingRule, guestRef: string): string {
  const params = new URLSearchParams({
    scope: "guest",
    ref: guestRef,
    pos: String(rule.pos),
    origin: rule.origin,
  });
  if (rule.groupName) {
    params.set("group", rule.groupName);
  }
  return `/firewall?${params.toString()}`;
}
