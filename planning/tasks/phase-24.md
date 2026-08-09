# Phase 24 — Operator leverage

**Roadmap:** [`docs/roadmap-leverage.md`](../../docs/roadmap-leverage.md) ·
**Plan:** [`../implementation-plan-leverage.md`](../implementation-plan-leverage.md)

Context for every card in this phase: `docs/architecture.md`, `docs/development.md`,
`docs/api.md`, `docs/data-model.md`.

**Three candidates were dropped before decomposition because the audit found them already
shipped** — post-confirm revert (`change/apply_restore.go`), standalone map export
(`web/src/topology/ExportMapMenu.tsx`), and no-self-approval (`ApprovalPolicy.AllowSelfApproval`).
Do not re-add them; if a report claims one is missing, the report is wrong.

---

## T-2401 · Scheduled automatic config snapshots ★

**kind:** implementation · **depends on:** —
**context:** `docs/features/change-management.md` §4, `internal/change/apply_snapshot.go`,
`internal/change/snapshots_service.go`

Today a snapshot exists only where vnprox itself acted: `pre`/`post` around an apply, or
`manual` when someone clicks. The class of change vnprox is *least* able to undo is therefore
the one it did not make — an `ssh node && vi /etc/network/interfaces && ifreload -a`. The drift
checker reports it within a cycle; there is no restore point from before it.

Add a `scheduled` snapshot kind captured on a configurable interval, with its own retention
ceiling, reusing `captureSnapshot`'s existing cluster fan-out unchanged.

- Config: `[snapshots] schedule_interval` (duration, `0` = off) and `schedule_keep` (count).
  Default **off**, because a snapshot is a full read of every node's interfaces file and the
  operator should opt in.
- **De-duplicate by content.** If every node's file is byte-identical to the newest existing
  `scheduled` snapshot, record nothing. An idle cluster must not accumulate identical rows.
- Retention prunes only `scheduled` rows, oldest first. It must never prune a `pre`, `post`, or
  `manual` snapshot — those belong to a changeset or to a human.
- Restore uses the existing `POST /snapshots/{id}/restore`; no new restore path.

**Acceptance**

1. With the interval set, a snapshot appears on tick, and its files match the nodes' content.
2. **A second tick with unchanged content creates nothing**, and the assertion is that the
   snapshot *count* is unchanged — not merely that no error occurred.
3. Changing one node's content between ticks produces exactly one new snapshot carrying **every**
   node's file, not only the changed one.
4. With `schedule_keep = N`, the N+1th distinct snapshot prunes the oldest — and a test with
   interleaved `manual` and `pre` rows asserts those survive untouched.
5. Interval `0` runs no loop at all (assert via a fake clock that never fires, not by absence of
   output).
6. Retention and interval are read from config; a malformed value fails startup with a message
   naming the key.

## T-2402 · Finding acknowledgement and mute ★

**kind:** implementation · **depends on:** —
**context:** `internal/findings/engine.go`, `internal/findings/hysteresis.go`,
`docs/features/monitoring.md` §5

43 checks across 15 sources with no triage surface. A finding that is understood and deliberate —
a deliberately asymmetric MTU, a bridge with no guests on a staging node — reappears on every
cycle forever, and the only way to stop looking at it is to stop looking at the stream.

- `POST /findings/{id}/ack` `{reason, expiresAt?}` and `DELETE /findings/{id}/ack`.
- Keyed on the finding's **stable ID**, which `hysteresis.go` already guarantees survives across
  cycles — not on a row identity that a recompute would invalidate.
- A reason is **required**. An acknowledgement with no reason is an unexplained silence.
- Expiry is optional; absent means "until un-acked". An expired ack stops applying without any
  cleanup job having to run — compute it at read time, so a stopped daemon cannot leave a finding
  muted past its date.
- Both directions are audited (`finding.ack` / `finding.unack`) with the reason in the entry.
- Acked findings are **not removed**. `GET /findings` returns them with `ack` populated;
  `?acked=only|exclude` filters. The open-count metric excludes them and a new
  `vnprox_findings_acked` counts them.

**Acceptance**

1. Ack survives a full recompute cycle: same finding, same stable ID, still acked.
2. An ack whose `expiresAt` is in the past does not apply, **with no cleanup having run** — prove
   it by setting the expiry directly in the store and reading back.
3. Ack with an empty or whitespace-only reason is refused with `validation_failed`.
4. Acking a finding that does not exist is refused rather than silently creating a dangling row.
5. `vnprox_findings_open` drops by exactly one on ack and returns on un-ack; `..._acked` mirrors it.
6. A finding whose condition *clears* and later returns is **not** silently still-acked past its
   expiry — the ack applies to the ID, and the test states which behaviour is intended and asserts it.

