# T-4301 — The design language stops at the canvas boundary

**Phase:** 43 (Canvas rendering) — this is the phase's first card, ahead of any drawing work
**Status:** done. Criterion 2 rewritten (see below) after finding that part of it was never an exception; criterion 3 found a second stale value the card had not counted.
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

## The canvas is also the least accessible surface in the product

Measured after T-4215 landed, each `SceneTheme` value against the fill it is actually drawn on
rather than against the page:

| | value | on | ratio | |
|---|---|---|---|---|
| light `kindText` | `#94a3b8` | `nodeFill` `#ffffff` | **2.56** | FAIL (AA 4.5) |
| dark `kindText` | `#64748b` | `nodeFill` `#1e293b` | **3.07** | FAIL |
| light `nodeText` | `#1e293b` | `#ffffff` | 14.63 | pass |
| dark `nodeText` | `#e2e8f0` | `#1e293b` | 11.87 | pass |
| light `badgeText` | `#475569` | `#e2e8f0` | 6.15 | pass |
| dark `badgeText` | `#cbd5e1` | `#334155` | 6.97 | pass |
| light `mgmtBadgeText` | `#92400e` | `#fde68a` | 5.69 | pass |
| dark `mgmtBadgeText` | `#fde68a` | `#78350f` | 7.28 | pass |

`kindText` is not decorative. `canvasDraw.ts:453` draws the entity kind in it at **9px**, and
`:447` draws the port speed marking at **8px** — well under the 18pt/14pt-bold threshold that
would exempt them, so AA applies in full. It fails in **both** themes, which is the same
"fails in opposite themes so neither is obvious" shape `slateContrast.test.ts` was written for,
except here it fails in both at once.

And the graphical objects, against WCAG 1.4.11's 3:1 floor for non-text content required to
understand the page:

| | value | on | ratio | |
|---|---|---|---|---|
| light `nodeBorderOk` | `#cbd5e1` | `nodeFill` | **1.48** | FAIL (3.0) |
| dark `nodeBorderOk` | `#475569` | `nodeFill` | **1.93** | FAIL |
| light `edgeDefault` | `#94a3b8` | `background` | **2.45** | FAIL |
| dark `edgeDefault` | `#475569` | `background` | **2.36** | FAIL |

`edgeDefault` is the default link stroke, 1.5px (`canvasDraw.ts:305`). **This is a network
topology tool whose connections are drawn at ~2.4:1 in both themes.** If any graphical object in
this product is "required to understand the content", an edge on the topology map is it.

Two honest caveats. `nodeBorderOk` sits *between* the node fill and the page background, so it has
two neighbours; it fails against both (1.48/1.42 light). And highlighted, simulated and
traffic-mode edges take different strokes — this is the default path, which is the common case
but not the only one. Neither caveat changes the conclusion.

So T-4301 is not only a consistency card. The most-looked-at screen in the product carries four
contrast failures that no existing gate can see, because every gate this repo has scans Tailwind
class names and the canvas has none.

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
5. ~~A categorical scale for entity kinds is defined in `docs/design-language.md`~~ — **answered
   at T-4302, and the answer is that no such scale can exist.** Six kinds + four statuses + one
   accent is eleven hues; at the 40deg separation the status scale holds itself to that needs
   440deg of hue circle and there are 360. Four of the six current `KIND_ACCENT` hues are already
   under the floor (bond is 13deg from the accent, guest 17deg from `ok`). Kind moves to the
   T-4205 pictograms — a shape channel with no capacity limit — and colour goes back to meaning
   only status. See T-4302.
6. The four measured failures are fixed and gated: `kindText` clears AA on `nodeFill` in both
   themes (`--color-fg-subtle` does, at 5.03 light / 6.28 dark — T-4215 supplied it), and
   `nodeBorderOk` and `edgeDefault` clear 3:1 against **both** neighbours. The gate measures the
   resolved palette against the resolved surfaces, in all four modes, the way
   `index.css.test.ts` now does for the DOM — a canvas-side equivalent, not a second hand-copy.
