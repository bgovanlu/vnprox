# T-4301 — The design language stops at the canvas boundary

**Phase:** 43 (Canvas rendering) — this is the phase's first card, ahead of any drawing work
**Status:** open
**Depends on:** T-4201 (accent ramp), T-4203 (surfaces), T-4204 (status scale), T-4206 (motion)
**Blocks:** effectively every other Phase 43 card

## What was found

Phase 42 re-pointed the whole app onto tokens. It re-pointed every *Tailwind utility*. The
topology canvas does not use Tailwind utilities — it uses `ctx.fillStyle = "#..."` — so the only
way Phase 42 could reach it was by someone copying values across by hand. Some of it was copied.
The section after next measures how well that went.

Counted on the tree after Phase 42 landed: **155 hardcoded hex literals in `web/src`, and they
are almost entirely in one directory.**

| File | literals | what it draws |
|---|---|---|
| `topology/canvasDraw.ts` | 35 | nodes, edges, labels — the map itself |
| `topology/TopologyCanvasV2.tsx` | 32 | selection, hover, focus states |
| `topology/export.ts` | 10 | the PNG/SVG export |
| `topology/EntityEdge.tsx` | 9 | edge styling |
| `topology/trafficMode.ts`, `latencyMode.ts`, `recencyOverlay.ts`, `diffOverlay.ts` | ~20 | the semantic overlays |
| `topology/Minimap.tsx` | 6 | minimap |
| `topology/federation/GlobalTopologyView.tsx` | 3 | cluster capsules |

Outside `src/`, two more carry the pre-T-4201 brand blue into places the *operating system*
renders: `web/index.html`'s `<meta name="theme-color" content="#2563eb">` and
`web/public/manifest.webmanifest`'s `"theme_color": "#2563eb"`. On a phone or an installed PWA the
browser chrome is still Tailwind blue-600, a colour the product no longer uses anywhere else.

**Nothing in the app reads a CSS custom property at runtime.** `getPropertyValue` appears three
times in `web/src`, all three inside comments explaining that jsdom cannot resolve them in tests.

## The part that makes this urgent rather than tidy

The canvas is not un-themed. `TopologyCanvasV2.tsx` builds a 16-field `SceneTheme` as a
`dark ? {...} : {...}` literal, and T-4204's adoption wave already extended it with six
status-derived fields for the finding badges. So a seam exists and the design language reached
it — by hand. The comment introducing those fields says so explicitly:

> *Kept in sync **by hand** with index.css's `--color-status-critical/-degraded/-info` (bare) and
> `-soft` values, same as this file already hand-syncs mgmtBadgeBg/Text.*

Measured against the tokens it names — which have not changed since that commit, so this is not
later drift:

| | token | canvas | RGB distance |
|---|---|---|---|
| light critical text | `#b52332` | `#b12c2e` | 10.6 |
| light critical soft | `#faeeef` | `#f7e7e7` | 11.0 |
| light degraded text | `#9b6200` | `#776300` | **36.0** |
| light degraded soft | `#f7f3eb` | `#f0ede1` | 13.6 |
| light info text | `#036f8c` | `#036f8c` | — exact |
| light info soft | `#ebf4f7` | `#e0eff2` | 13.1 |
| dark critical text | `#f76467` | `#ffa098` | **77.9** |
| dark critical soft | `#362233` | `#3a2837` | 8.2 |
| dark degraded text | `#fea92f` | `#dcbc33` | **39.2** |
| dark degraded soft | `#352d23` | `#2f3023` | 6.7 |
| dark info text | `#57cef7` | `#57cef7` | — exact |
| dark info soft | `#16334b` | `#16334b` | — exact |

**Three of twelve match.** The token values are byte-identical to what they were when this copy
was made, so the hand-sync was already wrong *at the moment it was written* — the dark critical
badge is 78 RGB units from the colour it claims to be, a salmon where the scale says red. The
commit that introduced it is titled "convert hand-picked status colours to the semantic scale."

That is the argument for this card in one line: **a comment promising manual synchronisation is
not a synchronisation mechanism**, and this one was broken on arrival, in the same change that
existed to remove hand-picked colours. Every further Phase 43 card would add more fields to the
same hand-copied literal.

## Why this is the phase's first card

The roadmap's whole premise is that the topology map is the product, and the honest baseline said
the visual parts were the weak ones. Phase 42 has now built a palette, a status scale, a surface
ladder and a motion system — and the one screen the product exists for can only receive them by
transcription, which the table above shows does not work. Every Phase 43 card that draws
something would otherwise hand-pick its colours again and widen the gap.

