// SPDX-License-Identifier: Apache-2.0

// T-4302 AC3: "A test asserts that no colour on a node varies by `kind` — the
// property this card exists to establish, and the one that will otherwise be
// quietly reintroduced by the next card that wants to distinguish something."
//
// canvasDraw.ts is not otherwise unit-tested, for the reason its own header
// gives: it needs a real CanvasRenderingContext2D and jsdom has none. That
// argument holds for the *pixels* and does not hold for this property, which
// is about which colours the draw pass reaches for — recordable from a
// context that only remembers what it was assigned. So the fake below
// implements the 28-call surface `drawScene` actually uses and records every
// `fillStyle`/`strokeStyle` write.
//
// The assertion is deliberately set-valued rather than sequence-valued: two
// kinds draw a different NUMBER of shapes (a bond's funnel is not a bridge's
// box), so the draw calls legitimately differ. What must not differ is the
// palette those calls draw from.
import { afterAll, beforeAll, describe, expect, it } from "vitest";

import type { Edge as FlowEdge, Node as FlowNode } from "@xyflow/react";
import type { EntityStatus } from "../api/types";
import type { EntityEdgeData } from "./EntityEdge";
import type { EntityNodeData } from "./EntityNode";
import { drawScene, type SceneTheme } from "./canvasDraw";
import { PICTOGRAM_KINDS } from "./canvasGlyphs";
import { DEFAULT_NODE_SIZE } from "./canvasScene";

/** Sentinel values, not plausible colours: if an assertion below fails, the
 * offending string names the theme role it came from, and any literal hex
 * reintroduced into the draw path is instantly distinguishable from a
 * legitimate theme read. */
const THEME: SceneTheme = {
  background: "role:background",
  nodeFill: "role:nodeFill",
  nodeText: "role:nodeText",
  kindText: "role:kindText",
  nodeBorderOk: "role:nodeBorderOk",
  statusDown: "role:statusDown",
  statusDegraded: "role:statusDegraded",
  statusUnknown: "role:statusUnknown",
  statusOk: "role:statusOk",
  badgeBg: "role:badgeBg",
  badgeText: "role:badgeText",
  minimapBg: "role:minimapBg",
  minimapDot: "role:minimapDot",
  mgmtBadgeBg: "role:mgmtBadgeBg",
  mgmtBadgeText: "role:mgmtBadgeText",
  edgeDefault: "role:edgeDefault",
  findingErrorFill: "role:findingErrorFill",
  findingErrorText: "role:findingErrorText",
  findingWarningFill: "role:findingWarningFill",
  findingWarningText: "role:findingWarningText",
  findingInfoFill: "role:findingInfoFill",
  findingInfoText: "role:findingInfoText",
};

const noop = (): void => undefined;

class RecordingContext {
  readonly colors: string[] = [];
  #fillStyle = "";
  #strokeStyle = "";

  get fillStyle(): string {
    return this.#fillStyle;
  }
  set fillStyle(v: string) {
    this.#fillStyle = v;
    this.colors.push(v);
  }
  get strokeStyle(): string {
    return this.#strokeStyle;
  }
  set strokeStyle(v: string) {
    this.#strokeStyle = v;
    this.colors.push(v);
  }

  font = "";
  globalAlpha = 1;
  lineCap = "butt";
  lineJoin = "miter";
  lineWidth = 1;
  lineDashOffset = 0;
  textAlign = "start";
  textBaseline = "alphabetic";

  // Geometry is not what this test reads, so every drawing call is a no-op.
  save = noop;
  restore = noop;
  translate = noop;
  scale = noop;
  beginPath = noop;
  closePath = noop;
  moveTo = noop;
  lineTo = noop;
  arc = noop;
  arcTo = noop;
  clip = noop;
  fill = noop;
  stroke = noop;
  fillRect = noop;
  strokeRect = noop;
  clearRect = noop;
  fillText = noop;
  setLineDash = noop;
  measureText = (text: string): { width: number } => ({ width: text.length * 6 });
}

