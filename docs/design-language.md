# vnprox design language

The reference for anyone adding UI to vnprox. It is the token half of what
[`planning/roadmap-visual.md`](../planning/roadmap-visual.md) calls T-5110, written as Phase 42
landed rather than at the end, because the ninety cards after Phase 42 all build against it and a
convention that is not written down is re-invented per page — which is how the product ended up
needing this roadmap in the first place.

**Sections marked _pending_ are filled in as their phase lands.** An empty section is a promise,
not an omission.

Everything below is defined in `web/src/index.css` and asserted in `web/src/index.css.test.ts`.
That test is not a formality: it recomputes every contrast number quoted here, so the measurements
in this document cannot quietly go stale the way the four hand-derivations before it did (T-905,
T-3401, T-3405, T-3406 — each correct, none of them leaving anything behind that would catch the
next one).

---

## 1. Principles

These come from the roadmap and they decide arguments, so they lead.

1. **The map is the product's poster.** Trade-offs between canvas quality and anything else
   resolve toward the canvas.
2. **Every visual encodes something true.** Colour is status, width is rate, position is topology,
   motion is change. A flourish that encodes nothing does not ship.
3. **Tables stay; pictures are added.** A visual counterpart complements a table, never replaces
   the operator's ability to sort, filter and copy.
4. **Both themes, deliberately.** Dark mode is designed, not inverted.
5. **Reduced motion is first-class.** Every animation has a static rendering that conveys the same
   information.
6. **Perf budgets apply to beauty.** Canvas work runs under the T-4107 envelope gates in
   `perf/budgets.json`. A visual that blows a budget ships with an LOD rule, not without the gate.

---

## 2. Colour

### 2.1 The accent — "signal azure", OKLCH hue 224

Eleven literal steps, `--color-accent-50` … `--color-accent-950`, consumed as `accent-*`
utilities. Never write a raw brand hex at a call site; the whole point of the alias layer is that
demo mode can re-tint the app by re-pointing eleven properties.

Through T-3401 the accent was an alias of Tailwind's stock indigo — the most-used accent on the
web, and the reason the product had no identity of its own. Hue 224 is a cold signal-azure, the
colour of a link light and an instrument trace, chosen under a hard constraint rather than by
taste: the status scale needs green, amber and red to be unmistakable, so the brand hue has to
stay far from all three.

**The ramp is solved, not picked.** `Button`'s primary variant is `bg-accent-600 text-white`,
shared with about ten other solid controls, so `accent-600` has to clear WCAG AA against white or
every one of those call sites moves. A naive azure ramp fails that at 3.67:1, because cyan is
intrinsically light at a given OKLCH lightness. So L(600) and L(700) were solved against contrast
targets and the rest of the curve interpolated around them:

| pairing | ratio | floor |
|---|---|---|
| white on `accent-600` — Button primary | 4.94 | 4.5 |
| white on `accent-500` — primary hover | 3.09 | transient state, not gating |
| `accent-700` on white — selected row/tab text | 7.01 | 4.5 |
| `accent-700` on a 10% `accent-600` wash over white | 6.13 | 4.5 |
| `accent-300` on `slate-900` — dark selected text | 10.58 | 4.5 |
| `accent-400` on `slate-900` — dark links | 7.91 | 4.5 |
| `accent-300` on a 10% wash over `slate-900` | 9.67 | 4.5 |

Because the ramp was solved to these, adopting the new identity moved **zero** component call
sites.

### 2.2 The semantic status scale

One health vocabulary for the whole product. Before T-4204 every page picked its own
emerald/amber/rose, so the same condition drew differently on the dashboard, in a findings badge
and on the canvas — drift that reads to an operator as a bug in the data.

Six states, three roles, and **no `dark:` prefix at call sites**:

| state | meaning |
|---|---|
| `ok` | healthy, and recently confirmed so |
| `degraded` | working, but not as intended — a bond down to one member |
| `critical` | not working |
| `info` | a neutral notice; deliberately sits **on** the brand hue |
| `unknown` | we cannot tell — not the same as `ok` |
| `stale` | a freshness qualifier, see below |

| role | token | use |
|---|---|---|
| bare | `text-status-ok` | text or an icon on the page surface |
| solid | `bg-status-ok-solid` | a filled badge; white text on it in light mode |
| soft | `bg-status-ok-soft` | a wash sitting behind bare-token text |

The tokens are redefined under `html.dark`, so `text-status-critical` resolves correctly in both
themes on its own. **Adoption therefore deletes conditional classes rather than adding them.** If
a diff adds a `dark:` variant for a status colour, it is wrong.

`stale` has **no solid and no soft variant**, deliberately. Stale means "we have not re-read this
recently", which can be true of a perfectly healthy entity, so it renders as a desaturation or a
dashed border layered on top of the real state — never as a filled badge competing with one.
Giving it a wash also produced the only sub-AA pairing in the whole set, which is the design
telling you the same thing.

Measured, and re-measured by the test suite:

