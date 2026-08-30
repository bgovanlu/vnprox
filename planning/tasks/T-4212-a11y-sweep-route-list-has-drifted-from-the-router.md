# T-4212 · The accessibility sweep's route list has drifted; two shipped pages have never been checked

**Found by:** T-4210, while deriving the visual gate's route inventory, 2026-08-28 · **size:** S ·
**depends:** — · **affects:** `web/e2e/a11y.spec.ts`, and the two routes below in every theme

## The observation

`a11y.spec.ts` sweeps every routed page with axe. Its route list, `SWEEP_ROUTES`, is a
hand-maintained `const` array. Diffing it against the `<Route path=...>` declarations in
`web/src/App.tsx`:

| declared in App.tsx | in SWEEP_ROUTES | verdict |
|---|---|---|
| `/guest` (GuestEgoPage, T-3906) | **no** | real AppShell page, never checked |
| `/wireguard` (WireGuardPage, T-4015) | **no** | real AppShell page, never checked |
| `/embed/dashboard`, `/embed/map`, `/embed/posture` | no | correctly out of scope — token-authenticated, chrome-less, outside `RequireAuth`/`AppShell` entirely |

So **two shipped pages have never had an accessibility check run against them**, in any theme, and
nothing failed to say so. Both were added after the sweep list was written, which is the entire
failure mode: the list does not know when the router grows.

## Why it matters beyond these two routes

The gate reports green over a list, not over the app. Every future page inherits the same silence
by default — the next route added is also unchecked, and also silently. The two found here are
just the two that happen to exist today.

This is the same class of defect as T-3717 (the OpenAPI gate was green because the routes it
missed were never mounted): **a coverage gate whose inventory is hand-maintained measures the
inventory, not the thing.**

## The fix already exists in the tree

Two inventories in this repo are already derived from the shipped source rather than hand-kept,
and both say so in their own header comments:

- `web/src/help/coverage.test.ts` — "the screen inventory... derived from the shipped source,
  never hand-maintained", using a `<Route[^>]*?\spath="([^"]+)"` scan of `App.tsx`.
- `web/e2e/routeInventory.ts` (new, T-4210) — the same technique, with explicit, *reasoned*
  exclusions (the `:param` route, the `/embed/*` routes, `/login`, the `*` catch-all) rather than
  an unexplained omission.

`routeInventory.ts` exports `routedPagePaths()`. Repointing `a11y.spec.ts` at it removes the
second hand-list entirely.

## Deliverables

- Repoint `a11y.spec.ts` at `routeInventory.ts` instead of its private `SWEEP_ROUTES` const.
- Run the sweep against `/guest` and `/wireguard` and **fix whatever it finds**. Do not add the
  routes and suppress the failures — the point of the card is the two pages, not the list.
- Where the two suites genuinely need different exclusions, express that as an explicit filter over
  the shared inventory with a stated reason per exclusion, never as a divergent copy.

## Acceptance criteria

1. `a11y.spec.ts` contains no hand-maintained array of route paths.
2. Adding a `<Route>` to `App.tsx` without any other change causes the a11y sweep to visit it —
   demonstrate this, do not assert it.
3. `/guest` and `/wireguard` pass the axe sweep in light, dark and demo modes, with any violations
   found recorded in the commit message rather than quietly fixed.

---

## Outcome

**Done.** `a11y.spec.ts` no longer holds a route list. It reads `routedPagePaths()` from
`routeInventory.ts` — the same derived inventory the visual gate uses — and the per-route heading
regex went with it, deliberately: a `path -> expected-heading` map is still a hand-maintained array
of route paths under a different name, which is what AC1 forbids. What it bought ("did we land on
the page we asked for?") is bought instead by asserting the URL, which needs no per-route knowledge
and cannot go stale when a page is retitled. `pathEndRegExp` moved into `routeInventory.ts` so both
gates share one definition, and `forceTheme`/`forceDemoAccent` moved into `e2e/helpers.ts` for the
same reason — two copies of "how do you put the app in demo mode" would be this card's own mistake
in a different register.

**AC2, demonstrated rather than asserted.** Adding one throwaway `<Route path="/zz-t4212-proof">`
to `App.tsx`, with no other change, took the axe sweep from 105 tests to 108 — and the visual gate
picked it up in the same breath, three captures, unprompted. Removing it returned both. Two gates,
one inventory, no hand-list left to drift.

**Demo mode joins light and dark** (AC3). The sweep had never run it. That is not a cosmetic third
pass: `html.demo` re-points every `--color-accent-*` step to a different hue family, and axe's
colour-contrast rule is the only automated check in this repo that would notice an accent that
stops clearing its floor there.

`/guest` and `/wireguard` — the two pages this card exists for — **pass in all three modes, with no
violations to record.** A null result, and worth stating as one.

## What the sweep found once it ran, and why nothing had

Two failures, both **pre-existing**: they reproduce identically at the previous commit. Confirmed by
stashing this card's changes and re-running, rather than assumed.

They had been invisible because **`make ci` — the pre-push gate — deliberately omits the e2e job.**
The Makefile says so in as many words ("this target OMITS the e2e and packaging-matrix jobs"); the
full matrix is `scripts/ci-local.sh`. So "make ci green" has never meant "the axe sweep is green",
and nothing on the push path would have said otherwise.

### 1. A chip manufacturing its own surface (fixed)

axe, on `/tools`, all three modes:

> insufficient color contrast of **4.11** (foreground `#9b6200`, background `#ebe7df`, 10px)

`#ebe7df` is not a token. It is `--color-status-degraded-soft` with `bg-black/5` composited over
it — an unnamed sixth surface, created at the call site, under text coloured by a token solved
against the fifth. **This is the phase's recurring defect for the fourth time** (T-4215, T-4305,
T-4301's remainder), and here the call site does not merely pick the wrong surface, it *invents*
one.

Measured across every status x tint, which is what axe cannot do — it reports only what the fixture
rendered, and the fixture rendered **one of five** failing combinations:

| light | soft | +black/5 | +black/10 | | dark | soft | +white/5 | +white/10 |
|---|---|---|---|---|---|---|---|---|
| ok | 5.02 | 4.51 | **4.00** | | ok | 7.33 | 6.31 | 5.40 |
| degraded | 4.58 | **4.11** | **3.67** | | degraded | 7.05 | 6.02 | 5.15 |
| critical | 5.70 | 5.11 | 4.55 | | critical | 4.86 | **4.20** | **3.58** |
| info | 5.15 | 4.62 | **4.13** | | info | 7.19 | 6.21 | 5.33 |

**Every status text clears AA on its own `-soft` wash. Nothing fails until a tint is added.** So the
repair is not a new colour — it is to stop manufacturing surfaces. The chips take `ring-1
ring-current/30`, which marks them without touching what the text sits on, and every combination
reverts to the passing column. `findingChipContrast.test.ts` carries the table, a guard-the-guard
asserting the tinted variants still fail, and a source scan forbidding `bg-black/N`/`bg-white/N` in
that file — verified to fail by reintroducing the pattern.

### 2. A hover state, caught because the cursor was parked on it (not a defect)

The v2-canvas test reported 58 colour-contrast nodes, every one the same white-on-`#039fc7` pair.
`#039fc7` is `--color-accent-500`, which is `Button`'s **hover** state; its resting `accent-600`
measures 4.94. That test is the only one in the file that clicks a button and then scans, and
Playwright leaves the cursor where it clicked.

`Button.tsx` has re-derived this exact pairing four times (T-905, T-3401, T-3405, T-4201) and states
plainly that "a hover state is transient and not the gating resting contrast". Per CLAUDE.md that is
a decision, not a defect, and not mine to re-litigate. The test now parks the pointer before
scanning, which makes it measure the same resting state every other test in the sweep measures —
the same reasoning `waitForLoadingPlaceholderToClear` already carries. The comment says all of this
at the call site so it cannot be misread later as a quiet suppression.

### 3. Two tokens solved against the wrong surfaces — one of them mine (fixed)

axe, on `/topology` light only:

> insufficient color contrast of **4.44** (foreground `#617087`, background `#faeeef`, 12px)
> insufficient color contrast of **4.48** (foreground `#717579`, background `#fafbfd`, 10px)

`#617087` is **`--color-fg-subtle`, introduced by T-4215 in this same session** (`8635f2fc`), and
`#faeeef` is `--color-status-critical-soft`. T-4215 solved that token against the four *surface*
levels and wrote down, as its own headline finding, that "a guard that measures against a surface
is blind to a call site that brings its own". It then shipped a token blind to a fifth kind of
surface — the `-soft` washes, which are not in the ladder. **The rule was correct and I applied it
one level too shallow.** That is left in the stylesheet comment rather than tidied away.

`#717579` is `--color-status-stale` (T-4204), at **4.48 on `--color-surface-page`**. It had a gate.
The gate measured it against `WHITE = "#ffffff"`, where it scores 4.61 and passes — but the light
page is `#fafbfd`, not white, and has been since T-4203 built the ladder. Measured against the
surfaces it is actually drawn on, `stale` failed **16 of 18** combinations.

Neither had ever been caught, for the reason above: the axe sweep is the only gate that measures
rendered pixels against real backgrounds, and it does not run on push.

Both re-solved in OKLCH with hue and chroma held, the same technique T-4215 used:

| token | was | worst | now | worst |
|---|---|---|---|---|
| `fg-subtle` light | `#617087` | 4.44 | `#5f6e85` | **4.57** |
| `status-stale` light | `#717579` | 4.29 | `#6c7074` | **4.61** |
| `status-stale` dark | `#82878c` | 3.91 | `#8e9398` | **4.58** |

`stale`'s constraint set is the four surface levels, not the soft washes: it is text at exactly one
call site (`SwitchView`'s 10px chip) and a *border* everywhere else (`Badge`'s stale modifier),
where the floor is 3:1 — still cleared at 4.41 / 4.20.

**And the gate was repointed, which matters more than the two values.** `index.css.test.ts` no
longer holds `WHITE`/`SLATE_900` literals for the status scale; it reads the ladder from the
stylesheet and measures every status foreground against all four levels in both themes. Verified by
restoring the old `stale` value, which now fails with `light stale on #fafbfd: 4.484`. That is the
third green-gate-pointed-at-the-wrong-surface in this phase (T-4215's slate table, T-4305's outline
token), and the first where the wrong surface was one I had introduced myself.
