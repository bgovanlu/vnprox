# T-2409-followup-01 · Land or retire the e2e store-isolation branch

**kind:** test-infrastructure debt · **status:** open, 2026-08-19 · **branch:**
`t-2409-e2e-store-isolation` (`a9559e79`, one commit, now ~145 behind `main`)

## Why this card exists

T-2409 built per-spec e2e store isolation, proved it works, and **refused to merge itself**. Its
commit message is the card that never got written: it records exactly which acceptance criteria it
met (AC1, AC2) and which it did not (AC3, AC4), with measurements rather than estimates, and names
its own two conditions for merging:

> Merging this would take the required, blocking e2e job from 89/0 to 88/3. It stays on a branch
> until the three failures are explained — T-1806-bug-01 is the precedent for refusing an
> unexplained result — and until +83% is either brought inside budget or the budget is
> renegotiated on the evidence.

That was the right call and it has been sitting unexamined ever since, which is not.

## What it actually does

31 spec files shared one `vnproxd` and one mutable SQLite store, so a spec that created a
changeset, saved a layout or registered an alert rule left it there for everything that ran after
— a spec's result depended on which specs ran before it. Every file now calls `isolatedStore()` and
gets its own daemon, database, session key, interfaces sandbox and port.

AC1/AC2 are proven by a harness test, not a feature test: `isolation-a-writes` creates a changeset
and `isolation-b-cannot-see` asserts it cannot see it — green under isolation, **red** under
`VNPROX_E2E_SHARED=1`, the arrangement the suite had before. A pair that passed in both modes would
prove nothing.