| | light fg/surface | fg/soft | white/solid | dark fg/surface | fg/soft |
|---|---|---|---|---|---|
| ok | 5.56 | 5.02 | 4.97 | 10.01 | 7.33 |
| degraded | 5.07 | 4.58 | 4.80 | 9.28 | 7.05 |
| critical | 6.46 | 5.70 | 5.79 | 5.92 | 4.86 |
| info | 5.74 | 5.15 | 5.15 | 9.83 | 7.19 |
| unknown | 5.88 | 5.31 | 5.24 | 9.59 | 7.00 |

### 2.3 How the hues were chosen, and why the metric was wrong twice

Worth reading before proposing a new colour, because the same trap is waiting.

Principle 2 only holds if the encodings are separable, so every pair was checked numerically
first. That moved `ok` to hue 145, away from the azure accent. Then the palette was **rendered and
looked at**, and the screen contradicted the arithmetic twice:

- The amber that satisfied the separation metric — hue 95 — read as **olive**. A "degraded" chip
  the colour of moss does not say degraded.
- The dark-mode critical the metric liked was a pale **salmon**, which does not say critical.

Four candidate amber/red pairs were then rendered side by side in both themes and compared. The
pair that reads correctly at a glance is `degraded` at hue 70 (a true burnt amber) with `critical`
at hue 22 and a deepened dark step (saturated, not pink).

The metric was the thing that was wrong. sRGB-HSL hue compresses the warm region, so it scored a
pair that reads unmistakably as 36 degrees apart. The test now measures separation in **OKLCH**,
the space the palette is designed in, where every pair clears 40 degrees with 48.2 the worst case
in both themes.

> **The rule this leaves behind: render it and look at it.** A palette gate that disagrees with
> what the screen shows will eventually be satisfied by something nobody can read.

### 2.4 Neutrals

Tailwind's `slate` scale, unchanged, and that is a finding rather than laziness. T-4201's card
called for "a hue-biased neutral ramp replacing raw slate"; measuring slate first showed it is
*already* hue-biased — chroma up to 0.046 at hue 257, not a pure grey. Replacing it would have
touched 3,764 call sites and invalidated every contrast measurement recorded against it, to
arrive at approximately the same colour.

`slateContrast.test.ts` is a live guard here: bare `text-slate-400` and `text-slate-500` fail AA
in *opposite* themes, which is why neither is obvious to someone working in one of them. Use
`text-slate-600 dark:text-slate-400`, which clears both.

### 2.5 Demo mode

`html.demo` re-points the accent so a demo cluster cannot be mistaken for a real one at a glance.
Demo mode passing is **never** implied by base mode passing — T-3406 found two real AA failures
that existed only under the demo accent. Any change to a solid-accent or accent-wash pattern must
be checked in both.

> **Known defect:** the demo amber now sits ~22 degrees from `degraded` in OKLCH, so in demo mode
> a *selected* row and a *degraded* row are close to the same colour. T-4204 introduced this by
> formalising an amber `degraded`. Filed as **T-4211**.

---

## 3. Typography

**IBM Plex Sans** (400/500/600/700) and **IBM Plex Mono** (400/500), SIL OFL 1.1, vendored in
`web/public/fonts/` with the licence beside them. Latin and latin-ext only, split by
`unicode-range`, so a Latin-script user downloads about 207 KB and never fetches the ext faces.

Self-hosted, and that is not a preference. The daemon serves this app from the PVE host, which is
routinely on a management network with no route to the internet. **A font CDN link would fail
silently there** — the page renders in the fallback and nobody files a bug. The test asserts every
`src` is a same-origin `/fonts/` path and that no remote stylesheet is imported.

Plex rather than Inter or Space Grotesk: both of those are the reflexive default, and a product
whose complaint was "no identity" gains nothing from the most-used sans on the web. Plex Mono
earns its place on merit — unambiguous `0`/`O`, `1`/`l`/`I`, and even digit widths, which are
load-bearing for the MACs, IPv6 addresses and hex this app renders on nearly every page.

Use `font-sans` and `font-mono`; `<code>`, `<kbd>`, `<samp>` and `<pre>` already resolve the mono
face and `tabular-nums` globally. **Reach for `tabular-nums` anywhere digits line up in a column**
— a proportional-figure column visibly jitters when it re-renders every second, which is exactly
what live rate counters, port statistics and latency samples do.

---

## 4. Surfaces and elevation

Four levels, redefined under `html.dark`, so `bg-surface-raised` is correct in both themes with no
prefix:

| level | use | light | dark |
|---|---|---|---|
| `surface-sunken` | an inset well — a code block, a nested region | `#f4f6f8` | `#090f1c` |
| `surface-page` | the page ground | `#fafbfd` | `#0f172b` |
| `surface-raised` | a card or panel sitting on the page | `#ffffff` | `#182133` |
| `surface-overlay` | a dialog, popover or menu floating above that | `#ffffff` | `#222b3d` |

