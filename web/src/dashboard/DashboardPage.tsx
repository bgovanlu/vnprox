// SPDX-License-Identifier: Apache-2.0

// Home dashboard (T-904, docs/features/monitoring.md §1/§3/§5,
// docs/features/topology.md §3; T-3911 made it composable). A
// network-at-a-glance landing page: seven built-in tiles
// (findings/drift/changesets/mgmt-redundancy/top-talkers/service-class/
// audit, each built entirely on routes that already existed pre-T-3911 —
// zero new backend surface for any of them) plus whatever `dashboardTile`
// plugins are installed, composed through one per-user, add/remove/
// reorder-able grid (DashboardGrid.tsx) rather than the fixed layout this
// page used to hardcode. Every tile still deep-links to its owning
// page/surface (AC3) and still has an explicit "all clear"/empty state
// rather than ever rendering blank (AC4) — see DashboardTile.tsx's shared
// shell for that contract, which every tile kind (built-in, plugin,
// unavailable-placeholder) now renders through identically.
import { PageHeader } from "../components/PageHeader";
import { DashboardGrid } from "./DashboardGrid";

export function DashboardPage() {
  return (
    <div className="flex h-full flex-col gap-4">
      <PageHeader
        title="Home"
        description="Network at a glance: open findings, drift, pending changesets, management-path redundancy, the busiest
          bridge's top talkers, service-network traffic, recent audit activity, and any dashboard tiles your
          installed plugins contribute. Every tile is read-only — click through to act. Add, remove, and
          reorder tiles with the controls above and beside each one."
      />
      <DashboardGrid />
    </div>
  );
}
