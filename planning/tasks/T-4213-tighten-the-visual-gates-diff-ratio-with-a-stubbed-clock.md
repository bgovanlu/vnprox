# T-4213 · The visual gate's 2% diff ratio is loose enough to hide a real regression

**Status:** delivered, but NOT as written — the ratio was never the binding constraint. See below.
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

## Demonstrated, 2026-08-28 — it hid a real change and cost a wrong conclusion

This card argued the 2% tolerance was "wide enough to hide a real regression". That is no longer
an argument; here is the instance.

T-4302 changed the v1 node's background from `bg-emerald-50` (`#ecfdf5`) to `bg-surface-raised`
(`#ffffff`) across every node on the map, removed the per-kind wash entirely, strengthened the
border and added a pictogram. A `--update-snapshots` run reported **3 passed and did not rewrite a
single baseline** — the files kept their timestamps from the previous run.

`--update-snapshots` only writes when the comparison *fails*. `#ecfdf5` against `#ffffff` sits
under Playwright's per-pixel YIQ threshold, so those pixels never counted as differing, and
`maxDiffPixelRatio: 0.02` had room to absorb what remained. The comparator concluded the two
images were the same.

Consequences worth recording, because they are the actual cost:

- **I read the stale baseline and concluded the change had not shipped.** It had. I then spent six
  tool calls on build-staleness theories — checking the bundle, the embed directive, the harness's
  `go run` decision — before checking the baseline's mtime, which would have settled it in one.
- **A `--update-snapshots` run that writes nothing is indistinguishable from one that writes
  everything.** Both print `N passed`. That is a second, separable defect from the tolerance
  itself.
- The change was ultimately verified by sampling the PNG directly
  (`convert -format '%[pixel:p{760,600}]'` → `srgb(255,255,255)`, and `srgb(24,33,51)` in dark),
  which is the check that should have been reached for first.

## Additional deliverables from that instance

5. **Report what was written.** A snapshot-updating run must say which baselines it rewrote and
   which it left alone. Silence on a no-op write is how the stale file was trusted.
6. **Lower the per-pixel threshold as well as the ratio.** The ratio was not what hid this — the
   per-pixel comparison was. Tightening only `maxDiffPixelRatio` would not have caught it.
7. Until both land, **delete the baselines before a regeneration run** rather than relying on
   `--update-snapshots` to overwrite them. Note this in the suite's header comment; it is the
   workaround that actually produced a correct capture.

---

## Outcome — the card asked me to tighten the wrong knob

Everything below is measured. Where a number contradicts this card, the number wins.

### What was delivered

| | |
|---|---|
| `page.clock.setFixedTime` in every test, `/login` included | AC1 |
| `maxDiffPixelRatio` | 0.02 -> **0.0005** (40x tighter) |
| Two consecutive clean verification runs | 102 / 102, twice |
| `docs/development.md` determinism paragraph | rewritten |

`setFixedTime`, not `install`: `install` also pauses the timer queue, which stalls the app's own
polling and can leave a page waiting for a data tick that never lands. The card's warning about
needing `fastForward` applies to `install` and not to this.

### The card's first premise was wrong: the clock was not the problem

T-4210 blamed antialiasing plus a relative-timestamp label, and recorded the second as
un-neutralizable. The clock **was** freezable (`page.clock` since Playwright 1.45; this repo pins
1.61.1) — and freezing it barely moved the number, because the dominant cause was never a
client-derived label. It was **server-generated content that the act of testing creates**:

| route | volatile content | measured |
|---|---|---|
| `/audit` | log rows the suite's own logins wrote | **103,641 px — 8.2%** |
| `/flows` | flow record timestamps | 56,879 px — 4.5% |
| `/settings/certificates` | `notAfter` + day count; certs are minted at daemon boot | 3,746 px |

`/audit` alone moved **four times the tolerance the gate was already set to**, between two
consecutive runs. So the gate was not merely loose, it was **loose and non-deterministic**, and
nothing had caught that because the visual suite had never been run twice in a row — and `make ci`,
the pre-push gate, omits the e2e job entirely.

