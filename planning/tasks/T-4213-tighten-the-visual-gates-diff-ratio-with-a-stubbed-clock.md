# T-4213 · The visual gate's 2% diff ratio is loose enough to hide a real regression

**Found by:** reviewing T-4210, 2026-08-28 · **size:** S · **depends:** T-4210 (landed) ·
**affects:** `web/e2e/visual.spec.ts`, `web/playwright.visual.config.ts`

## The observation

T-4210 set `maxDiffPixelRatio: 0.02` and documented two measured causes of flakiness at `0`:

1. sub-pixel font antialiasing, and
2. **a relative-timestamp label ("3s ago") ticking over between the baseline run and the
   verification run.**

Cause 2 was recorded as not neutralizable "from outside the app without a source change this
task's scope excludes". **That is not correct as of the Playwright version this repo pins.**
`@playwright/test` is 1.61.1 and `page.clock.install()` has existed since 1.45; the clock is
installed for the whole browser context and can be set before the first navigation. Freezing time
is available today, needs no application change, and is the standard fix for exactly this.

Meanwhile the cost of the loose ratio is real. The suite captures `fullPage` at 1400x900, so 2% of
pixels is on the order of **25,000 pixels** — comfortably more than a status badge that changed
colour, a row that shifted, or a chip that lost its wash. A gate that tolerates that much has a
blind spot the size of the things Phases 42-51 actually change, which defeats the reason T-4210
was scheduled first.

This is not a criticism of the number as a *starting* value: 0.02 with a written justification is
much better than 0 with a flaky suite everyone learns to re-run. It is the follow-up that
justification implies.

## Deliverables

- Install a fixed clock per test (`page.clock.install({ time: ... })`) before the first
  `page.goto`, so every relative timestamp renders identically on the baseline run and every
  verification run. Watch for the app's own polling: freezing time can stall `setInterval`-driven
  refreshes, so use `clock.pauseAt`/`fastForward` if any page needs its first data tick to land.
- With the clock stubbed, re-measure what antialiasing alone actually costs. Set the ratio from
  that measurement and record the number, the same way T-4210 recorded this one — do not simply
  pick a smaller value.
- Prove the new floor the way T-4210 proved the old one: generate baselines, run twice, both clean.
  Then deliberately introduce a one-badge colour change and confirm the gate **fails** on it. A
  tightened threshold nobody has shown to catch a real regression is not evidence of anything.

## Acceptance criteria

1. No test in `visual.spec.ts` depends on wall-clock time.
2. `maxDiffPixelRatio` is lower than 0.02, with its basis recorded in the config beside it.
3. Two consecutive verification runs pass clean, and an injected single-badge colour change fails
   the gate — both demonstrated, not asserted.
4. `docs/development.md`'s determinism paragraph is updated; it currently states the timestamp
   cause is not neutralizable without a source change.
