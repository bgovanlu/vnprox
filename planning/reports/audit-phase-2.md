# Phase 2 audit — checkpoint after T-205 (T-201…T-205)

**Date:** 2026-07-10 · **HEAD:** 6939449 · **Auditor:** Claude (4 parallel audit agents + direct audit of T-205, API contract, and ground rules)

**Method:** every acceptance criterion on the five cards verified against actual code and test
assertions, with the tests executed: `go test -race -count=1` on change (incl. change/ifaces), api,
host, store, pve — all pass; the T-202 AC4 benchmark run for real (`BenchmarkValidate_100Ops`:
**193µs**/100-op validation, plus the hard-asserting `TestValidate_100OpsUnder200ms` — both far under
the 200ms budget); `internal/change` coverage measured at **90.2%** (T-205 AC6's ≥90% — matches the
report's 90.3% claim within rounding); full `make check` re-run green (lint, vet, govulncheck, web
tsc/eslint/vitest, npm audit). API contract spot-checked **live**: pvemock + `vnproxd --config
testdata/dev.toml` started, then curl-driven login → create draft → strict-decode rejections →
CSRF-missing 403 → validate → diff → `?status=` filter → protected-interfaces GET → discard, all
verified against docs/api.md's envelope/field names (apply/confirm deliberately not fired against the
dev box — its `hostNodeAgent` points at the real `/etc/network/interfaces`; see F-22). Every claimed
correctness bug below was **independently re-executed** through the real pipeline entry points via
`go test -overlay` probes (repo untouched; probes A–F in the audit scratchpad). T-205's documented
residual risks (no real-ifupdown2 validation, single-daemon lock until T-304, the stage-write/ifreload
crash window, SDN rollback deferral) are accepted per the review record and not re-reported.

**Verdict: the changeset lifecycle core holds; the validation layer beneath it does not yet.**
T-201 and T-205 — the model and the safety-critical apply engine — survive adversarial re-execution
essentially intact: the state machine, commit-confirm timers (including DB re-arm across restart and
the real-`time.AfterFunc` past-deadline case), byte-identical rollback at every failure position of a
5-step two-node plan, the cluster apply lock, snapshots, audit trail, and WS stream all do exactly
what the reports claim, and the T-205 honesty contract checks out. However the phase's *first line of
defense* has real holes: **three safety-interlock bypasses** (F-01, F-02) and an order-fragility that
means T-203's own AC2 scenario is rejected by the referential class when run through the real
pipeline (F-03); **two classes of referential-validator correctness bugs** — a spec'd check that
deterministically never fires (F-04) and apply-blocking false positives on delete-then-recreate
drafts (F-05); and T-204's byte-level guarantees are under-proven across the corpus, with one genuine
output-corruption bug on CRLF files (F-06). F-01…F-06 should be fixed before T-207 exposes the
editing UX and before the v0.5 beta milestone is declared. The recurring phase-1 hygiene problem —
comments/reports quoting task-card authorization that doesn't exist — recurred three more times
(F-23).

## Criteria summary