Two design notes in that commit are worth preserving whatever happens to the branch:
`baseURL` cannot come from a Playwright fixture (fixtures resolve before a file's `beforeAll`, so a
fixture reading a stack started there throws every time — the port is derived at import time from
the calling file's path instead); and the helper is `isolatedStore`, not `useIsolatedStore`,
because the `use` prefix makes eslint's `react-hooks/rules-of-hooks` treat every call site as a
misplaced React hook.

## What changed today (2026-08-19)

**One of the three blocking failures is now explained, and it is not this branch's fault.**

The branch attributed its `scale.spec.ts` v2-canvas timeout to cold-start, tried the obvious fix
("wait for the first successful PVE poll" in `startStack`), and recorded that it did not work. That
hypothesis can now be closed properly: **the same test fails on `main` without this branch**, at
roughly the same rate. Three consecutive runs on a settled machine on `main` gave FAIL, FAIL, pass.
It is the long-standing hang quarantined as `T-2505-followup-01`, whose mechanism is still unknown
after three separate investigations (see `planning/tasks/debt-sweep-2026-08-19.md` for the full
evidence, including what has been ruled out).

So AC3's "88/3" is not three regressions introduced by isolation. At least one is a pre-existing
intermittent failure this branch merely stopped hiding — which is the same thing the branch says
about the sharded arrangement in general.

It also resolves an apparent contradiction worth recording, because it looked damning: the
quarantine entry concluded the hang depends on the **daemon's accumulated uptime**, yet this branch
gives every spec a fresh daemon and still hung. Both are true. `isolatedStore` is called once at
**file** scope, so all three tests in `scale.spec.ts` share one daemon, and
`describe.configure({ mode: "serial" })` keeps the v2 test running last. Under both arrangements
the v2 test is never actually exercised against a fresh daemon.

## What to do

1. **Rebase onto current `main`** (~145 commits, including Phase 34's redesign, which changed e2e
   selectors substantially). Expect real conflicts in the spec files.
2. **Re-measure AC3 honestly.** The +83% wall-clock figure (9.1 → 16.7 min) predates a lot of
   change, including this session's rAF pointer-move throttle. Measure again rather than carrying
   the old number forward; and measure on a quiet machine, because two axe specs in this repo flake
   at 33–47% purely on machine load.
3. **Re-triage the two `user-guide-tasks.spec.ts` failures** the same way `scale.spec.ts` was
   triaged: do they fail on `main` too? If they do, they are not this branch's to explain.
4. **Then decide, explicitly**: land it, or retire the branch and keep only the two design notes
   above. What must not happen is a fourth arc of it sitting unexamined — a branch that says "NOT
   READY TO MERGE" is a decision deferred, and this one has been deferred long enough that the
   codebase moved 145 commits underneath it.

**If AC3 still fails after re-measurement**, that is a budget conversation, not a silent hold: per-
spec isolation buys correctness that the shared store demonstrably did not have (the branch's own
red-under-shared test is the proof). Renegotiating a wall-clock budget on that evidence is a
legitimate outcome; leaving the suite knowingly order-dependent to protect the budget is not.

## 2026-08-21: one of the three blocking failures no longer exists

`scale.spec.ts`'s v2-canvas failure — the one this card had already established was **not this
branch's fault** — has been root-caused and fixed. The map container was the only `flex-1` child of
a fixed-height flex column and carried `min-h-0`, so banner growth could squeeze it to zero height,
which Playwright reports as `hidden`. Fixed in `a385f38a`; `T-2505-followup-01` is closed and
`web/e2e/quarantine.json` is empty. Evidence: 6/6 passing across two `--repeat-each=3` rounds
against 6/6 failing before, on the same machine within the hour.

**What that changes for this card.** AC3's headline number was "88/3" — three failures the branch
could not explain. One of those three is now simply gone, and it is gone for everyone, on `main`,
independent of this branch. The remaining question is two `user-guide-tasks.spec.ts` failures, which
step 3 above says to triage the same way: do they fail on `main` too?

It also removes the awkwardness recorded above about `isolatedStore` being file-scoped. That
paragraph existed to reconcile "the quarantine says daemon uptime matters" with "this branch gives
every spec a fresh daemon and still hung". Both halves are now explained by something simpler:
daemon uptime was a **proxy for banner height** — a longer-running daemon accumulates findings and
staleness, the banners grow, and the map is what gets squeezed. Neither theory about store isolation
or browser reuse was ever the mechanism.

**Still to do, unchanged**: rebase (now **167** commits behind `main`, spanning Phase 34's redesign,
Phase 35's faceplate rewrite and Phase 36's remediation buttons — expect real conflicts in the spec
files), re-measure the +83% wall-clock figure rather than carrying it forward, triage the two
remaining failures against `main`, and then decide explicitly: land it, or retire it and keep only
the two design notes.

That decision is still the owner's, and this card should not be closed by quietly rebasing and
merging on the strength of one blocker clearing. But the case for landing it is materially stronger
than it was this morning: the branch's own stated condition was "until the three failures are
explained", and the hardest of the three is now not merely explained but fixed.

## 2026-08-21: the land-or-retire question turns out to be the wrong question

Steps 1–3 were taken up. Step 3 did not need running, because **it had already been done on `main`,
by `T-2505`, and the answer is recorded in `planning/tasks/phase-25.md`**:

> **`user-guide-tasks.spec.ts` × 2 (IPAM reserve, firewall macro rule).** T-2409's branch applied
> `isolatedStore({ config: "testdata/dev-scale.toml" })` at **file** scope. On `main` only that
> file's *first* `describe` runs against the scale stack; the SDN/IPAM/firewall `describe` runs
> against three-node-vlan. The conversion silently moved four tests onto a fixture they were never
> written for. Named cause: **a fixture regression introduced by the isolation conversion, not
> order-dependence.**

Verified against the code rather than taken from the card: `web/e2e/shards.ts:217` reads
`"user-guide-tasks.spec.ts": ["default", "scale"]`, and the third failure (`history.spec.ts`) is
recorded there as the same class. So **all four of the branch's failures are now explained** — two
by T-2505 as the branch's own conversion bug, one (`scale.spec.ts`) root-caused and fixed on `main`
in `a385f38a`, and `history.spec.ts` not reproduced since.

**The branch's stated merge condition is therefore met.** It said "it stays on a branch until the
three failures are explained". They are.

### And that is exactly why it should not be merged

While this branch sat, `T-2505` solved the same problem at a coarser grain and **merged**.
`web/e2e/shards.ts` is not a neighbouring change; it is a considered decision *about this branch*,
written down at the top of the file:

> **WHY NOT A DAEMON PER SPEC FILE.** That is T-2409's branch: it works, and it cost +79% wall clock
> (16.3 min) for 31 daemon starts. Shard granularity buys the isolation that matters — a spec cannot
> corrupt another shard's store — at four daemon starts instead of thirty-one.

So the merge is no longer a rebase. The branch **deletes** every `vnproxd` entry from
`playwright.config.ts` (-80 lines) because each spec starts its own; `shards.ts` **generates** those
same entries per shard, on per-slot ports registered in `testdata/dev-ports.tsv`. Two designs, same
problem, different grain, colliding in the same file. Resolving that conflict is not conflict
resolution — it is choosing an architecture, and the choice has already been made and shipped.

### What is genuinely still open, and it is not this branch

Sharding is a **weaker** guarantee, and `T-2505` says so plainly rather than claiming otherwise:
per-shard isolation means shard-1's twelve spec files still share one daemon and one store. Its AC3
run proved the cost of that — `--repeat-each=2`, four shards, idle machine: **168 passed / 10
failed**, and every one of the ten mutates app-owned state and then asserts on a starting condition
its own first repeat destroyed (`onboarding`, `changesets`, `mgmt-redundancy`, `history`,
`alert-rules`, `simulator`, `flows`, `responsive-triage`, `guest-interior`).

T-2505 named the next step itself, and it is neither landing this branch nor retiring it:

> `--repeat-each` needs isolation *per run of a spec*. Sharding gives isolation *per shard*, which is
> a different and weaker guarantee. The construct that does satisfy it is T-2409's per-spec daemon
> [...] whose blocker was cost: +79% wall clock, 16.3 min serial. **That blocker is now much smaller
> than it was.** 16.3 min of serial per-spec isolation spread over four shards is ~4–5 min — inside
> the budget. **Combining the two is the obvious next card.**

### Recommendation

**Retire the branch; carry `isolated.ts` forward as the input to a combining card.** Not "retire and
keep the two design notes" as this card's step 4 offered — that undersells what is there.
`web/e2e/isolated.ts` is 395 lines of working, proven per-spec isolation, and its two hard-won design
notes (`baseURL` cannot come from a Playwright fixture, because fixtures resolve before a file's
`beforeAll`; the helper must not be called `useIsolatedStore`, or `react-hooks/rules-of-hooks` treats
every call site as a misplaced hook) are the kind of thing a reimplementation rediscovers the
expensive way.

What must not happen is a rebase-and-merge on the strength of "the three failures are explained".
That condition was written before the thing it was gating became obsolete, and satisfying a stale
condition is not the same as the change being right.

**Still the owner's call.** The measurement asked for in step 2 was not taken, deliberately: it
would have measured a branch whose merge path no longer exists, on a machine whose baseline run is
currently red for two unrelated reasons (see below). Measuring the *combined* design is the number
that would actually inform a decision, and that belongs to the combining card.

### Found while doing this: `main`'s e2e suite is red, and had been for days

The baseline run this card asked for surfaced two real, deterministic failures on `main` — not
flakes, and both invisible because the full suite had not been run since 2026-08-19:

- **`topology.spec.ts:136`** — `toHaveScreenshot` mismatch: the map renders 418px against a 568px
  committed baseline. Phase 36's remediation buttons made the topology banners taller and the
  baseline (last regenerated 2026-08-18, `2afbecc1`) was never updated. Same banner-height
  mechanism as `T-2505-followup-01`, one step milder: the map shrank rather than vanished, because
  `a385f38a`'s `min-h-[22rem]` floor held.
- **`alert-rules.spec.ts:80`** — `getByRole("checkbox", { name: "probe" })` times out. The
  2026-08-19 debt sweep gave `AlertRules.tsx` a `SOURCE_LABELS` map so all 17 finding sources are
  routable; that renamed the checkbox's accessible name from `probe` to `Verify live`. The wire
  value is unchanged, so the assertion two lines later still holds — only the locator was stale.

Both are recorded here rather than only in a commit message because they are the same lesson this
card is about: **a suite nobody runs in full stops being evidence.** Targeted runs proved what they
targeted, and reporting them as "green" was a reporting error.
