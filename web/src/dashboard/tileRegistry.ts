// SPDX-License-Identifier: Apache-2.0

// T-3911's built-in tile catalog: the fixed set of tiles DashboardPage.tsx
// used to render as a static grid (FindingsSeverityTile, DriftStatusTile,
// PendingChangesetsTile, MgmtRedundancyTile, TopTalkersTile,
// ServiceClassTile, RecentAuditTile), now composed through the same
// tile-grid mechanism plugin-provided tiles use (DashboardGrid.tsx) rather
// than hardcoded JSX. Each entry's `id` is a stable, versioned identifier
// persisted in a user's saved layout (docs/api.md's "Dashboard tile layout
// shape") — renaming a tile's title must never change its `id`, since that
// would silently orphan every existing saved layout that references it.
import type { ComponentType } from "react";
import { DriftStatusTile } from "./DriftStatusTile";
import { FindingsSeverityTile } from "./FindingsSeverityTile";
import { MgmtRedundancyTile } from "./MgmtRedundancyTile";
import { PendingChangesetsTile } from "./PendingChangesetsTile";
import { RecentAuditTile } from "./RecentAuditTile";
import { ServiceClassTile } from "./ServiceClassTile";
import { TopTalkersTile } from "./TopTalkersTile";

/** One built-in tile's registry entry. `label` is the tile's own `title`
 * (DashboardTile.tsx's `aria-label`d region name) — reused verbatim for the
 * "add tile" picker and the reorder buttons' accessible names so a screen
 * reader user hears the same name in both places. */
export interface BuiltinTileDef {
  id: string;
  label: string;
  Component: ComponentType;
}

export const BUILTIN_TILES: BuiltinTileDef[] = [
  { id: "builtin:findings", label: "Findings by severity", Component: FindingsSeverityTile },
  { id: "builtin:drift", label: "Drift status", Component: DriftStatusTile },
  { id: "builtin:changesets", label: "Pending changesets", Component: PendingChangesetsTile },
  { id: "builtin:mgmt-redundancy", label: "Management-path redundancy", Component: MgmtRedundancyTile },
  { id: "builtin:top-talkers", label: "Top talkers", Component: TopTalkersTile },
  { id: "builtin:service-class", label: "Service-network traffic", Component: ServiceClassTile },
  { id: "builtin:recent-audit", label: "Recent audit activity", Component: RecentAuditTile },
];

export function findBuiltinTile(id: string): BuiltinTileDef | undefined {
  return BUILTIN_TILES.find((t) => t.id === id);
}
