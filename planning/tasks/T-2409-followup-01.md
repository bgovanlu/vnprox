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
