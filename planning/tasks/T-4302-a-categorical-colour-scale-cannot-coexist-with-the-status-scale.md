# T-4302 — A categorical colour scale cannot coexist with the status scale, so kind must stop using colour

**Phase:** 43 (Canvas rendering)
**Status:** done — both renderers. AC5 needed no work and the reason is recorded below.
**Depends on:** T-4205 (pictogram set, landed), T-4301 (canvas palette, landed)
**Supersedes:** T-4301's acceptance criterion 5, which asked for a categorical scale to be
*designed*. It cannot be. This card is the answer to that criterion, not a deferral of it.

## The question T-4301 asked

`canvasDraw.ts` carries `KIND_ACCENT`, a hue per entity kind — bond sky, bridge indigo, VLAN
violet, SDN teal, guest emerald, physical slate — with no counterpart in the design language.
T-4301 filed it as "a design question to answer once in `docs/design-language.md`" and assumed the
answer was a properly-derived categorical scale.

## The answer is that no such scale can exist here

The status scale holds itself to **40° of OKLCH hue separation** between states
(`index.css.test.ts` asserts it). Adding a categorical scale means every categorical hue must
also clear that floor from every status hue, or "this is a bond" and "this is degraded" become
the same signal.

Six kinds + four statuses + one brand accent is **eleven hues**. Eleven × 40° needs **440° of hue
circle. There are 360.** A deficit of 80°. This is not a tuning problem that a better palette
solves; it is arithmetic.

And the current scale already shows it. Measured against the status hues and the accent:

| kind | hue | nearest | separation |
|---|---|---|---|
| bond `#0ea5e9` | 237 | info / accent | **13** |
| guest `#10b981` | 162 | ok | **17** |
| physnic `#64748b` | 257 | info / accent | **33** |
| sdn `#14b8a6` | 183 | ok | **37** |
| bridge `#6366f1` | 277 | info / accent | 53 |
| vlan `#8b5cf6` | 293 | info / accent | 68 |

**Four of six are already under the floor**, and the two worst — bond at 13° from the accent, guest
at 17° from `ok` — are the two most common entity kinds in any cluster. A selected bond and an
informational badge are near-identical; a guest node's rail and a healthy status are near-identical.

## What the canvas should do instead

Kind already has **two** encodings on a node and colour is the worse one:

1. `KIND_ACCENT` paints a 3–4px vertical rail on the node's left edge
   (`canvasDraw.ts:374`). At that width hue is the only channel available — a 3px sliver cannot
   express lightness or chroma difference — which is exactly the channel status needs.
2. `canvasDraw.ts:453` writes `d.kind.toUpperCase()` at **9px**. Until T-4301 that text measured
   2.56:1 against the node fill and was unreadable, so in practice the rail was carrying kind
   alone.

Meanwhile **T-4205 built 23 pictograms whose entire job is to say what kind of thing something
is**, with the kind strings taken verbatim from `internal/inventory/ref.go` — and the canvas draws
none of them. It has no glyph rendering at all.

So the proposal is not to design a categorical colour scale. It is:

- **Draw the T-4205 pictogram on the node.** Shape is a categorical channel with no capacity
  limit and no collision with status, and those glyphs were drawn to work small.
- **Retire `KIND_ACCENT`.** The rail becomes either nothing or a neutral, and the node's colour
  then means exactly one thing.
- This is the roadmap's own principle 2 — *colour is status* — applied where it was being
  violated by the product's main screen.

`STATUS_STROKE` goes at the same time: it maps `EntityStatus` to Tailwind-500 defaults
(`down: #ef4444`, `degraded: #f59e0b`), a second status scale sitting beside the real one, and
with the kind rail gone it is the only colour left on a node, so it had better be the real scale.

## Acceptance criteria

1. Nodes render their T-4205 pictogram. The glyphs are React components; the canvas needs them as
   `Path2D` or an offscreen-rendered sprite — decide which, and say why, rather than
   reimplementing 23 glyphs as canvas path calls (a second copy of the icon set is the same defect
   as the hand-copied palette T-4301 removed).
2. `KIND_ACCENT` is deleted. `STATUS_STROKE` is deleted in favour of `--color-status-*` through
   `canvasPalette`.
3. A test asserts that no colour on a node varies by `kind` — the property this card exists to
   establish, and the one that will otherwise be quietly reintroduced by the next card that wants
   to distinguish something.
