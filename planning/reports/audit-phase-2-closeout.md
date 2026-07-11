# Phase 2 close-out audit — T-206, T-207, T-208

**Date:** 2026-07-10 · **HEAD:** dfd5082 · **Auditor:** Claude (adversarial re-execution
of the three most recently completed cards; T-201…T-205 already covered by
`audit-phase-2.md` + `audit-phase-2-remediation.md` and re-audited here only where
T-206/207/208 integrate with them).

**Method:** every acceptance criterion on the three cards located to the test or code that
proves it, and the proof *executed* — not trusted. Full backend suite run race-clean:
`go test -race -count=1 ./internal/change/... ./internal/store/... ./internal/api/... ./cmd/...`
(all green: change 14–24s, store 17s, api 4.8s, vnproxctl 2.6s, vnproxd 2.2s). Web:
`npx vitest run` → **234/234 across 35 files**, `npx tsc --noEmit` clean, `npx eslint .` clean.
`gofmt -l` clean, `go vet` clean (see hygiene for one transient concurrent-checkout artifact).
The AC4 bundle-split test was run in isolation to confirm it performs a *real* `vite build`
(7.2s, content-asserted). T-206's daemon-down CLI path was read line-by-line and its
HTTP-freeness confirmed by grep (`net/http` appears only in the unrelated `status` ping). The
T-207 AC1 Playwright walkthrough was **built and run against the real stack** (production SPA
build → pvemock `three-node-vlan` → `vnproxd --config testdata/dev.toml`) — this is where the
one MAJOR finding surfaced. Accepted residual risks (vnproxctl real `ifreload`, T-207's
successful-apply→confirm on real hardware, T-208 unit-only/no-e2e) were confirmed honestly
documented and are not re-reported as findings.

**Verdict: T-206 and T-208 hold under re-execution; T-207's logic holds but its one automated
acceptance proof is red on the integrated tree.** Every T-206 and T-208 acceptance criterion is
backed by a genuine, executed, content-asserting test: zstd blob dedup is proven at *both* the
store and service level (each asserts a single `blobs` row for identical content); retention
pinning is a correct 7-day *floor* (only binds when `keepDays < pinDays`), time-travel-tested;
the `vnproxctl snapshots restore` / `rollback-now` disaster path is genuinely daemon-independent
(direct SQLite + local file write + injected `ifreload`, no HTTP anywhere) and tested with no
daemon in the harness; the restore inverse-synthesis that closed T-205's `ErrInverseUnsupported`
really reverses create/update/delete (table-covered) and passes the AC2 byte-level golden; the
F-10 rollback-window gate exists and is tested; `iface.raw.replace` runs the *full*
T-202/T-203 pipeline against the parsed entity delta (the mgmt-bridge deletion surfaces
`safety.protected_interface` and stays `draft`); the hash-conflict guard and the Monaco
code-split (content-asserted real build) both hold. T-207's write-side UX has **no direct-write
path** (every mutation routes through the `api/changesets` module), 234 web tests pass, and
tsc/eslint are clean — but **its AC1 Playwright walkthrough fails on `main`** because the F-22
dev-sandbox remediation (`dev_interfaces_dir` in `dev.toml`) landed *after* T-207's worktree
base and changed the apply outcome the spec asserts (F-01). The self-report's "Playwright 2/2
pass" is stale/false as-integrated. No fabricated-quote regressions in the three cards' new code
(the two quoted card phrases check out verbatim).

## Criteria summary

| Task | AC1 | AC2 | AC3 | AC4 | AC5 |
|---|---|---|---|---|---|
| T-206 snapshots/time-machine/audit | PASS (3-changeset timeline, cursor 4+2, diff-vs-live, self-diff) | PASS (restore N-2 → exactly two deletes, diff adds no line, post-apply snapshot==live byte-level golden) | PASS (dedup asserts single blob row at store *and* service level; time-travel prune + pin-floor) | PASS (CLI disaster path is DB+file+ifreload only, no HTTP; tested daemon-down) | PASS (audit filters user/result/target/range + cursor; every T-205 lifecycle event asserted in `GET /audit`) |
| T-207 editing UI | **FAIL** (walkthrough red on integrated main — F-01; steps 1–6 pass, final apply-outcome assertion is stale) | PASS (unit: findingFields drawer-line+form-field, applyFixToOps) | PASS (unit + partial e2e: draft-reload survival e2e-proven; mid-window countdown unit-only, honestly flagged) | PASS (e2e read-only spot-check green + capabilities unit test) | PASS (drawerMachine + opSummary vitest; tsc/eslint clean) |
| T-208 raw editor | PASS (unit: line-precise lint both layers; 250ms debounce < 300ms budget) | PASS (raw-replace → full T-202/T-203 pipeline; mgmt-bridge delete → `safety.protected_interface`, stays draft) | PASS (stale hash → `raw.hash_conflict`; matching hash clean) | PASS (real `vite build`; Monaco chunk >500KB, `MonacoEnvironment` present in it and absent from main entry) | — |