7. T-4107's performance envelope still passes — palette resolution happens on theme change, not
   per frame, and the test proves it by counting `getComputedStyle` calls across a render of the
   50-node fixture.
8. Rendered proof, per the phase rule: the map screenshotted in all four mode combinations, plus
   one exported PNG shown to match its on-screen source. A canvas that passes a token test and
   looks wrong is the failure mode this phase has already hit three times.

## Note on a build artifact

`web/dist/index.html` is tracked while `.gitignore` line 25 ignores `web/dist/*`, so every build
dirties the working tree with a new asset hash. Unrelated to this card, but it means a clean
`make ci` leaves `git status` dirty and the next agent has to decide whether that is their change.
Worth a separate one-line card.

## Progress after T-4302/T-4303 — what is done, and where criterion 2 breaks

**Done.** `canvasPalette` exists (criterion 1) and the four measured failures are fixed and gated
(criterion 6). Criterion 4's overlays landed at T-4303 — traffic and latency now name a severity
band from the status scale instead of carrying private ramps — and `STATUS_STROKE` is deleted, in
all three copies of it T-4302 found rather than the one this card named.

Three more consumers were swept afterwards, each because measuring it turned up a real defect
rather than merely a literal:

| | was | measured | now |
|---|---|---|---|
| `Minimap.tsx` dots | `#94a3b8` / `#475569` | **2.34 / 2.36** vs a 3:1 floor | `--color-fg-subtle` on `--color-surface-sunken`, 4.64 / 7.47 |
| `export.ts` status | its own 4-entry table | `ok` border **2.45** on its own node fill | the resolved palette, 3.25 |
| `export.ts` surfaces | `#f8fafc` node on `#ffffff` page | **ladder inverted in light mode** | `surface-raised` on `surface-page` |

The export one is the one worth remembering. This card predicted an export "stops matching the
screen it was exported from"; it was worse than stale. In light mode it drew nodes *darker* than
the page while the design language's ladder puts a raised surface *lighter* than it — so the
exported picture had the surface ladder upside down, in the theme most exports are taken in. Its
palette is now resolved off the live document at click time, which also gets demo mode right for
free, something the `theme?: "light" | "dark"` parameter it replaced could not express at all.

`renderSvg`'s palette is **required**, with no default: a default here would be the fallback
palette `canvasPalette`'s own doc comment argues against, since a fallback that drifts renders
plausibly while being wrong.

**Criterion 2 — "zero hex literals in `web/src/topology/**`" — cannot be met as written, and
should be rewritten rather than chased.** T-4303 established that `recencyOverlay` and
`diffOverlay` are *nominal* scales — added/removed/changed, and an age bucket that is not a
severity — for which hue is the correct channel and the status scale would state something false.
Those two files hold 21 of the 33 literals left. `SIM_STROKE`, `FLOW_EDGE_COLOR`,
`STP_BLOCKING_STROKE` and `BLAST_RADIUS_COLOR` are the same category.

So the remaining literals are not stragglers of one sweep; they are **one unanswered design
question**: the product has a status scale, one brand accent, and nothing at all for "these are
different KINDS of thing". T-4302 proved that a general categorical scale cannot be added at the
status scale's own 40deg separation, and solved the node case by moving kind to shape. The cases
left are the ones with nowhere to put a shape — an edge, a 2px badge — and they need their own
answer, not a token rename.

What is genuinely still a literal-with-a-role, and should be swept whenever its blocking decision
lands:

- `Minimap.tsx:104`, the viewport rectangle, and `canvasDraw`'s hover-highlight and selection
  rings: all three are the brand accent. They are blocked on **T-4214**, because `index.css`
  re-points every `--color-accent-*` step under `html.demo` but not under `html.dark` — a dark
  page picks a different *step*, so there is no single token to name. None is a defect meanwhile:
  the rectangle measures 4.72 / 4.85.
- Criterion 3 (`index.html`, `manifest.webmanifest`) is untouched and independent of all of this.


