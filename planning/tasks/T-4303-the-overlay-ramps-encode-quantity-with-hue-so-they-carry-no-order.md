# T-4303 — The overlay ramps encode a quantity with hue, so they carry no order

**Phase:** 43 (Canvas rendering)
**Status:** trafficMode and latencyMode done — neither the way this card proposed; recencyOverlay open
**Depends on:** T-4301 (canvas palette, landed)
**Related:** T-4302 (kind stops using colour) — same principle, different channel

## What was found

`trafficMode.ts` paints edge "heat" by link utilization, 0–100%, from a five-stop scale:

| stop | colour | OKLCH L |
|---|---|---|
| idle <1% | `#94a3b8` slate | 0.711 |
| light <25% | `#38bdf8` blue | 0.754 |
| moderate <50% | `#22c55e` green | 0.723 |
| busy <75% | `#f59e0b` amber | 0.769 |
| saturated | `#ef4444` red | 0.637 |

Lightness runs **0.71 → 0.75 → 0.72 → 0.77 → 0.64** — up, down, up, down. The scale is not
monotonic in any perceptual dimension.

Utilization is a **quantity**. An encoding of a quantity has to be orderable by eye or it is not
an encoding, just a lookup table rendered in colour: nothing about this ramp tells a viewer that
green is more than blue, or that amber is more than green. They have to consult a legend for
every edge, which is precisely the work the paint mode exists to remove. This is the rainbow-ramp
problem, and it is well enough established that it does not need re-deriving here — but the
numbers above are this ramp's own, measured, so it does not need taking on faith either.

`latencyMode.ts` and `recencyOverlay.ts` should be measured the same way before they are changed;
this card assumes nothing about them.

## The second half, stated carefully

Every one of the five stops sits within 33° of a status hue, and four of the five within 8°:

| stop | nearest status | separation |
|---|---|---|
| busy <75% | `degraded` | **0°** |
| moderate <50% | `ok` | 5° |
| saturated | `critical` | 3° |
| light <25% | `info` | 8° |
| idle <1% | `info` | 33° |

`trafficMode.ts`'s own comment says this is deliberate — *"matching the codebase's existing
status-color vocabulary … so 'hot' reads consistently with 'down'/'degraded' elsewhere on the
map."*

**On edges specifically, that reasoning holds and there is no ambiguity.** `canvasDraw.ts:301–312`
picks the edge stroke exclusively: sim verdict, else traffic mode, else status. An edge in traffic
mode is never also painted by status, so amber-on-an-edge has exactly one meaning at a time.

The collision is across the *screen*, not within the edge. Node borders (`statusBorder`) and
finding badges keep the status palette while traffic mode is on, so a single view can show an
amber edge meaning "75% utilized, healthy" beside an amber node border meaning "degraded". Same
hue, two meanings, no visual cue which is which. That is narrower than "the ramp collides with
status" and it is what the evidence actually supports.

## The card's own proposal was wrong, and building it is what showed that

This card asked for the ramp to be made **monotonic in lightness and chroma**. I derived one:
interpolating OKLCH from `--color-outline` to `--color-status-critical` gives five stops that are
monotonic in both, clear 3:1 against the page in both themes, and end on the product's own word
for "critical". It satisfied every constraint the card wrote down.

It is still unusable, for two reasons found only by building it.

**There is nowhere on the hue circle to put it.** A ramp from a cool neutral to red must pass
through either green and amber — which are `ok` and `degraded` — or through violet and magenta.
The derived midpoints took the second route and landed **3deg from `BLAST_RADIUS_COLOR`** and
**5deg from `SIM_STROKE.indeterminate`**. Holding hue constant instead avoids every collision and
tints every *idle* link faintly red, which is worse: idle is the majority state on a healthy map.
This is T-4302's finding again in a new place — the circle is full.

**And the quantity was already encoded, correctly, in another channel.**
`utilizationStrokeWidth` maps 0-100% to 1.5-6px, linear, continuous, monotonic. It was never
broken. The colour ramp was a *redundant second encoding of the same number* — and it was the one
without an order.

