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

## Acceptance criteria

1. `web/src/index.css` defines accent role tokens in `@theme` and re-points them
   under `html.dark`:
   - `--color-accent-fg` — accent text/icon on `surface-page` and `surface-raised`
   - `--color-accent-soft` — the accent wash used behind selected/active affordances
   - `--color-accent-border` — accent hairline, if the sweep shows a third distinct role
   Derive them by *solving* against the contrast targets, as T-4201 did for the ramp
   itself; do not pick a step that looks close.
2. `web/src/index.css.test.ts` asserts, for **both** themes:
   - `accent-fg` clears AA (4.5:1) on `surface-page`, `surface-raised` and `surface-sunken`
   - `accent-fg` clears AA on `accent-soft`
   - `html.dark`'s accent-role key set equals `@theme`'s, the same equality the status
     tokens already get
   - the demo accent (`html.demo`) satisfies all of the above too — T-4211 is open on
     the demo hue and this must not regress it further
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