Those three are marked `data-volatile-time` in the app and masked in every capture. Masking rather
than excluding the routes: chrome, layout, spacing and colour stay gated; only the cells whose
content is a wall-clock reading are painted out. One selector, marked beside the volatile thing,
rather than a per-route mask table that would drift the way T-4212's route list did.

Two corrections to my own working, both from reading a single run as a result:

- I first masked `/audit`'s timestamp CELL. Measured: 8.2% -> 8.0%. The **rows** differ, not their
  times. The mask is the table body.
- I reported 246 px as "the antialiasing floor" and set 0.0005 from it. 246 px was a lucky
  `/settings/certificates` run where the generated dates happened to render similarly; the same
  page measured 3,746 px next time. The floor was one sample and the number derived from it was
  built on sand.

### The card's second premise was wrong: no ratio could ever have fixed this

**`playwright.visual.config.ts` had never set `threshold`,** so it ran on Playwright's default of
`0.2`. That is a per-PIXEL tolerance in YIQ space: a pixel whose colour moved less is not counted
as different, so `maxDiffPixelRatio` never sees it.

Changing `--color-status-degraded` from `#9b6200` to `#8a5a10` — a real, visible token regression,
exactly the class this gate exists for — moves YIQ luminance by **0.0312, six times below the
default**. The suite reported 102 passed. **No value of `maxDiffPixelRatio` would have changed
that.** This is the same trap that cost T-4302 six tool calls when `--update-snapshots` silently
rewrote nothing for an `#ecfdf5 -> #ffffff` change.

Lowering `threshold` was tried and measured, and the two settings trade against each other:

| | threshold 0.2 | threshold 0.02 |
|---|---|---|
| `/settings/certificates` noise | 246-3,746 px | 3,750 px |
| `/audit` (masked) | 0 px | 144,690 px |
| one small `Badge` | ~1,200 px | ~1,200 px |

At 0.02 the antialiasing floor on a text-dense page **exceeds a single badge**, so no ratio can both
suppress the noise and catch the badge.

### AC3's second half cannot be met, and that is the finding

> "deliberately introduce a one-badge colour change and confirm the gate **fails** on it"

**Not achievable with this technique at this viewport**, and the config now says so with the numbers
rather than implying otherwise. A `StatusDot` is 64 px; a soft `Badge` chip ~1,200 px; the tightest
ratio that survives two clean runs is 630 px — so a badge is right at the boundary and a dot is
permanently below it.

Three injection attempts, and the first two were **vacuous rather than uncaught**, which is worth
recording because each looked like a result:

1. `Badge`'s `ok` tone — `Badge` has one non-test call site (`NoticeStack`), and `ok` never renders
   there. The one failure that run was an unrelated URL race (below), which I nearly reported as
   the proof.
2. `Badge`'s `degraded` tone — same call site, and the notice stack **collapses** 3 notices into a
   summary row, so that `<Badge>` never mounts.
3. The `--color-status-degraded` token — genuinely rendered, and invisible for the `threshold`
   reason above.

**An injected-regression test proves nothing unless you check WHICH captures failed and why.**

### A latent flake in T-4212, found by this card

`/firewall/compiled` rewrites its own URL to `?node=pve1` after mount, and the `pathEndRegExp` I
added in T-4212 anchored on `$`. That made the assertion a race: it passed 102/102 twice and
106/106 on the axe sweep, then failed once. Now `(?:[?#].*)?$`. A gate that is wrong only sometimes
is the worst kind, and it was mine.

### What this gate is for, and what covers the rest

Layout, geometry, spacing, and colour changes above ~ΔY 0.2. Everything finer is covered by
assertions that measure values rather than photographs of them — `index.css.test.ts` (every status
and foreground role against every surface it lands on) and the axe sweep (rendered pixels against
real backgrounds). A follow-up card should decide whether a `threshold`/ratio pair is worth solving
per-route, or whether full-page screenshots are simply the wrong instrument for colour.
