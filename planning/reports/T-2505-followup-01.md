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
