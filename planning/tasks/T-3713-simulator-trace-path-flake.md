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

## ⚠️ Disproven 2026-08-27 — the cause above is NOT the cause

**The settle-condition hypothesis was implemented, measured, and refuted.** `waitForLayout` was
replaced with `waitForLayoutSettled` (in `web/e2e/helpers.ts`, applied across all nine calling
specs): it polls `.react-flow__node` transforms once per animation frame and returns only after
they are byte-identical across N consecutive frames — a genuine "layout has settled" condition.

**It did not help.** Full `--repeat-each=10 --workers=1` run of `simulator.spec.ts`, the
trace-path test's ten repeats in order:

```
✓ 18.9s   ✘ 1.3m   ✓ 1.2m   ✘ 1.2m   ✘ 950ms
✓ 24.7s   ✓ 40.6s  ✓ 28.4s  ✘ 1.2m   ✓ 45.7s
        → 6 passed / 4 failed   (40%, vs the 33% documented pre-fix)
        → whole file: 46 passed / 4 failed in 13.5m; every failure is this one test
```

Run conditions: 1-min load average 16.95 falling to ~14 on a 32-core host shared with an unrelated
`turbo test:ci`. Contention inflates durations and is a fair caveat on the absolute numbers — it is
**not** an explanation for the fix failing to move the rate, and every passing repeat still took
18.9–45.7s against 5–7s for sibling tests in the same file.

### What the failure actually is

The failing call is **not** the initial layout wait. It is `traceFromContextMenu`
(`simulator.spec.ts:122-124`), whose right-click → context-menu → menu-item sequence is already
wrapped in `.toPass({ timeout: 60_000 * slowFactor })`. The whole 60s retry budget is exhausted.
Playwright's own call log, from a failing repeat, is decisive:

```
- waiting for element to be visible, enabled and stable
- element is visible, enabled and stable          ← stability IS satisfied
- scrolling into view if needed / done scrolling
- <div class="flex items-center justify-between gap-2">…</div> from
  <div … class="react-flow__node react-flow__node-entity nopan selectable draggable">…</div>
  subtree intercepts pointer events               ← occlusion, not motion
- retrying click action
- <div class="react-flow__pane draggable">…</div> intercepts pointer events
- retrying click action
- element is outside of the viewport              ← viewport, not motion
```

Three distinct blockers, none of them "the element is still moving":

1. **The node's own subtree intercepts its pointer events** — an inner `div` sits over the
   accessible-name-bearing element Playwright targets, so the click lands on a child.
2. **`.react-flow__pane` intercepts pointer events** — the canvas pane overlays the node.
3. **The element is outside of the viewport** — layout placed it off the visible canvas and
   `scrolling into view` did not bring it back, which a `fitView` before interaction would.

This also explains the **950ms failure**, which was never consistent with a stability timeout: a
sub-second failure is the viewport/occlusion branch failing fast, not a budget being exhausted.

### Ranked surviving hypotheses, for whoever takes this next

1. **Hit-target occlusion (most likely).** The right-click target should be the node's own
   pointer-event-receiving element, not an ancestor whose child intercepts. Fix candidate: target a
   stable inner selector (or `data-testid`) rather than `getByRole("button", { name })`, or make
   the node's interactive surface the element that carries the accessible name.
2. **Viewport.** Call react-flow's `fitView` (or assert the node's bounding box is inside the
   canvas) before interacting. Note `scrolling into view if needed` cannot fix an off-canvas
   react-flow node, because the node moves in canvas transform space, not document scroll space.
3. **Pane z-order/pointer-events.** `.react-flow__pane` intercepting suggests a stacking or
   `pointer-events` rule that intermittently puts the pane above nodes.

**Do NOT raise the timeout, and do not re-attempt a settling fix.** Both are now empirically
excluded: the budget is already 60s with a `slowFactor`, and stability is explicitly reported as
satisfied at the moment of failure.

### Disposition

- `waitForLayoutSettled` is **kept on its own merits** — waiting for settled layout is strictly
  more correct than waiting for layout to *start*, and the nine specs are better for it. It is
  explicitly **not** the fix for this card. (Same disposition T-2505-followup-01 gave its
  rAF-throttling change after that change was disproven as the fix for the `scale.spec.ts` hang.)
- **The quarantine entry stays**, unchanged, expiring 2026-09-22.
- Traces were captured and are the fastest way in for the next attempt:
  `npx playwright show-trace web/test-results/whole-suite/simulator-T-504-AC5-Trace--69bfd--guest--external-IP--guest--repeat8/trace.zip`

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
