# Debt sweep — 2026-08-19

A survey of every roadmap track, task card, blocked-validation register and quarantine entry
(run 2026-08-19, HEAD `d8d2879b`) produced a ranked list of outstanding debt. The owner asked for
all of it, in ranked order. This file is the worklist and its live status — it exists so that a
sweep this size cannot quietly drop an item, which is the failure mode the survey itself was
written to catch.

**Product decisions taken by the owner, 2026-08-19** (each card had refused to choose
unilaterally, per `CLAUDE.md`):

| Item | Decision |
|---|---|
| `sdn.apply` foreign pending state | **Surface and confirm** — review screen shows "this apply will also commit …" and requires explicit acknowledgement. Not block; not lock-taking. |
| Tenant read leak | **Scope reads to own tenants**; `GET /tenants/{id}` returns 404 (not 403) for a non-member, so existence is not leaked. |
| `read_only` token scope | **Split `automation`** into a read half (WS `events`) and a write half (webhook registration); `read_only` clears `capture` and the write half. |
| Public DNS for apt/demo/registry | **Deferred** — owner will supply VPS credentials later. Not started; remains the gap to a real install experience. |

## Worklist

| # | Item | Tracked as | Status |
|---|---|---|---|
| 1 | `scale.spec.ts` v2-canvas hang; quarantine expires **2026-09-15** | `T-2505-followup-01` | **Fix built, NOT yet proven.** Quarantine deliberately left in place — see below |
| 2 | `sdn.apply` commits foreign staged SDN changes | `T-3101-followup-01` | |
| 3 | Cross-tenant scope/membership read leak | `T-3002-followup-01` | **Done (reads only)** — mutation routes still unscoped, see below |
| 4 | `read_only` does not restrain `capture`/`automation` | `T-3003-followup-01` | **Done** — carries a real behaviour change for existing tokens |
| 5 | Phases 32/33 have no task-card files | process gap | **Done** — cards written retroactively |
| 6 | `docs/status-matrix.md` never swept past Phase 29 | doc staleness | **Done** — §8 added, 9 rows corrected |
| 7 | apt/demo/registry do not resolve publicly | `T-3301`/`T-3303` | **Deferred by owner** — needs VPS credentials |
| 8 | `install.sh` multi-node rollout never copies the PVE token file | deployment bug | **Done** — untestable here (one node, no cluster); filed to needs-hardware-validation |
| 9 | 11 of 16 finding sources render as literal `undefined` | `T-3004-followup-01` | **Done** — it was 12 of **17**, see below |
| 10 | `T-2409` built but unmerged on `t-2409-e2e-store-isolation` | branch debt | |

## Document staleness found by the same survey

| Item | Where |
|---|---|
| `roadmap.md` does not mention Phase 34 at all | `docs/roadmap.md` |
| `roadmap-earned.md` still says "Status: proposed 2026-08-15" though 29/30 shipped | `docs/roadmap-earned.md` |
| `roadmap-proven.md` self-corrects T-2006/T-2102 but omits T-2104/T-2105, which `phase-21.md` marks partial | `docs/roadmap-proven.md` |
| `project-status.md` §6.4 claims all 25 cards of Phases 25–28 shipped; `phase-28.md:253` documents it contradicting itself | `docs/project-status.md` |
| `internal/blueprint/suggest.go` defers to IPAM "which does not exist on this branch's base" — IPAM shipped in T-405 | `internal/blueprint/suggest.go` |

## Item 1 — what is actually known, 2026-08-19

A fix is built and the quarantine entry is **deliberately still in place**. Recording the state
precisely, because "we fixed it" and "the test stopped failing once" are different claims and the
gap between them is the whole risk here.

**Established:**

