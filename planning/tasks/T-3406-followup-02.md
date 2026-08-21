# T-3406-followup-02 · Stop the bare-slate contrast defect recurring

**kind:** accessibility debt + prevention · **status:** open, 2026-08-21 · **depends:**
`T-3406-followup-01` (DONE, 2026-08-19) · **context:** `planning/tasks/phase-34-followup.md`
("Follow-on work this card surfaced and did NOT do"), `web/src/topology/SwitchView.tsx:88` (the
measured precedent), `web/src/topology/mapContainerFloor.test.ts` and `web/src/index.css.test.ts`
(the source-scan guard idiom this card follows)

## Why this card exists

Three cards in a row have found the same defect class and fixed it locally:

| Found by | What it was | Fixed in |
|---|---|---|
| T-2004 | `text-slate-400` on `bg-white`, measured **2.63:1** | `SwitchView.tsx` |
| T-3406 | `bg-accent-600/10 text-accent-700` selection wash | Phase 34 close-out |
| T-3406-followup-01 | ~130 call sites across 19 routes, plus a `platformCommon.tsx` hint the sweep never rendered | `d56312ca`, `d8d2879b` |

The follow-up's own conclusion was that "a fourth discovery is not a strategy". This is the card
that stops the fourth discovery, and it is deliberately weighted toward **prevention** rather than
toward another sweep — because a sweep is exactly what has already been run three times.

**144 bare occurrences remain** across the `web/src` tree (`text-slate-400` or `text-slate-500`
with no `dark:` pairing on the same `className`). They were not missed by carelessness: the axe
sweep proves what it *renders*, and no spec reaches SDN's non-default tabs, the onboarding
walkthrough's transient states, or most of these call sites at all.

## The measurement, so the fix is not a matter of taste

Computed from Tailwind's slate palette against the two surfaces this app actually uses
(`bg-white`, and `dark:bg-slate-900` — 147 call sites vs 16 for `slate-950`):

| foreground | on white | on slate-900 |
|---|---|---|
| `slate-400` | **2.56** ✗ | 6.96 ✓ |
| `slate-500` | 4.76 ✓ | **3.75** ✗ |
| `slate-600` | 7.58 ✓ | 2.36 ✗ |

AA is 4.5:1 for normal text. So the two bare classes are defects **in opposite themes** —
`text-slate-400` fails light, `text-slate-500` fails dark — which is precisely why neither is
obvious to someone working in one theme, and why both have survived three sweeps.

One pairing fixes both: **`text-slate-600 dark:text-slate-400`** (7.58 / 6.96). That is not a new
convention; it is the one `SwitchView.tsx:88` already documents with its own measurement. It has
simply never been enforced.

## What to do

1. **Add a source-scan guard**, in the idiom this repo already uses twice for CSS facts vitest
   cannot resolve (`index.css.test.ts`, `mapContainerFloor.test.ts`): scan `web/src/**/*.tsx` for
   a `className` carrying `text-slate-400`/`text-slate-500` with no `dark:text-` beside it, and
   fail with the ratio table above in the message. Prefer this to an ESLint rule: there is no
   custom-rule infrastructure in `web/eslint.config.js` today, adding a plugin is a dependency
   decision, and a vitest failure can carry the *reason* — a lint code cannot.
2. **The guard needs an exception hook for always-dark surfaces — which turns out to be empty.**
   The first draft of this card named five such files, taken from a grep that matched
   `dark:bg-slate-900` by substring. That was wrong, and the corrected scan (whole tokens, no
   variant prefix) finds only **three**: `demo/DemoBanner.tsx`, `incidents/IncidentsPage.tsx` and
   `topology/SwitchFaceplate.tsx`'s name plate. **None of the three contains a bare slate text
   step**, so the exception list ships empty.

   The hook stays anyway, because the situation is one always-dark panel away from changing and a
   guard whose only escape hatch is "edit the detector" gets its detector edited. But the useful
   finding is the opposite of what was assumed: there was never a conflict to navigate, and the
   caution about a blanket rewrite — real for the *wash* class below — did not apply here.
