# T-4214 — The accent ramp has no semantic aliases, so call sites hand-pick a step per theme

**Phase:** 42 (Design language)
**Status:** open
**Depends on:** T-4201 (accent ramp), T-4204 (status scale), T-4208 (component library)

## What was found

T-4201 gave the accent an eleven-step ramp (`--color-accent-50` … `--color-accent-950`)
and T-4204 gave the *status* colours semantic names (`--color-status-critical`,
`-solid`, `-soft`, and later `--color-status-on-solid`). The status tokens re-point
under `html.dark`, so a status call site writes one unprefixed utility and is correct
in both themes.

The accent got the ramp but never got the aliases. A ramp step is a *value*, not a
*role*, so it cannot re-point — accent-600 means the same colour in both themes, and
that colour is only legible on one of them. Every accent call site therefore has to
name two steps and a `dark:` conditional, which is precisely the conditional the token
system exists to delete.

Measured on the tree at the time of writing: **21 adjacent `accent-N dark:accent-M`
pairs across 19 files.** They collapse into three roles:

| Role | Light | Dark | Occurrences |
|---|---|---|---|
| accent foreground on a page surface | `text-accent-600` / `-700` / `-800` | `text-accent-300` / `-400` | 15 |
| accent soft wash | `bg-accent-50` / `-100` / `bg-accent-600/10` | `bg-accent-900` / `-950` / `bg-accent-500/15` | 6 |
| accent solid fill | (already handled inside `Button.tsx`) | | — |

One recipe is duplicated **verbatim in nine files**:

```
"bg-accent-600/10 text-accent-700 dark:bg-accent-500/15 dark:text-accent-300"
```

That is a selected-tab/selected-chip treatment, copy-pasted. `SegmentedControl` and
`Chip` (T-4208) now render exactly this, so most of the nine are also duplicating a
component that already exists.

`EmptyIllustration.tsx` is the sharpest illustration of the gap: its own `BadgeSpec`
comment reads *"Bare (no `dark:`) — status tokens and the accent ramp are both
pre-resolved for both themes"*, and the very next accent value is
`text-accent-600 dark:text-accent-400`. The comment describes the system as intended;
the value describes the system as built. Nothing caught it because no gate asserts
the claim.

## Why this matters beyond tidiness

The `--color-status-on-solid` finding (T-4208) was the same shape: a role that needs
opposite values per theme, spelled out at the call site, correct only for as long as
everyone remembers the inversion. It was found by building a component that had to
render text on a fill. This one was found by reading a comment that contradicted the
line below it. Both are cases of the design system being *documented* as
role-based while parts of it stayed *value*-based.

## Derivation (done up front, so the implementer does not re-solve it)

### This is a maintainability fix, not an accessibility fix

Measured before proposing anything — worst-case surface in each theme:

| In use today | Worst surface | |
|---|---|---|
| `text-accent-600` `#027a9a` (light) | 4.56 on `surface-sunken` | PASS |
| `text-accent-700` `#01617b` (light) | 6.47 on `surface-sunken` | PASS |
| `dark:text-accent-300` `#6ed4f9` | 8.41 on `surface-overlay` | PASS |
| `dark:text-accent-400` `#0bbbe9` | 6.28 on `surface-overlay` | PASS |

Nothing here is broken. That changes the shape of the fix: **invent no colours.**
Set each role to the step already used at the *majority* of sites, so the sweep moves
no pixels there and the T-4210 screenshot baselines mostly survive. Same principle as
T-4201's ramp solve, where the ramp was fitted to the call sites rather than the call
sites to the ramp.

`accent-fg` = light `700`, dark `300` → **12 of the 15 foreground sites do not change
colour at all**; only the two `600` and two `800` sites shift one step.

### All four gates pass on those values

| | accent-fg | worst surface | fg on soft | wash direction |
|---|---|---|---|---|
| base / light | `#01617b` | 6.47 | 5.91 | darker than page ✓ |
| base / dark | `#6ed4f9` | 8.41 | 8.64 | lighter than page ✓ |
| demo / light | `#973c00` | 6.54 | 5.99 | darker than page ✓ |
| demo / dark | `#ffd230` | 9.80 | 9.59 | lighter than page ✓ |

T-3406's finding re-measured on today's surfaces: demo `amber-600` on its own wash is
**4.25** (that phase recorded 4.31 against the pre-T-4203 surfaces), still a fail;
`amber-700`/`800` — the same hex `#973c00` after that phase's remap — measure 5.99.
So the demo light foreground must stay at 700-or-darker, and the four-way table above
is not symmetric by accident.