- **The payload-growth candidate is largely ruled out.** Diffing `/api/v1/topology` between a
  fresh daemon and a 150s-idle one shows only timestamps moving and one source flipping to
  `stale`. Node/edge lists and badges are otherwise byte-identical. Findings *do* grow (328 → 356,
  all `health:path_loss` maturing), but `internal/findings/health_latmesh.go`'s `latMeshFinding()`
  never sets `Refs` — and `Refs` is exactly what `paintFindings` keys off — so that growth cannot
  reach the topology payload at all. Caveat: measured without browser load, not in the precise
  "after two prior Playwright runs" configuration.
- **The T-2409 branch does NOT contradict the uptime theory.** It looked like it did: that branch
  gives every spec its own daemon, and the v2 test still hung. But `isolatedStore` is called once
  at **file** scope, so all three tests in `scale.spec.ts` share one daemon, and
  `describe.configure({ mode: "serial" })` keeps v2 running last. Under both arrangements v2 is
  never actually exercised against a fresh daemon. The observation corroborates rather than
  refutes.
- **The fix**: pointer-move-driven state commits in `TopologyCanvasV2.tsx` are coalesced to one
  per animation frame, instead of a synchronous React state update → render → effect → full
  `drawScene()` per native `pointermove`. Independently a real performance improvement during any
  fast gesture, not only a test accommodation. 473/473 topology unit tests pass.

**DISPROVEN — the rAF throttle does not fix the hang.**

Three consecutive full-file runs, orchestrator-run 2026-08-19 14:05–14:15 on a settled machine
(1-minute load 3.07 and falling, no stray `playwright`/`vnproxd`/`pvemock` processes),
`VNPROX_E2E_SHARD=shard-1 npx playwright test scale.spec.ts --workers=1`:

| Run | v2 pan/zoom test | Wall clock |
|---|---|---|
| 1 | **FAILED** — 120s timeout | 3.4m |
| 2 | **FAILED** — 120s timeout | 3.5m |
| 3 | passed, 32.6s | 1.7m |

**2 of 3 still hang.** The single clean post-fix run that the investigation had was luck, and the
33%-pass shape here matches the pre-fix behaviour rather than improving on it. This is precisely
why one run was not accepted as evidence.

**Consequences:**

- The quarantine entry **stays**, unchanged, expiry still 2026-09-15. It is now closer than when
  the sweep began and the root cause is still unknown.
- **The rAF throttle is kept anyway**, as a performance change on its own merits — it coalesces
  redundant pointer-move → state → render → full-`drawScene` passes during any fast gesture, and
  473/473 topology unit tests pass with it. It must **not** be described anywhere as the fix for
  this hang. It is not.
- Both original candidate mechanisms remain live, and were never A/B'd against each other. The
  payload-growth candidate is separately weakened by the fresh-vs-150s diff (above), which leaves
  the synchronous per-mousemove redraw as the surviving suspect — now with the rAF throttle
  ruling out *its* most obvious form.

**Next step for whoever picks this up:** the mechanism is still unidentified after three separate
investigations. Rather than a fourth round of hypothesis-and-patch, capture a Playwright trace of
a *failing* run and find what the wedged `page.mouse.move()` is actually waiting on — the existing
entry already establishes the failure is one CDP call that never returns, not a cascade. Note that
runs 1 and 2 failing and run 3 passing, in that order, is itself a clue worth explaining: whatever
the trigger is, it did not accumulate monotonically across three back-to-back runs.

## ~~STILL OPEN~~ — CLOSED 2026-08-19, 35 minutes after this was written

