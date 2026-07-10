# Phase 2 audit remediation — completion report

**Date:** 2026-07-10 · **Base:** 6939449 · **Scope:** findings F-01…F-23 in
`planning/reports/audit-phase-2.md`, per the assigned remediation scope (T-206-owned and
T-304-owned halves explicitly excluded to avoid colliding with the concurrent T-206/T-207
worktrees).

## How the work was organized

Two agents on `main` (no file overlap): the orchestrator took the validator/interlock core
(`internal/change/*.go`), `internal/api` (except one new test file), `internal/config`,
`cmd/vnproxd`, `testdata/dev.toml`, and docs/reports; a fix agent took the T-204 territory
(`internal/change/ifaces/**` plus the new `internal/api/diff_route_test.go`). Every fix follows
the audit's own re-execution probe: a regression test that fails on the pre-remediation code and
passes after.

## Per-finding disposition

### Major — fixed

- **F-01 (guest-reattach interlock value/net-effect-blind)** — FIXED. `guestBearingBridgeFindings`
  (`internal/change/validate_safety.go`) rewritten to be net-effect-based: it folds the whole
  changeset into a final projection, computes every running guest NIC's *final* attachment (last
  `guest.nic.update` wins, else the snapshot value), and errors on any `bridge.delete` whose net
  effect leaves a NIC attached to nothing that survives — including "reattachment" to the doomed
  bridge itself and reattachment to a target another op in the same changeset deletes. Findings
  attribute to the delete op that dooms the final target. Tests: probes A/B as
  `TestSafetyValidate_ReattachToDoomedBridge_StillErrors` /
  `TestSafetyValidate_ReattachTargetAlsoDeleted_StillErrors`, plus surviving/new-bridge controls
  (`internal/change/audit_phase2_regression_test.go`). The pre-existing "reattaching every guest
  clears the error" fixture had to gain a real `vmbr3` — under the old value-blind check it passed
  while reattaching to a *nonexistent* bridge, which is precisely the F-01 bug.
- **F-02 (mgmt IP parked on a path-less bridge)** — FIXED. `protectedInterfaceFindings` now also
  checks *what carries* a surviving protected IP: if it moved to a bridge other than its original
  protected carrier and that bridge's final port count is 0, that is a hard
  `safety.protected_interface` error. Test: probe C as
  `TestSafetyValidate_MgmtIPOnPortlessNewBridge_Errors` + ported-bridge control.
- **F-03 (AC2 order-fragility / referential net-effect)** — FIXED. `referentialValidate` records
  every iface delete's position (`projection.pendingDelete`) and the address-overlap and
  duplicate-enslavement checks ignore conflicts with an entity the same changeset deletes *later*
  (`deletedLater`); a doomed entity re-created after its delete re-enters conflict detection
  (asserted by `TestValidateWithSafety_DoomedOwnerRecreated_ConflictRestored`). The AC2 mgmt-IP
  migration (create-first *and* delete-first, ports moved along) now validates clean through the
  full `ValidateWithSafety` pipeline: `TestValidateWithSafety_MgmtIPMigration_BothOrders_Clean`,
  plus a no-delete control proving genuine overlaps still error. The original class-level
  `TestSafetyValidate_ChainAnalysis` stands; the new test is the pipeline-level AC2 assertion the
  audit asked for.
- **F-04 (duplicate enslavement never fires vs snapshot bonds)** — FIXED. `newProjection` seeds in
  two passes (index every iface name first, then resolve bond slave names), so snapshot bonds'
  slaves resolve regardless of `snap.All()` ordering. Tests: probe E as
  `TestValidate_DuplicateEnslavement_SnapshotBondSlave` (+ bridge-steals-bond-slave flavor), and a
  new bond-flavor golden case in `goldenCases()` (`validate_test.go`).