In light mode elevation rises toward white and shadow does the work. **In dark mode shadow does
not work** — a drop shadow on a near-black ground is invisible, which is why dark themes built by
inverting a light design lose all their depth. The dark ladder lightens instead, and each step is
a real surface an element can sit on. Body text (`#e2e8f0`) measures 15.53 / 14.46 / 13.06 / 11.50
against the four levels, so the ladder never costs legibility as it climbs. The test asserts the
dark ladder is monotonic: if two levels ever collapse to the same luminance, a dialog stops
reading as being above the page.

---

## 5. Motion

Three durations on `:root` — `--motion-fast` 120ms, `--motion-base` 180ms, `--motion-slow` 320ms —
consumed as `duration-[var(--motion-base)]`. Three curves as `ease-standard`, `ease-entrance`,
`ease-exit`.

The curves are **asymmetric on purpose**: things enter decelerating (they arrive and settle) and
leave accelerating (they get out of the way). A single symmetric ease used for both directions is
the tell of motion that was added rather than designed.

**One global reduced-motion gate**, at the bottom of `index.css`, zeroes all three durations. A
component that animates through a token gets reduced-motion behaviour for free without knowing the
rule exists; a blanket override underneath catches everything that does not yet use a token,
including third-party styles (Radix, React Flow) this app does not own. Durations collapse to
`0.01ms` rather than `0` so animation-end events still fire and state machines waiting on them do
not hang.

`useReducedMotion()` in `web/src/lib` stays alongside it. The two are halves of one policy: CSS
cannot reach JS-driven or canvas animation, and some components must render a genuinely
*different*, static representation rather than the same one faster. Use the hook when the
rendering changes; use the tokens when only the timing does.

---

## 6. Pictograms

23 hand-authored glyphs in `web/src/icons/`, one per entity kind. `lucide-react` stays in use for
generic UI *actions* — chevrons, close, settings — and the two sets are drawn to the same stroke
weight so they read as one family. The division is the useful part: **lucide draws verbs, these
draw nouns.**

Render through the registry, never a per-call-site switch:

```tsx
import { PICTOGRAMS, getPictogram, type PictogramKind } from "../icons/registry";
const Glyph = getPictogram(entity.kind);   // falls back to UnknownPictogram
<Glyph size={32} />
```

`PictogramKind` uses `internal/inventory/ref.go`'s `Kind` strings **verbatim** wherever a real Kind
exists, so `PICTOGRAMS[topologyNode.kind]` works without translation. The handful of concepts with
no backend Kind of their own — `vxlan`, `gateway`, `switch`, `port`, `firewall-group` — use plain
descriptive strings, documented per entry. The set is curated rather than a 1:1 mirror of the
enum; extending it for a niche kind is a small additive change.

Rules for drawing a new one:

- **24×24 viewBox, 2px round-capped stroke, `currentColor` only.** Never a hex — the caller owns
  the colour, which is what lets a glyph pick up a status token or an accent without a variant.
- **Three sizes have to work**: ~16px inline, ~32–48px as a canvas node glyph, 96px+ as an
  illustration seed. This is the hard part, not an afterthought. Detail that survives at 24px turns
  to mud at 16px, so glyphs branch on `isDetailed()` (`sizing.ts`) and drop interior dots, halve
  grids, and shed arrowheads at the small size. Some losses are deliberate and documented — the OVS
  variants fall back to their plain sibling's silhouette at 16px rather than render an illegible
  corner mark.
- **Draw it, render it, and look at it before you believe it.** T-4205's own visual pass caught two
  glyphs that tested green and read wrong: the fabric mesh rendered as a boxed X — a *cancel* icon —
  and the zone glyph's dot arrangement read as a face. Both were redrawn. This is section 2.3's rule
  again in a different medium.
- **Know your risk pairs.** The ones most easily confused are documented in-code: `physnic` vs
  `port` (physnic is weakest at 16px), and `wg-peer` vs `fw-ruleset`/`firewall-group`, all three
  shield-based.

A registry entry missing for a declared kind fails the build — `Pictogram.test.tsx` asserts
completeness in both directions, so a new kind cannot ship without a glyph.

## 7. Components

_Pending — T-4207, T-4208, T-4209._

## 8. Canvas grammar

_Pending — Phase 43. Node and edge rendering, group capsules, the overlay channel assignment
(hue / hatch / halo / badge / ornament) that lets several overlays coexist legibly, and the
dark-canvas art direction._

## 9. Charts

_Pending — T-4501, T-4503, T-4510. Until the shared theme lands, charts ship recharts defaults;
do not hand-theme one chart in isolation._

---

## 10. Adding to this system

- **Check the token exists before adding a colour.** A one-off hex in a component is the exact
  drift T-4204 was created to undo.
- **Measure, then render, then look.** In that order, and do not skip the third — section 2.3 is
  what happens when you stop after the first.
- **Both themes and demo mode**, every time. Demo mode is a separate palette, not a tint.
- **If you change a token, the test tells you what you broke.** It is the guard that four earlier
  hand-derivations of this same palette did not leave behind.