4. Legibility at the sizes that matter: the glyph must be identifiable at the LOD band where nodes
   are smallest before the physical-layer capsule collapse takes over (`lod.ts`). Screenshot it at
   each band rather than asserting a pixel size.
5. `index.css.test.ts`'s hue-separation gate is extended to cover the accent as well as the
   statuses — this card was only findable because I measured a comparison nothing tests. See
   T-4211, which is open on the same gap in demo mode.

## It also unblocks the node border (added after T-4305 measured v1)

`EntityNode.tsx` carries a second `KIND_ACCENT` — the same concept, a different table
(`bg-sky-50 dark:bg-sky-950` and friends), with nothing keeping the two agreed. Two things follow.

First, a caveat that *narrows* this card for v1: a `50`/`950` background wash does not compete
with a status border for attention the way v2's saturated `#0ea5e9` rail does, and the 40deg
hue-separation argument barely applies at that chroma. For v1 the case is duplication and
consistency, not legibility. The stronger v2 framing should not be smuggled across.

Second, a reason to do it that is stronger than either: **those washes are why the node border
cannot be fixed.** The `ok` border measures 1.43:1 light and 2.35:1 dark against a 3:1 floor, and
`--color-outline` — which fixes exactly this in v2 — only reaches 2.64 / 2.21 against the washes,
because it was solved against the surface ladder and the washes are not in it. Delete
`KIND_ACCENT` and the node sits on `surface-raised`, where the existing token already measures
3.25 / 3.43.

So the order matters: this card first, the border second. Fixing the border first means solving a
second outline value against a set of backgrounds this card is about to remove.

## How the canvas got the glyphs — AC1's "decide which, and say why"

The card offered `Path2D` or an offscreen sprite. **Neither, as posed** — and the reason is a fact
about the icon set that neither option anticipated.

**Path2D from lifted path data does not survive contact with the set.** The obvious version pulls
each glyph's `d` string into a table. Counted across the four glyph modules, the set is 48
`<line>`, 30 `<circle>`, 15 `<path>`, 11 `<rect>` and 2 `<ellipse>` — **a `d` string exists for
under one shape in seven.** Lifting the geometry would mean re-authoring all 23 glyphs as path
data, which is exactly the second copy this card forbids, and it would silently drop the
detailed-vs-inline interiors `isDetailed` switches on.

**An `<img>` sprite works and buys async for nothing.** Serialising each glyph to a `data:` URI and
decoding it through `Image` is a real option. It costs a decode round-trip, so the first frame
after a theme or LOD change draws a hole and needs a redraw callback threaded back through
`TopologyCanvasV2` — machinery whose entire job is to service the decode.

**So `canvasGlyphs.ts` reads the real components' element tree and replays it as canvas ops.** The
glyphs are pure functions returning SVG elements — there is not a hook anywhere in `icons/` — and a
React element is a plain object, so the geometry is readable without a renderer, without
`react-dom/server`, and without a DOM. Extraction is synchronous, so `drawScene` stays the pure
function its own header promises. Editing a glyph changes the canvas in the same commit, because
there is no transcription step in between: the property T-4301 established for colour, applied to
shape.

What constrains it is written down where it can be enforced, not just where it can be read: a glyph
that grows a hook, or a sixth SVG primitive, would stop rendering. `canvasGlyphs.test.ts` renders
every registered kind in both interiors and fails on an empty op list, and `walk` throws by
construction on an unhandled element. A screenshot would not catch either, because **a missing
glyph looks exactly like a node that has not been given one yet.**

The test also asserts the property that makes shape a usable categorical channel at all: **no two
kinds draw the same thing.** It predicted, without being told, exactly the two collisions the icon
set documents as deliberate — `bond`/`ovs-bond` and `bridge`/`ovs-bridge` collapse to a shared
silhouette at 16px, which `glyphs.linklayer.tsx` calls "a deliberate, documented loss of the OVS
distinction at the smallest size". That the extraction reproduces the icon set's own documented
behaviour without being told about it is the strongest evidence available that it is reading the
real thing.

## AC4, screenshotted rather than asserted as a pixel size