- **F-05 (stale indexes on delete branches)** — FIXED. `deleteIface` now clears matching
  `vlanIfaces` entries; the `SdnSubnetDeleteParams` fold branch now also removes the subnet from
  `subnetsByVnet`. Tests: probe F as `TestValidate_VlanDeleteThenRecreate_Clean`,
  `TestValidate_SubnetDeleteThenRecreate_Clean`, plus a no-delete overlap control.
- **F-06 (CRLF corruption + missing no-newline marker)** — FIXED (fix agent). Rendered lines use
  the file's dominant line terminator (`dominantEOL`, `ifaces/entryutil.go`); `UnifiedDiff` emits
  `\ No newline at end of file`; fixture 14 joined the golden matrix; GNU `patch --binary`
  reproduces the mutated CRLF file byte-for-byte from the rendered diff
  (`TestUnifiedDiff_CRLFFixtureAcceptedByGNUPatch` — skips on hosts without patch(1); it ran and
  passed here). Fail-before demonstrated against the pre-fix renderer.
- **F-07 (golden matrix under-coverage)** — FIXED (fix agent). 8 new goldens
  (`ifaces/golden_matrix_test.go`): OVS `bond.update`, Linux-form
  `bridge.delete`/`bridge.port.remove`, and a write-op golden on each hostile fixture 08/09/14/15.
  Two behaviors pinned by these goldens are noted as accepted quirks, not bugs:
  (a) removing a bridge's *only* port drops the `bridge-ports` line entirely, where PVE itself
  would write `bridge-ports none` — functionally equivalent to ifupdown2, worth aligning when
  T-204's renderer is next touched; (b) on fixture 08, an inserted `mtu` option lands after a
  trailing comment the parser attributes to the open stanza — placement is unusual to the eye but
  correct per interfaces(5) semantics.
- **F-08 (property test randomizes 3/12 op types)** — FIXED (fix agent). The generator now also
  randomizes update/delete/port ops against random existing targets from the parsed fixture, same
  reparse-and-inspect oracle, still seeded/deterministic.
- **F-09 (diff endpoint 200 path untested; dead handler)** — FIXED (fix agent).
  `internal/api/diff_route_test.go` drives `GET /changesets/{id}/diff` to a 200 through the mounted
  router with a configured apply engine and asserts the documented body; the dead
  `ifaces.NewDiffHandler`/`ChangesetLookup`/`ErrChangesetNotFound` were deleted.

### Minor — fixed

- **F-12** — FIXED. `beginApply` calls `auditSafetyOverride` after the pre-apply revalidation, so
  an apply that proceeds only because `allow_dangerous_ops` downgraded interlock errors leaves an
  apply-time audit entry. Test: `TestApply_AllowDangerousOps_AuditsOverrideAtApplyTime` (protected
  set lands *between* create and apply, so exactly the apply-time entry is asserted; verified
  fail-before by reverting the call).
- **F-13** — FIXED. New `[safety] protected_path` config option (default
  `/etc/pve/vnprox/protected.json`), wired through `cmd/vnproxd/server.go`; `testdata/dev.toml`
  sets `var/dev-protected.json`. Tests: `TestLoad_ProtectedPath` (internal/config) and
  `TestDefaultProtectedPath_MatchesConfigPackage` pinning the duplicated constant.
- **F-14** — FIXED (wired as the card intended). New `Service.SuggestProtected` composes
  `host.ReadCorosyncConf` (new `change.Config.CorosyncPath`, default pmxcfs path; missing file
  degrades to mgmt-IP-only) with the live inventory snapshot through `DetectProtected`; exposed as
  `GET /protected-interfaces/suggest` (netRead). Tests:
  `TestService_SuggestProtected_ComposesCorosyncAndInventory` (realistic corosync fixture +
  two-node snapshot), the missing-file fallback test, and the mounted-router test
  `TestProtectedRoutes_Suggest`. `DetectProtected`/`ReadCorosyncConf` are no longer dead code.
- **F-15** — FIXED. `SetProtected` rejects (400 via `*ErrInvalidProtectedRef`) any ref whose
  embedded node differs from its map key. Test: `TestService_SetProtected_RejectsNodeKeyRefMismatch`.