---

## Criterion 2, revisited: "cannot be met as written" was half right

The paragraph above rules that `zero hex literals in web/src/topology/**` cannot be met, because
the remaining literals are nominal scales for which hue is the correct channel. That is true of
`diffOverlay`, `recencyOverlay`, `simVerdict` and `STP_BLOCKING_STROKE`, and it is the right call.

**It was not true of three of them, and the ruling hid that.** The hover ring, the selection
outline (`canvasDraw.ts`) and the minimap's viewport rectangle (`Minimap.tsx`) were `#3b82f6` and
`#2563eb` — Tailwind blue-500/600, the **pre-T-4201 brand blue**, which this very card's opening
section names as a colour "the product no longer uses anywhere else". They are not a nominal scale
needing a hue of its own. They are the *accent*, drawn in a dead value.

**The visible consequence was in demo mode**, which is the mode built to make accent changes
obvious: `html.demo` re-points every `--color-accent-*` step to another hue family, so selecting a
node on the map produced a BLUE ring while every other selected control on the page went purple.
The map and the app disagreeing about what "selected" looks like.

They survived for a reason worth recording: the audit counted 155 literals, ruled on the *hard*
cases correctly, and let that ruling cover the easy ones by adjacency. A blanket "these are all
design exceptions" is the same shape of error as a blanket "these are all stragglers" — the count
was never the interesting number, the classification was.

`Minimap.tsx`'s own comment shows the other half of it. It deferred the choice explicitly — *"the
accent has no per-theme alias yet … choosing that step is T-4214's decision"* — and that was
accurate when written. **T-4214 has since landed** (`--color-accent-fg`, accent-700 light /
accent-300 dark). The deferral was correct and simply outlived its blocker, with nothing watching
for the unblocking. The `dark` prop that existed solely to hand-pick a step is gone with it.

### What the criterion says now

Not a count. **Every literal left is a named exception with a recorded reason**, enforced by
`hexLiterals.test.ts`: a file not on the allowlist may hold none, every allowlist entry carries the
argument for why a token would be *wrong*, and a guard-the-guard fails any entry that no longer
holds a literal so the list cannot quietly widen. It also names the retired brand blue specifically,
since its reappearance means something different from a generic literal — it means the map has
drifted back off the accent.

`canvasDraw.ts` — the file this card counted 35 literals in, the one that draws the map itself —
now holds **zero**. Its only remaining match is inside a doc comment describing the pattern it
deleted.

Verified by reintroducing `#2563eb` at the selection ring: three of the four assertions fail,
including the one that names the colour.


## Criterion 3: the brand the operating system paints

`index.html`'s `<meta name="theme-color">` and `manifest.webmanifest`'s `theme_color` both carried
`#2563eb`, so on a phone or an installed PWA the browser chrome and the splash screen were still
Tailwind blue — a colour the product had stopped using everywhere else. Both now carry
`--color-accent-600`.

**The manifest had a second stale value this card never counted.** `background_color` was
`#0f172a` — Tailwind slate-900 — where the dark surface ladder settled on `#0f172b` at T-4203. One
hex digit. Nobody was ever going to catch that by eye, and that is the entire argument for the test
rather than the fix.

These two files are the one part of the design language that **cannot** be re-pointed by editing
`index.css`: they are not CSS, they cannot read a custom property, and their values must be
literal. So `web/src/brandChrome.test.ts` makes them derived the only way available — it reads the
tokens out of `index.css` and asserts both files match, `theme_color` against the accent and
`background_color` against the *dark* page surface (the splash is deliberately dark, so an
installed app does not open on a white flash). It also names the retired brand blue explicitly,
because its reappearance here means something more specific than a wrong value: it means the PWA
has drifted back to an abandoned colour in the one surface no stylesheet edit can reach.

Verified in both directions: putting `#2563eb` back in the manifest fails two assertions, and
moving `--color-accent-600` in the stylesheet without touching the two files fails two others.
