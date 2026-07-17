// Home dashboard (T-904, docs/features/monitoring.md §1/§3/§5,
// docs/features/topology.md §3): a network-at-a-glance landing page built
// entirely on routes that already exist (findings, changesets,
// protected-interfaces/status, metrics/live, audit) — zero new backend
// surface, read-only throughout (no tile's query code issues a mutating
// request; see this task's own report for the grep confirming it). Every
// tile deep-links to its owning page/surface (AC3), and every tile has an
// explicit "all clear"/empty state rather than ever rendering blank
// (AC4) — see DashboardTile.tsx's shared shell for that contract.
import { FindingsSeverityTile } from "./FindingsSeverityTile";
import { DriftStatusTile } from "./DriftStatusTile";
import { PendingChangesetsTile } from "./PendingChangesetsTile";
import { MgmtRedundancyTile } from "./MgmtRedundancyTile";
import { TopTalkersTile } from "./TopTalkersTile";
import { RecentAuditTile } from "./RecentAuditTile";

export function DashboardPage() {
  return (
    <div className="flex h-full flex-col gap-4">
      <div>
        <h1 className="text-xl font-semibold">Home</h1>
        <p className="text-sm text-slate-500 dark:text-slate-400">
          Network at a glance: open findings, drift, pending changesets, management-path redundancy, the busiest
          bridge&apos;s top talkers, and recent audit activity. Every tile is read-only — click through to act.
        </p>
      </div>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
        <FindingsSeverityTile />
        <DriftStatusTile />
        <PendingChangesetsTile />
        <MgmtRedundancyTile />
        <TopTalkersTile />
        <RecentAuditTile />
      </div>
    </div>
  );
}
