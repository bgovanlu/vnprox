# T-4215 — The design language has no foreground or border tokens, which is why ~1000 `dark:` pairs survive

**Phase:** 42 (Design language)
**Status:** done. Criterion 4's remainder converted (1284 pairs -> 82) and criterion 5's gate
rebuilt, after finding that what shipped as "5 done" measured contrast rather than adoption
**Depends on:** T-4203 (surface ladder), T-4204 (status scale)
**Blocks:** the remaining T-4203/T-4204 adoption sweep, and T-4301 (canvas palette)

## What was found

The token inventory in `web/src/index.css` after Phase 42 is: an 11-step accent ramp, 16 status
values, 4 surfaces, 6 motion values, 2 fonts, 1 radius. **There is no token for text and no token
for a border.** Every foreground and every hairline in the app is a raw Tailwind slate step with a
hand-written `dark:` partner.

Counted:

| pattern | occurrences |
|---|---|
| `text-slate-N dark:text-slate-M` | **999** |
| `border-slate-N dark:border-slate-M` | 44 |
| status-coloured `text-*` pairs | 135 |
| status-coloured `bg-*` / `border-*` pairs | 13 |
| accent pairs (T-4214) | 21 |

I have spent this phase counting the bottom three rows. The top row is larger than all of them
together by a factor of eight, and — unlike the others — **it cannot be swept at all**, because
there is no token to point the call sites at. The "remaining mechanical sweep" recorded in
T-4204's commit message is not mechanical; it is blocked on a design decision nobody has made.

## The roles are already there, undeclared

999 pairs, but they collapse to four recipes covering 971 of them:

| light | dark | uses | role |
|---|---|---|---|
| `slate-900` / `slate-800` | `slate-100` | 77 | headings, primary text |
| `slate-700` | `slate-200` | 106 | body copy |
| `slate-600` | `slate-400` / `slate-300` | 616 | secondary text |
| `slate-500` | `slate-400` | 172 | labels, captions, units |

So the design language already *has* a foreground scale. It is written out longhand 999 times
instead of being declared once.

The 616-use row shows what that costs: `slate-600` is paired with `dark:slate-400` at 468 sites
and `dark:slate-300` at 148. The codebase disagrees with itself about the dark partner of its
most-used text colour, and both readings are defensible because nothing states the intent. Both
clear AA (5.53 and 9.55 worst-case), so this is consistency rather than correctness — but it is
exactly the disagreement a role token exists to end.

## A live AA failure the existing guard cannot see

Derived against the T-4203 surface ladder, worst case across all four surfaces:

| role | light | worst | dark | worst |
|---|---|---|---|---|
| `fg` | `slate-900` | 16.48 | `slate-100` | 12.94 |
| `fg-body` | `slate-700` | 9.56 | `slate-200` | 11.50 |
| `fg-muted` | `slate-600` | 6.99 | `slate-400` | 5.53 |
| `fg-subtle` | `slate-500` | **4.39 — FAIL** | `slate-400` | 5.53 |

`text-slate-500` on `--color-surface-sunken` (`#f4f6f8`) measures **4.39:1** against a 4.5 floor.
172 call sites use it.

`slateContrast.test.ts` exists precisely to catch this class, and it cannot. Its measurement table
is written against **`bg-white` and `dark:bg-slate-900`** — it records slate-500 on white as
4.76 ✓, which is still true. T-4203 then introduced a surface *ladder*, and `surface-sunken` is
darker than white. **Introducing the ladder changed the denominator of every contrast measurement
in the codebase, and the one guard that measures contrast still uses the old denominator.**

That guard's own header says this defect class "has now been found and fixed three times" and
explains why each local fix failed to stop the next. This is the fourth, and the reason is new:
not an unvisited route this time, but a guard measuring against surfaces the app has moved off.

Solved fix, holding slate-500's hue and chroma and darkening only its OKLCH lightness by 0.012:

```
--color-fg-subtle (light): #617087   sunken 4.64  raised 5.03   PASS
```

RGB distance from `#64748b` is ~5 — imperceptible at 172 call sites — and it stays 1.51x lighter
than `fg-muted`, so the two roles remain visibly distinct.

## Why this also blocks T-4301

The canvas's `SceneTheme` has 16 fields. Six are status colours (T-4301 measures how badly the
hand-copy of those went). Most of the other ten — `nodeText`, `kindText`, `badgeText`,
`nodeBorderOk`, `edgeDefault`, `badgeBg` — are foreground and border roles. There is nothing in
`index.css` for them to resolve from, so a canvas palette module written today would still
hand-pick two-thirds of its values. **The canvas hand-picks because there was nothing to point
at.** T-4215 lands first, T-4301 second.

## Acceptance criteria

1. `index.css` declares `--color-fg`, `--color-fg-body`, `--color-fg-muted`, `--color-fg-subtle`,
   `--color-border`, `--color-border-strong`, re-pointed under `html.dark`. Values as derived
   above, with `fg-subtle` light at the solved `#617087` rather than `slate-500`.
   Demo mode does not re-point neutrals — state that in the comment so the next reader does not
   wonder whether it was forgotten.
