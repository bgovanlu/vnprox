// SPDX-License-Identifier: Apache-2.0

// Pure tree-selection/formatting logic for the SDN cockpit, split out from
// SdnPage.tsx so it's directly Vitest-able without React Testing Library
// (docs/development.md: "Vitest + Testing Library on logic-bearing
// components").
import type { SdnSubnet, SdnTree, SdnVnet, SdnZone } from "../api/types";

/** Identifies one selected row in the zone -> vnet -> subnet tree. Kept as
 * a plain discriminated union (not a Ref string) since these aren't
 * inventory entities — SDN zones/vnets/subnets are the only nesting this
 * page needs and their ids can collide across zones (two zones can each
 * have a vnet named the same in principle), so the full path is carried
 * explicitly rather than relying on id uniqueness. */
export type SdnSelection =
  | { kind: "zone"; zoneId: string }
  | { kind: "vnet"; zoneId: string; vnetId: string }
  | { kind: "subnet"; zoneId: string; vnetId: string; subnetId: string };

export interface SdnSelectedEntity {
  selection: SdnSelection;
  zone: SdnZone;
  vnet?: SdnVnet;
  subnet?: SdnSubnet;
}

/** Resolves a selection against the current tree, returning undefined if
 * the tree hasn't loaded yet or the selected id(s) no longer exist (e.g.
 * the tree refetched and the entity was deleted). */
export function resolveSdnSelection(
  tree: SdnTree | undefined,
  selection: SdnSelection | undefined,
): SdnSelectedEntity | undefined {
  if (!tree || !selection) return undefined;
  const zone = tree.zones.find((z) => z.id === selection.zoneId);
  if (!zone) return undefined;
  if (selection.kind === "zone") return { selection, zone };

  const vnet = zone.vnets.find((v) => v.id === selection.vnetId);
  if (!vnet) return undefined;
  if (selection.kind === "vnet") return { selection, zone, vnet };

  const subnet = vnet.subnets.find((s) => s.id === selection.subnetId);
  if (!subnet) return undefined;
  return { selection, zone, vnet, subnet };
}

/** Renders one field value from a PendingDiff's staged/running map for
 * display: arrays join with commas, booleans render yes/no, missing values
 * render an em dash — never a raw `undefined`/`[object Object]`. */
export function formatDiffValue(v: unknown): string {
  if (v === undefined || v === null) return "—";
  if (Array.isArray(v)) return v.length > 0 ? v.map((x) => formatDiffValue(x)).join(", ") : "—";
  if (typeof v === "boolean") return v ? "yes" : "no";
  if (typeof v === "number" || typeof v === "string" || typeof v === "bigint") return String(v);
  // Any remaining type (plain object, symbol, function, ...) — JSON.stringify
  // gives a meaningful rendering instead of String()'s "[object Object]".
  return JSON.stringify(v);
}

/** The first selectable row of tree, for the page's default-selection
 * effect (nothing selected on first load) — the first zone, so the detail
 * panel isn't empty as soon as data arrives. */
export function firstSelection(tree: SdnTree): SdnSelection | undefined {
  const zone = tree.zones[0];
  return zone ? { kind: "zone", zoneId: zone.id } : undefined;
}
