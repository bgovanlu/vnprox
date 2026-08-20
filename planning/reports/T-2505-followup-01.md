# T-2505-followup-01 · `scale.spec.ts › v2 canvas` times out after its two file-mates

**kind:** defect · **found by:** `T-2505`'s bisection of a hosted-runner failure · **status:**
open, quarantined until **2026-09-15**

No card owns a fix for this yet. It is filed here because `CHANGELOG.md:217`,
`docs/project-status.md`, and `planning/tasks/phase-25.md` all cite `T-2505-followup-01` as a
document and none of them previously pointed at one — this file, and the inline record in
`planning/tasks/phase-25.md` (search that file for `T-2505-followup-01`) it is copied from, are
now the same fact in two places.

## The symptom

`scale-lab (v2 canvas renderer): pan/zoom frame timings at the documented scale target` — the
`scale.spec.ts` spec — times out at 120s, but **only** when both of its two file-mates in
`scale.spec.ts` have already run in the same Playwright worker. Run alone, or with either
file-mate alone, it passes comfortably (14–20s). Run after both, it never finishes.

## How it was found

`T-2505` sharded the e2e suite across four workers. The previously-serial suite had been hiding
timing-sensitive failures inside a single long-lived process; sharding, plus a separate
CPU-restricted reproduction requested by `T-2505-input-02`, surfaced two specs the hosted runner
had failed on commit `4968bf3`: `guest-interior.spec.ts:23` (root-caused separately, see
`T-2505-followup-02`) and this one.

## The evidence

Bisected 2026-08-12, reproduced **4/4** on an otherwise-idle machine:

| Arrangement | Result |
|---|---|
| Test 127 + 258 (two of the three specs in the file) | passes, 33s |
| Test 174 + 258 | passes, 58s |
| Test 127 + 174 + 258 (all three, in file order) | **fails**, times out at 120s |
| The failing test alone | passes, 14.2s |
| The failing test alone, `taskset -c 0,1` (2 cores) | passes, 19.5s |
| The failing test alone, unmodified `main`, pre-`T-2505` Playwright config | reproduces the same failure — **not** an artifact of the new sharded config |

Ruled out by the evidence, not by assumption:

- **Not CPU-bound.** The failing test alone, even pinned to 2 cores, passes well inside budget.
  Whatever is happening only happens when it runs third in the same process.
- **Not order-dependent application state carried in the store.** The scale stack's own SQLite
  store holds no layout row after the failing run — there is nothing left over from the earlier
  two tests for the third to trip on.
- **Not caused by sharding.** It passed inside the old 91-test serial suite. Sharding did not
  introduce this failure; it only stopped being the thing that hid it (running fewer tests per
  worker process changed which combinations execute back-to-back).

## Root cause