- **F-16** — FIXED. (a) Three fix-corpus entries added (bridge.update MTU clamp, bridge.update
  Vids clamp, vlan.create MTU clamp) — all 14 fix-emitting paths now enforced by
  `TestValidate_FixProperty`. (b) `TestService_AutoValidation_ComputesFindingsOnMutation` asserts
  the positive half: known-bad op ⇒ non-empty findings on Create, recomputed on UpdateDraft,
  cleared when replaced.
- **F-17** — FIXED (adjacent to F-16 in the same file, so taken along despite not being on the
  assigned list). `hundredOps`/`BenchmarkValidate_100Ops` now use the audit's heavier workload:
  unique addresses, unique snapshot-NIC enslavements, VID trunk lists, against a 100-NIC +
  50-addressed-VLAN snapshot. Measured ~2.5ms/100 ops — still ~80× under the 200ms budget; the
  hard-asserting test keeps passing.
- **F-19** — FIXED. New `opsField` wrapper in `internal/api/changesets.go` decodes the `ops` array
  element-wise and prefixes decode-error paths with `ops[i].`. Existing single-op tests updated to
  the indexed shape; new multi-op regression
  `TestChangesetsRoutes_CreateBadOpInMultiOpBody_IndexedPath`. docs/api.md notes the indexed path.
- **F-21** — FIXED (fix agent). `checkGolden` now `t.Fatal`s on a missing golden file;
  regeneration (creating missing or rewriting existing goldens) happens only under the explicit
  `VNPROX_UPDATE_GOLDEN=1` mechanism. `ifaces/golden_helper_test.go` pins the fail-on-missing
  behavior with a fatal-recording TB; the pre-existing 15 goldens were verified byte-identical
  before/after the remediation.
- **F-22, `make dev` half** — FIXED. New `[safety] dev_interfaces_dir` option: when set (dev.toml:
  `var/dev-host`), `cmd/vnproxd` wires `newDevNodeAgent` — same staging/commit/backup logic
  sandboxed under that directory (seeded fixture) with ifreload as a logged no-op — instead of the
  production host agent, and logs a prominent DEV MODE warning. A `make dev` daemon can therefore
  never read, stage, or ifreload the real `/etc/network/interfaces`. Tests: `TestNewDevNodeAgent`
  (sandboxed paths, seed, no-op reload, no re-seed over edits) and
  `TestDevConfig_SandboxesHostWriter` (pins dev.toml itself).