There is also a second palette here that Phase 42 never defined. `canvasDraw.ts`'s `KIND_ACCENT`
assigns a hue per entity kind — bond sky, bridge indigo, VLAN violet, SDN teal, guest emerald,
physical slate — a full *categorical* scale with no counterpart in the design language, which has
semantic status colours and a brand accent but nothing for "these are different kinds of thing".
`STATUS_STROKE` likewise maps `EntityStatus` to Tailwind-500 defaults (`down: #ef4444`,
`degraded: #f59e0b`), a second status scale sitting beside the real one. Both need *deciding*,
not just re-pointing: a categorical scale is a design question to answer once in
`docs/design-language.md`, not to leave to whichever card draws next.

It is also already drifting. `Minimap.tsx:70` paints its background `#0f172a`; the design
language's dark `--color-surface-page` is `#0f172b`. One digit apart, chosen independently, and
nothing can ever tell them to agree.

And the per-theme conditional the token system exists to delete is alive and well here, just
written where no `dark:` grep will ever find it:

```ts
ctx.fillStyle   = dark ? "#0f172a" : "#f1f5f9";   // Minimap.tsx:70
ctx.strokeStyle = dark ? "#3b82f6" : "#2563eb";   // Minimap.tsx:90
```

That is the same defect as T-4211 and T-4214 — a value where a role belongs — in the place it is
hardest to see and most expensive to leave.

## Approach

Canvas cannot consume utilities, so it needs the tokens as *values*, resolved at runtime from the
same stylesheet the DOM uses. One module, read once per theme change, exposing a typed palette:

```ts
// resolves --color-* off document.documentElement, so html.dark / html.demo /
// html.demo.dark all resolve correctly with no branch in drawing code
export function canvasPalette(): CanvasPalette
```

Constraints that fall out of the rest of the phase:

- **One read, not one per frame.** `getComputedStyle` forces style recalculation; calling it in a
  draw loop over hundreds of entities would be a measurable regression against T-4107's
  50-node/5000-guest envelope. Resolve on mount and on theme change, cache the struct.
- **jsdom cannot resolve custom properties** — the three existing comments say so. The palette
  module needs a test seam that does not depend on a real layout engine, and the drawing tests
  that currently assert on literal hex need to assert on palette *keys* instead.
- **`export.ts` must use the same palette**, or an exported PNG stops matching the screen it was
  exported from. It currently has its own ten literals.
- **The overlays are the real prize.** `trafficMode`, `latencyMode`, `recencyOverlay` and
  `diffOverlay` each hand-rolled a colour ramp. They encode severity and age — exactly what
  `--color-status-*` already means. Re-deriving them from the status scale is what makes the map
  legible in the same visual language as the rest of the app, and it is the point of doing this
  before any new drawing work.

## Acceptance criteria

1. A `canvasPalette` module resolves every colour the topology code draws with from
   `--color-*` custom properties, correct under all four mode combinations (base/demo x
   light/dark). See T-4214 for the `html.demo.dark` cascade trap — the canvas will hit it too, and
   will hit it silently.
2. Zero hex literals remain in `web/src/topology/**` outside the palette module itself. A lint
   rule or test enforces this, so the next canvas card cannot reintroduce one.
3. `web/index.html` and `web/public/manifest.webmanifest` carry the current brand accent, not
   `#2563eb`.
4. The overlays' ramps are derived from `--color-status-*` rather than independently chosen, and
   `STATUS_STROKE`'s second status scale is deleted in favour of the real one.
5. A categorical scale for entity kinds is defined in `docs/design-language.md` — with the same
   contrast and separation gates the status scale gets, in all four modes — and `KIND_ACCENT`
   reads it. Do not simply re-point the existing Tailwind-500 picks; they were never designed to
   be distinguishable from each other or legible on the canvas surfaces.
6. T-4107's performance envelope still passes — palette resolution happens on theme change, not
   per frame, and the test proves it by counting `getComputedStyle` calls across a render of the
   50-node fixture.
7. Rendered proof, per the phase rule: the map screenshotted in all four mode combinations, plus
   one exported PNG shown to match its on-screen source. A canvas that passes a token test and
   looks wrong is the failure mode this phase has already hit three times.

## Note on a build artifact

`web/dist/index.html` is tracked while `.gitignore` line 25 ignores `web/dist/*`, so every build
dirties the working tree with a new asset hash. Unrelated to this card, but it means a clean
`make ci` leaves `git status` dirty and the next agent has to decide whether that is their change.
Worth a separate one-line card.
