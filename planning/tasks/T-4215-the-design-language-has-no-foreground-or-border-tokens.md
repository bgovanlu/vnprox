# T-4215 — The design language has no foreground or border tokens, which is why ~1000 `dark:` pairs survive

**Phase:** 42 (Design language)
**Status:** open
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

## Note

This card is the reason to be sceptical of the phrase "the remainder is mechanical" in T-4204's
commit message — which I wrote. The remainder was not mechanical; it was blocked, and counting
only the patterns that *had* a token made the blockage invisible for the whole phase.
