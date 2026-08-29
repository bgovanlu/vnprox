// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";

import type { TopologyDiffResponse, TopologyEntityDiff } from "../api/topologyDiff";
import type { DiffMark } from "./diffOverlay";
import {
  diffGlyphColor,
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

describe("diffGlyphColor (T-4303)", () => {
  // The badge glyph is this overlay's non-colour channel. It was drawn in
  // flat white on every mark, and white measures 3.30 on `added` and 3.96 on
  // `changed` — below AA, so the accessibility mitigation was the part that
  // failed. Asserted by measuring every mark rather than by naming the two
  // that were wrong, so a future palette change is caught instead of the
  // table silently drifting.
  const AA = 4.5;
  const relLum = (hex: string) => {
    const ch = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255);
    const lin = ch.map((c) => (c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4));
    return 0.2126 * (lin[0] ?? 0) + 0.7152 * (lin[1] ?? 0) + 0.0722 * (lin[2] ?? 0);
  };
  const contrast = (a: string, b: string) => {
    const [hi, lo] = [relLum(a), relLum(b)].sort((x, y) => y - x);
    return ((hi ?? 0) + 0.05) / ((lo ?? 0) + 0.05);
  };

  const MARKS: Pick<DiffMark, "change" | "attributed">[] = [
    { change: "added", attributed: true },
    { change: "removed", attributed: true },
    { change: "modified", attributed: true },
    { change: "modified", attributed: false },
  ];

  it("every mark's glyph clears AA against its own badge fill", () => {
    for (const mark of MARKS) {
      const ratio = contrast(diffGlyphColor(mark), diffMarkColor(mark));
      expect(ratio, `${mark.change}/${String(mark.attributed)}: glyph on its own badge`).toBeGreaterThanOrEqual(AA);
    }
  });

  it("would fail if the glyph went back to flat white", () => {
    // Guards the guard: flat white is exactly what was wrong, so if nothing
    // fails under it the assertion above has stopped measuring anything.
    const failing = MARKS.filter((m) => contrast("#ffffff", diffMarkColor(m)) < AA);
    expect(failing.length).toBeGreaterThan(0);
  });
});
