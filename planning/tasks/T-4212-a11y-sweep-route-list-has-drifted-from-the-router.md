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
