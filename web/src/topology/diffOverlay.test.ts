import { describe, expect, it } from "vitest";

import type { TopologyDiffResponse, TopologyEntityDiff } from "../api/topologyDiff";
import {
  computeDiffOverlay,
  diffMarkColor,
  diffMarkGlyph,
  summarizeDiffOverlay,
  type DiffMarkChange,
} from "./diffOverlay";

function row(
  ref: string,
  change: DiffMarkChange,
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
    from: { requested: "snap-1", snapshotId: "snap-1", kind: "scheduled", at: 100 },
    to: { requested: "now", live: true, at: 200 },
    added: [],
    removed: [],
    modified: [],
    coverage: { nodes: ["pve1"], paths: ["/etc/network/interfaces"] },
    unattributedCount: 0,
    ...partial,
  };
}

const attributed = { attributed: true, changesetId: "cs-1", changesetTitle: "add vmbr9", actor: "alice@pve", at: 150 };
const unattributed = { attributed: false };

describe("computeDiffOverlay", () => {
  it("keeps attributed and unattributed changes distinguishable", () => {
    const overlay = computeDiffOverlay(
      diff({
        added: [row("bridge:pve1:vmbr9", "added", attributed)],
        modified: [row("bridge:pve1:vmbr0", "modified", unattributed)],
      }),
      () => true,
    );

    expect(overlay.attributedCount).toBe(1);
    expect(overlay.unattributedCount).toBe(1);
    const byRef = new Map(overlay.marks.map((m) => [m.nodeId, m]));
    expect(byRef.get("bridge:pve1:vmbr9")?.attributed).toBe(true);
    expect(byRef.get("bridge:pve1:vmbr9")?.changesetId).toBe("cs-1");
    // The whole point of the overlay: this one has no changeset behind it.
    expect(byRef.get("bridge:pve1:vmbr0")?.attributed).toBe(false);
    expect(byRef.get("bridge:pve1:vmbr0")?.changesetId).toBeUndefined();
  });

  it("says who made an attributed change and says plainly when nobody did", () => {
    const overlay = computeDiffOverlay(
      diff({
        added: [row("bridge:pve1:vmbr9", "added", attributed)],
        modified: [row("bridge:pve1:vmbr0", "modified", unattributed)],
      }),
      () => true,
    );
    const labels = overlay.marks.map((m) => m.label);
    expect(labels).toContain("vmbr9 added by alice@pve");
    expect(labels).toContain("vmbr0 changed outside vnprox");
  });

  it("routes entities not on the current map to offMap instead of dropping them", () => {
    // A removed bridge is no longer a map node. Dropping it would make a
    // range whose only difference was a deletion render as "no changes".
    const overlay = computeDiffOverlay(
      diff({ removed: [row("bridge:pve1:vmbr5", "removed", unattributed)] }),
      (ref) => ref !== "bridge:pve1:vmbr5",
    );
    expect(overlay.marks).toHaveLength(0);
    expect(overlay.offMap.map((m) => m.nodeId)).toEqual(["bridge:pve1:vmbr5"]);
    expect(overlay.unattributedCount).toBe(1);
  });

  it("counts every difference even when none of them can be painted", () => {
    const overlay = computeDiffOverlay(
      diff({
        added: [row("bridge:pve1:vmbr9", "added", attributed)],
        removed: [row("bridge:pve1:vmbr5", "removed", unattributed)],
      }),
      () => false,
    );
    expect(overlay.marks).toHaveLength(0);
    expect(overlay.attributedCount + overlay.unattributedCount).toBe(2);
    expect(summarizeDiffOverlay(overlay)).toContain("2 differences");
    expect(summarizeDiffOverlay(overlay)).toContain("not on the current map");
  });

  it("is empty for an absent diff without throwing", () => {
    const overlay = computeDiffOverlay(undefined, () => true);
    expect(overlay.marks).toHaveLength(0);
    expect(summarizeDiffOverlay(overlay)).toBe("No differences in this range.");
  });

  it("preserves the server's ref order so the overlay does not shuffle between refreshes", () => {
    const d = diff({
      added: [row("bridge:pve1:vmbr1", "added", attributed), row("bridge:pve1:vmbr2", "added", attributed)],
      modified: [row("physnic:pve1:eno1", "modified", unattributed)],
    });
    const first = computeDiffOverlay(d, () => true).marks.map((m) => m.nodeId);
    for (let i = 0; i < 20; i += 1) {
      expect(computeDiffOverlay(d, () => true).marks.map((m) => m.nodeId)).toEqual(first);
    }
    expect(first).toEqual(["bridge:pve1:vmbr1", "bridge:pve1:vmbr2", "physnic:pve1:eno1"]);
  });
});

describe("diffMarkColor", () => {
  it("gives every unattributed change one shared color regardless of its kind", () => {
    const kinds: DiffMarkChange[] = ["added", "removed", "modified"];
    const colors = new Set(kinds.map((change) => diffMarkColor({ change, attributed: false })));
    expect(colors.size).toBe(1);
  });

  it("never paints an unattributed change the same color as an attributed one", () => {
    const kinds: DiffMarkChange[] = ["added", "removed", "modified"];
    const outOfBand = diffMarkColor({ change: "modified", attributed: false });
    for (const change of kinds) {
      expect(diffMarkColor({ change, attributed: true })).not.toBe(outOfBand);
    }
  });

  it("distinguishes the three attributed change kinds from each other", () => {
    const kinds: DiffMarkChange[] = ["added", "removed", "modified"];
    const colors = new Set(kinds.map((change) => diffMarkColor({ change, attributed: true })));
    expect(colors.size).toBe(3);
  });
});

describe("diffMarkGlyph", () => {
  it("is a distinct glyph per change kind", () => {
    expect(new Set(["added", "removed", "modified"].map((c) => diffMarkGlyph(c as DiffMarkChange))).size).toBe(3);
  });
});
