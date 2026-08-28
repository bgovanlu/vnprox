// SPDX-License-Identifier: Apache-2.0

// T-2804's presentation logic. The two assertions that matter are the two
// the feature exists for: one chronological order ACROSS sources (not five
// blocks), and honesty about what the view could not see.
import { describe, expect, it } from "vitest";

import type { IncidentEvent, IncidentSourceReport, IncidentTimeline } from "../api/incidents";
import { diffSummary, orderEvents, sourceGaps, windowLabel } from "./timeline";

function event(id: string, at: number, source: IncidentEvent["source"]): IncidentEvent {
  return { id, at, source, kind: "k", summary: `${source} at ${String(at)}` };
}

function timeline(partial: Partial<IncidentTimeline> = {}): IncidentTimeline {
  return {
    incident: {
      id: "inc-1",
      title: "vmbr0 down",
      status: "open",
      openedBy: "brian@pam",
      openedAt: 1000,
      startedAt: 1000,
      retroactive: false,
      annotations: [],
    },
    window: { from: 1000, to: 2000, live: false },
    events: [],
    sources: [],
    caveats: [],
    ...partial,
  };
}

describe("orderEvents", () => {
  it("interleaves sources by time rather than grouping them", () => {
    // Deliberately shuffled, and deliberately NOT same-source runs: a
    // renderer that sorted within each source and concatenated the blocks
    // would pass a same-source fixture and fail this one.
    const ordered = orderEvents([
      event("flow:1", 1040, "flow"),
      event("finding:1", 1000, "finding"),
      event("capture:1", 1030, "capture"),
      event("changeset:1", 1010, "changeset"),
      event("annotation:1", 1045, "annotation"),
      event("diagnosis:1", 1020, "diagnosis"),
    ]);
    expect(ordered.map((e) => e.source)).toEqual([
      "finding",
      "changeset",
      "diagnosis",
      "capture",
      "flow",
      "annotation",
    ]);
  });

  it("breaks a timestamp tie on the stable event id, so the order never flickers", () => {
    const a = orderEvents([event("flow:2", 100, "flow"), event("finding:1", 100, "finding")]);
    const b = orderEvents([event("finding:1", 100, "finding"), event("flow:2", 100, "flow")]);
    expect(a.map((e) => e.id)).toEqual(b.map((e) => e.id));
    expect(a[0]?.id).toBe("finding:1");
  });

  it("does not mutate its input", () => {
    const input = [event("b", 2, "flow"), event("a", 1, "finding")];
    orderEvents(input);
    expect(input.map((e) => e.id)).toEqual(["b", "a"]);
  });
});

describe("sourceGaps", () => {
  it("reports every source that contributed less than everything, and stays quiet otherwise", () => {
    const sources: IncidentSourceReport[] = [
      { source: "finding", status: "ok", count: 3 },
      { source: "flow", status: "unavailable", count: 0, detail: "no flow samples are collected on this node" },
      { source: "capture", status: "error", count: 0, detail: "listing capture sessions failed: boom" },
      { source: "changeset", status: "truncated", count: 200, detail: "the newest 200 are shown" },
    ];
    const gaps = sourceGaps(sources);
    expect(gaps.map((g) => g.source)).toEqual(["flow", "capture", "changeset"]);
    expect(gaps[0]?.detail).toContain("no flow samples");

    // The control: an all-ok timeline produces no strip at all, so the
    // presence of one above is a reading of the statuses.
    expect(sourceGaps([{ source: "finding", status: "ok", count: 3 }])).toEqual([]);
  });
});

describe("diffSummary", () => {
  it("never renders a refusal as 'nothing changed'", () => {
    const summary = diffSummary(
      timeline({
        diffError: 'change: no snapshot covers the from point "1000"; nearest available: snap-9 (scheduled)',
        diffErrorCode: "no_snapshot_in_range",
      }),
    );
    expect(summary.available).toBe(false);
    expect(summary.code).toBe("no_snapshot_in_range");
    // The message names the snapshots that DO exist — that is the whole
    // reason it is surfaced verbatim.
    expect(summary.message).toContain("snap-9");
    expect(summary.message).not.toContain("nothing changed");
  });

  it("counts the differences and names how many nobody made through vnprox", () => {
    const summary = diffSummary(
      timeline({
        diff: {
          from: { requested: "1000", snapshotId: "snap-1", at: 1000 },
          to: { requested: "2000", snapshotId: "snap-2", at: 2000 },
          added: [
            {
              ref: "iface:pve1:wg0",
              kind: "iface",
              change: "added",
              fields: [],
              attribution: { attributed: false },
            },
          ],
          removed: [],
          modified: [],
          coverage: { nodes: ["pve1"], paths: ["/etc/network/interfaces"] },
          unattributedCount: 1,
        },
      }),
    );
    expect(summary.available).toBe(true);
    expect(summary.changed).toBe(1);
    expect(summary.message).toContain("1 difference");
    expect(summary.message).toContain("1 made outside vnprox");
  });

  it("says a covered window with no differences is exactly that", () => {
    const summary = diffSummary(
      timeline({
        diff: {
          from: { requested: "1000", at: 1000 },
          to: { requested: "now", live: true, at: 2000 },
          added: [],
          removed: [],
          modified: [],
          coverage: { nodes: ["pve1"], paths: ["/etc/network/interfaces"] },
          unattributedCount: 0,
        },
      }),
    );
    expect(summary.available).toBe(true);
    expect(summary.message).toContain("nothing changed");
  });
});

describe("windowLabel", () => {
  it("says 'now' for a window that is still unfolding", () => {
    expect(windowLabel(timeline({ window: { from: 1000, to: 9999, live: true } }))).toContain("→ now");
  });

  it("names both ends of a frozen window", () => {
    expect(windowLabel(timeline({ window: { from: 1000, to: 2000, live: false } }))).not.toContain("now");
  });
});
