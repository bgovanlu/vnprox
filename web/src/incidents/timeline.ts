// SPDX-License-Identifier: Apache-2.0

// Pure presentation logic for T-2804's incident timeline. Kept free of React
// so it is directly unit-testable — and because the two things worth asserting
// about this feature's UI are both pure: that events are rendered in one
// chronological order across sources rather than grouped by source, and that a
// source which contributed nothing says WHY.
import type { IncidentEvent, IncidentSource, IncidentSourceReport, IncidentTimeline } from "../api/incidents";

/** Human label for a source, used in the legend and the per-source status
 * list. */
export function sourceLabel(source: IncidentSource): string {
  switch (source) {
    case "finding":
      return "Findings";
    case "changeset":
      return "Changesets";
    case "diagnosis":
      return "Diagnosis";
    case "capture":
      return "Captures";
    case "flow":
      return "Flows";
    case "annotation":
      return "Notes";
  }
}

/** A short glyph per source, so a reader can scan one column and see the
 * interleaving rather than reading every row's label. */
export function sourceGlyph(source: IncidentSource): string {
  switch (source) {
    case "finding":
      return "!";
    case "changeset":
      return "Δ";
    case "diagnosis":
      return "?";
    case "capture":
      return "◉";
    case "flow":
      return "→";
    case "annotation":
      return "✎";
  }
}

/** The order the server sent is already strictly chronological (docs/api.md:
 * "in strict chronological order"). This re-sorts defensively on the same
 * rule — `at`, then the stable event id — so a client that merges an
 * optimistically-added annotation still renders one ordered timeline rather
 * than appending it to the end.
 *
 * It deliberately does NOT group by source: grouping is exactly the failure
 * this feature exists to remove. */
export function orderEvents(events: readonly IncidentEvent[]): IncidentEvent[] {
  return [...events].sort((a, b) => (a.at === b.at ? a.id.localeCompare(b.id) : a.at - b.at));
}

/** A source that contributed nothing, with the reason. Renders as a warning
 * strip above the timeline: an empty timeline because a source is dead is a
 * different statement from an empty timeline because nothing happened, and an
 * operator must not have to guess which one they are looking at. */
export interface SourceGap {
  source: IncidentSource;
  label: string;
  status: IncidentSourceReport["status"];
  detail: string;
}

export function sourceGaps(sources: readonly IncidentSourceReport[]): SourceGap[] {
  return sources
    .filter((s) => s.status !== "ok")
    .map((s) => ({
      source: s.source,
      label: sourceLabel(s.source),
      status: s.status,
      detail: s.detail ?? "",
    }));
}

/** What the diff strip should say. Exactly one of `diff` and `error` is
 * meaningful, and an absent diff is NEVER rendered as "nothing changed" — the
 * change engine's refusal names the snapshots that do exist, which is both the
 * explanation and the fix. */
export interface DiffSummary {
  available: boolean;
  changed: number;
  unattributed: number;
  message: string;
  code?: string;
}

export function diffSummary(timeline: IncidentTimeline): DiffSummary {
  if (!timeline.diff) {
    return {
      available: false,
      changed: 0,
      unattributed: 0,
      message: timeline.diffError ?? "no point-in-time diff could be computed for this window",
      code: timeline.diffErrorCode,
    };
  }
  const diff = timeline.diff;
  const changed = diff.added.length + diff.removed.length + diff.modified.length;
  return {
    available: true,
    changed,
    unattributed: diff.unattributedCount,
    message:
      changed === 0
        ? "nothing changed in /etc/network/interfaces across this window"
        : `${String(changed)} ${changed === 1 ? "difference" : "differences"}, ${String(diff.unattributedCount)} made outside vnprox`,
  };
}

/** Window label: "still unfolding" is a materially different thing from a
 * frozen window, and the export of each means something different. */
export function windowLabel(timeline: IncidentTimeline): string {
  const from = new Date(timeline.window.from * 1000).toLocaleString();
  if (timeline.window.live) {
    return `${from} → now`;
  }
  return `${from} → ${new Date(timeline.window.to * 1000).toLocaleString()}`;
}
