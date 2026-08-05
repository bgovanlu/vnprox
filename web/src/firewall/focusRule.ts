// Parses the firewall deep-link query params (`scope`, `ref`, `pos`,
// `origin`, `group`) two independent producers already emit:
// web/src/fwlog/deeplink.ts's ruleDeepLinkPath (T-505, correlated log
// lines) and web/src/simulator/deeplink.ts's blockingRuleDeepLinkPath
// (T-504, a deny verdict's blocking rule). Both were designed against the
// same contract (guestRef/rulesetRef + pos + origin — never DOM position)
// specifically so this one consuming module could read either's link the
// same way; FirewallPage.tsx is the only caller.
import type { FwOrigin, ResolvedRuleView } from "../api/types";

export interface FocusRule {
  pos: number;
  origin: FwOrigin;
  groupName?: string;
}

/** Whether `entry` is the exact rule `focusRule` names — position alone
 * isn't quite enough to disambiguate origin/group in principle, so this
 * checks the full identity triple the deep link carries. Shared by
 * ResolvedViewTable (which row to highlight/scroll to) and FirewallPage
 * (T-2002 AC1: whether the deep link's target still exists at all, so a
 * rule that's been deleted/reordered/moved degrades with a message instead
 * of a silent empty highlight). Lives here rather than in
 * ResolvedViewTable.tsx so this module — already the "no components, just
 * the deep-link contract" file — stays the one place either consumer
 * imports the matching logic from. */
export function matchesFocus(entry: ResolvedRuleView, focusRule: FocusRule | undefined): boolean {
  if (!focusRule) return false;
  return entry.pos === focusRule.pos && entry.origin === focusRule.origin && (entry.groupName ?? "") === (focusRule.groupName ?? "");
}

export interface FirewallDeepLinkParams {
  scope?: string;
  ref?: string;
  focusRule?: FocusRule;
}

// "default" never appears on a real rule (see FwOrigin's own doc comment
// in api/types.ts) — a deep link naming it is malformed, not a real target.
const FOCUSABLE_ORIGINS = new Set<string>(["cluster", "group", "guest"]);

/** Parses a query string (or URLSearchParams) into a FirewallDeepLinkParams.
 * A malformed/partial `pos`/`origin` degrades to `focusRule: undefined`
 * (the page still honors `scope`/`ref` alone) rather than throwing — a
 * hand-edited or truncated URL should never crash the firewall page. */
export function parseFirewallDeepLink(search: string | URLSearchParams): FirewallDeepLinkParams {
  const params = typeof search === "string" ? new URLSearchParams(search) : search;
  const scope = params.get("scope") ?? undefined;
  const ref = params.get("ref") ?? undefined;
  const posStr = params.get("pos");
  const origin = params.get("origin");
  const groupName = params.get("group") ?? undefined;

  if (posStr === null || origin === null || !FOCUSABLE_ORIGINS.has(origin)) {
    return { scope, ref };
  }
  const pos = Number(posStr);
  if (!Number.isInteger(pos)) {
    return { scope, ref };
  }
  return { scope, ref, focusRule: { pos, origin: origin as FwOrigin, groupName } };
}
