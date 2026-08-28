// SPDX-License-Identifier: Apache-2.0

// Deep-link construction from a correlated log line to its rule (T-505
// AC2). Deliberately its own tiny module rather than living in
// web/src/firewall/ (T-501/T-502's rule-table pages, concurrently under
// active development in this task's own timeline): linking by stable
// identity — guest ref + resolved-order position + origin — rather than
// by any DOM query or route state those pages happen to expose today
// means this keeps working even if their internal structure changes
// later, per this task's completion report.
import type { FwLogRuleRef } from "../api/types";

/** Builds the `/firewall` path + query string a click on a correlated log
 * line should navigate to. The firewall pages (web/src/firewall/) do not
 * currently read these query params themselves (T-501's FirewallPage.tsx
 * manages scope/selection as local component state, not URL state) — this
 * function still emits a well-defined, documented contract so that a
 * follow-up task can wire the read side up without this call site
 * changing, and so the link is at least useful today (it navigates to the
 * Firewall page; the guest ref and rule position are visible in the URL
 * for copy/paste/support purposes even before that wiring lands). */
export function ruleDeepLinkPath(rule: FwLogRuleRef): string {
  const params = new URLSearchParams({
    scope: "guest",
    ref: rule.guestRef,
    pos: String(rule.pos),
    origin: rule.origin,
  });
  if (rule.groupName) {
    params.set("group", rule.groupName);
  }
  return `/firewall?${params.toString()}`;
}