- **F-23 (comment/report hygiene)** — FIXED, with one correction to the audit:
  - (a) `planning/reports/T-201.md`: correction section appended identifying the two fabricated
    card quotes (WS-hub allowance, sqlite-migration deliverable).
  - (b) `internal/change/ifaces/changeset.go` comment rewritten without the fabricated card quote
    (fix agent).
  - (c) **Audit finding is itself inaccurate — no change.** `docs/features/blueprints.md` line 10
    literally reads: “Format is versioned (`"blueprintVersion": 1`)…”, verified at HEAD 6939449
    (`git show HEAD:docs/features/blueprints.md`). `protected.go`'s citation of the "versioned
    format" convention and `blueprintVersion` is genuine; recorded here so the false positive
    isn't "fixed" into a worse comment.
  - (d) `internal/api/changesets.go` stale "registered but stubbed 501" comments rewritten (the
    routes are real since T-205); `apply_seams.go` no longer claims the production NodeAgent routes
    peer-node writes (honest "T-304 scope, not implemented; must not be handed peer steps" note
    pointing at `hostNodeAgent`'s constraint); `validate_codes.go` no longer references the
    nonexistent `classPipeline`.
  - (e) Count corrections appended to `T-202.md` (52 cases incl. 6 clean) and `T-204.md`
    (15 fixtures / 47 test functions, plus a note that `handler.go` was dead code, now deleted).

### Accepted / deferred (recorded, not fixed here)

- **F-10 (retention gate on rollback of committed)** — DEFERRED to the in-flight **T-206**
  worktree, which owns retention/pinning; fixing it here would conflict with that agent's
  territory. The 7-day constant must be shared with T-206's pruning policy.
- **F-11 (HTTP-layer apply contract: 202/409/422/404 wire shapes)** — DEFERRED: explicitly listed
  out of scope for this remediation; the service-level behavior and error mapping are tested, only
  the wire assertions are missing. Suggested home: one API test alongside the new
  `diff_route_test.go` harness (T-208 checkpoint candidate).
- **F-18 (T-201 wire-contract/audit-detail assertions, duplicate draft broadcast)** — DEFERRED:
  explicitly out of scope; test-rigor gaps plus one redundant (not incorrect) WS broadcast.
- **F-20 (CSRF fail-open by type assertion)** — DEFERRED: explicitly out of scope; production is
  safe today (`authServiceAdapter` embeds `*auth.Service`). The one-line
  `var _ api.CSRFEnforcer = authServiceAdapter{}` guard in cmd/vnproxd is a good first-commit
  candidate for whoever touches that file next.
- **F-22, peer-enforcement half (NodeAgent fails closed for non-local nodes)** — DEFERRED to
  **T-304** per the audit's accepted residual; the misleading `apply_seams.go` comment that
  *claimed* peer routing exists was fixed under F-23(d), and `hostNodeAgent`'s doc comment already
  states the constraint honestly.

## Behavior changes reviewers should know about

- Referential validation is now net-effect-aware for same-changeset deletes: drafts that
  transiently "overlap" an entity deleted later in the same changeset validate clean (T-203 AC2's
  natural order). Per-node file ops of one changeset are applied as a single staged write +
  ifreload, so no transient duplicate state reaches the network.
- The guest-bearing-bridge error message changed (now speaks of the changeset's *final state* and
  is attributed to the delete op dooming the NIC's final target).
- `POST/PUT /changesets` decode errors now return op-indexed `details.path` (`ops[3].params.mtu`).
- New API route `GET /protected-interfaces/suggest`; the protected-interfaces routes are now
  documented in docs/api.md.
- New config keys: `[safety] protected_path`, `[safety] dev_interfaces_dir` (dev-only).
- `make dev`'s daemon now operates on `var/dev-host/interfaces` with no-op reloads — apply flows
  are exercisable in dev without root risk.

## Notes for the T-206 / T-207 / T-208 agents

- **T-206:** F-10 is yours: gate `Service.Rollback` on committed changesets older than the 7-day
  retention window (409), sharing the constant with your pruning policy. Nothing in this
  remediation touched retention, snapshots, or pruning.
- **T-207:** F-05's false positives on delete-then-recreate drafts are gone; the drawer can rely on
  `vid_overlap`/`address_overlap` errors being genuine. Decode errors now carry op-indexed paths —
  useful for highlighting the offending op card. The suggest endpoint
  (`GET /protected-interfaces/suggest`) exists if onboarding UI needs it.
- **T-208 (or next checkpoint):** deferred F-11/F-18/F-20 above are the remaining known test-rigor
  gaps; the audit's F-23(c) is recorded as a false positive with evidence.

## Verification

- `go test -race -count=1 ./internal/change/... ./internal/api/ ./internal/config/
  ./cmd/vnproxd/` — green (validators, interlocks, apply engine, ifaces incl. the extended golden
  matrix and property test, API routes, config, daemon wiring).
- `make check` (gofmt + golangci-lint, full Go + Vitest suites (100/100), govulncheck, npm audit)
  — green at completion.
- The GNU-patch acceptance test requires patch(1) and skips where absent; it executed (and passed)
  in this environment.
- Every audit probe (A–F and the minor-finding reproductions) exists as a committed regression
  test that was run against the pre-fix code and observed failing (F-12's fail-before was verified
  by reverting the one-line fix; F-06/F-21 fail-before verified by the fix agent).