## T-2403 · Entity change history ("blame") ★

**kind:** implementation · **depends on:** T-2401, T-2404
**context:** `internal/api/audit.go`, `internal/change/changeset.go`, `internal/topology/detail.go`

Standing on any entity in the inspector, there is no way to ask "what has been done to this, and
by whom." The data exists — every op carries its target and every changeset carries its author —
but it is only reachable by reading the whole audit list and filtering by eye.

- `GET /inventory/{ref}/history` → newest-first entries, each `{kind, at, actor, changesetId,
  summary, opId?}`, merging: changesets containing an op targeting `ref`, audit rows whose target
  is `ref`, and snapshots covering `ref`'s node.
- Paginated with the same cursor convention as `/audit`.
- Inspector gains a **History** tab; each row deep-links to the changeset, audit entry, or snapshot.

**Acceptance**

1. A changeset with an op targeting `vmbr0` appears in `vmbr0`'s history and **not** in `vmbr1`'s.
2. Ref parsing is `PathUnescape`d — the exact defect that made guest-interior return 400 to every
   browser request. A test uses a ref containing `/` and `:`.
3. Ordering is strictly newest-first across all three sources merged, asserted with interleaved
   timestamps rather than three same-source rows.
4. Requires the same capability as `/audit`; a caller without it gets 403, not an empty list.
5. An unknown ref returns an empty page, not 500.

## T-2404 · Blast-radius preview before apply ★

**kind:** implementation · **depends on:** —
**context:** `internal/change/apply_plan.go`, `internal/change/mgmttouch.go`,
`internal/change/protected.go`

The diff says *what changes*; nothing says *who notices*. `touchesMgmtPath` already computes the
most important half of this and uses it only to gate scheduling.

- `Impact{Nodes, Guests, Bridges, TouchesMgmtPath, Disruption}` computed from the ops plus the
  live inventory graph, returned on `GET /changesets/{id}/diff` and shown in the apply dialog.
- `Disruption` is a per-op enum — `none`, `brief` (a reload that re-creates an existing carrier),
  `outage` (a delete, or a bridge losing its last uplink) — with the **reason** attached, so the
  UI never asserts an impact it cannot explain.
- Guests are those attached to an affected bridge, resolved through the inventory graph, and are
  reported per-node so a multi-node changeset is legible.

**Acceptance**

1. A changeset deleting a bridge with three guests attached reports those three guests and
   `outage`, naming the bridge as the reason.
2. An MTU-only update on an unused bridge reports `brief` and zero guests.
3. `TouchesMgmtPath` agrees with `mgmttouch.go` on every case that file already tests — asserted
   by driving both from one table, so the two can never diverge silently.
4. Impact is computed **server-side**. A test asserts the UI cannot supply or override it.
5. An empty changeset yields a zero impact, not a nil-deref.

## T-2405 · OpenAPI 3.1 document and completeness gate ★

**kind:** implementation · **depends on:** —
**context:** `docs/api.md`, `internal/apicontract/`

186 routes described only in 1,316 lines of hand-written Markdown. That is the missing
prerequisite under `T-2101` (Terraform/Ansible), generated clients, and `T-2105`'s docs site — and
it is why a route can be added today with no mechanical signal that the contract grew.

- A machine-readable OpenAPI 3.1 document served at `GET /api/v1/openapi.json` (unauthenticated —
  it is a contract, not data) and written to `docs/openapi.json` by `make openapi`.
- **The gate is the point.** A test walks the chi router's *real* registered routes and fails
  naming any route absent from the document, and any document path absent from the router. A new
  endpoint cannot ship undocumented.
- Frozen-field regression: the document is committed, so a removed field shows up as a diff in
  review — the general form of `T-2002-bug-01`.

**Acceptance**

1. The gate fails when a route is added to the router and not the document — proven by adding one
   in the test, not by asserting the current state passes.
2. The gate fails in the **other** direction too, for a documented path the router does not serve.
3. The served document parses as valid OpenAPI 3.1 (validated structurally, not merely as JSON).
4. `/openapi.json` is reachable without a session; every other route it describes is not.
5. Path templating matches chi's (`{id}`, not `:id`) — asserted against a real registered pattern.

## T-2406 · `vnproxctl doctor --live` ★

**kind:** implementation · **depends on:** — · **closes:** `T-1904-followup-02`
**context:** `internal/doctor/`, `docs/deployment.md`

