// SPDX-License-Identifier: Apache-2.0

// EmbedDashboard (T-1706): a read-only embed of the home dashboard (T-904).
// It reuses the existing dashboard tile components verbatim — each one is
// already read-only (docs/features/monitoring.md §1: "every tile is
// read-only; click through to act") and issues only netRead-backed reads,
// so a netRead-scoped embed token renders them without a write surface. The
// curated subset here is exactly the tiles whose data a netRead embed token
// can fetch; tiles gated on other scopes (audit, flows) are intentionally
// omitted rather than shown in a permanent error state.
import { FindingsSeverityTile } from "../dashboard/FindingsSeverityTile";
import { DriftStatusTile } from "../dashboard/DriftStatusTile";
import { PendingChangesetsTile } from "../dashboard/PendingChangesetsTile";
import { MgmtRedundancyTile } from "../dashboard/MgmtRedundancyTile";

export function EmbedDashboard() {
  return (
    <div
      className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3"
      data-testid="embed-dashboard"
    >
      <FindingsSeverityTile />
      <DriftStatusTile />
      <PendingChangesetsTile />
      <MgmtRedundancyTile />
    </div>
  );
}
