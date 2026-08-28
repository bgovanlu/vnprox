# T-4303 — The overlay ramps encode a quantity with hue, so they carry no order

**Phase:** 43 (Canvas rendering)
**Status:** open
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

## What to do

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
