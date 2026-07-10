// Pure timeline-grouping logic for the History page: snapshots grouped by
// the changeset that produced them (docs/features tasks/T-206: "snapshot
// timeline grouped by changeset"), manual/scheduled snapshots standing
// alone. Kept free of React so it's directly unit-testable.
import type { SnapshotSummary } from "../api/snapshots";

/** One timeline entry: either a changeset's pre/post snapshot pair (or
 * whichever of the two exists) or a single standalone snapshot. */
export interface TimelineGroup {
  /** Grouping key: the changeset id, or the snapshot's own id for
   * standalone (manual/scheduled) snapshots. */
  key: string;
  changesetId?: string;
  /** Newest takenAt in the group — the group's position on the timeline. */
  at: number;
  snapshots: SnapshotSummary[];
}

/** Groups an already-newest-first snapshot page into timeline groups,
 * preserving newest-first order between groups. Snapshots sharing a
 * changesetId collapse into one group (its `at` is the newest member's);
 * snapshots without one (manual/scheduled) each form their own group. */
export function groupSnapshots(snapshots: SnapshotSummary[]): TimelineGroup[] {
  const groups: TimelineGroup[] = [];
  const byChangeset = new Map<string, TimelineGroup>();

  for (const snap of snapshots) {
    if (snap.changesetId) {
      const existing = byChangeset.get(snap.changesetId);
      if (existing) {
        existing.snapshots.push(snap);
        existing.at = Math.max(existing.at, snap.takenAt);
        continue;
      }
      const group: TimelineGroup = {
        key: snap.changesetId,
        changesetId: snap.changesetId,
        at: snap.takenAt,
        snapshots: [snap],
      };
      byChangeset.set(snap.changesetId, group);
      groups.push(group);
      continue;
    }
    groups.push({ key: snap.id, at: snap.takenAt, snapshots: [snap] });
  }

  // Input is newest-first, but a group's `at` may have grown after later
  // (older) members joined; re-sort to keep the timeline strictly ordered.
  return groups.sort((a, b) => b.at - a.at);
}

/** Human label for a snapshot's kind within its group ("Before apply" /
 * "After commit" reads better on a timeline than raw pre/post). */
export function kindLabel(kind: SnapshotSummary["kind"]): string {
  switch (kind) {
    case "pre":
      return "Before apply";
    case "post":
      return "After commit";
    case "manual":
      return "Manual";
    case "scheduled":
      return "Scheduled";
  }
}
