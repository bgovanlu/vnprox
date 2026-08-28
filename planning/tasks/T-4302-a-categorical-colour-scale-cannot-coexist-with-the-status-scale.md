# T-4302 — A categorical colour scale cannot coexist with the status scale, so kind must stop using colour

**Phase:** 43 (Canvas rendering)
**Status:** open
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

## Note on scope

This does not remove colour from the map. Traffic, latency, recency and diff overlays all
legitimately encode a *scalar* in colour, and those ramps are T-4301's remaining work. The claim
here is narrower: **a nominal category should not compete with status for hue when the product
has a purpose-built shape channel sitting unused.**