3. **Fix the named debt the previous card listed explicitly**: `sdn/DhcpView.tsx:133`,
   `sdn/EvpnView.tsx:209,221` (three sites; no spec reaches either tab), and
   `onboarding/OnboardingWalkthrough.tsx` (nine sites, most in transient "checking…"/"none found"
   states a passing walkthrough never shows).
4. **Extend the axe sweep's reach**, since the recurring lesson is that it only proves what it
   renders: SDN's non-default tabs are the concrete gap, and the previous card also asked to
   **vary the sweep's session capability** so enabled-state controls stop hiding behind
   `disabled`.

## Acceptance criteria

1. The source-scan guard exists, is part of `make check`, and **fails** when a bare
   `text-slate-400`/`text-slate-500` is reintroduced outside the exception list. Demonstrate the
   failure, not just the pass — a guard only ever seen green is a guard nobody has tested.
2. Every occurrence outside the exception list is fixed; the exception list is short enough to
   read and each entry says why.
3. `make check` green; the full e2e suite passes in light, dark and demo-amber.

## The opacity-wash class recurred a fourth time, during this card

This section was originally written as "explicitly not in scope". That was wrong within the hour:
the full e2e run this card's audit triggered found **12 axe violations, every one of them at exactly
3.13:1**, from a single element —

```
bg-amber-200/70 text-amber-800 dark:bg-amber-900/60 dark:text-amber-200
```

— the drift badge, rendered on `SwitchFaceplate.tsx`'s **dark name plate** (`bg-slate-800`, added by
T-3503). Solving for what the 70% wash was letting through confirms it:
`(#baad70 − 0.7·amber-200) / 0.3 ≈ #1b283f`, which is the plate.

So the badge is not miscoloured. It is a badge designed for a light card, placed on a dark surface,
where a 70%-alpha background stops being a colour and becomes a blend with whatever is behind it.
Opaque, the same pair measures **5.70:1**.

Fixed here for the topology badges (`SwitchFaceplate.tsx`, `EntityNode.tsx`) by dropping the alpha
on the badge backgrounds — `bg-amber-200/70` → `bg-amber-200`, and the same for the blue, sky and
slate variants beside it.

**This makes the second guard load-bearing rather than speculative.** The prediction in the previous
card ("a fourth discovery is not a strategy") was correct, and the fourth discovery arrived before
this card was finished. What makes the class hard is that the defect is not visible in the source:
`bg-amber-200/70 text-amber-800` is a perfectly good pair, and only becomes a defect when something
dark is placed behind it — which is a fact about the *tree*, not the class list. A source scan
therefore cannot decide it, and the honest guard is the axe sweep itself, run over surfaces where
the badge actually renders.

**Revised scope for the second guard:** not a class-list rule, but a sweep-coverage rule — assert
that every surface which composites a translucent badge is *reached* by `a11y.spec.ts`. That is a
different and more useful card than the one deferred above, and it is what the evidence now
supports.

### The badge mapping is duplicated five ways, and that is why the first fix missed

The severity→classes mapping lives canonically in `web/src/topology/findingBadges.ts`
(`findingBadgeClass`), and is *also* written out inline in `SwitchFaceplate.tsx` (three places) and
`EntityNode.tsx` (two). The first attempt at this fix swept `web/src/topology/*.tsx` and therefore
repaired the five copies while leaving the original untouched — the axe sweep re-ran and reported
the identical `3.13:1` against the identical `#973c00`/`#baad70`, from the one file the sweep of
`.tsx` could not reach.

Two things worth carrying forward:

- **A `.tsx`-only sweep is not a sweep.** Class strings live in `.ts` helpers too, and the canonical
  copy is exactly the one most likely to be in a helper.
- **Consolidate the duplication.** Five copies of one mapping is five chances for the next fix to be
  partial, and the failure mode is silent: four of five copies repaired still renders the defect,
  because the components pick whichever copy they were written against. `findingBadgeClass` should
  be the only definition, with the call sites using it.
