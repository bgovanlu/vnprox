// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";

import type { TopologyDiffResponse, TopologyEntityDiff } from "../api/topologyDiff";
import {
  JUST_NOW_SECONDS,
  TODAY_SECONDS,
  WEEK_SECONDS,
  computeRecencyOverlay,
  recencyBucketForAge,
  recencyBucketForInstant,
  recencyBucketPhrase,
  recencyMarkColor,
  recencyMarkGlyph,
  summarizeRecencyOverlay,
  type RecencyBucket,
} from "./recencyOverlay";

function row(
  ref: string,
  change: TopologyEntityDiff["change"],
  attribution: TopologyEntityDiff["attribution"],
  name?: string,
): TopologyEntityDiff {
  return {
    ref,
    kind: ref.split(":")[0] ?? "bridge",
    node: ref.split(":")[1],
    name: name ?? ref.split(":")[2],
    change,
    fields: [{ field: "MTUDeclared", before: "1500", after: "9000" }],
    attribution,
  };
}

function diff(partial: Partial<TopologyDiffResponse>): TopologyDiffResponse {
  return {
    from: { requested: "snap-1", snapshotId: "snap-1", kind: "scheduled", at: 0 },
    to: { requested: "now", live: true, at: 1_000_000 },
    added: [],
    removed: [],
    modified: [],
    coverage: { nodes: ["pve1"], paths: ["/etc/network/interfaces"] },
    unattributedCount: 0,
    ...partial,
  };
}

const NOW = 1_000_000;

function attributedAt(at: number) {
  return { attributed: true, changesetId: "cs-1", changesetTitle: "add vmbr9", actor: "alice@pve", at };
}
const unattributed = { attributed: false };

describe("recencyBucketForAge", () => {
  it("buckets the four documented tiers at their exact edges", () => {
    expect(recencyBucketForAge(0)).toBe("justNow");
    expect(recencyBucketForAge(JUST_NOW_SECONDS - 1)).toBe("justNow");
    expect(recencyBucketForAge(JUST_NOW_SECONDS)).toBe("today");
    expect(recencyBucketForAge(TODAY_SECONDS - 1)).toBe("today");
    expect(recencyBucketForAge(TODAY_SECONDS)).toBe("thisWeek");
    expect(recencyBucketForAge(WEEK_SECONDS - 1)).toBe("thisWeek");
    expect(recencyBucketForAge(WEEK_SECONDS)).toBe("older");
    expect(recencyBucketForAge(WEEK_SECONDS * 10)).toBe("older");
  });

  it("clamps a negative age (clock skew) to the hottest bucket rather than erroring", () => {
    expect(recencyBucketForAge(-500)).toBe("justNow");
  });
});

describe("recencyBucketForInstant", () => {
  it("matches recencyBucketForAge given the equivalent elapsed time", () => {
    expect(recencyBucketForInstant(NOW, NOW - 60)).toBe("justNow");
    expect(recencyBucketForInstant(NOW, NOW - TODAY_SECONDS - 1)).toBe("thisWeek");
  });
});