**Unexplained.** The mechanism was never identified. A follow-on attempt on 2026-08-13 tried the
next planned experiment — restart the browser between the three tests (three separate Playwright
invocations against one long-lived daemon) to see whether the failure follows the browser process
or the daemon process — and it did not produce a usable answer: the hand-started daemon in that
setup came up healthy on `/health` but its SQLite store had never been migrated
(`SQL logic error: no such table: sessions`), so all three invocations failed for a reason
unrelated to the thing being tested. The experiment's setup, not its result, was the finding: it
established that a daemon started by hand (rather than through `scripts/e2e-shards.sh`'s wrapper)
is not provably the same daemon the real harness uses, and chasing the browser-vs-daemon question
further with an unequivalent harness would not have produced a trustworthy answer. That attempt
was stopped deliberately rather than completed on a broken premise.

**What a later attempt should carry forward** (from that 2026-08-13 note):

1. Find how the store is actually created and migrated for a shard stack — read
   `scripts/e2e-shards.sh` or whatever wraps `vnproxd` for the suite — before hand-starting a
   daemon and assuming it is equivalent.
2. Run on a genuinely quiet machine. The 2026-08-13 attempt ran alongside four other active
   agents; `T-2505-input-03` is the standing reminder that "idle" on the dev host has to be
   checked, not assumed.

## Current status

**Quarantined**, not fixed. `web/e2e/quarantine.json` carries the one entry:

```json
{
  "file": "scale.spec.ts",
  "title": "scale-lab (v2 canvas renderer): pan/zoom frame timings at the documented scale target",
  "reason": "Times out at 120s if and only if BOTH other tests in its own file ran first. Bisected 2026-08-12 and reproduced 4/4 on an idle machine, INCLUDING on unmodified main with the pre-T-2505 Playwright config: 127+258 passes (33s), 174+258 passes (58s), 127+174+258 fails. Not order-dependent app state — the scale stack's own SQLite store holds no layout row after the failing run. Not CPU: the same test alone under taskset -c 0,1 passes in 19.5s. It passed in the 91-test serial suite, so the sharded arrangement did not cause it, only stopped hiding it.",
  "ticket": "T-2505-followup-01",
  "expires": "2026-09-15"
}
```

`internal/e2egate` treats a quarantine entry as tolerance, not exemption: a build with this entry
still fails if the shard reports are missing, if the entry names a test that actually passed
(an expiry that only bites on failure isn't a deadline), or if the entry is malformed (reason
under 20 characters, no ticket, unparseable or more than 42 days out). `TestRepoQuarantineIsValid`
re-checks this exact file against the real clock on every `make check`.

## What closes it

**The quarantine entry expires 2026-09-15. This is a hard deadline, not a suggestion.** Per
`internal/e2egate`'s own behaviour, an expired entry fails the build whether or not the
underlying test is still red — a build that goes quiet on 2026-09-16 because nobody re-triaged
this is exactly the failure mode the expiry mechanism exists to force. `docs/roadmap-earned.md`
absorbs this into `T-3204` ("Test-debt closure") and calls the deadline out explicitly, phase
order notwithstanding.

Closing this card for real requires, in order:

1. A written root cause naming the actual mechanism — not a bisection, a mechanism — with
   evidence that distinguishes it from CPU load, store state, and sharding, all three of which
   are already refuted above.
2. A reproduction that fails before the fix and passes after it.
3. Removal of the `web/e2e/quarantine.json` entry, or, if the date arrives first and the cause is
   still unknown, a conscious re-triage and a new entry with a fresh expiry — never an entry that
   silently rolls forward.

## 2026-08-20: a deterministic reproduction, found while measuring something else

T-3505 added per-frame drawing to `canvasDraw.ts`'s node loop, which meant `topology.scale
.v2_pan_zoom_p95_frame_ms` had to be re-measured before it could ship. It could not be: the
measuring test is this one. Establishing that the failure was pre-existing rather than a new perf
regression produced, incidentally, the thing three previous investigations did not have — **a
recipe that fails every time.**

```
E2E_ARGS="-g v2.canvas --repeat-each=3" scripts/e2e-shards.sh shard-1
```

Two runs, one with T-3505's canvas changes in the tree and one with them stashed, nothing else
different:

| attempt | with T-3505 | without T-3505 |
|---|---|---|
| 1 | `timedOut` 126397 ms | `timedOut` 126302 ms |
| 2 | `failed` 33787 ms | `failed` 33675 ms |
| 3 | `failed` 34115 ms | `failed` 33984 ms |

6/6 failures, and the two columns agree to within 0.3% on every attempt. Two things follow.

**T-3505 is exonerated.** The per-frame jack drawing changes neither the outcome nor the timing.
(The p95 itself is still unmeasured — the test never reaches the measurement — so that budget
remains unverified on this host, exactly as it was before T-3505. Said plainly rather than
inferred from "the suite is green".)

**The failure is not the timeout it was filed as.** Attempts 2 and 3 fail in ~34 s, which is the
`expect(v2).toBeVisible()` 30 s timeout plus setup — not the 126 s test timeout attempt 1 hits.
The error is specific and worth quoting, because "hang" has been the working description
throughout and it is the wrong word:

```
Locator: getByTestId('topology-canvas-v2')
Expected: visible
Received: hidden
  55 × locator resolved to <div data-testid="topology-canvas-v2"
       class="relative h-full w-full touch-none overflow-hidden">…</div>
     - unexpected value "hidden"
```

The element **mounts** — it resolves 55 times over 30 s, with its classes applied. It is
`hidden`, which for Playwright means a zero-size or otherwise non-visible box. So this is not the
renderer failing to start, nor a wedged rAF loop: it is a layout/sizing problem, and the
candidates are the container measurement path (`h-full w-full` inside a parent that has no
resolved height yet) and whatever the first attempt does differently, since attempt 1 fails by
timeout and 2 and 3 fail by visibility. That distinction is a mechanism-shaped question, which is
what requirement 1 above asks for and what the CPU-load / store-state / sharding theories already
refuted here never were.

Not investigated further — this was a side effect of a perf check on another task, and is written
down rather than acted on so the next attempt starts from a reproduction instead of a bisection.
The deadline is unchanged.

### The mechanism, measured (same day)

A geometry dump of the ancestor chain and siblings, taken on the scale-lab stack at the moment the
v2 canvas is mounted:

| sibling of the map container | height |
|---|---|
| toolbar (`flex flex-wrap`, wrapped onto two rows) | 120 |
| history timeline | 58 |
| **staleness + unref-findings banners** | **385** |
| LLDP/over-cap notice | 34 |
| **map container (`min-h-0 flex-1`)** | **139** |

Page root (`TopologyPage.tsx:898`, `flex h-full flex-col gap-3`) is 796px. The fixed-size siblings
total 737px, plus 84px of `gap-3` — 821px of demand against 796px of supply. The map, being the only
`flex-1` child, absorbs the entire shortfall and is left 139px, 17% of the page.

**That is the mechanism.** `min-h-0 flex-1` in a fixed-height column is not "the map gets the
remaining space", it is "the map gets whatever nobody else took, including nothing". A few more
banner rows and it resolves to 0, at which point Playwright's `toBeVisible()` reports `hidden` —
which is exactly the observed failure, and exactly why it is intermittent: which banners render, and
how many rows each has, depends on poll-timing-dependent state. It is scale-lab-specific because
that fixture (8 nodes, 300 guests, 40 VNets) produces the most banner content.

**The 385px is one banner, and it is a same-day regression of T-3501's.** The dump shows that block
has exactly one child — `StalenessBanner` rendered nothing (no stale sources), so the whole 385px is
`UnrefFindingsBanner`, added hours earlier by T-3501 to stop ref-less findings painting nowhere. Its
`<ul>` had no height cap, so it grew one row per finding. This does not make T-3501 the cause of a
quarantine that predates it — the layout was already fragile, which is the actual defect — but it
very plausibly explains why the test went from passing roughly 1 run in 3 to failing 6/6 today.

Fixed by capping both banners' lists (`max-h-28 overflow-y-auto`) so neither can displace the thing
it is reporting about, with a regression test each asserting the cap *and* that no finding is
dropped — capping height must not become capping content.

**The underlying fragility is untouched and still owns this card.** The map area is still the
leftover of a fixed-height column with six conditional siblings, so a sufficiently noisy cluster can
still squeeze it to nothing; capping two lists raises the bar, it does not remove it. The real fix
is a layout where the map has a floor (a `min-h` on the container, or the banner stack scrolling as
a group) rather than one where it is whatever is left. Not attempted here: this was a perf check
that turned into a diagnosis, and the layout change deserves its own scoped card.