/** jsdom has no Path2D, and canvasGlyphs.ts builds one per shape. It is only
 * ever handed straight back to `fill`/`stroke`, so a shell with the four
 * geometry methods is enough to exercise the real draw path. */
class FakePath2D {
  arc = noop;
  ellipse = noop;
  roundRect = noop;
  moveTo = noop;
  lineTo = noop;
}

const globals = globalThis as unknown as { Path2D?: unknown };
let originalPath2D: unknown;
beforeAll(() => {
  originalPath2D = globals.Path2D;
  globals.Path2D = FakePath2D;
});
afterAll(() => {
  globals.Path2D = originalPath2D;
});

function node(kind: string, status: EntityStatus): FlowNode<EntityNodeData, "entity"> {
  return {
    id: `${kind}:pve1:x`,
    type: "entity",
    position: { x: 40, y: 40 },
    data: {
      label: "vmbr0",
      kind,
      status,
      badges: [],
      dimmed: false,
      highlighted: false,
      isGuestGroup: false,
    },
  };
}

/** The distinct colours a single node's draw pass reaches for, at a zoom
 * where every channel is live (label, kind word, badges row, pictogram). */
function paletteFor(kind: string, status: EntityStatus = "ok", zoom = 1): string[] {
  const ctx = new RecordingContext();
  drawScene(ctx as unknown as CanvasRenderingContext2D, {
    nodes: [node(kind, status)],
    edges: [] as FlowEdge<EntityEdgeData, "entity">[],
    viewport: { x: 0, y: 0, zoom },
    view: { width: 800, height: 600 },
    theme: THEME,
    nodeSize: DEFAULT_NODE_SIZE,
  });
  return [...new Set(ctx.colors)].sort();
}

describe("drawScene: kind is a shape, not a colour (T-4302 AC3)", () => {
  it("draws every kind from an identical palette", () => {
    const reference = paletteFor(PICTOGRAM_KINDS[0] ?? "bridge");
    expect(reference.length).toBeGreaterThan(0);
    for (const kind of PICTOGRAM_KINDS) {
      expect(paletteFor(kind), `${kind} reached for a colour no other kind does`).toEqual(reference);
    }
  });

  it("holds at the low-zoom band too, where the pictogram is centred", () => {
    // The band the deleted accent rail used to own by itself, and the one
    // most likely to be given a kind colour again by someone who only
    // checked the default zoom.
    const reference = paletteFor(PICTOGRAM_KINDS[0] ?? "bridge", "ok", 0.5);
    for (const kind of PICTOGRAM_KINDS) {
      expect(paletteFor(kind, "ok", 0.5), `${kind} at zoom 0.5`).toEqual(reference);
    }
  });

  it("still lets STATUS change the palette — otherwise the test above is vacuous", () => {
    // Guards the guard. "No colour varies by kind" is trivially satisfied by
    // a node drawn in one colour; the point is that the channel kind gave up
    // is the one status now owns alone.
    const ok = paletteFor("bridge", "ok");
    expect(paletteFor("bridge", "down")).not.toEqual(ok);
    expect(paletteFor("bridge", "degraded")).not.toEqual(ok);
    expect(paletteFor("bridge", "unknown")).not.toEqual(ok);
  });

  it("takes each status colour from the design language, not a literal", () => {
    // The three states that used to be `#ef4444`/`#f59e0b`/`#94a3b8` in two
    // separate tables. A sentinel palette makes a surviving literal obvious:
    // it would appear here as a hex among "role:" strings.
    expect(paletteFor("bridge", "down")).toContain("role:statusDown");
    expect(paletteFor("bridge", "degraded")).toContain("role:statusDegraded");
    expect(paletteFor("bridge", "unknown")).toContain("role:statusUnknown");
  });

  it("draws the pictogram in the kind-text role rather than a colour of its own", () => {
    expect(paletteFor("bridge")).toContain("role:kindText");
  });
});