Captured in headless Chromium against the v2 canvas (the flag set before boot, the way
`lod.spec.ts` does it), both themes, at wheel-driven zoom — **not** via the `svZoom` deeplink,
which does not survive the load-time view fit: `svZoom=1` and `svZoom=0.35` rendered pixel-
identically, which is worth knowing before someone else builds a zoom-dependent capture on it.

The glyph stays identifiable down to the last band before the physical-layer capsule collapse
(zoom < 0.2) takes over — subnet, vnet, bridge, guest and guest-nic remain separable at ~8px, at
which point the simplified interiors are doing the work they were drawn for. Below `GLYPH_MIN_PX`
(7px) nothing is drawn at all: a 5px pictogram is not a smaller pictogram, it is a smudge, and the
rail it replaced was no more legible there.

Two layout consequences of a glyph being wider than a 4px rail, both handled: the label's left pad
is computed from the glyph box rather than the old flat 8px, and its max width shrinks to match, so
long entity names truncate where they always did. Below `showText` the glyph moves to the node's
centre — the band the rail used to own by itself — except on physnic/guest-nic, where T-3505's
drawn jack keeps that spot because copper-vs-fibre-vs-virtual is strictly more information than
"this is a NIC".

## STATUS_STROKE was three tables, not one

AC2 names one symbol. There were three, and they did not agree:

| | `ok` | `unknown` |
|---|---|---|
| `canvasDraw.ts`'s `STATUS_STROKE` (v2 edges) | `#94a3b8` | `#94a3b8` |
| `canvasDraw.ts`'s `statusBorder` (v2 nodes) | `--color-outline` | `#94a3b8` |
| `EntityEdge.tsx`'s `STATUS_STROKE` (**v1** edges) | `#94a3b8` | `#94a3b8` |

All three now resolve `--color-status-critical` / `-degraded` / `-unknown`, the canvas through
`canvasPalette` and the DOM through `var()` — the division of labour `toneVar` already documents.
`unknown` gains a colour of its own, which it should have had: the product has a token named for
precisely that state, and the dash remains as the second channel.

**Doing only the two canvas tables would have repeated T-4305's finding one card later** — the fix
landing on the renderer that is off by default while the renderer users actually get keeps the
literals.

One measured consequence worth stating: `statusDown` and `statusDegraded` resolve the *same* two
tokens the finding badges already read. `canvasPalette.test.ts` now records 17 lookups over 14
distinct tokens, which is the assertion that a node border saying "down" and a badge saying "error"
are one colour. Two tables of literals could not promise that, and when T-4301 measured them, they
had not delivered it.

## AC5 was already satisfied, and by a card filed before this one

The criterion asks for `index.css.test.ts`'s hue-separation gate to be extended to the accent.
**It already covers the accent, in both themes, and has since T-4204** (commit `13edd26b`) — the
gate sets `hues.set("accent", ...)` alongside `ok`/`degraded`/`critical` and asserts every pair
clears 40deg. Recorded rather than quietly ticked, because the criterion was written on a wrong
premise and the next reader deserves to know which.

The comparison this card *actually* measured — kind hues against status hues — is now untestable in
the good way: the kind scale is deleted, so there is nothing left to measure. What remains
untested is the residue named below.

## What this card did not fix, stated so it is not mistaken for done

`SIM_STROKE`, `FLOW_EDGE_COLOR`, `STP_BLOCKING_STROKE` and `BLAST_RADIUS_COLOR` are still
categorical hex literals, each carrying a comment asserting it is "distinct from" the others.
Nothing measures that claim — it is the same species of promise as T-4301's "kept in sync by hand",
which was wrong the day it was written. Those comments referenced `KIND_ACCENT` and `STATUS_STROKE`
by name; the references are updated, the underlying gap is not closed. It belongs to whichever card
takes the sim/flow overlays, and it should be an assertion, not a comment.

`mgmtBadgeBg`/`mgmtBadgeText` stay literal in `canvasPalette`'s ROLE table for a reason this card
sharpens rather than removes: mgmt marks an **edge**, and an edge has no room for a glyph, so the
one categorical distinction the map still draws in colour is the one with nowhere else to go.

## Note on scope

This does not remove colour from the map. Traffic, latency, recency and diff overlays all
legitimately encode a *scalar* in colour, and those ramps are T-4301's remaining work. The claim
here is narrower: **a nominal category should not compete with status for hue when the product
has a purpose-built shape channel sitting unused.**