2. `index.css.test.ts` asserts every foreground role clears its target (7:1 for `fg`/`fg-body`,
   4.5:1 for `fg-muted`/`fg-subtle`) on **all four** surfaces in **both** themes. Written against
   the surface tokens, not against literal white — so the next surface change breaks the test
   rather than the contrast.
3. `slateContrast.test.ts` is re-pointed at the surface ladder, and its measurement table is
   rewritten with the current numbers. Leaving a stale table in place is worse than having none,
   because it reads as verified.
4. The 999 + 44 pairs are converted. Expect the count to fall to near zero; anything left needs a
   comment saying why.
5. A gate that fails on reintroduction of `text-slate-*`/`border-slate-*` in `web/src`, with an
   allowlist. The three always-dark surfaces `slateContrast.test.ts` already documents
   (`DemoBanner`, `IncidentsPage`, `SwitchFaceplate`) are the known exceptions.

## What landed, and what the sweep turned up

Offences went **186 → 0**. 172 were the one subtle recipe and fell to a scripted substitution.
The other 14 needed reading, and three were worth more than their count:

- **The changeset badge chips measured 4.08:1** — worse than any page surface — because they sit
  on their own `bg-slate-200/70` fill. A guard that measures against surfaces still cannot see a
  call site that brings its own background. That limitation is now the honest boundary of this
  test rather than an unknown.
- **`FabricsView`'s "no member nodes reported"** was live informational text at 2.37:1.
- **Two chart gridlines looked inverted and were not.** `text-slate-200 dark:text-slate-700` on a
  `CartesianGrid stroke="currentColor"` is the *correct* faint pairing; forcing it to AA would
  overpower the chart it belongs to. Established by opening the element, not by reading the
  pattern — the pattern says "inverted, broken in both themes."

`ALWAYS_DARK_SURFACES` became `OFF_LADDER`, keyed by className rather than by file. Exempting a
file silently exempts every element added to it later, and "always dark" turned out to be one of
three ways to sit off the ladder — the others being an always-*coloured* fill (a `slate-900`
count pill on `bg-amber-500`, identical in both themes) and a non-text stroke.

One change was deliberately *not* made: `EvpnView`'s chip keeps `bg-slate-100 dark:bg-slate-800`
rather than `bg-surface-sunken`. The chip sits below the page in light mode and above it in dark,
which is the ordinary subtle-chip idiom; the surface token would have put it below in both. That
is a visible change to fix a contrast failure that was never in the background.

## Note

This card is the reason to be sceptical of the phrase "the remainder is mechanical" in T-4204's
commit message — which I wrote. The remainder was not mechanical; it was blocked, and counting
only the patterns that *had* a token made the blockage invisible for the whole phase.


## Criterion 4's remainder, and what it turned up

**1284 convertible pairs -> 82.** Done in two passes, and the split between them is the point.

**Pass 1 (900 sites, 172 files) changed no pixel.** Five pairings are byte-identical to a token in
*both* themes — `text-slate-900 dark:text-slate-100` **is** `--color-fg`, and so on for `fg-body`,
`fg-muted`, `border` and `border-strong`. Those needed no judgement, only care in the mechanics:
substitution is per-string-literal and by exact token equality, so a light step in one class list
can never pair with a dark step in another and `group-hover:text-slate-600` is never mistaken for
`text-slate-600`. `--color-fg-subtle` is deliberately absent from that list — its light value
(`#5f6e85`) was *solved* for contrast and is not a slate step, so converting to it would be a
visible change and belongs with pass 2, not pass 1.

**Pass 2 (301 sites) needed two decisions, and both were asked rather than assumed.**

- 180 sites spelled muted text as `text-slate-600 dark:text-slate-300` — the same role as the 487
  already converted, in a second spelling. Converted; dark-mode secondary text dims from 9.55 to
  5.53, both comfortably AA.
- 109 sites used `border-slate-200 dark:border-slate-700`, which is `--color-border`'s light value
  with `--color-border-strong`'s dark value. **Neither token spelled the app's most common border
  idiom**, and converting to `border` as it stood would have pushed dark borders from 1.72 to
  **1.22** — a border that is not a border.

### The border tokens were solved against a floor nobody was failing

`--color-border` measured **1.03** against its worst dark surface. Meanwhile 109 call sites had
independently written `dark:border-slate-700` and 12 more `dark:border-slate-600`: the codebase's
own idiom was a step lighter than the token, in both roles, and *no call site had adopted the
derived value*. That is this card's own finding turned around and pointed at the token — a value
correct against a denominator someone picked, while the thing it described sat elsewhere.

