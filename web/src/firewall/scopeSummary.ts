// "What will happen" summaries for scope enable/disable (docs/features/
// firewall.md §2: "Enable/disable firewall at each scope with an explicit
// 'what will happen' summary ... the classic PVE footgun made visible").
// Pure string generation — acceptance criterion 5 wants exact golden
// strings per scope, so every word here is deliberate; changing the
// wording is a product decision, not a refactor.
export type FwToggleScope = "cluster" | "node" | "guest";

export interface ScopeToggleSummaryInput {
  scope: FwToggleScope;
  /** true = the pending fw.options.update turns the scope's firewall ON;
   * false = turns it OFF. */
  enabling: boolean;
  /** Required (and only meaningful) for scope "node". */
  node?: string;
  /** Required (and only meaningful) for scope "guest" — the guest's
   * display label (hostname or ref), not the raw Ref string. */
  guestLabel?: string;
}

/**
 * Returns the exact "what will happen" sentence for one scope's enable/
 * disable toggle. Each scope's OFF wording states the actual PVE
 * enforcement cascade (docs/features/firewall.md §1's "cluster rules
 * apply directly to every guest" model, per internal/fw's Resolve doc
 * comment): turning the datacenter off silently disables every node and
 * guest too, with no per-scope override — the footgun this summary exists
 * to make visible before the user confirms.
 */
export function scopeToggleSummary(input: ScopeToggleSummaryInput): string {
  switch (input.scope) {
    case "cluster":
      return input.enabling
        ? "Turning the datacenter firewall ON activates every cluster-wide rule immediately, for every node and every guest — including any rule that could block management access. Review the ruleset before confirming."
        : "Turning the datacenter firewall OFF deactivates every rule in the cluster ruleset. Because cluster rules apply directly to every guest's evaluation order, every node's and every guest's firewall becomes unenforced too — there is no way to keep an individual node or guest protected while the datacenter firewall is off.";
    case "node": {
      const node = input.node ?? "this node";
      return input.enabling
        ? `Turning the firewall ON for node ${node} activates that node's own host-level rules. The datacenter firewall and every guest's firewall are unaffected by this change.`
        : `Turning the firewall OFF for node ${node} deactivates only that node's own host-level rules; the datacenter firewall and every guest's firewall are unaffected.`;
    }
    case "guest": {
      const guest = input.guestLabel ?? "this guest";
      return input.enabling
        ? `Turning the firewall ON for ${guest} activates its own rules and any security groups it includes. The datacenter firewall and node firewall are unaffected by this change.`
        : `Turning the firewall OFF for ${guest} deactivates its own rules and any security groups it includes; the datacenter firewall and node firewall are unaffected.`;
    }
    default:
      return "";
  }
}