describe("computeRecencyOverlay", () => {
  it("buckets an attributed change by its exact changeset instant", () => {
    const overlay = computeRecencyOverlay(
      diff({ modified: [row("bridge:pve1:vmbr0", "modified", attributedAt(NOW - 60))] }),
      () => true,
      NOW,
    );
    expect(overlay.marks).toHaveLength(1);
    expect(overlay.marks[0]?.bucket).toBe("justNow");
    expect(overlay.marks[0]?.changesetId).toBe("cs-1");
    expect(overlay.marks[0]?.at).toBe(NOW - 60);
    expect(overlay.driftCount).toBe(0);
  });

  it("routes an unattributed change to the drift bucket instead of guessing a time", () => {
    const overlay = computeRecencyOverlay(
      diff({ modified: [row("bridge:pve1:vmbr0", "modified", unattributed)] }),
      () => true,
      NOW,
    );
    expect(overlay.marks[0]?.bucket).toBe("drift");
    expect(overlay.marks[0]?.at).toBeUndefined();
    expect(overlay.marks[0]?.changesetId).toBeUndefined();
    expect(overlay.driftCount).toBe(1);
  });

  it("never invents a changeset for a drift mark even when other fields are present", () => {
    // Regression guard: a mark's changesetId must come only from a truly
    // attributed row — never leak an unrelated field.
    const overlay = computeRecencyOverlay(
      diff({ added: [row("bridge:pve1:vmbr9", "added", unattributed)] }),
      () => true,
      NOW,
    );
    expect(overlay.marks[0]?.attributed).toBe(false);
    expect(overlay.marks[0]?.changesetId).toBeUndefined();
  });

  it("routes entities not on the current map to offMap, mirroring diffOverlay", () => {
    const overlay = computeRecencyOverlay(
      diff({ removed: [row("bridge:pve1:vmbr5", "removed", unattributed)] }),
      (ref) => ref !== "bridge:pve1:vmbr5",
      NOW,
    );
    expect(overlay.marks).toHaveLength(0);
    expect(overlay.offMap.map((m) => m.nodeId)).toEqual(["bridge:pve1:vmbr5"]);
    expect(overlay.changedCount).toBe(1);
  });

  it("is empty for an absent diff without throwing", () => {
    const overlay = computeRecencyOverlay(undefined, () => true, NOW);
    expect(overlay.marks).toHaveLength(0);
    expect(summarizeRecencyOverlay(overlay)).toBe("No changes in the lookback window.");
  });

  it("counts drift separately from the total in the summary line", () => {
    const overlay = computeRecencyOverlay(
      diff({
        modified: [
          row("bridge:pve1:vmbr0", "modified", attributedAt(NOW - 60)),
          row("bridge:pve1:vmbr1", "modified", unattributed),
        ],
      }),
      () => true,
      NOW,
    );
    const summary = summarizeRecencyOverlay(overlay);
    expect(summary).toContain("2 entities changed");
    expect(summary).toContain("1 outside vnprox");
  });

  it("preserves the server's ref order", () => {
    const d = diff({
      added: [row("bridge:pve1:vmbr1", "added", attributedAt(NOW)), row("bridge:pve1:vmbr2", "added", attributedAt(NOW))],
      modified: [row("physnic:pve1:eno1", "modified", unattributed)],
    });
    const marks = computeRecencyOverlay(d, () => true, NOW).marks.map((m) => m.nodeId);
    expect(marks).toEqual(["bridge:pve1:vmbr1", "bridge:pve1:vmbr2", "physnic:pve1:eno1"]);
  });
});

describe("recencyMarkColor", () => {
  it("gives every bucket a distinct color", () => {
    const buckets: RecencyBucket[] = ["drift", "justNow", "today", "thisWeek", "older"];
    expect(new Set(buckets.map(recencyMarkColor)).size).toBe(buckets.length);
  });

  it("keeps drift off the justNow/today/thisWeek/older heat gradient", () => {
    // Regression guard for the "drift is not merely a bit stale" design
    // decision (recencyOverlay.ts's doc comment) — drift must never
    // coincide with any timed bucket's color.
    const timed: RecencyBucket[] = ["justNow", "today", "thisWeek", "older"];
    for (const b of timed) {
      expect(recencyMarkColor("drift")).not.toBe(recencyMarkColor(b));
    }
  });
});

describe("recencyMarkGlyph", () => {
  it("is a distinct, single-character glyph per bucket", () => {
    const buckets: RecencyBucket[] = ["drift", "justNow", "today", "thisWeek", "older"];
    const glyphs = buckets.map(recencyMarkGlyph);
    expect(new Set(glyphs).size).toBe(buckets.length);
    for (const g of glyphs) expect(g).toHaveLength(1);
  });
});

describe("recencyBucketPhrase", () => {
  it("gives every bucket a non-empty, human-readable phrase", () => {
    const buckets: RecencyBucket[] = ["drift", "justNow", "today", "thisWeek", "older"];
    for (const b of buckets) {
      expect(recencyBucketPhrase(b).length).toBeGreaterThan(0);
    }
  });
});