> **Correction, added 2026-08-27.** The section below was accurate for about half an hour.
> Commit `4713d72c` ("api: scope tenant mutations too, and lock the scope boundary to fleet
> admins", 14:56 — this sweep was written at 14:21) closed it: `tenantMutationScope` in
> `internal/api/tenant.go` now gates all five routes, scope mutations are locked to fleet admins
> outright, and `TestTenantScoping_NoCrossTenantLeakage` /
> `TestTenantSelfService_MemberCanManageOwnTenant` cover both directions.
>
> **This document was never updated, so a fixed security issue read as open for eight days** and
> was still being carried as an open item on the 2026-08-27 roadmap's debt gate. Re-derived and
> written up in `planning/tasks/T-3714-tenant-mutation-scoping.md`. The original text is kept
> below unedited, because a sweep is a dated record — but a record that outlives its accuracy
> without saying so is the same failure mode as a fixture nobody re-checked.

**Tenant *mutation* routes are not scoped to membership at all.** `POST`/`DELETE /tenants`, and
`PUT`/`DELETE` on `.../scopes` and `.../members`, gate only on `netWrite`. A caller holding
`netWrite` and membership in one tenant can still mutate **any** tenant's scopes and members.

Item 3 fixed the *read* leak, because that is what the owner's decision covered and what the
original card described. But a read leak discloses; this one lets a tenant member rewrite another
tenant's boundary. It was found while fixing item 3, is recorded in `tenant_test.go`'s comment,
and **has no card**. It should get the next decision after this sweep.

Also still open on the same route family: `handleGetTenant` swallows `ScopesForTenant` /
`MembersForTenant` errors on the single-tenant path (pre-existing, named in the original card).
The new `handleListTenants` path propagates them as 500s rather than copying the bug.

## Behaviour change shipped by item 4 — operators must be told

`read_only` now clears `capture` outright, and `automation` has been split: `Automation` (read
half — WS `events`, `GET /webhooks`) survives `read_only`; a new `AutomationWrite` (`POST`/`DELETE
/webhooks`) does not.

**A token minted with only `automation` before this change could register and delete webhooks.
After it, that token needs `automationWrite` and will start failing.** `GET /auth/me`'s `caps`
gains `automationWrite` (additive). This is a deliberate consequence of the owner's chosen option,
not an accident — but it is a live behaviour change for existing automation tokens and belongs in
release notes, not only in `docs/api.md`.

## Found during the sweep, not yet carded

The sweep's own premises turned out to be wrong in places. Recording that here, because a survey
that is never checked against the code becomes the next stale document:

- **The survey undercounted item 9.** `internal/findings.Source` has **17** constants, not 16 —
  `gitsync` (T-2701) was added after the count was written. So 12 sources rendered `undefined`,
  not 11. Read from `internal/findings/types.go`, which is authoritative; the card was copying an
  older count.
- **The survey overcounted the headless backends.** It claimed six Phase-26/27 cards ship a
  backend with no `web/src` client. Verified individually against real frontend callers: five of
  the six now have UI. Only **T-2702** (changeset → PR) is genuinely headless.
- **`docs/roadmap-earned.md` claimed T-3202 closed `T-1904-followup-01`. It did not** —
  `packaging/install.sh` still reports rather than aborts on a failing doctor run. Corrected in
  place.
- **`web/src/settings/AlertRules.tsx` has its own `AlertSourceFilterValue` union carrying the
  same 5-of-17 defect** item 9 just fixed in the findings panel. Out of that item's scope, left
  untouched, **uncarded** — it will render the same `undefined` for 12 sources.
- **`internal/blueprint/suggest.go`'s deferred IPAM delegation** was never implemented; only its
  stale comment was corrected. **Uncarded.**
- **`docs/docs-site.md` still says GitHub Pages is not enabled**, which `gh api` shows is false —
  it is built and live. **Uncarded.**

## Rules for this sweep

- **No agent runs `make check` or the e2e suite.** Five workstreams in one worktree previously
  produced a full suite of failures that were pure cross-run contention (Playwright's
  `Object with guid response@… was not bound in the connection`), plus two axe specs that flake
  purely on machine load. Each workstream verifies scoped to its own files; the orchestrator runs
  the real gate once, serially, at the end.
- **A security fix lands with a test that fails before it and passes after.** Items 3 and 4 have
  tests that deliberately assert *today's* wrong behaviour; those tests are expected to go red and
  must be rewritten, not deleted quietly.
- **Item 2 has no test pinning it**, and cannot get one until `internal/pvemock` can model SDN
  pending state and a foreign edit. That mock work is a prerequisite, not an optional extra.
