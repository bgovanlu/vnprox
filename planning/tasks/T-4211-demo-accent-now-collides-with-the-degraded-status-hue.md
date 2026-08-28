# T-4211 · Demo mode's amber accent now collides with the `degraded` status hue

**Found by:** T-4201/T-4204 while deriving the design language, 2026-08-28 · **size:** S ·
**depends:** T-4204 (landed) · **affects:** demo mode only, every routed page

## The observation

T-4204 gave the product a formal semantic status scale for the first time. `degraded` is a burnt
amber — `#9b6200` in light mode, `#fea92f` in dark — chosen after rendering four candidate pairs
and looking at them (see `web/src/index.css`'s T-4204 comment for why the arithmetic alone picked
the wrong colour twice).

Demo mode's accent is *also* amber. `html.demo` re-points `--color-accent-*` at Tailwind's amber
scale, a decision made at **T-2801**, when this app had no status scale at all and every page
picked its own emerald/amber/rose by hand. Measured in OKLCH:

| | hue | role |
|---|---|---|
| `html.demo` accent-600 (`#bb4d00`) | ~48deg | selected rows, tabs, primary buttons, focus rings |
| `--color-status-degraded` (`#9b6200`) | ~70deg | a degraded bond, a warning finding |

**~22 degrees apart.** Everywhere else in the palette the floor is 40, and the worst real pair is
48.2. So in demo mode — and only in demo mode — a *selected* row and a *degraded* row are close to
the same colour, which is exactly the "colour encodes status" guarantee the roadmap's principle 2
asks for, broken.

This is a collision **T-4204 introduced**. T-2801's choice was sound when it was made; formalising
an amber `degraded` is what put the two on top of each other. It is recorded here rather than
fixed in place because the fix ripples further than the derivation did — see below.

## Why it was not fixed in the same change

Re-pointing demo mode at a non-amber hue means re-deriving an 11-step ramp with the contrast work
T-3406 already did once by hand (that sweep found two real WCAG AA failures that the base accent
did not have — demo mode passing is never implied by base mode passing), and touching T-2801's and
T-3406's recorded decisions. Three other Phase 42 agents were writing to the tree at the time,
including one whose adoption work runs against the demo-mode axe sweep. Landing a demo-accent
change into that would have been the churn the alias layer exists to avoid.

## Deliverables

- Pick a demo hue that clears 40deg from **every** status hue — ok 145, degraded 70, critical 22,
  info/accent 224 — as well as from the base accent. Around **320deg** (orchid/magenta) satisfies
  all of them with margin and is nowhere near any health state; verify rather than assume.
- Re-derive the 11 steps the way T-4201 derived the base ramp: solve the lightness of 600 and 700
  against their contrast targets rather than picking swatches, so `Button`'s
  `bg-accent-600 text-white` and the `bg-accent-600/10 text-accent-700` selected-row wash both
  clear AA **in demo mode** without any call site moving.
- Keep the "not your cluster" legibility test that motivated T-2801: it has to read as wrong from
  across a room. Check that a saturated magenta still does this — amber's advantage was that it
  looks like a warning, and a replacement must earn the same read some other way.
- Update `html.demo`'s comment in `web/src/index.css`, which currently explains at length why the
  colour is amber and specifically why it is "not, say, green". That reasoning is about to be
  wrong; rewrite it rather than leaving it as a fossil.

## Acceptance criteria

1. `index.css.test.ts` gains a case asserting the demo accent clears 40deg in OKLCH from every
   `--color-status-*` hue, in both themes. It fails against today's amber and passes after.
2. The existing demo-mode contrast assertions still pass, unchanged in strictness.
3. The demo-mode axe sweep is green on every routed page.
4. A rendered before/after of a page showing a selected row **and** a degraded badge together,
   in demo mode, in both themes — the failure this card describes is a visual one and a number is
   not sufficient evidence that it is gone.