| Task | AC1 | AC2 | AC3 | AC4 | AC5 | AC6 |
|---|---|---|---|---|---|---|
| T-201 changeset model | PASS (39-op vocabulary independently recounted; strict decode; live-curl verified) | PASS (hand-written 8×8 table) | PASS | PASS | — | — |
| T-202 validators | PASS (52 golden cases, exact code+ref) | PASS (mechanism; but see F-04/F-05) | PASS (11-case fix property; 3 paths uncovered — F-16) | PASS (193µs; workload trivial — F-17) | — | — |
| T-203 interlocks | PASS | PARTIAL (class-level only; real pipeline rejects the AC's op order — F-03) | PARTIAL (listing/clearing tested, but reattach check bypassable — F-01) | PASS | — | — |
| T-204 writer/differ | PARTIAL (12/12 ops golden'd but only fixtures 01–04; F-06/F-07) | PARTIAL (randomizes 3/12 op types — F-08) | PASS (all 15 corpus files, byte-identity) | PARTIAL (function tested; mounted route's 200 path never — F-09) | — | — |
| T-205 apply engine ★ | PASS (real pvemock, WS stream exact, file verifiably changed) | PASS (byte-identical restore; restart re-arm proven twice, incl. real timer) | PASS (all 5 positions, property asserted) | PASS (service level; HTTP 409 wire never asserted — F-11) | PASS (inverse draft + inverse diff asserted) | PASS (90.2%, race-clean, honest §4) |

Ground rules: slog-only — clean; `%w`-wrapped errors across boundaries — clean (bare `return err`
sites are typed in-package errors or pre-wrapped); no shadow-authoritative PVE state persisted
(changesets/snapshots/audit only; snapshots are point-in-time rollback artifacts, not authority);
all mutations flow through `internal/change` — verified, the API has no other write path.

## Findings — major

### F-01 · MAJOR · T-203 — guest-reattach interlock is value-blind and net-effect-blind (bypass, re-executed)
`validate_safety.go:184-191,200-207`: any `guest.nic.update` with a non-nil `bridgeOrVnet` counts as
a reattachment regardless of destination, and each `bridge.delete` checks attachment only against the
base snapshot. Two probes through the real `ValidateWithSafety` validate **clean** (zero error
findings) where docs/security.md demands a hard error: (a) "reattach" the NIC to the very bridge
being deleted; (b) reattach to vmbr3, then delete vmbr2 *and* vmbr3 in the same changeset. Net effect
in both: a running guest's bridge is deleted with the guest still attached.
**Fix:** fold `GuestNic.BridgeOrVnet` through the projection and evaluate each NIC's *final*
attachment: it must exist in the final projection and differ from every bridge the changeset deletes.

### F-02 · MAJOR · T-203 — protected-IP check accepts the management IP parked on a path-less new bridge (bypass, re-executed)
`delete vmbr0 (protected, mgmt IP); create vmbr9 with the same IP and no ports` validates fully clean
(probe C: only an advisory comment warning) although the net effect severs management connectivity.
`hasHostIP` (`validate_projection.go`) only asks whether the IP survives on *some* interface; the
bridge-path/port-count check applies only to protected refs present in the base snapshot.
**Fix:** in `protectedInterfaceFindings`, when the protected IP survives on a bridge (original or
newly created), additionally require that carrying bridge's final `portCount > 0`.

### F-03 · MAJOR · T-203 — AC2's chain analysis is proven only at class level and is order-fragile
`TestSafetyValidate_ChainAnalysis` calls the unexported `safetyValidate` directly. Re-run through the
real pipeline (probe D), the AC's scenario in create-new-bridge-first order is **rejected** with
`referential.address_overlap` (the new bridge's mgmt IP overlaps the not-yet-deleted vmbr0's) before
the safety class runs; only delete-first order validates clean — and that ordering transiently
leaves the mgmt IP nowhere. The criterion "moving the mgmt IP … in one changeset validates clean" is
not true of the shipped pipeline for the natural op order.
**Fix:** make the referential address-overlap check net-effect-aware for same-changeset deletes (or
emit a `fix` reordering hint); convert the AC2 test to assert clean through `ValidateWithSafety`.

### F-04 · MAJOR · T-202 — "no duplicate enslavement" deterministically never fires against pre-existing bonds (re-executed)
`newProjection` (`validate_projection.go:127-131`) seeds bond-slave enslavement by resolving slave
*names* through `p.names` in a single pass over `snap.All()` — which sorts by `Ref.String()`, so
`bond:…` entries are processed before `physnic:…` entries and the slave is never resolvable yet. The
enslavement index is therefore empty for every snapshot bond. Probe E: snapshot bond0 owns eno1;
`bond.create bond1 {slaves:[eno1]}` validates clean (expected `referential.duplicate_enslavement`).
Bridges are unaffected only because `Bridge.Ports` are already Refs. Applying such a changeset writes
a double-enslaved NIC into the interfaces file.
**Fix:** two-pass seeding (index all names first, then resolve bond slaves), plus a golden case for
the bond flavor (the existing case only covers the bridge flavor).

### F-05 · MAJOR · T-202 — projection delete branches leave stale indexes → apply-blocking false positives on delete-then-recreate (re-executed)
`deleteIface` (`validate_projection.go:317-326`) never clears `vlanIfaces`, and the
`SdnSubnetDeleteParams` fold branch clears `subnetNames` but not `subnetsByVnet`. Probe F:
`[vlan.delete vmbr0.20, vlan.create vmbr0.20 (vid 20)]` → blocking error `referential.vid_overlap`
("vid 20 is already in use") for a legitimate recreate draft; the same structure yields a false
`referential.address_overlap` for subnet delete-then-recreate (code-confirmed, same pattern). Errors
block apply, so these drafts are un-appliable. The T-202 report described fold's delete branches as
merely *uncovered*; they are *buggy*.
**Fix:** clear matching `vlanKey` entries in `deleteIface` and filter the ref out of
`subnetsByVnet` on subnet delete; add delete-then-recreate golden cases.

### F-06 · MAJOR · T-204 — CRLF corpus file gets LF lines injected; rendered diff is rejected by GNU patch
`optionItem`/`managedByComment` hardcode `\n` (`ifaces/entryutil.go:12-27`), so mutating
`14-crlf-no-trailing-newline.interfaces` produces a mixed-line-ending file that apply would write to
`/etc/network/interfaces`; `UnifiedDiff` also emits no `\ No newline at end of file` marker
(`diff.go:200-205` silently appends one), and for this fixture GNU `patch` fails the hunk (probe
verified). The renderer round-trips byte-exactly on the other 14 fixtures.
**Fix:** render new Raw lines with the stanza's/file's dominant terminator; emit the no-newline
marker; add fixture 14 to the golden matrix.

### F-07 · MAJOR · T-204 — AC1's golden matrix under-covers the corpus
Every op type has at least one committed byte-level golden (15 goldens, byte-exact compare), but only
corpus fixtures 01–04 are ever mutated. "Each op applied to each relevant corpus file" is not met:
OVS `bond.update` (`bond.go:81-105`) has zero byte-level coverage; Linux `bridge.delete` and
`bridge.port.remove` are golden-tested only in OVS form; hostile fixtures 08 (exotic comments — the
"comments preserved" clause), 09 (dual-stack), 14 (CRLF), 15 (messy brownfield) receive no write-op
golden at all.
**Fix:** add a golden row per (op, relevant-fixture) pair — relevance is mechanical (fixture contains
an entity of the op's kind).

### F-08 · MAJOR · T-204 — AC2's property test randomizes only 3 of 12 op types
`property_test.go` is genuinely seeded-random (300 iterations) and its reparse-and-inspect oracle is
meaningful, but only `bond.create`/`bridge.create`/`vlan.create` are randomized; update/delete/port
ops — where minimal-edit bugs live — get fixed-parameter cases only. The "inventory-level effect"
oracle is a `host.Entry` proxy, not `inventory.*` types (implementer flagged this half honestly).
**Fix:** extend the generator to pick random existing targets from the parsed fixture with random
update/delete params, keeping the same oracle.

### F-09 · MAJOR · T-204 — diff endpoint's success path is never tested through the mounted router; T-204's HTTP handler is dead code
No test drives `GET /changesets/{id}/diff` to a 200 through `internal/api` (the only route-level test
asserts 503-unconfigured); the multi-node three-node assertion (`TestDiffChangeset_ThreeNodeVlan`,
which is real and passes) tests `ifaces.DiffChangeset` directly. Meanwhile `ifaces.NewDiffHandler` /
`ChangesetLookup` / `ErrChangesetNotFound` (`ifaces/handler.go`) — the handler T-204 built and tested
returning 200 — has zero production references; the wired path is T-205's `Service.Diff`. (The live
curl probe in this audit did return a correct 200 diff, so the wiring works; it's just unpinned by
any test.)
**Fix:** add an `internal/api` test with a configured apply engine + three-node harness asserting 200
and the documented body; delete or actually mount `NewDiffHandler`.

## Findings — minor

### F-10 · minor · T-205 — no retention-window check on rollback of a committed changeset
docs/features/change-management.md §4: manual rollback of a committed changeset "is offered for 7
days"; docs/api.md: "also valid on `committed` **within retention**". `Service.Rollback`
(`apply.go:252-254`) accepts a committed changeset of any age. Not mentioned in the T-205 report's
deviations (retention *pruning* is T-206, but the gate belongs to this route).
**Fix:** reject with 409 when `now - updatedAt > 7d` (constant shared with T-206's pinning policy).

### F-11 · minor · T-205 — HTTP-layer apply contract untested; report overstates "API error mapping" coverage
`writeApplyError` is 53% covered and `handleApplyChangeset` 57% — only the 503 `apply_unavailable`
path is asserted (`TestChangesetsApplyRoutes_Unconfigured`). The `202 Accepted` success shape, `409
changeset_locked`, and `422 validation_failed`-with-findings wire responses are never asserted at the
HTTP layer (AC4's "→ 409 changeset_locked" is proven via the typed error + mapping code, not the
wire). The T-205 report's "additional coverage: … the API error mapping" is overstated.
**Fix:** one API test with the apply harness driving 202/409/422/404.

### F-12 · minor · T-203 — apply-time `allow_dangerous_ops` use is not audited
`beginApply` (`apply.go:94`) correctly revalidates through `safetyOptions()`, but never calls
`auditSafetyOverride` — a changeset applied *because* the flag downgraded interlock errors leaves no
apply-time override audit entry (card: "its use audited"; only create/validate-time entries exist).
**Fix:** call `s.auditSafetyOverride(ctx, author, id, findings)` after the revalidation in `beginApply`.

### F-13 · minor · T-203 — `PUT /protected-interfaces` cannot work in the dev environment; no config seam
`server.go:106` hardcodes `change.DefaultProtectedPath` (`/etc/pve/vnprox/protected.json`);
`internal/config` and `testdata/dev.toml` have no override, so PUT 500s on `MkdirAll` (EACCES)
outside a real PVE node. GET degrades gracefully.
**Fix:** add a `protected_path` config option (defaulting to the pmxcfs path) and a `var/`-relative
dev.toml value.

### F-14 · minor · T-203 — protected-interface *detection* is dead code
`change.DetectProtected` and `host.ReadCorosyncConf` have zero non-test callers — the card's
"detection of protected interfaces per node" deliverable is unreachable in the running daemon (no
suggest endpoint, no onboarding wiring), and the parse→detect composition is never tested together.
The report admits the missing endpoint but frames detection as delivered.
**Fix:** wire a suggest endpoint (or the onboarding task must), and add one integration test
composing the realistic corosync fixture with `DetectProtected`.

### F-15 · minor · T-203 — `SetProtected` accepts node-key/ref mismatches
`{"nodes": {"pve1": ["bridge:pve2:vmbr0"]}}` persists fine (`service.go:436-457` checks only
parseability); the validator would then test pve2's bridge against pve1's address table — silently
meaningless. **Fix:** reject or normalize entries where `ref.Node != map key`.

### F-16 · minor · T-202 — fix corpus and auto-validation test-rigor gaps
(a) The fix property corpus covers 11 of 14 fix-emitting paths (missing: bridge.update MTU clamp,
bridge.update Vids clamp, vlan.create MTU clamp — probed, the property holds, but it's unenforced);
the report's coverage claim is inaccurate for bridge.update. (b) Auto-validation on draft mutation
has no positive test — the existing test asserts `hasError == false`, which would also pass if
`Create` never validated; no `UpdateDraft` test asserts recomputed findings.
**Fix:** 3 corpus entries; one known-bad-op create/update test asserting non-empty findings.

### F-17 · minor · T-202 — the AC4 benchmark workload exercises no referential cross-checking
`hundredOps()` is 100 independent `bridge.create`s (comments+MTU only) on a near-empty snapshot, so
the O(n²) paths (`overlappingAddr`, `checkVIDOverlap`) degenerate to map lookups. A probe with
addresses+ports+VID ranges on a 100-NIC snapshot measured 1.37ms — still ~145× under budget, so the
AC stands — but the shipped benchmark can't catch a referential-pass regression.
**Fix:** adopt the heavier workload as the benchmark body.

### F-18 · minor · T-201 — wire-contract and audit-detail assertions missing
(a) No test asserts the WS topic `"changesets"` or `event == "changeset.status"` — fakes record but
never check them; a typo would pass every Go test and break the frontend. (b) `GET
/changesets?status=` is untested at the HTTP layer (service/repo level only). (c) Audit `detail_json`
content (`{"title","opCount"}`) is never asserted. (d) One duplicate broadcast: a validation-blocked
apply on a still-`draft` changeset broadcasts `draft` with no transition (`apply.go:98-106`).
**Fix:** one topic/event/payload assertion; one `?status=` request; one detail assertion; guard the
broadcast on `prevStatus != cs.Status`.

### F-19 · minor · T-201 — `details.path` doesn't identify which op failed
For a multi-op body, an unknown param field yields `"params.mtuu"` with no op index — ambiguous when
op 7 of 40 is bad. **Fix:** prefix `ops[i].` to the path at the API decode layer.

### F-20 · minor · T-201 — CSRF enforcement is fail-open by type assertion
If the `AuthService` doesn't implement `CSRFEnforcer`, mutating changeset/protected routes mount
silently without CSRF (`changesets.go:147-149`; acknowledged in a comment). Production is safe only
because the adapter embeds `*auth.Service`; a refactor would drop CSRF with no compile error or test
failure. **Fix:** `var _ api.CSRFEnforcer = authServiceAdapter{}` in cmd/vnproxd, and/or refuse to
mount mutating routes when the assertion fails outside tests.

### F-21 · minor · T-204 — silent golden bootstrap
`checkGolden` writes-and-passes when the golden file is missing, so a deleted/renamed golden silently
regenerates and passes in CI (plus the `VNPROX_UPDATE_GOLDEN=1` bypass). Committed runs do
byte-compare today. **Fix:** `t.Fatal` on missing golden; gate regeneration solely behind the env var.

### F-22 · minor · T-204/T-205 — the documented local-only NodeAgent constraint is unenforced, and `make dev` wires the production agent
`hostNodeAgent.ReadInterfaces` ignores its `node` argument (`changeagent.go:63-71`). The T-304
deferral is documented (accepted residual), but nothing *enforces* "do not route peer-node file steps
to it": on a real cluster today a peer-node draft would silently diff/stage against the local file.
Verified live: this audit's dev-daemon diff for "pve1" rendered against the dev box's real
`/etc/network/interfaces` — which also means `make dev` + a root shell is one POST away from
ifreloading the developer's machine.
**Fix:** have `hostNodeAgent` fail closed for non-local node names until T-304; consider a
fixture-backed NodeAgent for the dev stack.

### F-23 · minor · hygiene — comment/report accuracy regressions (phase-1's recurring failure class)
(a) `planning/reports/T-201.md` quotes card text ("a new sqlite migration + repo for changesets…",
"reuse whatever WS hub T-106 built…") that appears nowhere in phase-2.md. (b)
`ifaces/changeset.go:47-49` claims the card asks for diff logic "testable and callable independently
of the HTTP route" — no such phrase; the card said "wired". (c) `protected.go:25-27` cites a
docs/features/blueprints.md "versioned format" convention and a `blueprintVersion` identifier that
exist nowhere. (d) Stale comments: `internal/api/changesets.go:32-37,114-120` still say
diff/apply/confirm/rollback "remain registered but stubbed 501" in the same file that implements
them; `apply_seams.go:27-30` states the production NodeAgent "routes … peer-node writes to that
peer's daemon" (it doesn't — contradicted by changeagent.go's own honest note);
`validate_codes.go:8` references a nonexistent `classPipeline`. (e) Report arithmetic: T-202 "49
cases plus 5 clean" (actual: 52 incl. 6 clean); T-204 "14 golden fixtures"/"34 test functions"
(actual: 15/47). None of these change behavior; per the phase-0/1 decisions of record
(comment-hygiene rule), fix the comments and annotate the reports.

## Hygiene

- `make check` green end to end at HEAD (re-run during this audit); `go vet` clean; `-race` clean on
  every phase-2 package; repo left untouched (probes ran via `go test -overlay` from the scratchpad;
  the live daemon used gitignored `var/` state).
- Coverage: `internal/change` 90.2% (AC met); `internal/change/ifaces` 90.5% (reproduced). The ~39
  compile-time-only `isChangeParams()` markers dilute the denominator; the real logic sits higher.
- The five self-reports were substantially honest — every T-205 claim re-executed true, and T-202/
  T-203/T-204's admitted deviations all check out — but all five audits found *unadmitted* gaps, and
  the fabricated-quote pattern (F-23) now has six phase-2 instances after three in phase 1. Consider
  making "verify every quoted authorization against the card" a standing reviewer checklist item.
- Recommended fix order: F-04/F-05 (validator correctness) → F-01/F-02/F-03 (interlock bypasses; they
  depend on the projection fixes) → F-06 (writer corruption) → the rest. T-206/T-207 (in flight) are
  not blocked, but T-207's drawer UX will surface F-05's false positives immediately.
