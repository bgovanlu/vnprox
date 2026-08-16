# Phase 26 — Guardrails

**Roadmap:** [`docs/roadmap-adopted.md`](../../docs/roadmap-adopted.md) ·
**Plan:** [`../implementation-plan-adopted.md`](../implementation-plan-adopted.md)

Context for every card in this phase: `docs/architecture.md`, `docs/development.md`,
`docs/api.md`, `docs/data-model.md`, `docs/features/change-management.md`.

The change engine's guarantees are strong and **advisory**. It tells you what will happen; it
does not refuse. Phase 24 improved the narration (`T-2404` blast radius, `T-2403` history). This
phase gives the engine the ability to say no, and to stop halfway.

**The binding constraint for this phase:** every card here adds a gate, and
`CLAUDE.md`'s core guarantee is that all mutation flows stage → validate → diff → apply →
confirm/rollback. Nothing below adds a second write path; every gate is installed *inside* that
sequence, at the stage named on the card.

---

## T-2601 · Policy-as-code guardrails at the validate stage ★

**kind:** implementation · **depends on:** —
**context:** `internal/change/validate.go`, `internal/change/protected.go`,
`internal/validation/schema.go`, `internal/spec/`

`protected.go` encodes exactly one organisational rule — don't cut the management path — and it
is hard-coded. Every other rule an organisation has ("no guest on VLAN 1", "every bridge carrying
guests needs two uplinks", "never touch `vmbr9` on the storage nodes") lives in a wiki, and is
enforced by whoever happens to review.

Add a declarative policy file evaluated at the **validate** stage, so a violating changeset never
reaches diff.

- Policies are declarative data, not a scripting language. A rule is
  `{id, description, severity, match, assert}` over the same op and inventory shapes the change
  engine already uses. No embedded interpreter, no new dependency.
- `severity: deny` blocks validation with the rule's ID and description in the error.
  `severity: warn` annotates the changeset and is surfaced in review but does not block.
- **A policy that matches nothing is an error, not a silent pass.** An unmatched rule on every
  changeset for N days is reported as probably-misconfigured.
- `vnproxctl policy test --policy=f.yaml --changeset=id` evaluates without staging, so a rule can
  be developed against a real changeset safely.
- Policies are cluster-scoped and versioned in the store; changing one is audited
  (`policy.update`) with a diff of the rule set.

**Acceptance**

1. A `deny` rule blocks a violating changeset at validate, and the error names the rule ID **and**
   its description; a conforming changeset is unaffected.
2. A `warn` rule does not block, and its annotation survives to the review surface — asserted at
   the API, not only in the engine.
3. Policy evaluation happens **before** diff: a denied changeset produces no diff and no plan,
   proven by asserting the diff was never computed rather than that it was discarded.
4. Every shipped example policy has a fixture that makes it fire **and** one that makes it pass.
   A rule with only a passing fixture fails the build.
5. A malformed policy file fails at load with a message naming the file, the rule ID, and the
   field — the daemon does not start with a policy it cannot parse.
6. An empty policy set is valid and changes nothing, asserted against the full existing changeset
   test suite running with the policy engine enabled.
7. Policy changes are audited with the rule-set diff; the audit entry alone is enough to
   reconstruct what changed.

## T-2602 · Canary / staged multi-node apply ★

**kind:** implementation · **depends on:** —
**context:** `internal/change/apply.go`, `internal/change/apply_plan.go`,
`internal/change/apply_snapshot.go`, `docs/features/change-management.md` §4

An apply fans out to every affected node. If the change is wrong, it is wrong everywhere at once,
and the operator finds out from all nodes simultaneously. Commit-confirm catches this — after the
damage is cluster-wide.

`T-1803` cannot prove multi-node safety partly because the current design gives it nothing
intermediate to observe. Introduce a staged apply with a hold between stages.

- `applyStrategy: {mode: all|canary, canaryNodes: [..], holdFor: duration, gate: manual|auto}`.
  Default stays `all`, so no existing behaviour changes.
- After the canary stage, the apply **pauses in a resumable state**: the changeset is neither
  applied nor rolled back, and the store records exactly which nodes are in which state.
- `gate: auto` proceeds if no `error` finding attributable to the canary nodes appeared during the
  hold, and health checks pass. `gate: manual` waits for `POST /changesets/{id}/continue`.
- **Abort rolls back only the stages that ran.** A node that was never touched is never restored,
  and the test asserts it was not contacted.
- The commit-confirm timer covers the whole staged sequence, not each stage — a stalled canary
  cannot hold the cluster open past the window.

**Acceptance**

1. A canary apply touches only the canary nodes before the hold; the remaining nodes' clients
   record **zero** calls, asserted on the mock transport.
2. Abort after the canary stage restores exactly the canary nodes and contacts no others.
3. `gate: auto` proceeds on a clean hold and **does not** proceed when an `error` finding
   attributable to a canary node appears during it. Both directions asserted.
4. A daemon restart mid-hold recovers the paused state from the store and either resumes or
   rolls back per the recorded strategy — never leaves the changeset in an unknown state.
5. The commit-confirm deadline is measured from the start of the whole sequence; a hold that
   outlasts the window triggers rollback of everything applied so far.
6. `mode: all` is byte-for-byte the existing behaviour, asserted by running the pre-existing apply
   test suite unchanged.
7. A canary node list naming a node not affected by the changeset is a validation error.

## T-2603 · Finding-triggered auto-rollback inside the confirm window ★

**kind:** implementation · **depends on:** T-2602
**context:** `internal/change/apply.go`, `internal/change/localtimer.go`,
`internal/change/changeset.go` (confirm deadline), `internal/change/impact.go` (T-2404's
`Impact`), `internal/findings/engine.go`, `internal/change/apply_restore.go`

Commit-confirm rolls back when the operator fails to confirm — which means the failure mode it
handles well is "the operator lost their connection". The more common one is "the change was
wrong and the operator is still staring at the screen wondering why". In that case vnprox already
*knows*: the findings engine detects the breakage within a cycle, and does nothing with it.

- Within an unconfirmed changeset's confirm window, a **new** `error` finding attributable to an
  entity the changeset touched triggers an immediate rollback.
- Attribution reuses `T-2404`'s `Impact` — the set of nodes, guests, and bridges the changeset
  touches. A finding outside that set never triggers a rollback, however severe.
- **Pre-existing findings never trigger.** The trigger is on findings whose stable ID
  (`hysteresis.go`) was not present in the cycle before the apply.
- Off by default per changeset (`autoRollbackOnError`), settable as a cluster default. Enabling it
  is audited.
- The rollback records **which finding** caused it, in the audit entry and on the changeset, so
  the operator is never told only that "something went wrong".

**Acceptance**

1. A new `error` finding on a touched entity inside the window rolls the changeset back, and the
   audit entry names the finding's stable ID.
2. A finding that existed **before** the apply does not trigger, proven by seeding it in the
   pre-apply cycle rather than by timing.
3. A new `error` finding on an entity outside the changeset's `Impact` does not trigger.
4. A `warning` finding never triggers, at any position inside or outside `Impact`.
5. With the flag off, none of the above triggers anything — the existing confirm behaviour is
   unchanged, asserted against the pre-existing confirm suite.
6. A finding arriving **after** the window closed does not trigger a rollback of an
   already-confirmed changeset.
7. Interaction with T-2602 is specified and tested: a trigger during a canary hold aborts the
   sequence and restores only the applied stages.

## T-2604 · Enforced two-person rule on protected op classes

**kind:** implementation · **depends on:** T-2601
**context:** `internal/change/review.go`, `ApprovalPolicy`, `internal/change/protected.go`

`ApprovalPolicy.AllowSelfApproval` already prevents an author approving their own changeset. What
does not exist is a rule that *some classes of change require approval at all*. Today an operator
with the capability can stage and apply a management-path change alone at 3am.

- Op classes (`fw.*`, `sdn.*`, anything `touchesMgmtPath`, anything a `T-2601` policy tags) can be
  declared as requiring N distinct approvers.
- Enforced **server-side at apply**, not at the UI. The API refuses, and the refusal names the
  class and the shortfall.
- Emergency break-glass exists, requires a written reason, is audited at a distinct action
  (`change.breakglass`), and raises a `error` finding that cannot be acked for 24 hours.
- Approvers must be distinct principals; the same person via two tokens is one approver.

**Acceptance**

1. A changeset in a protected class with N-1 approvals is refused at apply with a message naming
   the class and the count required.
2. The refusal is server-side: a request crafted directly against the API, bypassing the UI, is
   refused identically.
3. Two approvals from the same principal through different tokens count as one, asserted
   explicitly.
4. Break-glass with no reason is refused; with a reason it proceeds, is audited under its own
   action, and raises the finding.
5. The break-glass finding cannot be acked before 24 hours have passed, proven by setting the
   clock rather than by waiting.
6. An unprotected changeset is unaffected, asserted against the existing apply suite.

## T-2605 · Post-apply topology preview

**kind:** implementation · **depends on:** —
**context:** `internal/change/apply_plan.go`, `internal/change/validate_projection.go`,
`internal/topology/`, `internal/blueprint/`, `web/src/topology/`

**Do not duplicate `validate_projection.go`.** It already projects op effects onto each node's
flat `interfaces(5)` namespace to detect duplicate creates and address overlaps — but it builds
key maps for referential validation, not a renderable snapshot, and it discards everything it
learns. This card should extract that projection or build alongside it, and the report must state
which it did and why.

`T-2404` answers "what breaks". The diff answers "what fields change". Neither answers the
question an operator actually forms in their head before clicking apply: *what will the map look
like*.

`internal/blueprint` already expands entities against a live `inventory.Snapshot` without applying
anything. The same machinery can produce a post-apply snapshot for rendering.

- `GET /changesets/{id}/preview` returns a projected `inventory.Snapshot` — the current one with
  the changeset's ops applied in memory.
- The map renders it in a distinct visual mode with added, removed, and modified entities marked.
- **Projection is explicitly best-effort and says so.** Any op whose effect cannot be projected
  (an out-of-band-dependent result, a raw-file edit) is listed by name in the response as
  unprojectable rather than silently dropped.
- Read-only and side-effect free: the projection never touches the store or PVE.

**Acceptance**

1. A changeset creating a bridge produces a projected snapshot containing it, marked added; the
   live snapshot is unchanged, asserted by re-reading it.
2. A changeset deleting an entity marks it removed rather than omitting it.
3. An unprojectable op appears in the `unprojectable` list with a reason; a test uses a raw
   `/etc/network/interfaces` edit.
4. The endpoint is side-effect free: a store checksum before and after is identical, and the PVE
   mock records zero write calls.
5. Projecting a changeset that fails validation is refused rather than projecting nonsense.
6. Projection of the empty changeset equals the live snapshot exactly.

---

## Phase 26 — delivery record (2026-08-12)

| Card | State | Note |
|---|---|---|
| `T-2601` | ● Shipped | Declarative policy rule set (`{id, description, severity, match, assert}`) enforced at validate; `severity: deny` blocks before diff/plan is computed, `severity: warn` annotates and travels to review. Data, not a scripting language — no embedded interpreter. `vnproxctl policy test/lint/examples`; default policy set is empty (`24c48fb`) |
| `T-2602` | ● Shipped | Canary apply (`applyStrategy: {mode: canary, ...}`) pauses in a resumable state after touching only the canary nodes; `gate: manual`/`gate: auto`; survives a daemon restart mid-hold; commit-confirm covers the whole sequence (`c535750`). **API/CLI-only** — CHANGELOG's own v3.5.0 note is explicit that there is no canary-strategy picker in the review screen yet |
| `T-2603` | ● Shipped | `autoRollbackOnError`, off by default per-changeset or as a cluster default; a new `error` finding on a touched entity rolls back inside the confirm window; a pre-existing or out-of-blast-radius finding never triggers, nor does a `warning`. Wires the canary health checker into `cmd/vnproxd`'s findings watcher, closing `T-2602`'s `gate: auto` gap (`2fb8c4e`; confirmed wired, not left dangling, by `docs/project-status.md:231`) |
| `T-2604` | ● Shipped | N-approver protected op classes (`[[changesets.protected_class]]`); review screen disables Apply with a named count of approvals still needed; two approvals from the same person through two tokens still count as one. Break-glass override requires a written reason, is audited (`change.breakglass`), and raises a 24h-unacknowledgeable finding — but **isn't a button on any screen**, reachable only by calling the route directly (`0856607`) |
| `T-2605` | ● Shipped | `GET /changesets/{id}/preview` projects a post-apply `inventory.Snapshot` in memory (read-only, side-effect free); unprojectable ops are named rather than silently dropped (`432bd86`) |

All five cards have their own commit (`24c48fb`, `c535750`, `2fb8c4e`, `0856607`, `432bd86`,
2026-08-10 through 2026-08-12) and are corroborated by `CHANGELOG.md`'s `v3.5.0` entry and
`docs/project-status.md` §6.4, which agree on every card's state. The one qualification worth
repeating rather than smoothing over: two of the five guardrail cards (`T-2602`, `T-2603`) are
real, working, and reachable only through the API and CLI — CHANGELOG says so directly ("there is
no canary-strategy picker or auto-rollback toggle in the review screen yet"), and `T-2604`'s
break-glass path shares the same gap. Giving these a screen is `T-3002`/`T-3005` in
`docs/roadmap-earned.md`, not a re-open of this phase.
