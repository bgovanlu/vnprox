// SPDX-License-Identifier: Apache-2.0

// Home dashboard (T-904, docs/features/monitoring.md §1/§3/§5,
// docs/features/topology.md §3): a network-at-a-glance landing page built
// entirely on routes that already exist (findings, changesets,
// protected-interfaces/status, metrics/live, audit) — zero new backend
// surface, read-only throughout (no tile's query code issues a mutating
// request; see this task's own report for the grep confirming it). Every
// tile deep-links to its owning page/surface (AC3), and every tile has an
// explicit "all clear"/empty state rather than ever rendering blank
// (AC4) — see DashboardTile.tsx's shared shell for that contract.
//
// ServiceClassTile (T-1504) extends this set: per-serviceClass bytes/sec
// breakdown over GET /flows' retained window (migration/backup/Ceph/
// corosync attribution) — same "existing route, client-side computation"
// convention every other tile here follows.
import { PageHeader } from "../components/PageHeader";
import { FindingsSeverityTile } from "./FindingsSeverityTile";
import { DriftStatusTile } from "./DriftStatusTile";
import { PendingChangesetsTile } from "./PendingChangesetsTile";
import { MgmtRedundancyTile } from "./MgmtRedundancyTile";
import { TopTalkersTile } from "./TopTalkersTile";
import { ServiceClassTile } from "./ServiceClassTile";
import { RecentAuditTile } from "./RecentAuditTile";

export function DashboardPage() {
  return (
    <div className="flex h-full flex-col gap-4">
      <PageHeader
        title="Home"
        description="Network at a glance: open findings, drift, pending changesets, management-path redundancy, the busiest
          bridge's top talkers, service-network traffic, and recent audit activity. Every tile is read-only —
          click through to act."
      />
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
        <FindingsSeverityTile />
        <DriftStatusTile />
        <PendingChangesetsTile />
        <MgmtRedundancyTile />
        <TopTalkersTile />
        <ServiceClassTile />
        <RecentAuditTile />
      </div>
    </div>
  );
}
