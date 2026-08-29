// SPDX-License-Identifier: Apache-2.0

// T-4302: the T-4205 pictogram set, rendered onto the v2 <canvas>.
//
// The card names the constraint before the mechanism: "decide which, and say
// why, rather than reimplementing 23 glyphs as canvas path calls (a second
// copy of the icon set is the same defect as the hand-copied palette T-4301
// removed)". So the question is not "Path2D or sprite" in the abstract, it is
// which of them can be driven by the icon set ITSELF.
//
// **Path2D from lifted path data does not survive contact with the set.**
// The obvious version — pull each glyph's `d` string into a table the canvas
// walks — assumes the glyphs are paths. Counted across the four glyph
// modules, they are 48 `<line>`, 30 `<circle>`, 15 `<path>`, 11 `<rect>` and
// 2 `<ellipse>`; a `d` string exists for under one shape in seven. Lifting
// geometry would mean re-authoring every glyph as path data, which is the
// second copy the card forbids, and it would silently lose the
// detailed-vs-inline interiors `isDetailed` switches on.
//
// **An <img> sprite works but buys async for nothing.** Serialising each
// glyph to a data: URI and decoding it through `Image` is a real option, and
// it is what a sprite atlas normally means. It costs a decode round-trip, so
// the first frame after a theme or LOD change draws a hole and needs a
// redraw callback threaded back through TopologyCanvasV2 — machinery that
// exists only to service the decode.
//
// **So: read the real components' element tree and replay it as canvas ops.**
// The glyphs are pure functions returning SVG elements — no hooks anywhere in
// `icons/` (asserted below by the fact that this module calls them at all, and
// directly by canvasGlyphs.test.ts, which walks every registered kind). A
// React element is a plain object, so its geometry is readable without a
// renderer, without `react-dom/server`, and without a DOM. Editing a glyph
// changes the canvas in the same commit, because there is no transcription
// step in between — the property T-4301 established for colour, applied to
// shape.
//
// The narrow thing this buys that matters: extraction is synchronous, so
// `drawScene` stays the pure `(elements, viewport, theme) -> pixels` function
// its own header promises, with no cache-miss frame and no redraw plumbing.
//
// What constrains it: a glyph that grows a hook, or a sixth SVG primitive,
// stops rendering on the canvas. Both are caught by canvasGlyphs.test.ts,
// which renders every kind in both interiors and fails on an empty op list or
// an unhandled element — not by a screenshot, and not in production.
import { Fragment, isValidElement, type ReactElement, type ReactNode } from "react";

import { getPictogram, PICTOGRAM_KINDS } from "../icons/registry";
import { INLINE_THRESHOLD } from "../icons/sizing";

/** Icon.tsx's `viewBox="0 0 24 24"`. Every op below is in this space; the
 * caller scales once. Named rather than inlined because the scale factor and
 * the stroke width both derive from it. */
export const GLYPH_GRID = 24;

/** Icon.tsx's `strokeWidth={2}`, in grid units — scaled with the glyph, which
 * is what an SVG does and what keeps a canvas glyph optically identical to
 * the same glyph in a table row beside it. */
const GLYPH_STROKE_WIDTH = 2;

/** Whether a shape is filled, stroked, and dashed. Taken from the element's
 * own props, so `fill="currentColor" stroke="none"` (the set's convention for
 * a solid terminal dot) and `strokeDasharray` (ZoneIcon, LldpNeighborIcon and
 * UnknownPictogram's "observed/uncertain" marking) both survive the trip.
 * A shape with no `fill` prop inherits IconShell's `fill="none"`, which is
 * why the default here is "not filled" rather than "filled". */
interface Paint {
  fill: boolean;
  stroke: boolean;
  dash: number[] | null;
}

export type GlyphOp =
  | (Paint & { shape: "path"; d: string })
  | (Paint & { shape: "circle"; cx: number; cy: number; r: number })
  | (Paint & { shape: "ellipse"; cx: number; cy: number; rx: number; ry: number })
  | (Paint & { shape: "rect"; x: number; y: number; width: number; height: number; rx: number })
  | (Paint & { shape: "line"; x1: number; y1: number; x2: number; y2: number });

/** Every SVG element name the pictogram set actually uses, plus the two
 * structural ones that carry no geometry. Anything outside this set is a new
 * primitive that this module would silently drop, so it throws instead — see
 * this file's header on why silence is the failure mode worth designing out. */
const STRUCTURAL = new Set(["svg", "title", "g"]);

function num(value: unknown, fallback = 0): number {
  if (typeof value === "number") return value;
  if (typeof value === "string") {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) return parsed;
  }
  return fallback;
}

function paintOf(props: Record<string, unknown>): Paint {
  const dashAttr = props.strokeDasharray;
  const dash =
    typeof dashAttr === "string"
      ? dashAttr
          .split(/[\s,]+/)
          .map(Number)
          .filter((n) => Number.isFinite(n))
      : null;
  return {
    fill: props.fill === "currentColor",
    stroke: props.stroke !== "none",
    dash: dash !== null && dash.length > 0 ? dash : null,
  };
}