Four of `doctor`'s ten checks — `pve_reachable`, `pve_privileges`, `clock_skew`, `peer_secret` —
are implemented and tested and report `skip` from the CLI, because they need a running daemon.
Six-of-ten green is a weaker signal than it looks, and `T-1904` was explicit that `skip` is not
`pass`.

- `--live` asks the local daemon over its admin socket/API and reports the real verdict.
- Without `--live`, behaviour is unchanged: still `skip`, still with the reason.
- With `--live` and no daemon reachable, the check reports `skip` **naming the daemon** as the
  reason — never `fail`, which would blame the wrong thing.
- Adds the certificate/SAN preflight `T-1906-bug-01` asked for, since the certificate inventory now
  exists.

**Acceptance**

1. All ten checks return a non-`skip` verdict against a live fixture daemon.
2. Daemon down + `--live` → `skip` naming the daemon; **not** `fail`, asserted explicitly.
3. Each newly-wired check has a broken fixture proving it can `fail` — the `T-1904` bar.
4. `Report.Validate()` still refuses a `fail`/`warn` without remediation, for the new checks too.
5. Exit codes are unchanged for the pre-existing paths.

## T-2407 · Alert quiet hours and digest coalescing ★

**kind:** implementation · **depends on:** T-2402
**context:** `internal/automation/`, `internal/findings/webhook.go`, `internal/api/alertrules.go`

An alert rule fires per event. A flapping uplink is a hundred events, so the rule that was
supposed to page someone trains them to mute the channel — the same failure T-2402 addresses for
the stream, one layer out.

