# T-2505-followup-02 · the guest-interior panel never refetches after its toggle is enabled

**kind:** defect · **found by:** `T-2505`'s two-core reproduction of a hosted-runner failure ·
**status:** **FIXED as of v4.0.0 (2026-08-14)**, commit `da58781`

No card owned this document before now. It is filed here because `CHANGELOG.md:217`,
`docs/project-status.md`, and `planning/tasks/phase-25.md` all cite `T-2505-followup-02` and
none of them previously pointed at a standalone file. `CHANGELOG.md`'s `[4.0.0]` "Fixed" entry
is the authority on the corrected diagnosis; this file expands it with the evidence trail, which
the changelog entry deliberately keeps short.

## The symptom

The topology map's guest-network interior panel could get stuck showing a permanent error —
"Could not read this guest's interior right now — it may be unreachable, or no live PVE session
is available" — after the operator enabled the interior toggle, on some fraction of runs. The
panel never recovered without a tab remount.

## How it was found

`T-2505-input-02` asked for one experiment: re-run the two specs the hosted runner had failed on
commit `4968bf3` under `taskset -c 0,1` (2 cores), on an otherwise-idle machine, with a control at
each step. Single-spec probes (`guest-interior.spec.ts:23` alone, `user-guide-tasks.spec.ts:73`
alone, `scale.spec.ts:258` alone) all passed on 2 cores — refuting the simple version of "the
runner is just slower." Whole-shard runs told a different story: shard-4 (20 tests) passed 3/3 on
32 cores but failed 2/2 on 2 cores, a **different** test each time. One of those two failures was
`guest-interior.spec.ts:23` — one of the exact two specs the hosted runner had failed.

Deadlines were made to scale with `availableParallelism()` as a first response (×2.5 under 4
cores, ×1.5 under 8), which fixed the timing-budget half of what `T-2505-input-01`'s load
hypothesis predicted. It did **not** fix `guest-interior`: on the next two-core run it failed
again, waiting out its full (now-scaled) 75-second budget. That ruled out "this is just a slow
machine" and pointed at a real defect underneath.

## The evidence

The daemon log for a failing run, over 60ms, in order:

```
GET  /api/v1/guests/guest:pve1:200/interior-toggle   200
GET  /api/v1/guests/guest:pve1:200/interior          404      <-- toggle still off
PUT  /api/v1/guests/guest:pve1:200/interior-toggle   200      <-- spec turns it on
GET  /api/v1/guests/guest:pve1:200/interior-toggle   200
(nothing further)
```

The interior read fires once, before the toggle is on, and correctly 404s. Nothing follows the
`PUT`. On a fast machine the initial `GET` lands after the toggle flips, so the bug is invisible;
under CPU restriction the read races ahead of the toggle and the panel is stuck showing the error
branch until the tab is remounted.

## The root cause originally diagnosed — and why it was wrong

The log above is completely accurate, and the inference first drawn from it — "nothing follows
the `PUT`, therefore enabling the toggle never invalidates the interior query" — was **wrong**,
even though it fit the log. The original diagnosis, recorded when this defect was found and
tracked as `T-2505-followup-02` before this file existed, concluded the fix was to add an
invalidation to `useSetGuestInteriorToggleMutation`'s `onSuccess`.

**That invalidation was already present and always had been.** `onSuccess` already invalidated
both the toggle-query key and the interior-query key. Silence after the `PUT` in the log was
consistent with two different causes — no invalidation firing, or an invalidation firing against
a query that can't be un-stuck by any refetch — and the diagnosis committed to the one that would
have been fixed by adding code that was already there.

## The actual root cause

`useGuestInteriorQuery`'s `queryFn` treated the expected `interior_not_enabled` 404 as "nothing
to show yet" and resolved to **`undefined`**. TanStack Query v5 treats a `queryFn` that resolves
to `undefined` as a bug in the caller: `Query.fetch()` throws `"data is undefined"` internally
and forces the query into a genuine `isError` state — which *is* the amber "Could not read this
guest's interior right now" branch. The query was never holding a stale success that a missing
invalidation failed to refresh; it was parked in a **synthetic error state**, and the
already-present invalidation could never clear it, because invalidating a query that is sitting
in `isError` schedules a refetch, and the panel's own render logic was reading the error branch
regardless of what the next fetch would have produced. Compounding it: the synthetic error is a
plain `Error`, not an `ApiError`, so `queryClient`'s "do not retry a 4xx" rule doesn't recognize
it as the 404 it actually came from either.

Reproducible only under CPU restriction, because the race needs the interior `GET` to land before
the toggle `PUT` — which is exactly why it had read as a flake rather than a deterministic defect.

## The fix

Commit `da58781` (2026-08-13, `topology: stop a raced interior 404 from becoming a permanent
error (T-2505-followup-02)`): the sentinel is now **`null`**, not `undefined`. A query resolving
to `null` stays in a genuine success state, so the invalidation that was always present can do
its job the moment the toggle actually turns on. `InteriorTab` needed no change — its truthiness
check already read `null` the same way it read `undefined`.

Verified by mutation, not just by passing: with the sentinel put back to `undefined`, the new
regression test (`web/src/topology/InteriorTab.test.tsx`, "does not surface the raced interior
404 as a fetch error, and still refetches") fails on the exact error copy the original card
quoted; restored, it and the rest of the file pass (5/5), `tsc` is clean, and the full 466-test
topology suite passes. The test also picked up a `beforeEach` `mockClear` for its module-scoped
mocks — `vite.config.ts`'s `restoreMocks` does not clear call history for `vi.fn()`s created at
module scope, which had made the new test's call-count assertions order-dependent inside a
full-file run.

## Current status

**Fixed and shipped as of `v4.0.0` (2026-08-14)**, per `CHANGELOG.md`'s `[4.0.0]` "Fixed" entry
and `docs/project-status.md` §6.4/§6.5, both of which name `da58781`'s corrected root cause.

**A worth-repeating discrepancy in the existing docs, flagged rather than silently fixed here**
(this task's file allowlist does not include `docs/status-matrix.md`): `docs/status-matrix.md`
still describes this card as "found by `T-2505`'s two-core reproduction, still open" — that line
predates the fix and is stale. It is one of the "~24 missing Arc-5/v4.0 rows" `docs/roadmap-earned.md`
already names as an open documentation debt, absorbed into `T-2906`.

## What closes it

Already closed. Nothing further is owed on this card. The reading lesson it leaves behind — that
an accurate log is not the same thing as a correct inference from it, and that the fact
distinguishing "missing invalidation" from "invalidation firing against a query stuck in a
synthetic error" was never in the daemon log at all, only in the client's own query state — is
recorded here and in the commit message so the next raced-fetch defect doesn't repeat the same
misreading.