function opFor(tag: string, props: Record<string, unknown>): GlyphOp | null {
  const paint = paintOf(props);
  switch (tag) {
    case "path":
      return { shape: "path", d: typeof props.d === "string" ? props.d : "", ...paint };
    case "circle":
      return { shape: "circle", cx: num(props.cx), cy: num(props.cy), r: num(props.r), ...paint };
    case "ellipse":
      return {
        shape: "ellipse",
        cx: num(props.cx),
        cy: num(props.cy),
        rx: num(props.rx),
        ry: num(props.ry),
        ...paint,
      };
    case "rect":
      return {
        shape: "rect",
        x: num(props.x),
        y: num(props.y),
        width: num(props.width),
        height: num(props.height),
        rx: num(props.rx),
        ...paint,
      };
    case "line":
      return {
        shape: "line",
        x1: num(props.x1),
        y1: num(props.y1),
        x2: num(props.x2),
        y2: num(props.y2),
        ...paint,
      };
    default:
      return null;
  }
}

function walk(node: ReactNode, out: GlyphOp[]): void {
  if (node === null || node === undefined || typeof node === "boolean") return;
  if (Array.isArray(node)) {
    for (const child of node as ReactNode[]) walk(child, out);
    return;
  }
  if (!isValidElement(node)) return; // a bare string/number carries no geometry
  const element = node as ReactElement<Record<string, unknown>>;
  const props: Record<string, unknown> = element.props;

  // Fragment first: the glyphs' `{detailed && (<>...</>)}` branches are
  // fragments, and this check has to precede the function-component one
  // because narrowing past that leaves `type` as `string`, where TS can prove
  // the Fragment comparison never matches — while at run time it plainly
  // does. Ordering keeps the check both correct and typeable.
  if (element.type === Fragment) {
    walk(props.children as ReactNode, out);
    return;
  }
  // A function component — IconShell, or the glyph itself. Calling it is the
  // whole mechanism: these are pure and hook-free (see the header), so the
  // element tree they return is available without a renderer.
  if (typeof element.type === "function") {
    walk((element.type as (p: Record<string, unknown>) => ReactNode)(props), out);
    return;
  }
  if (typeof element.type !== "string") return;

  if (!STRUCTURAL.has(element.type)) {
    const op = opFor(element.type, props);
    if (op === null) {
      throw new Error(
        `canvasGlyphs: <${element.type}> is not a primitive this module can draw. ` +
          `Add it to opFor(), or the glyph using it will vanish from the v2 canvas.`,
      );
    }
    out.push(op);
  }
  walk(props.children as ReactNode, out);
}

/** Geometry is colour-independent (the set paints entirely in `currentColor`,
 * applied at draw time), so a kind needs at most two entries — one per
 * interior. Keyed on the interior rather than on the pixel size for the same
 * reason: 41px and 42px are the same drawing. */
const CACHE = new Map<string, GlyphOp[]>();

/** The pictogram for `kind`, as canvas ops in the 24x24 grid.
 *
 * `size` is the size the glyph will be DRAWN at, not a request for a
 * resolution: it selects `isDetailed`'s simplified-vs-detailed interior, so a
 * glyph shrinking past INLINE_THRESHOLD on the canvas sheds the same interior
 * lines it sheds in a table row. Unknown kinds resolve through
 * `getPictogram`'s UnknownPictogram fallback rather than drawing nothing, so
 * a future backend kind is a plain box, not a hole. */
export function glyphOps(kind: string, size: number): GlyphOp[] {
  const detailed = size > INLINE_THRESHOLD;
  const key = `${kind}|${String(detailed)}`;
  const hit = CACHE.get(key);
  if (hit !== undefined) return hit;

  const Glyph = getPictogram(kind);
  const ops: GlyphOp[] = [];
  walk(Glyph({ size: detailed ? GLYPH_GRID : INLINE_THRESHOLD - 4 }), ops);
  CACHE.set(key, ops);
  return ops;
}

/** Every kind with a pictogram of its own — the iteration surface for tests
 * and for any future canvas legend. Re-exported so canvas-side callers do not
 * reach into `icons/` for it. */
export { PICTOGRAM_KINDS };

/** Replays `ops` into a 2D context at `(x, y)`, boxed to `size` px square.
 *
 * Untested directly, like the rest of the canvas draw path — jsdom has no
 * CanvasRenderingContext2D. The part worth testing is the extraction above,
 * which is pure data and is tested exhaustively. */
export function drawGlyph(
  ctx: CanvasRenderingContext2D,
  ops: readonly GlyphOp[],
  x: number,
  y: number,
  size: number,
  color: string,
): void {
  const scale = size / GLYPH_GRID;
  ctx.save();
  ctx.translate(x, y);
  ctx.scale(scale, scale);
  ctx.strokeStyle = color;
  ctx.fillStyle = color;
  ctx.lineWidth = GLYPH_STROKE_WIDTH;
  ctx.lineCap = "round";
  ctx.lineJoin = "round";
  for (const op of ops) {
    ctx.setLineDash(op.dash ?? []);
    let path: Path2D;
    switch (op.shape) {
      case "path":
        path = new Path2D(op.d);
        break;
      case "circle":
        path = new Path2D();
        path.arc(op.cx, op.cy, op.r, 0, Math.PI * 2);
        break;
      case "ellipse":
        path = new Path2D();
        path.ellipse(op.cx, op.cy, op.rx, op.ry, 0, 0, Math.PI * 2);
        break;
      case "rect":
        path = new Path2D();
        path.roundRect(op.x, op.y, op.width, op.height, op.rx);
        break;
      case "line":
        path = new Path2D();
        path.moveTo(op.x1, op.y1);
        path.lineTo(op.x2, op.y2);
        break;
    }
    if (op.fill) ctx.fill(path);
    if (op.stroke) ctx.stroke(path);
  }
  ctx.setLineDash([]);
  ctx.restore();
}