Both re-pointed one step lighter (`border` -> slate-700, `border-strong` -> slate-600). It had to
move as a **ladder**: `border-strong` was already slate-700, so lifting `border` alone would have
collapsed two roles into one colour in dark while leaving them distinct in light. Worst-case dark
contrast is now 1.37 / 1.87, still under the `<2` hairline ceiling `index.css.test.ts` holds
`border` to, and 121 sites convert with zero visual change instead of regressing.

### The re-point found a third thing

`canvasPalette`'s badge pair dropped to **4.04** in dark. The cause is not the re-point — which
*improved* the badge's contrast against the node it sits on, 1.23 to 1.56, and that same value
doubles as the stale-node fill. The cause is that **`--color-border` is a hairline token being used
as a fill**, and `index.css.test.ts` holds it under 2:1 against every surface precisely so it stays
a hairline. Nothing in that contract makes it a legible background for text, so a foreground solved
against *surfaces* was never safe on it. Fixed on the text side (`badgeText` -> `--color-fg-body`,
8.40 in both themes) rather than by moving the fill, which measured 1.08/1.19 against the node and
would have made a stale node harder to see.

## Criterion 5: what shipped as "done" was measuring the other property

`slateContrast.test.ts` fails on slate that fails **AA**. Criterion 5 asks for a gate that fails on
**reintroduction**. Those are different properties, and the contrast gate would have stayed at zero
offences while every call site drifted back off the tokens, because a legible slate pair is still a
slate pair.

`slateAdoption.test.ts` is the missing half. It asserts something narrower than the criterion's
literal words, on purpose: not "no slate anywhere" — 82 pairs remain that no token spells exactly,
and converting those means choosing a visible change — but **"if a token already spells this exact
pairing, use the token."** Those conversions are byte-identical, so there is never a reason to write
the long form.

Its token->slate mapping is derived from `index.css`, which matters more than usual here: a
hand-written table would have gone on policing the pre-re-point border pairing. A guard-the-guard
asserts the derivation is non-empty, since a renamed token would otherwise make every assertion
pass vacuously. Verified by writing a converted pairing back out — it fails, naming the file and
the utility to use.

### What the 82 are

`text-slate-800 dark:text-slate-100` is the largest group (42): slate-800 sits between
`--color-fg` and `--color-fg-body` and matches neither. The rest are similar near-misses. Each
needs a decision about which token it should become and an accepted visible change — a design
question per group, not a sweep, which is why the count stops here rather than at zero.


## The axe sweep caught what `make ci` could not, and it was mine

`make ci` deliberately omits the e2e job, so the whole sweep above shipped green through a gate that
never renders a page. Ran the axe suite by hand afterwards because this change touched 1200 class
names and two token values, and it found **3 failures on `/ipam`, dark and demo** — 103 passed.

```
Element has insufficient color contrast of 4.03
(foreground #94a3b8, background #314158, 10px)
<span class="rounded bg-slate-200 px-1.5 py-0.5 text-[10px] font-medium
             text-fg-muted dark:bg-slate-700">read-only</span>
```

A `read-only` chip. Pass 2 converted its `text-slate-600 dark:text-slate-300` to
`--color-fg-muted`, which is safe against all four surfaces in both themes — and this chip does not
sit on a surface. It brings `dark:bg-slate-700`, where `fg-muted` lands at 4.04.

**That is this card's own documented boundary, walked into by this card's own sweep.** T-4215 wrote,
about the changeset badge chips: *"A guard that measures against surfaces still cannot see a call
site that brings its own background."* The sentence was correct and the sweep did not act on it.

Scanned for every instance rather than fixing the one axe happened to render — the fixture decides
what axe sees, so a chip on a route with no data is invisible to it. **Three sites, two files**, all
`text-fg-muted` on `bg-slate-700` at 4.04: `ipam/IpamPage.tsx` (x2) and `topology/findingBadges.ts`
— which is the file T-4212 already fixed once for this same defect class. All three take
`text-fg-body` (8.40 in both themes); a chip is text on a fill, not text on a page, and wants the
stronger foreground. Re-ran axe: 4 passed.

### `tokenOnOwnFill.test.ts`

Four occurrences of one defect is a pattern, not a coincidence:

| | what happened |
|---|---|
| T-4215 | changeset badge chips at 4.08 on their own `bg-slate-200/70` |
| T-4212 | a `/tools` chip compositing `bg-black/5` over a wash, 4.11 |
| T-4306 | the canvas badge — `--color-border`, a *hairline* token, used as a fill |
| T-4215 | this one: the sweep's own conversion, 4.04 |

Each was found by a different accident: a manual read, an axe run against whichever fixture
rendered the element, a failing sibling test. So the scan became a gate. It reads the source rather
than the DOM, which is the point — coverage no longer depends on what a test happened to mount —
and it carries a guard-the-guard, since a renamed token would otherwise turn it into a green light
for everything. Verified by restoring the regression: it fails with `4.04`, naming the file.

Composited fills (`bg-black/5` over a `-soft` wash) are not resolvable from source and stay axe's
job. Stating the boundary rather than implying the gate is complete — which is the mistake the
first row of that table made.