So the repair is not a better ramp. It is to stop colour competing for a channel that was already
doing the job:

- **Width keeps the quantity.** It is a better channel for magnitude than hue, and it was correct.
- **Colour names a severity band**: neutral below 75%, `status-degraded` to 90%, `status-critical`
  above. That is what the status scale is for, and it borrows no hue the circle cannot spare.

`utilizationTone()` returns a token *name*, not a colour, because the two renderers consume colour
differently — v1's `EntityEdge` puts `var(--color-*)` straight into an SVG `stroke`; v2's canvas
cannot, and resolves the same name through `canvasPalette`. One name, two resolutions, no third
copy of the palette.

The 75 boundary is the one the old scale already drew (its busy/saturated split). 90 is the single
addition. Neither is presented as an operational standard.

## latencyMode: measured first, and it was a different defect

The card said to measure `latencyMode` before changing it and to assume nothing. That was right:
its ramp (violet-300 → violet-400 → fuchsia-600 → pink-800) **was already monotonic in
lightness** — 0.811, 0.709, 0.591, 0.459, steadily darkening as latency rises. The rainbow problem
trafficMode had is not present here.

It failed on **contrast**, at both ends, for one root cause: a single set of hues served both
themes.

| stop | vs light page | vs dark page |
|---|---|---|
| excellent `#c4b5fd` | **1.78** | 9.66 |
| good `#a78bfa` | **2.63** | 6.55 |
| borderline `#c026d3` | 4.55 | 3.79 |
| degraded `#9d174d` | 7.62 | **2.26** |

Against a 3:1 floor. `excellent` is invisible on white; `degraded` — the state most needing to be
seen — is nearly invisible on the dark page. **A ramp that does not re-point per theme cannot
clear a floor at both ends, whatever its hues.**

The repair is the one trafficMode arrived at, and it fixes the contrast by construction rather
than by re-picking colours: `latencyStrokeWidth` already encodes the magnitude linearly, so colour
names a severity band from the status scale, which re-points per theme. The bands are this
module's own existing thresholds — 0.625x warn, warn — not new ones.

One test had to be **inverted** rather than updated: it asserted latency's palette collided with
no colour already on the map. That was the correct contract for a private four-hue ramp and is the
wrong one now — sharing the status vocabulary with traffic mode is the point, since both answer
"how bad is this link?" and should answer it the same way.

## What to do (for recencyOverlay, still unmeasured)

- **Make each overlay ramp monotonic** in lightness *and* chroma, so more load reads as more ink
  without a legend. A single-hue or two-hue sequential ramp does this; five categorical hues
  cannot.
- **Derive it from the design language**, through `canvasPalette` — the ramps are currently
  literals in four separate modules, which is the hand-copy problem T-4301 removed from
  `SceneTheme` and did not yet remove from here.
- **Keep the top of the ramp reading as alarming.** The current scale's one real virtue is that
  "saturated" looks like trouble; a sequential ramp must not lose that, which usually means
  ending it in the critical hue rather than starting it there.
- **Resolve the cross-screen collision explicitly**, rather than by picking hues and hoping: either
  the overlay dims the status-coloured elements while it is active, or the ramp is held to the
  same 40° separation from `--color-status-*` that the status states hold from each other. Decide
  and write it down; do not leave it to the next card to rediscover.

## Acceptance criteria

1. `trafficMode`, `latencyMode` and `recencyOverlay` each expose their ramp as a pure function
   over a resolved palette, with no hex literals in the module.
2. A test asserts each ramp is monotonic in OKLCH lightness across its stops — the property this
   card exists to establish, and one no screenshot review would reliably catch.
3. `diffOverlay` is exempt from monotonicity and should say so in its own comment: added / removed
   / changed is a **nominal** set, not a quantity, so hue is the correct channel there. Getting
   this exemption written down matters as much as the rule, or the rule gets applied where it
   does no good.
4. The cross-screen decision above is implemented and stated in `docs/design-language.md`.
5. Rendered proof at the phase rule: the map in traffic mode, both themes, with a legend visible,
   showing the ramp ordered.