### `--color-accent-soft` must carry alpha, not be a flat hex

The obvious move is to resolve `bg-accent-600/10` to a flat hex and store that. It is
wrong. A flat hex is only equal to the modifier where the wash sits on `surface-page`;
elsewhere it diverges, measured as RGB distance from the flat value:

| | sunken | page | raised | overlay |
|---|---|---|---|---|
| base / light | 7.5 | 0 | 6.7 | — |
| base / dark | 14.3 | 0 | 13.9 | **28.3** |
| demo / dark | 15.6 | 0 | 12.8 | **27.7** |

28 units on the dark overlay is visible — a wash on a popover would not match the same
wash on the page. So define the token *with* its alpha:

```css
--color-accent-soft: color-mix(in srgb, var(--color-accent-600) 10%, transparent);
```

re-pointed under `html.dark` to 15% of `accent-500`. `bg-accent-soft` then composites
over whatever is actually behind it, exactly as the modifier does, and stays
surface-correct. The contrast gate must therefore composite the token over **each**
surface before measuring, not measure the token in isolation.

### The cascade trap: `html.demo.dark` does not exist

`html.dark` and `html.demo` have equal specificity (0,0,1,1) and `html.demo` comes
second in the file, so **demo wins on source order for any property both define.**
Today that is harmless: `html.demo` redefines only the accent ramp, and `html.dark`
redefines only surfaces and status — the two blocks are disjoint.

Adding accent *role* tokens breaks that. A role token needs a different value in demo
than in base **and** a different value in dark than in light, so it lands in both
blocks, and a page that is demo *and* dark would get demo's **light** value. Four
combinations need four blocks:

| | selector |
|---|---|
| base / light | `@theme` |
| base / dark | `html.dark` |
| demo / light | `html.demo` |
| demo / dark | `html.demo.dark` — **must be added** |

This is worth its own gate. A test that only checks `html.dark`'s key set against
`@theme`'s — the shape the status tokens already have — would pass while demo+dark
rendered the wrong colour, because the missing block is not either of the two it
compares.

## Acceptance criteria

1. `web/src/index.css` defines `--color-accent-fg` and `--color-accent-soft` in
   `@theme`, and re-points them in `html.dark`, `html.demo` **and a new
   `html.demo.dark`**, using the values in the derivation table above. `accent-soft`
   carries its own alpha via `color-mix`; it is not a flat hex.
   Add `--color-accent-border` only if the sweep turns up a third genuinely distinct
   role — the survey found two, so do not invent a third pre-emptively.
2. `web/src/index.css.test.ts` asserts, across **all four** theme combinations:
   - `accent-fg` clears AA (4.5:1) on `surface-sunken`, `surface-page`,
     `surface-raised` and `surface-overlay`
   - `accent-fg` clears AA on `accent-soft` **composited over each of those surfaces**,
     not on the token in isolation
   - the wash moves toward the accent: darker than `surface-page` in light, lighter in
     dark. A ratio alone does not catch a wash going the wrong way — the first pass at
     this derivation produced a near-black "wash" for dark mode that satisfied its
     ratio constraint perfectly.
   - the accent-role key set is identical in all four blocks, not just in `html.dark`
     vs `@theme` — see the cascade trap above for why the two-way check would pass
     while demo+dark was wrong
3. All 21 pairs are converted. The nine `bg-accent-600/10 text-accent-700 …` sites use
   `SegmentedControl` or `Chip` where the surrounding markup allows it, and the token
   pair only where it does not.
4. `EmptyIllustration.tsx`'s `unconfigured` badge uses `text-accent-fg`, and its
   comment becomes true.
5. A gate that fails on reintroduction: extend the existing lint/test guard so a
   `dark:*-accent-*` utility anywhere in `web/src` fails, with an allowlist entry
   required to add one back. Without this the sweep decays, exactly as the status
   sweep decayed into the 114 files T-4204 is still working through.

## Notes

- Do **not** widen this into a general `dark:` purge. The neutral pairing
  `text-slate-600 dark:text-slate-400` is deliberate and guarded by
  `slateContrast.test.ts`; it stays.
- The `-solid` accent fill is already correct inside `Button.tsx` and must not move —
  T-905 → T-3401 → T-3405 → T-4201 is four rounds of churn on that one file and the
  comment there asks for no fifth.
