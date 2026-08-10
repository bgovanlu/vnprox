# Phase 27 — Config as code

**Roadmap:** [`docs/roadmap-adopted.md`](../../docs/roadmap-adopted.md) ·
**Plan:** [`../implementation-plan-adopted.md`](../implementation-plan-adopted.md)

Context for every card in this phase: `docs/architecture.md`, `docs/development.md`,
`docs/api.md`, `docs/data-model.md`.

`internal/spec` already exports and imports a declarative cluster network spec, with a round-trip
test. What it does not have is anywhere to live. This phase gives it a home in git, a way back
out again, and the two read surfaces — a real topology diff and a compliance projection — that
make "what is the difference between intent and reality" answerable.

**The invariant this phase is most at risk of breaking, and must not:** Proxmox stays the source
of truth. A git repository becomes the source of *intent*. Nothing here makes the repo
authoritative over the live config; divergence is always resolved by **producing a changeset** a
human reviews, never by a daemon deciding the file wins.

---

## T-2701 · Git-backed spec sync ★

**kind:** implementation · **depends on:** —
**context:** `internal/spec/`, `internal/change/`, `docs/features/change-management.md`

There is no git integration anywhere in the tree. For a large class of users — anyone who already
runs their infrastructure from a repository — that alone is disqualifying, and it is also the
missing substrate under `T-2101`'s Terraform provider.

- `[gitsync]` config: remote URL, ref, path within the repo, poll interval, and credentials via
  the existing secret handling. Off by default.
- On each poll: fetch, read the spec at `path`, run the existing `spec.Import` + plan, and if the
  plan is non-empty **open a draft changeset** and stop. It never applies.
- One open sync changeset at a time. A second detected divergence updates the existing draft
  rather than accumulating drafts.
- Read-only clone. vnprox never pushes on this path (that is `T-2702`, and it is explicit and
  operator-initiated).
- Signature verification: if `require_signed_commits` is set, an unsigned or
  unverifiable-signature commit is refused and raises a finding, rather than being applied.
- A `vnproxctl gitsync status` shows the last fetched SHA, the last plan, and why the current
  draft exists.

**Acceptance**

1. A spec change in the remote produces exactly one draft changeset whose ops match the plan; the
   changeset is **not** applied, asserted by checking apply was never called.
2. Polling twice with no change produces no second draft and no store write.
3. A spec change while a sync draft is already open updates that draft; the count of open sync
   changesets never exceeds one, asserted across three successive divergent polls.
4. An unparseable spec raises a finding naming the file and the parse error, and does **not**
   discard or modify the existing draft.
5. With `require_signed_commits`, an unsigned commit is refused with a finding; a correctly signed
   one proceeds. Both directions, with a real signature fixture.
6. Credentials never appear in logs, findings, audit entries, or `gitsync status` output — one
   assertion per surface.
7. A remote that is unreachable degrades to a finding and a retry, and never blocks daemon
   startup or any other subsystem.

## T-2702 · Changeset → pull request

**kind:** implementation · **depends on:** T-2701
**context:** `internal/spec/export.go`, `internal/change/review.go`, `T-2701`'s git layer

If intent lives in git, then a change made in the vnprox GUI is a change made outside the system
of record. Close the loop: let a staged changeset become a proposed commit against the spec repo.

- `POST /changesets/{id}/propose` renders the changeset as a spec delta, commits it on a branch,
  pushes, and opens a pull request through the host's API (GitHub and GitLab at minimum).
- The PR body carries the semantic diff, the `T-2404` blast radius, and the `T-2605` preview
  summary — the review context lives with the review.
- The changeset records the PR URL; the PR is linked from the review surface.
- **vnprox does not merge, gate, or poll for approval.** It opens the PR and stops. Whatever
  happens next comes back through `T-2701`'s normal sync.

**Acceptance**

1. Proposing a changeset produces a branch whose spec, when re-imported, plans to the same op set
   as the original changeset — the round-trip is asserted semantically, not textually.
2. The PR body contains the blast radius and diff; a changeset with an empty diff cannot be
   proposed.
3. A host API failure leaves no orphan branch — either both the branch and PR exist, or neither.
4. Proposing twice updates the existing PR rather than opening a second.
5. Credentials are absent from the PR body, the commit message, and the branch name.
6. GitHub and GitLab are both exercised against a mock host; the host client is behind an
   interface with no host-specific logic above it.

## T-2703 · Drift-to-git reconciliation

**kind:** implementation · **depends on:** T-2701, T-2704
**context:** `internal/drift/`, `internal/spec/`, `T-2704`'s diff

Drift detection reports that config and live have diverged. With a spec in git there is now a
third position — intent — and the operator's real question becomes *which of these three is
right*. Answer it with two explicit, symmetric actions rather than a guess.

- On a drift finding, offer **"adopt reality"** (propose a spec commit matching live, via
  `T-2702`) and **"restore intent"** (stage a changeset bringing live back to the spec).
- Both produce a reviewable artifact. Neither happens automatically, ever, at any severity.
- The finding names all three positions — spec, config, live — and what differs between each pair.

**Acceptance**

1. "Adopt reality" produces a PR whose spec re-imports to a plan that is **empty** against current
   live — the definition of having adopted it.
