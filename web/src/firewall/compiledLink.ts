// SPDX-License-Identifier: Apache-2.0

// Deep-link construction between the rule editor (RuleEditor.tsx, this
// directory) and the compiled-ruleset inspector
// (web/src/nftables/CompiledRulesetPage.tsx, T-3904) — the two directions
// acceptance criterion 2's "cross-linked" language asks for. Deliberately
// its own tiny module, matching web/src/fwlog/deeplink.ts's precedent
// (link by stable identity, not DOM position), and deliberately scoped to
// cluster/node only: this is exactly the pair internal/api/nftables.go's
// attributeRule can determine a match for (see that file's doc comment) —
// guest/vnet-scope rules have no compiled-chain attribution today, so no
// link is offered for them (an absent affordance, not a broken one).
export type CompiledLinkScope = "cluster" | "node";

/** Builds the `/firewall/compiled` path a rule row's "View compiled
 * chain" action should navigate to. `node` is required for scope="node"
 * (which node's compiled ruleset to show); omitted for scope="cluster"
 * (the compiled view's own node selector still applies — cluster rules
 * compile into every node's host chain alike, so there is no single
 * "the" node to default to here). */
export function compiledChainDeepLinkPath(scope: CompiledLinkScope, pos: number, node?: string): string {
  const params = new URLSearchParams({ scope, pos: String(pos) });
  if (node) params.set("node", node);
  return `/firewall/compiled?${params.toString()}`;
}

/** Builds the `/firewall` path a compiled rule's attribution link (back to
 * its originating cluster/node rule) should navigate to — the reverse
 * direction, reusing the exact `scope`/`pos` param names FirewallPage.tsx
 * already reads for cluster/node scope (see focusRule.ts's doc comment:
 * cluster/node scope deep links don't carry `origin`/`group`, unlike the
 * guest-scope resolved-view contract those params exist for). */
export function ruleEditorDeepLinkPath(scope: CompiledLinkScope, pos: number, node?: string): string {
  const params = new URLSearchParams({ scope, pos: String(pos) });
  if (node) params.set("node", node);
  return `/firewall?${params.toString()}`;
}
