// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { groupSnapshots, kindLabel } from "./timeline";
import type { SnapshotSummary } from "../api/snapshots";

function snap(id: string, kind: SnapshotSummary["kind"], takenAt: number, changesetId?: string): SnapshotSummary {
  return { id, kind, takenAt, changesetId, nodes: ["pve1"] };
}

describe("groupSnapshots", () => {
  it("groups a changeset's pre/post pair into one timeline entry", () => {
    const groups = groupSnapshots([
      snap("s-post", "post", 200, "cs-1"),
      snap("s-pre", "pre", 100, "cs-1"),
    ]);
    expect(groups).toHaveLength(1);
    expect(groups[0]?.changesetId).toBe("cs-1");
    expect(groups[0]?.at).toBe(200);
    expect(groups[0]?.snapshots.map((s) => s.id)).toEqual(["s-post", "s-pre"]);
  });

  it("keeps manual snapshots as standalone groups", () => {
    const groups = groupSnapshots([
      snap("m-2", "manual", 300),
      snap("s-post", "post", 200, "cs-1"),
      snap("m-1", "manual", 150),
      snap("s-pre", "pre", 100, "cs-1"),
    ]);
    expect(groups.map((g) => g.key)).toEqual(["m-2", "cs-1", "m-1"]);
    expect(groups[1]?.snapshots).toHaveLength(2);
  });

  it("orders groups newest-first even when a later page adds an older member", () => {
    // cs-1's post (250) arrives before the manual snapshot at 220, but
    // cs-1's pre (90) arrives after — the group must still sort at 250.
    const groups = groupSnapshots([
      snap("s-post", "post", 250, "cs-1"),
      snap("m-1", "manual", 220),
      snap("s-pre", "pre", 90, "cs-1"),
    ]);
    expect(groups.map((g) => g.key)).toEqual(["cs-1", "m-1"]);
  });

  it("returns no groups for no snapshots", () => {
    expect(groupSnapshots([])).toEqual([]);
  });
});

describe("kindLabel", () => {
  it("labels every kind", () => {
    expect(kindLabel("pre")).toBe("Before apply");
    expect(kindLabel("post")).toBe("After commit");
    expect(kindLabel("manual")).toBe("Manual");
    expect(kindLabel("scheduled")).toBe("Scheduled");
  });
});