2. "Restore intent" produces a changeset which, if applied, makes the drift finding clear;
   asserted by applying it against the mock and re-running the detector.
3. Neither action fires without an explicit request, asserted with a drift finding at every
   severity and a transport that fails the test if called.
4. A three-way divergence (spec, config, and live all different) is reported with all three
   positions named, not collapsed into a two-way diff.
5. No drift finding exists in a state where both actions are offered but neither is applicable.

## T-2704 · Point-in-time topology diff

**kind:** implementation · **depends on:** —
**context:** `internal/topology/`, `internal/change/apply_snapshot.go`,
`internal/change/snapshots_scheduled.go` (T-2401), `internal/change/entityhistory.go` (T-2403),
`internal/api/history.go`, `web/src/history/`

Changesets record what **vnprox** did. The history timeline plays those back. Neither answers
"what is different about this cluster compared to Tuesday", and the gap is exactly the class of
change the drift checker exists to catch: the one made by a human over SSH.

- `GET /topology/diff?from=<ts|snapshotId>&to=<ts|snapshotId|now>` returns added, removed, and
  modified entities with per-field before/after.
- Built from the snapshot series `T-2401` now produces on a schedule, which is what makes an
  arbitrary historical point available at all.
- **Attribution where it exists, honestly absent where it does not.** A difference explained by a
  changeset names it; one that is not is explicitly marked unattributed — that marking is the
  product value, since an unattributed change is an out-of-band change.
- The map renders a diff overlay for a selected range.

**Acceptance**

1. A change made through a changeset appears in the diff attributed to that changeset.
2. A change made **outside** vnprox (simulated by mutating the mock directly) appears marked
   unattributed. This is the card's central assertion.
3. `from` and `to` accept both timestamps and snapshot IDs, and a `from` after `to` is refused.
4. A range with no snapshots at either end returns a stated error naming the nearest available
   snapshots, rather than an empty diff that reads as "nothing changed".
5. Diffing a point against itself returns empty.
6. Field-level before/after is present for modified entities, not merely "modified".

## T-2705 · Mutating MCP tools that stage, never apply

**kind:** implementation · **depends on:** T-2601
**context:** `internal/mcp/`, `internal/change/`, `docs/security.md`

The MCP surface exposes nine read-only tools. An AI operator can therefore diagnose a problem in
full and then must hand a human a paragraph of instructions to type. The safe half of the useful
thing is missing: staging.

- New tools that **stage** ops into a draft changeset: create bridge, edit interface, add
  firewall rule, reserve IPAM address. Each produces a draft and returns its ID.
- **No tool applies, confirms, approves, or deletes.** This is enforced structurally: the MCP
  service is constructed with a change-engine interface that has no apply method, so an applying
  tool cannot be written without changing the type.
- Every staged op passes `T-2601`'s policy engine at stage time; a denied op returns the rule ID
  to the model, which is actionable feedback rather than an opaque failure.
- MCP-staged changesets are tagged with the tool and session, and are visually distinct in review.
- Rate-limited per session, with a cap on open MCP drafts.

**Acceptance**

1. Every mutating tool produces a draft and nothing else; the apply path records zero calls across
   the whole tool suite, asserted once per tool.
2. The change-engine interface the MCP service holds has **no** apply, confirm, or approve method
   — asserted by a compile-time assertion, not a runtime check.
3. A policy-denied op returns the rule ID and description to the caller.
4. An MCP-staged changeset is tagged and the tag survives to the review API.
5. Exceeding the open-draft cap refuses further staging with a message naming the cap.
6. The existing MCP guard tests still pass unchanged, and a new guard asserts no tool name matches
   an apply/confirm/delete verb.

## T-2706 · Compliance profiles and evidence export

**kind:** implementation · **depends on:** T-2601
**context:** `internal/findings/`, `internal/posture/`, `internal/docexport/`

43 checks across 15 sources already produce most of what an auditor asks for. What is missing is
the mapping — nobody asks "does check `mtu_asymmetric` pass", they ask "demonstrate network
segmentation control 1.2".

- A profile is a declarative mapping from control IDs to the checks, policies (`T-2601`), and
  posture factors that evidence them. Ship one general profile; the format is the deliverable, not
  a claim of certification.
- `GET /compliance/{profile}` returns per-control status with the underlying evidence, and
  `docexport` renders it as a timestamped report.
- **A control with no mapped evidence reports `unmapped`, never `pass`.** This is the difference
  between a compliance feature and a compliance liability.
- Historical: a report is reproducible for a past date from the retained finding history, or
  states plainly that the window does not reach back that far.

**Acceptance**

1. A control whose mapped checks all pass reports `pass` with each check named as evidence.
2. A control with no mapping reports `unmapped`; a test asserts `unmapped` can never be rendered
   as passing in any output format.
3. One failing mapped check fails its control, and the failing check is named.
4. The rendered report round-trips through the parser and names the vnprox version, profile
   version, and generation time.
5. Requesting a date outside the retention window returns a stated error naming the earliest
   available date, not a partial report.
6. Adding a check to the codebase without mapping it does not silently degrade any control — a
   test asserts the unmapped-check list is reported rather than ignored.