- Per-rule `quietHours` (local-time windows, with the deployment's zone stated explicitly) during
  which delivery is **deferred, not dropped**.
- Per-rule `digest` window: coalesce events within it into one delivery summarising counts by
  severity and source.
- **Critical severity bypasses quiet hours** by default, overridable per rule — and the default is
  asserted by a test, because getting it backwards is silent.
- Deferred and coalesced deliveries are audited, so "we never got paged" is answerable.

**Acceptance**

1. Ten events inside one digest window produce exactly **one** delivery naming ten.
2. An event during quiet hours is delivered after the window ends, not dropped — assert the
   delivery, not the absence.
3. A `critical` event during quiet hours is delivered immediately under the default policy.
4. Windows crossing midnight work (22:00–06:00), asserted at 23:00 and 05:00.
5. DST transition does not double-deliver or drop — driven by a fake clock over a real zone.

## T-2408 · Batch-fix findings into one changeset ★

**kind:** implementation · **depends on:** T-2402
**context:** `internal/api/findings.go`, `internal/findings/engine.go`

`POST /findings/{id}/fix` stages one changeset per finding. Twenty fixable findings is twenty
changesets, twenty reviews and twenty confirm windows, which is why nobody fixes twenty findings.

- `POST /findings/fix` `{ids: [...]}` → one draft changeset carrying every fixable finding's ops.
- **Refuses the whole batch** if any id is unknown, unfixable, or acked — partial success here is
  a trap, because the caller cannot tell from the response which half applied.
- Conflicting ops (two findings proposing different values for one field) are refused with both
  finding ids named, not silently last-write-wins.
- Stages only. Apply is unchanged and still goes through review/approval.

**Acceptance**

1. Three fixable findings produce **one** changeset containing all three findings' ops.
2. One bad id in a batch of three stages **nothing** — assert the changeset count is unchanged.
3. Two findings targeting the same field with different values are refused naming both ids.
4. An acked finding is refused, so the batch cannot silently undo a deliberate decision.
5. The resulting changeset is an ordinary draft: it validates, diffs and requires approval like
   any other.

## T-2409 · Per-spec e2e store isolation · **closes `T-2108-followup-01`**

**kind:** implementation · **depends on:** —
**context:** `web/e2e/`, `docs/development.md`

36 specs share one `vnproxd` and one mutable store. That is what let drawer and timeline state
leak across files during the `T-2108` triage and turn latent ambiguous locators into failures —
and it means a spec's pass depends on which specs ran before it.

**Acceptance**

1. Each spec file gets a store whose mutations are invisible to every other spec.
2. Proven by a **deliberate cross-contamination test**: one spec creates a changeset, another
   asserts it cannot see it. This must fail on the current shared-store arrangement.
3. Full-suite wall-clock does not regress by more than 25%; report the actual figure.
4. `--repeat-each=2` is clean, which it is not today.

## T-2410 · Packaging matrix `cluster-ssh` root cause · **closes `T-1806-bug-02`**

**kind:** investigation · **depends on:** —
**context:** `packaging/test/`, `.github/workflows/`, `docs/development.md`

Red on the runner, green locally under podman, on 2 of the last 3 pushes. A pipefail/SIGPIPE
theory was written, tested at three sizes, failed to reproduce, and was **reverted rather than
shipped** — that record stands and must not be re-derived. `T-1807-bug-02` eliminated two further
candidates (port collisions on 2201-2203 and the 8008 fallback); those are also closed.

The deliverable is an **explanation**, then a fix. A green run with no explanation does not close
this card.

**Acceptance**

1. A written root cause naming the mechanism, with the evidence that distinguishes it from the
   already-refuted candidates.
2. A reproduction — on the runner if that is where it lives — that fails before the fix.
3. The job green on three consecutive runs after it.
4. If it remains unexplained: say so plainly on the card, record what was excluded, and **do not**
   close it. An unexplained green is what `T-1806-bug-01` exists to prevent.


---

## Phase 24 — delivery record (2026-08-08)

| Card | State | Note |
|---|---|---|
| `T-2401` | ● Shipped | Off by default; content-de-duplicated; retention scoped to the `scheduled` kind in SQL |
| `T-2402` | ● Shipped | Reason required, expiry evaluated at read time, `vnprox_findings_acked` added |
| `T-2403` | ● Shipped | `GET /inventory/history?ref=` — query param, not a path segment; `truncated` in the contract |
| `T-2404` | ● Shipped | `GET /changesets/{id}/impact`; every verdict carries its reason |
| `T-2405` | ○ **Not started** | OpenAPI + completeness gate. Deferred, not attempted |
| `T-2406` | ◐ **Partial** | `pve_reachable` + `pve_privileges` closed (6→8 of 10 answered). `clock_skew` and `peer_secret` still skip — `T-2406-followup-01/02` |
| `T-2407` | ○ **Not started** | Alert quiet hours. Deferred, not attempted |
| `T-2408` | ● Shipped | All-or-nothing batch; conflicts name both findings |
| `T-2409` | ○ **Not started** | Per-spec e2e store isolation. Deferred, not attempted (revisited 2026-08-09; see below) |
| `T-2410` | ● **Root-caused, fixed, and closed** | Closes `T-1806-bug-02`. AC3 met 2026-08-09: three consecutive green runner runs — `c551b11`, `3133b55`, `60e7eec` |

Six shipped, one partial, three not started. The three untouched cards are recorded as untouched
rather than downscoped — a card marked done at 30% is how a backlog stops meaning anything.

## Phase 24 — second pass (2026-08-09)

The three cards left untouched above were picked back up.

| Card | State | Note |
|---|---|---|
| `T-2405` | ● Shipped | `GET /api/v1/openapi.json`, generated by walking the router; `docs/openapi.json` committed and gated both directions. **215** operations, not the 186 the card assumed |
| `T-2407` | ● Shipped | Quiet hours + digest, with a durable deferral queue. Two corrections to the card below |
| `T-2409` | ◐ **Built, does not meet its own AC — parked on a branch** | Isolation works and is proven; the wall-clock budget and a green suite are not met. Numbers below |
| `T-2411` | ● Shipped | `vnproxd`/`vnproxctl` untracked (51 MB), `.gitignore`d |

### What T-2405 does NOT contain, said where an integrator will see it

The document carries every path, method, path parameter, security scheme and error response, all
mechanically checked. It carries **no request or response body schemas**. A client generated from
it gets correct routes, parameters and auth and no typed bodies; `docs/api.md` says so in the
section that introduces it. Claiming schema coverage we do not have would make generated clients
worse than none.

The gate walks the daemon `testdata/dev.toml` brings up, so a route behind a subsystem that config
leaves disabled (the MCP transport, the plugin hub) is outside it. Stated on the table itself.

### T-2409 missed two of its four acceptance criteria, measured

The mechanism works. Each spec file gets its own `vnproxd`, database, session key,
interfaces sandbox and port, and the deliberate cross-contamination pair proves it: green
in isolation, and **red** under `VNPROX_E2E_SHARED=1` with the message naming the leak.
AC1 and AC2 are met.

AC3 and AC4 are not, and the figures are the point of reporting rather than a footnote:

| | Baseline (shared store) | Isolation, run 1 | Isolation, run 2 (idle machine) | AC |
|---|---|---|---|---|
| Wall clock | **9.1 min** (91 tests) | 16.7 min (93) | **16.3 min** (93) | ≤ +25%; actual **+79%** ✗ |
| Result | 89 pass / 0 fail / 2 skip | 88 / 3 / 2 | **87 / 4 / 2** | green ✗ |
| `--repeat-each=2` | — | not run | not run | ✗ |

Run 2 was deliberately made on an otherwise-idle machine, because run 1 competed with other
work and CPU contention was the obvious confound for a *frame-timing* test. It is not the
explanation: the wall clock reproduced within 0.4 min, and the suite got **worse**, not
better — `history.spec.ts › History playback` joined the three from run 1.

So the failures are **reproducible and order-dependent, not load**:

- `scale.spec.ts › scale-lab (v2 canvas renderer)` — 120s test timeout in the suite, but
  **passes alone in 41.3s** on this same branch. Pass-alone/fail-in-suite is precisely the
  signature this card was written to eliminate, now appearing in a different spec.
- `user-guide-tasks.spec.ts` × 2 (IPAM reserve, firewall macro rule).
- `history.spec.ts › History playback` (run 2 only).

A cold-daemon warm-up gate — wait for the first successful PVE poll before any test runs —
was implemented and is in the branch. It did **not** fix the canvas timeout. Recorded as
*tried and insufficient* so the next person does not re-derive it and mistake it for the
answer. The cause is unidentified.

**Not committed to `main`.** The `e2e` job is required and blocking (`T-2108`); landing a
change that takes the suite from 89/0 to 88/3 would make the branch red for everyone. The
work sits on `t-2409-e2e-store-isolation`. Closing it needs the three failures explained —
`T-1806-bug-01` is exactly the precedent for not accepting an unexplained result — and the
+83% brought inside budget or the budget renegotiated on the evidence.

### Two corrections to T-2407's card

- **There is no `critical` severity.** The card says "critical severity bypasses quiet hours".
  vnprox's vocabulary is `error|warning|info` and `error` is the top tier. Inventing a fourth
  severity would mean every producer, filter, UI badge and stored rule learns it. The field is
  `bypassQuietHoursOnError`, defaults to true, and a test asserts the default *and* the override.
- **"Deferred, not dropped" is a durability claim.** A quiet window is routinely eight hours; an
  in-memory queue turns any restart inside one into dropped alerts — the exact failure the feature
  exists to prevent. Held events live in `alert_pending`, a table.

One composition decision worth stating because it is a judgement call, not a derivation: a
bypassing `error` skips quiet hours but **still** passes through the digest window. Minutes to send
one message instead of a hundred is the point of a digest; deferring an urgent alert until morning
is not.

### A third defect the work produced, again caught by a gate

`RunFlushLoop` returned `ctx.Err()` on shutdown. `runGroup` surfaces a non-nil actor error as
`runDaemon`'s return value, so three `cmd/vnproxd` daemon tests correctly reported a failed
shutdown. Same family as `T-2401`'s boot failure above: an actor's contract with the run group is
easy to get subtly wrong and the daemon tests are what catch it.

Also recorded: `make check` was read as green from a background job's exit code once during this
pass — the code belonged to the `echo` appended after it, not to `make`. The log was read
afterwards and showed the failure above. Same process error as the one recorded in `0bb7317`; the
fix is to read the log, never the notification.

### Two defects the work itself produced, both caught by a gate

Recorded because both are the kind that would otherwise be silently fixed and forgotten:

- **The daemon exited at boot.** `T-2401` registered the snapshot scheduler unconditionally, and
  `runGroup.run` cancels every actor as soon as ANY actor returns — so the disabled scheduler's
  immediate return shut the daemon down at startup. Caught by `cmd/vnproxd`'s own daemon tests.
- **`ComputeImpact` dropped guests on a non-delete op.** `carrierSet` stored `deleting` as the map
  VALUE while `guestsOnCarriers` tested membership BY value, so an update on a bridge with two
  guests reported zero. Caught by the deliberate control test (`update on a USED bridge`) that
  exists alongside the `unused bridge` case for exactly this reason.

### Two audit corrections

- **GitHub Actions was never unfunded.** `status-matrix.md` §5.7, `development.md`, and
  `project-status.md` all said no workflow ran. `gh run list` shows CI and Packaging matrix on
  every push. The cost was concrete: `T-1806-bug-02` sat unexplained for two days with "reproduce
  under runner-like conditions" as its next step, while the runner's own log held the answer.
- **`vnproxd` and `vnproxctl` (53 MB of build artifacts) are tracked in git.** Pre-existing, not
  introduced here, but every commit that rebuilds them adds a fresh multi-megabyte blob to history.
  Worth a `git rm --cached` + `.gitignore` in its own change; filed as `T-2411`.
