# T-3713 · `simulator.spec.ts` "Trace path from the map" is ~33% flaky on element stability

**Found by:** the e2e run gating T-3709's Sigstore commit, 2026-08-25. · **size:** S ·
**depends:** — · **affects:** every e2e run; it is currently the only thing standing between a green
tree and a red one

## The observation

`e2e/simulator.spec.ts:181` — `T-504 AC5: Trace path from the map pre-fills and runs
(guest->guest, guest->external, IP->guest)` — failed the full sharded suite (229 passed, 1 failed,
`e2e gate: FAIL`).

**It is not caused by the change it failed under, and that was established by experiment rather than
assumed.** Re-run in isolation on the candidate tree, single worker, three repetitions: **1 of 3
failed.** Re-run identically on `62e63841`, the commit *before* any of that session's work:
**1 of 3 failed**. Same rate, same failure family.

```
candidate tree   14 passed, 1 failed   (repeat-each=3, workers=1)
  Error: locator.click: Timeout 5000ms exceeded.
    - waiting for getByRole('button', { name: 'vm-b/net0' })
      - waiting for element to be visible, enabled and stable

62e63841 (control)  2 passed, 1 failed  (--grep 'Trace path', repeat-each=3, workers=1)
  Error: locator.click: Timeout 5000ms exceeded.
    - waiting for getByRole('menuitem', { name: 'Trace path to external' })
      2 x waiting for element to be visible, enabled and stable
        - element is visible, enabled and stable
```

Both are the same defect: a 5-second click timeout that expires while Playwright waits for an
element to stop moving. Note the control's last line — *"element is visible, enabled and stable"* —
it settled, just not inside the budget.

## The cause

`waitForLayout` (simulator.spec.ts:71) is a weak settling condition:

```js
return nodes.length >= 4 && transforms.size > 1;
```

That returns true as soon as the react-flow layout has **started** to diverge — at least four nodes
exist and at least two of them differ. It says nothing about layout having **finished**. The test
then clicks nodes carrying `transition-opacity`, so a click can land mid-animation and burn the
whole 5s budget waiting for stability.

Durations corroborate it: sibling tests in the same file run in 5–7s; this one took 33.7s, 1.2m and
1.2m across three runs. It is not a little over budget, it is riding the edge.

## Deliverables

- Replace the "layout has started" check with one that means "layout has settled" — e.g. transforms
  unchanged across consecutive animation frames, or react-flow's own settled/`onLayout` signal if it
  exposes one. Do not simply raise the timeout: that hides the race instead of removing it, and this
  test's own history shows the budget has already been outgrown once.
- Check `waitForLayout`'s other callers for the same weakness — the helper is shared.
- Verify with `--repeat-each=10 --workers=1` and no failures, on this spec **and** the other specs
  that call the helper.

## Acceptance criteria

1. `npx playwright test e2e/simulator.spec.ts --repeat-each=10 --workers=1` passes ten times out of
   ten, twice in a row.
2. The full sharded suite passes with `e2e gate: PASS`.
3. `web/e2e/quarantine.json` is empty again — its own comment says empty is the healthy state.

## Quarantine

Quarantined on 2026-08-25 under this ticket, expiring **2026-09-22** (28 days, inside the file's
42-day cap). The entry exists so a known, characterised, pre-existing flake does not block unrelated
work; it is not permission to leave the test broken. On 2026-09-23 the build fails whether or not
the test does, which is the mechanism working as intended.