Ground rules across the three: **slog-only** — clean (no `fmt.Println`/`log.Print`; vnproxctl's
`fmt.Fprint` is legitimate CLI stdout/stderr, not logging). **`%w`-wrapped errors across
boundaries** — clean (snapshots_service 10, store/snapshots 19, store/blobs 9 wrap sites; no
bare `return err` across package boundaries in the new files; the api handlers write JSON
errors, they don't wrap). **No shadow-authoritative PVE state** — snapshots are content-addressed
point-in-time rollback artifacts (`{node,path,sha256}` + zstd blobs), not an authoritative config
copy; the CLI reads the daemon's own DB, not a shadow. **All mutations flow through the changeset
APIs** — verified: no `POST/PUT/DELETE` and no `apiFetch`/`fetch` mutation call exists in `web/src`
outside the `src/api/` modules; T-207's editors/map/bulk and T-208's raw save all funnel through
`useDrawerActions().addOps` → `api/changesets`. **Integration seam (T-208 ↔ merged T-301/T-302
peer work):** clean — the peer server mounts via the `PeerServer` interface under `/api/peer/*`,
outside `/api/v1`, with no route/middleware collision with the changeset/snapshot/audit/raw
routes; all `internal/api` tests pass race-clean on the integrated tree.

## Findings — major

### F-01 · MAJOR · T-207 — the AC1 Playwright walkthrough is red on the integrated `main` tree; the report's "2/2 pass" is stale
`web/e2e/changesets.spec.ts`'s main walkthrough (`T-207 walkthrough: bridge editor, reload
survival, bulk guests, review tabs, apply outcomes`) **fails** when built and run against the real
stack at HEAD dfd5082 (`npm run build && npx playwright test e2e/changesets.spec.ts` → **1 failed,
1 passed**). The read-only spot-check (test 2) passes; steps 1–6 of the walkthrough pass; the
failure is the final assertion (spec line 181): it waits 60s for a `role=status` banner containing
"failed to apply" that never appears. Root cause, confirmed from the captured Playwright DOM
snapshot: the apply **succeeded** and the changeset is in `awaiting_confirm` showing the countdown
banner — *"vnprox applied your change — confirm you still have connectivity (60s remaining)"* with
Confirm / Roll back now buttons — not the `failed` outcome the spec asserts. The spec was written
assuming a non-root daemon "deterministically fails the stage step" (spec lines 20–26, 174–176),
but the **F-22 dev-sandbox remediation** (`[safety] dev_interfaces_dir = "var/dev-host"`, now in
`testdata/dev.toml` — which `playwright.config.ts`'s `vnproxd` webServer loads) makes the non-root
daemon stage into the sandbox and no-op the reload, so the apply now *succeeds*. That remediation
merged into `main` *after* T-207's worktree base (the spec's own comment at lines 37–45 says the
sandbox was "in flight on another branch when this was written"), so the report's "Playwright: 2/2
pass" was true on its base but is **false as integrated**. This is deterministic, not flaky (it
will fail on every non-root run given `dev_interfaces_dir` is set). `npm run e2e` is opt-in and not
part of `make check`, so default CI stays green — which is exactly why the regression slipped
through the merge.
**Fix (low-effort, and the report itself predicted it):** update the spec to assert the
successful `awaiting_confirm` → countdown → Confirm → `committed` path (spec lines 42–45 already
sketch this: "enable the sandbox in testdata/dev.toml, drop the two guards below, and assert the
countdown → confirm path") — the sandbox has landed, so this *closes* the previously-un-automatable
gap rather than merely fixing a red test. Alternatively, gate the failure-path assertion on
`dev_interfaces_dir` being unset. Either way, re-run `npm run e2e` in CI (or as a merge gate) so an
integration-time behavior change can't leave the sole e2e acceptance proof red again.

## Findings — minor

### F-02 · minor · T-206 — the card's "side-by-side/unified diff viewer" ships unified-only
T-206 deliverables name a "side-by-side/unified diff viewer"; only the unified viewer
(`web/src/history/DiffView.tsx` over `parseDiff.ts`) was built. The report (deviation #2) discloses
this honestly, reading the "/" as either-or. It is a genuinely under-delivered deliverable, not a
correctness bug, and the diff data (`parseDiff.ts` output) already supports a side-by-side mode as
a pure-frontend addition.
**Fix:** either add the side-by-side mode over the existing parsed diff, or get the reduced scope
ratified in the decisions-of-record so the card text and the shipped surface agree.

### F-03 · minor · cross-task (deferred to a "T-208 checkpoint") — HTTP-layer apply-contract wire assertions still missing
`audit-phase-2.md` F-11 (the `202 Accepted` success shape, `409 changeset_locked`, and `422
validation_failed`-with-findings wire responses of the apply route are unasserted at the HTTP
layer) was deferred by the remediation with "Suggested home: one API test alongside the new
`diff_route_test.go` harness (T-208 checkpoint candidate)." T-208 did not pick it up; grep confirms
`internal/api/*_test.go` still asserts none of `StatusAccepted`/`changeset_locked`/the apply
route's `422`. The service-level behavior and error mapping remain tested (and the snapshots
lifecycle test drives create→apply→confirm→rollback through the change service), so this is a
test-rigor gap, not a defect.
**Fix:** add one `internal/api` apply-route test driving 202/409/422/404 through the mounted
router with the apply harness (the `snapshots_test.go` harness already wires a configured
`change.Service`).

## Hygiene

- `make check`-equivalent components green at HEAD: full `go test -race` (all phase-2 packages),
  `gofmt -l` clean, `go vet` clean, vitest 234/234, tsc clean, eslint clean. The bundle-split test
  does a genuine production build (not a mocked rollup) and asserts by content.
- **Coverage drift (note, not a finding):** `internal/change` statement coverage is now **87.7%**
  on the integrated tree — below T-205 AC6's one-time 90% gate and below the T-206 report's claimed
  89.7% (that figure was measured on the T-206 worktree before T-208 added ~380 lines of
  `validate_raw.go` and the raw service). T-206/207/208 set no coverage bar, so no AC is violated,
  but the number is worth watching before v0.5 beta; the T-206 report's 89.7% is optimistic vs.
  `main`.
- **Transient build artifact during the audit (environmental, not a finding):** one mid-audit
  `go vet ./internal/change/` invocation failed with `fakeHostReader does not implement
  peer.HostReader (missing method Links)` at `distributed_test.go:234`, while a `-race` run of the
  same package moments earlier and later passed. The file (git-clean, matching HEAD) had `Links` at
  line 63 and its use at line 237 — the reported line 234 shows it was momentarily mid-rewrite by a
  concurrent process (the active `t303-fanout`/`t304-rollback` worktrees share the object store;
  mtime landed inside this session). Re-run after it settled: `go vet` exit 0, package builds and
  tests pass. HEAD is consistent; flagged only so a future reader who sees the same flicker knows
  it is worktree concurrency, not a real breakage in the three audited cards.
- **Fabricated-quote / stale-comment class (the recurring phase-1/2 failure):** *clean* for the
  three cards' new code. The two card phrases quoted in new comments —
  `raw_service.go:16` ("file hash captured at open, mismatch on save → reload prompt") and
  `raw_service.go:119` ("syntax errors underline with line-precise messages as you type") — are
  verbatim from T-208's card; `blobs.go:19`'s "dedup" claim matches T-206's deliverables. No
  invented authorizations found in the T-206/207/208 comments or reports. The self-reports'
  numbers check out (234 web tests / 35 files matches the live run; T-208's chunk sizes satisfy the
  build assertion). Reports are substantially honest — the one material overstatement is F-01's
  now-stale "2/2 pass," which was an integration-order casualty rather than a fabrication.
- **Accepted residuals confirmed honestly documented:** vnproxctl's real `ifreload`/interfaces
  semantics (T-206 §4.1), T-207's successful-apply→confirm on real hardware / mid-window reload
  against a live apply (T-207 AC1 caveats + "For the next agent"), and T-208's no-Playwright /
  unit-only coverage (T-208 deviations) are each flagged as needing hardware/e2e validation. F-01
  narrows T-207's gap: the successful-apply→countdown path is now *reachable* in the dev sandbox and
  should be asserted rather than left as a hardware-only item.
