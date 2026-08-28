// SPDX-License-Identifier: Apache-2.0

// Pure Ref-string helpers for T-502's firewall editor navigation.

/**
 * Recovers a guest's own Ref ("guest:<node>:<vmid>") from its firewall
 * ruleset's Ref ("fw-ruleset:<node>:guest/<kind>/<vmid>") — the shape
 * GET /firewall/rulesets?scope=guest's list items carry (RulesetView.ref
 * mirrors the FwRuleset entity's own Ref, not the guest's), while
 * fetchGuestRuleset's `ref` param must be the guest's own ref (docs/api.md:
 * "?ref=<guest ref> (a guest:<node>:<vmid> Ref triplet)"). Mirrors
 * internal/fw's guestRefFromRulesetID (Go) — same "guest/<kind>/<vmid>" ID
 * convention, parsed client-side instead of server-side, so the guest tab
 * can go from "which ruleset is this" to "which guest owns it" without a
 * second round trip.
 *
 * Returns undefined if `fwRulesetRef` doesn't match the expected shape
 * (e.g. it's a cluster or node ruleset ref).
 */
export function guestRefFromFwRulesetRef(fwRulesetRef: string): string | undefined {
  const parts = fwRulesetRef.split(":");
  if (parts.length !== 3) return undefined;
  const node = parts[1];
  const id = parts[2];
  if (!node || !id) return undefined;
  const idParts = id.split("/");
  if (idParts.length !== 3 || idParts[0] !== "guest") return undefined;
  const vmid = idParts[2];
  if (!vmid) return undefined;
  return `guest:${node}:${vmid}`;
}

/**
 * Recovers a vnet's own Ref ("sdn-vnet::<zone>/<vnet>") from its firewall
 * ruleset's Ref ("fw-ruleset::vnet/<zone>/<vnet>") — the T-3103 vnet-scope
 * counterpart of guestRefFromFwRulesetRef. A vnet ruleset is cluster-scoped
 * (empty node), unlike a guest ruleset.
 *
 * Returns undefined if `fwRulesetRef` doesn't match the expected shape.
 */
export function vnetRefFromFwRulesetRef(fwRulesetRef: string): string | undefined {
  const parts = fwRulesetRef.split(":");
  if (parts.length !== 3) return undefined;
  const id = parts[2];
  if (!id) return undefined;
  const idParts = id.split("/");
  if (idParts.length !== 3 || idParts[0] !== "vnet") return undefined;
  const zone = idParts[1];
  const vnet = idParts[2];
  if (!zone || !vnet) return undefined;
  return `sdn-vnet::${zone}/${vnet}`;
}

/** Parses a firewall ruleset Ref ("fw-ruleset:<node>:<cluster|node|guest/kind/vmid|vnet/zone/vnet>")
 * into the scope tab + node/guest/vnet identity it belongs to, for deep-linking
 * from an object's "referenced by" list (RuleRef.ref, docs/api.md) back to
 * the rule's own editor tab. */
export interface FwRulesetLocation {
  scope: "cluster" | "node" | "guest" | "vnet";
  node?: string;
  guestRef?: string;
  vnetRef?: string;
}

export function locateFwRulesetRef(fwRulesetRef: string): FwRulesetLocation | undefined {
  const parts = fwRulesetRef.split(":");
  if (parts.length !== 3) return undefined;
  const node = parts[1];
  const id = parts[2];
  if (id === "cluster") return { scope: "cluster" };
  if (id === "node") return { scope: "node", node };
  const vnetRef = vnetRefFromFwRulesetRef(fwRulesetRef);
  if (vnetRef) return { scope: "vnet", vnetRef };
  const guestRef = guestRefFromFwRulesetRef(fwRulesetRef);
  if (guestRef) return { scope: "guest", node, guestRef };
  return undefined;
}
