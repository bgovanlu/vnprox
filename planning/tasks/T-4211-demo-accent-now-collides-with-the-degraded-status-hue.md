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

## Re-measured against the rendered specimen (2026-08-28, after T-4208 landed)

The T-4208 component specimen renders every primitive in all three modes, which gave the first
chance to look at this collision instead of computing it. Two things the original entry above
got wrong or missed:

**`critical` is nearer than `degraded`.** Exact OKLCH separations from `html.demo`'s accent-600
(`#bb4d00`, hue 45.4deg):

| | light | dark |
|---|---|---|
| critical | **23.5** | **23.5** |
| degraded | 24.9 | 24.7 |
| ok | 99.7 | 99.7 |
| unknown | 149.8 | 157.4 |
| info | 178.9 | 178.8 |

So the demo accent does not sit *next to* the warning colour, it sits **between the warning
colour and the error colour**, ~24deg from each, against a 40deg floor. This card's title
understates it. The clearest place to see it is the specimen's Progress row: in base mode the
four bars read azure / green / gold / red and separate cleanly; in demo mode the accent bar
lands inside the amber-to-red severity ramp, so a bar that means "this is the selected metric"
reads as a bar that means "this is nearly critical".

**The asserted convergence inverts.** `index.css.test.ts` asserts that `info` sits within 10deg
of the accent — "an informational status IS the accent", the one deliberate exception to the
40deg rule. That test resolves the accent from `@theme` only. In demo mode the accent moves to
amber while `info` stays azure, so the measured separation goes from **0.3deg to 178.9deg** — not
merely violated but exactly inverted, `info` becoming the accent's hue complement. The suite is
green throughout, because it never looks at `html.demo`.

**Nothing tests accent-vs-status at all.** The 40deg floor is asserted only between status pairs.
The accent is exempt from it in both modes, which is why a 23.5deg collision could be introduced,
reviewed and merged without a failing test.

**And the metric has a blind spot in the base theme too.** Base accent-600 to `unknown` measures
**31.1deg** (light) / 23.5deg (dark) — also under the floor. It is not a real problem, because
`unknown` is a near-neutral grey whose hue is close to meaningless at that chroma. But that is
the point: a separation gate on hue alone produces false alarms on low-chroma colours and false
comfort on high-chroma ones. Whatever gate this card adds should weight separation by chroma, or
exempt near-neutrals explicitly, rather than pretending a grey has a hue worth comparing. This is
the same failure the phase already hit twice — a colour metric that agrees with itself and not
with the screen.

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
   The separation must be chroma-aware — see the blind spot above; a naive hue comparison flags
   the base accent against `unknown` at 31deg, which is noise, while saying nothing useful about
   two saturated colours 24deg apart, which is the real defect.
2. The `info`-is-the-accent convergence assertion is re-run against **every** mode, not just
   `@theme`. Today it passes at 0.3deg while demo mode measures 178.9deg. Either the intent holds
   in demo mode after the re-hue, or the intent is base-mode-only and the test says so out loud —
   but it must stop asserting something it does not check.
3. The existing demo-mode contrast assertions still pass, unchanged in strictness.
4. The demo-mode axe sweep is green on every routed page.
5. A rendered before/after of a page showing a selected row **and** a degraded badge together,
   in demo mode, in both themes — the failure this card describes is a visual one and a number is
   not sufficient evidence that it is gone.
