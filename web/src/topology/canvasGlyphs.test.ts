// SPDX-License-Identifier: Apache-2.0

// T-4302: the canvas draws kind as a SHAPE, so these assertions are the ones
// that make the shape channel trustworthy — that every kind resolves to a
// drawing, that no two kinds resolve to the SAME drawing (a categorical
// channel with collisions is the defect this card removed from colour, and it
// would be no better in shape), and that the extraction keeps working as the
// icon set changes underneath it.
//
// The last one is the reason this file is exhaustive rather than
// representative. canvasGlyphs.ts drives the real pictogram components, which
// is what keeps the canvas from carrying a second copy of the icon set — but
// it does so by calling them as plain functions, which holds only while they
// stay pure and stay inside the five SVG primitives it can draw. Neither
// constraint is visible at the call site. Both fail loudly here: a glyph that
// grows a hook throws when called, and a sixth primitive throws by
// construction in `walk`. Neither would be caught by a screenshot, because a
// missing glyph looks exactly like a node that has not got one yet.
import { describe, expect, it } from "vitest";

import { GLYPH_GRID, PICTOGRAM_KINDS, glyphOps, type GlyphOp } from "./canvasGlyphs";

const DETAILED = 32; // above INLINE_THRESHOLD
const INLINE = 16; // below it

function fingerprint(ops: readonly GlyphOp[]): string {
  return JSON.stringify(ops);
}

describe("glyphOps (T-4302)", () => {
  it("renders every registered kind to a non-empty drawing, in both interiors", () => {
    // Also the hook guard: PICTOGRAMS' components are called directly, so a
    // glyph that starts using a hook throws here rather than vanishing from
    // the map.
    for (const kind of PICTOGRAM_KINDS) {
      expect(glyphOps(kind, DETAILED), `${kind} detailed`).not.toHaveLength(0);
      expect(glyphOps(kind, INLINE), `${kind} inline`).not.toHaveLength(0);
    }
  });

  it("gives every kind a distinct drawing at the detailed size", () => {
    // The whole premise of moving kind off colour is that shape has no
    // capacity limit. That only holds if the shapes are actually different:
    // two kinds sharing a drawing is exactly the collision T-4302 measured in
    // the hue table, relocated.
    const seen = new Map<string, string>();
    for (const kind of PICTOGRAM_KINDS) {
      const key = fingerprint(glyphOps(kind, DETAILED));
      const clash = seen.get(key);
      expect(clash, `${kind} draws identically to ${String(clash)}`).toBeUndefined();
      seen.set(key, kind);
    }
  });

  it("keeps kinds distinct at the inline size too, where interiors are dropped", () => {
    // The simplified interiors are where a collision is actually plausible:
    // glyphs.linklayer.tsx documents OvsBondIcon/OvsBridgeIcon deliberately
    // falling back to their plain sibling's silhouette at 16px, so those two
    // pairs are expected duplicates and everything else must not be.
    const byDrawing = new Map<string, string[]>();
    for (const kind of PICTOGRAM_KINDS) {
      const key = fingerprint(glyphOps(kind, INLINE));
      byDrawing.set(key, [...(byDrawing.get(key) ?? []), kind]);
    }
    const collisions = [...byDrawing.values()].filter((kinds) => kinds.length > 1).map((k) => k.sort());
    expect(collisions.sort()).toEqual([
      ["bond", "ovs-bond"],
      ["bridge", "ovs-bridge"],
    ]);
  });

  it("selects the simplified interior below INLINE_THRESHOLD", () => {
    // Not a restatement of the above: this asserts the size argument reaches
    // `isDetailed` at all. If it did not, the canvas would draw the detailed
    // interior at every zoom, which is the "mud at 16px" the icon set's own
    // sizing.ts exists to avoid.
    const detailed = glyphOps("bond", DETAILED);
    const inline = glyphOps("bond", INLINE);
    expect(inline.length).toBeLessThan(detailed.length);
  });

  it("carries the set's fill/stroke convention through, rather than stroking everything", () => {
    // BondIcon's terminal dot is `fill="currentColor" stroke="none"`. Losing
    // that would draw it as a hollow ring — a different glyph, silently.
    const dot = glyphOps("bond", DETAILED).find((op) => op.shape === "circle");
    expect(dot?.fill).toBe(true);
    expect(dot?.stroke).toBe(false);
  });

  it("carries strokeDasharray through, since dashing is load-bearing in this set", () => {
    // UnknownPictogram, ZoneIcon and LldpNeighborIcon all use a dash to mean
    // "observed/uncertain" — the dash is the meaning, not decoration.
    const dashed = glyphOps("no-such-kind-exists", DETAILED).filter((op) => op.dash !== null);
    expect(dashed).not.toHaveLength(0);
    expect(dashed[0]?.dash).toEqual([2, 2]);
  });

  it("falls back to a drawn box for an unknown kind instead of a hole", () => {
    // A `kind` string the frontend does not model is a data problem; drawing
    // nothing would present it as an empty node.
    expect(glyphOps("some-future-pve-kind", DETAILED)).not.toHaveLength(0);
  });

  it("keeps every shape inside the 24x24 grid the caller scales from", () => {
    // The caller boxes the glyph at a computed size and scales by
    // size/GLYPH_GRID; a shape outside the viewBox would overflow that box
    // and paint over the node's label.
    for (const kind of PICTOGRAM_KINDS) {
      for (const op of glyphOps(kind, DETAILED)) {
        const extents: number[] = [];
        switch (op.shape) {
          case "circle":
            extents.push(op.cx - op.r, op.cx + op.r, op.cy - op.r, op.cy + op.r);
            break;
          case "ellipse":
            extents.push(op.cx - op.rx, op.cx + op.rx, op.cy - op.ry, op.cy + op.ry);
            break;
          case "rect":
            extents.push(op.x, op.x + op.width, op.y, op.y + op.height);
            break;
          case "line":
            extents.push(op.x1, op.x2, op.y1, op.y2);
            break;
          case "path":
            break; // bounds of a `d` string are not worth a parser here
        }
        for (const v of extents) {
          expect(v, `${kind} ${op.shape}`).toBeGreaterThanOrEqual(0);
          expect(v, `${kind} ${op.shape}`).toBeLessThanOrEqual(GLYPH_GRID);
        }
      }
    }
  });
});
