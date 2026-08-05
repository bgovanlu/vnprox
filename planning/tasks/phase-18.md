# Phase 18 — Proven on iron (v3.1)

Goal: stop shipping on faith. Three arcs of network-mutation features were developed against
`internal/pvemock` with one single-node PVE box ever touched;
`planning/reports/needs-hardware-validation.md` holds 105 unchecked items against 1 validated.
Nothing in this phase is a feature. Everything in it is evidence that what already shipped does
what its docs say — plus the one card (T-1805) that closes a genuine hole in the core safety
guarantee, found in `planning/reports/T-502.md` and owned by nobody since.

Decisions this phase is built on (`docs/roadmap-proven.md` § Decisions): **D1** revert ticket at
apply time; **D2** `pvecube` only, single node; **D3** blocked register plus a harder mock;
**D5** agents build harnesses, humans run them; **D7** scripts emit JSON, an agent triages.

Dependency shape: **T-1806 (CI) is the root** — until it lands, every other card's "`make check`
green" is a claim, not a gate. **T-1801** (evidence protocol) gates the three validation cards
that consume it (T-1802, T-1804, T-1808). **T-1805** is independent backend work that should
start immediately and in parallel, because T-1804 cannot prove the firewall-only lockout case
heals until it exists. **T-1803** deliberately follows T-1802's first run, so the mock's new
failure modes are informed by observed hardware rather than guessed.

A standing rule for this phase, restated on every `kind: validation` card: **an agent may never
tick a checklist item it did not receive evidence for.** The dividing line is not "did the agent
reason carefully" — it is "did a human run it on iron and return the blob."

Exit demo: every `pvecube`-reachable checklist item is green with the PVE version recorded and
its evidence committed; the blocked register accounts for the rest; a scripted management-path
lockout during an apply heals itself unattended on real hardware — including for a
firewall-only changeset, which today it would not; CI is green, required, and believed.

---

## T-1801 · Validation harness and evidence protocol ★
**model:** sonnet-5 · **kind:** validation · **size:** M · **depends:** T-1806 · **context:** `planning/reports/needs-hardware-validation.md` (the checklist this serves), `docs/roadmap-proven.md` §Decisions D5/D7, `docs/development.md`, `internal/pvemock/`

**Objective:** The machinery that turns a 105-item hardware checklist into roughly eight human
turns instead of sixty. One runnable script per checklist section, each printing a
machine-readable evidence blob that an agent can diff against a declared expected-outcome table.

**Why this is its own card:** the burndown (T-1802) is worthless without a protocol, and a
protocol invented mid-burndown gets invented three different ways. This card fixes the format
before any evidence exists.

**Deliverables:**
- `planning/validation/` — the new home for this arc's validation material (named in
  `planning/implementation-plan-proven.md` so no sibling card invents a second layout):
  - `harness/<section>.sh` — one POSIX-shell script per checklist section, runnable as
    `ssh <node> 'bash -s' < harness/<section>.sh`, requiring nothing on the target but a stock
    PVE install. **Read-only by default**; any script that mutates state carries an explicit
    `MUTATES=1` banner and refuses to run without `--i-understand-this-mutates`.
  - `expected/<section>.md` — the expected-outcome table: per checklist item, what the blob
    should contain and what a divergence would mean.
  - `evidence/` — committed evidence blobs, one per run, named `<section>-<pve-version>-<date>.json`.
- An evidence blob schema (documented, versioned): harness version, PVE version, node identity,
  per-item `{id, command, raw, verdict-inputs}`. Raw output is captured verbatim — the script
  never decides pass/fail, because the whole point is that the triage is auditable after the
  fact.
- A redaction pass in the harness itself: tokens, tickets, private keys, and PSKs are scrubbed
  before the blob is printed. An evidence blob is going to be pasted into a chat transcript;
  treat it as public.
- `planning/validation/README.md` — the human's runbook: how to run a section, what to paste
  back, what happens next.

**Acceptance criteria:**
1. Running any harness script against `internal/pvemock` (not hardware) produces a schema-valid
   evidence blob — proving the harness itself is testable without a cluster.
2. A deliberately wrong expected-outcome entry causes triage to flag a divergence rather than
   silently pass; asserted by a unit test over the triage logic with a fixture blob.
3. No harness script mutates state without the explicit flag; asserted by a test that greps
   every script for mutating verbs (`set`, `create`, `delete`, `ifreload`, `ifup`, `ifdown`)
   and requires a matching `MUTATES=1` banner.
4. A blob containing a synthetic PVE token, ticket, and WireGuard private key emerges with all
   three redacted — table-driven test, one case per secret class.
5. `planning/validation/README.md` is complete enough that the run step needs no chat context:
   a reader who has never seen this card can run a section and know what to return.

---

## T-1802 · Hardware-validation burndown, `pvecube`-reachable sections ★
**model:** sonnet-5 · **kind:** validation · **size:** L · **depends:** T-1801 · **context:** `planning/reports/needs-hardware-validation.md`, `planning/validation/` (T-1801), `planning/reports/T-608.md` (the one prior hardware pass, and what it found)

**Objective:** Burn down every checklist item reachable on a single node — roughly 60 of 105 —
with evidence committed and the PVE version recorded, converting each divergence into a bug card
rather than a doc amendment.

**Prior art worth taking seriously:** the only checklist item ever validated (T-608, pmxcfs
secret store) found **two real bugs on first contact with hardware**, one of which — `link(2)`
rejected outright by pmxcfs — would have failed on every real-hardware secret generation ever
attempted. Expect a similar hit rate. Budget for bug cards, not for ticking boxes.

**Deliverables:**
- Section-by-section execution of T-1801's harness across the reachable checklist areas: PVE API
  behavior, host reader/writer, the change engine's local paths, firewall, SDN, IPAM, WireGuard,
  and capture.
- `needs-hardware-validation.md` edited in place — items ticked `[x]` with the PVE version
  tested and a link to the committed evidence blob. **Do not fork the checklist.**
- One bug card per divergence, filed in `planning/tasks/phase-18.md` as a `T-1802-bug-NN`
  subsection with a reproduction and a severity, or escalated into its own `T-18NN` card if it
  is large enough to need its own dependency edges.
- A running summary in `planning/reports/T-1802.md`: items attempted, ticked, diverged, deferred,
  with the divergence rate called out plainly.

**Acceptance criteria:**
1. Every reachable checklist item is either ticked with evidence and a PVE version, or explicitly
   deferred with a written reason — no item is left in its original unmarked state.
2. Every tick has a committed evidence blob under `planning/validation/evidence/`; a test asserts
   no checklist item is ticked without a referenced blob that exists.
3. Every divergence has a bug card with a reproduction; none is resolved by editing the doc to
   match the observed behavior without an explicit, argued note saying the doc was wrong.
4. Items unreachable on a single node are **not** ticked, not skipped silently, and not
   mock-closed — they are handed to T-1803's blocked register.
5. `make check` green; `planning/reports/T-1802.md` states the divergence rate.

---

## T-1803 · Blocked-validation register and multi-node mock fidelity ★ 🔒
**model:** strong (Opus/Fable-class) · **size:** L · **depends:** T-1802 (first run) · **context:** `internal/pvemock/`, `internal/peer/`, `internal/ha/`, `internal/change/` (distributed rollback, local-timer protocol), `planning/reports/T-301.md`, `planning/reports/T-304.md`, `planning/reports/T-1704.md`

**Objective:** Two deliverables for the ~45 items single-node hardware cannot reach, per D3.
First, make the gap a **known quantity**: a written register of what is unproven, why, and what
breaks if it is broken. Second, **make the mock worth trusting**: give `internal/pvemock` the
multi-node failure modes hardware would exhibit, then re-run the cluster suites against it.

**Why `model: strong` and 🔒:** a mock that people treat as a proxy for hardware is a liability
if it is more permissive than reality. Every fidelity improvement here must make the mock
*harder* to pass, never easier. A card that ends with more green tests than it started with, and
no new failures found, has almost certainly failed.

**Deliverables:**
- `planning/reports/blocked-validation.md` — one entry per unreachable item: the item, why a
  single node cannot prove it, the concrete failure mode that would go undetected, and a severity
  if it is broken in production. Ordered by severity, not by checklist order.
- `internal/pvemock` fidelity work, each with fixtures and each wired into the existing suites:
  - **N-node cluster fixtures** — genuine multi-node cluster status, per-node divergent state,
    and node join/leave.
  - **pmxcfs semantics** — replication latency, partial writes, and the `link(2)`-rejected /
    mode-coercion behaviors T-608 found the hard way, so the next such bug is caught in CI.
  - **corosync** — quorum loss, partition, and the read-only-on-loss-of-quorum behavior.
  - **peer daemons** — slow, unreachable, and returning stale-but-valid responses.
- Cluster suites (`internal/peer`, `internal/ha`, `internal/change`'s distributed rollback,
  `internal/federation` fan-out) re-run against the hardened mock, with every new failure either
  fixed or filed.
- `docs/architecture.md` and the relevant `docs/features/*.md` gain an explicit statement of which
  cluster behaviors are mock-validated only.

**Acceptance criteria:**
1. Every item T-1802 handed over appears in the register with a severity and a named failure mode
   — count matches, asserted by a test that cross-references the two documents.
2. The hardened mock finds **at least one real defect** in shipped cluster code, filed as a bug
   card. (If it genuinely finds none, the report must argue why the fidelity work was
   nevertheless adversarial — a clean pass is a claim requiring evidence, not a result.)
3. Quorum loss, pmxcfs partial write, and peer-unreachable each have a test that fails against
   the pre-T-1803 mock and passes (or fails loudly, if it found a defect) against the new one.
4. No fidelity change makes an existing test easier to pass; a reviewer can confirm this from the
   diff, and the report names any test whose expectations were relaxed and why.
5. Docs state plainly which cluster behaviors are mock-validated only; `make check` green.

---

## T-1804 · Failure-injection proof of commit-confirm ★
**model:** sonnet-5 · **kind:** validation · **size:** M · **depends:** T-1801, T-1805 · **context:** `docs/architecture.md` §4 (change engine lifecycle) and §6, `internal/change/`, `planning/reports/T-304.md`, `planning/reports/T-402.md`

**Objective:** The product's central claim is "if the change locks you out, it reverts itself."
That path has never run on hardware against a real lockout. Prove it, or find out it does not
hold.

**Deliverables:**
- A scenario table, written **before** any run, each with a declared expected outcome:
  1. Management link dropped mid-apply (the classic lockout).
  2. `vnproxd` killed inside the confirm window (crash recovery re-arms the timer).
  3. Node hard-reset inside the confirm window.
  4. Confirm window expires with no session alive at all.
  5. **A firewall-only changeset under each of the above** — the T-1805 case, which before this
     arc would not have reverted.
  6. Apply interrupted between the write and the `ifreload`.
- A mutating harness under `planning/validation/harness/` (T-1801's `MUTATES=1` contract), each
  scenario individually runnable with a documented recovery path if it goes wrong — including
  out-of-band console recovery, since the point of the exercise is to lose the network.
- Triage of returned evidence into ticks and bug cards, as T-1802.

**Acceptance criteria:**
1. Every scenario has a written expected outcome recorded before its run; the git history shows
   the expectation predating the evidence blob.
2. Each scenario has an evidence blob and a verdict; a scenario that does not self-heal is a
   **release blocker** filed as such, not a finding.
3. Scenario 5 (firewall-only) is proven to heal, closing the gap `planning/reports/T-502.md`
   flagged — this is the card's headline result and T-1805's real acceptance test.
4. Every scenario documents the recovery path actually used, so a future run is repeatable by
   someone who was not there.
5. `docs/features/change-management.md` gains a "what has actually been proven, on what hardware,
   at what PVE version" section reflecting the results.

---

## T-1805 · Unattended revert for `fw.*` and `sdn.apply` via apply-time revert ticket ★ 🔒
**model:** strong (Opus/Fable-class) · **size:** L · **depends:** — (start immediately) · **context:** `planning/reports/T-502.md` (the flagged gap), `docs/architecture.md` §4 and §6 D3, `internal/change/` (apply, confirm, rollback, local timers), `internal/store/cipher.go` (SessionCipher), `internal/pve/auth.go` (ticket lifetime and renewal), `docs/security.md`

**Objective:** Close the one genuine hole in the change engine's safety guarantee. PVE firewall
and SDN writes require the *user's* ticket, so today a `fw.*`-only changeset that reaches
`awaiting_confirm` and then times out — or whose daemon crashes mid-window — **is not
automatically reverted**, unlike node-file changes. Per **D1**, seal the applying user's PVE
ticket for the confirm window and revert with it.

**Safety analysis (required section, T-703/T-1701-level rigor, cross-referenced by the test names
below):**
- **The ticket is a credential at rest and is treated as one.** Sealed with the existing
  `SessionCipher` (AES-256-GCM) — the identical primitive `sessions.pve_ticket_enc` /
  `clusters.credential_enc` use, **never** a second cipher or key pair. It is never returned by
  any API response, never logged, never in an audit detail, and stripped from `GET /changesets`
  exactly as `presharedKey` already is.
- **Its lifetime is bounded from both ends.** Wiped on confirm, on rollback, on expiry, and on
  changeset deletion — whichever comes first. A wipe is not best-effort: a changeset that leaves
  `awaiting_confirm` by any path must have no ticket bytes left, asserted directly against the
  stored row.
- **It authorizes exactly one thing.** The sealed ticket is reachable only from the revert path
  for that one changeset's own ops. There is no code path — no API route, no MCP tool, no plugin
  capability — that unseals it for anything else. Verified by an enumeration test, not by
  convention.
- **Expiry is surfaced, never silently swallowed.** A PVE ticket lives ~2h. If the confirm window
  can outlive it, the operator must be told **at apply time** that unattended revert will not be
  available for the whole window — not discover it at minute 121.
- **This is not a second mutation path.** The revert runs the existing rollback machinery with a
  credential it previously lacked; it introduces no new op type and no route that applies
  anything.

**Deliverables:**
- Schema migration (33): `changesets.revert_ticket_enc` (sealed, nullable) plus the expiry
  timestamp needed to reason about coverage.
- `internal/change`: capture and seal at apply time; use in the confirm-timeout and
  crash-recovery revert paths for `fw.*` and `sdn.apply` ops; wipe on every terminal transition.
- Crash recovery: `ArmPendingOnStartup`'s siblings must find the sealed ticket after a restart
  and complete the revert unattended.
- API: apply responses carry whether unattended revert is available for this changeset and until
  when; `GET /changesets` strips the sealed field entirely (`redactOpSecrets`' precedent).
- UI: the apply dialog states plainly when a changeset will **not** self-revert, and for how long
  it will. No changeset may imply a safety net it does not have.
- `docs/architecture.md` §6 (D3's "PVE writes require the user's ticket" reasoning),
  `docs/security.md` (a new credential class), `docs/api.md`, `docs/data-model.md`.

**Acceptance criteria:**
1. A `fw.*`-only changeset that reaches `awaiting_confirm` and times out with no session alive is
   reverted unattended — the case that fails today.
2. The same, after `vnproxd` is killed and restarted mid-window (crash recovery unseals and
   completes).
3. The sealed ticket never appears in any API response, log line, or audit detail — one
   table-driven test per surface, mirroring `TestWireGuardRepo_PrivateKeyEncryptedAtRest`'s shape,
   plus a stored-bytes assertion that the plaintext ticket is absent from the DB file.
4. The ticket is wiped on confirm, on rollback, on expiry, and on changeset deletion — asserted
   against the stored row in all four cases.
5. Registry enumeration test: no route, MCP tool, or plugin capability can reach the sealed
   ticket. It is unsealable only from that changeset's own revert path.
6. A changeset whose confirm window would outlive its ticket reports reduced coverage **at apply
   time**, and the UI shows it.
7. `sdn.apply` gets the same treatment as `fw.*`, or the card's report argues concretely why it
   cannot — `planning/reports/T-502.md` names both.
8. `make check` green; docs updated as above.

---

## T-1806 · Trustworthy CI and branch protection ★
**model:** sonnet-5 · **size:** M · **depends:** — (start here, before everything) · **context:** `.github/workflows/`, `Makefile` (`check`, `lint`, `test`), `docs/development.md`

**Objective:** The `check` and `fuzz` jobs fail independently of the diff and `main` has no
branch protection — the signal everyone ignores is also the signal nothing enforces. Make CI
green, deterministic, and required.

**Deliverables:**
- Root-cause and fix (or delete, with an argued reason) the flaky `check` and `fuzz` jobs. A job
  that cannot be made deterministic should not be in the required set pretending otherwise.
- Toolchain pinning across Go, Node, `golangci-lint`, and `govulncheck` so a CI run is
  reproducible.
- `npm audit`: replace the permanent red X with an explicit allowlist of accepted transitive
  advisories, each with a rationale and an **expiry date** that fails the build when it passes.
  The current set — `brace-expansion`, `postcss`, `dompurify` via `monaco-editor`,
  `react-router` — is the starting inventory.
- Branch protection on `main`: required status checks, no force-push.
- `docs/development.md` documents what is required, what is advisory, and how to add a check.

**Acceptance criteria:**
1. Ten consecutive CI runs on an unchanged commit are green — flakiness is measured, not asserted.
2. `make check` locally and CI agree on the same failures for the same tree; a deliberately
   introduced lint error and a deliberately introduced test failure each fail both.
3. The `npm audit` allowlist has a rationale and an expiry per entry, and an expired entry fails
   the build (tested by setting one to a past date).
4. Branch protection is on with required checks; a PR failing them cannot merge.
5. `docs/development.md` reflects the final state.

---

### T-1806-bug-01 · The Playwright e2e suite has never been run by anything

**Severity:** High — this is the largest CI-trustworthiness gap found in this card, squarely
inside T-1806's objective ("make CI green, deterministic, and required"). It doesn't fail CI
(nothing invokes it, so there's nothing to go red); it silently doesn't exist as a check at all,
while being cited as evidence across the project as if it did.

**Finding:** `web/e2e/` holds **31** `*.spec.ts` files (`find web/e2e -name '*.spec.ts' | wc -l`)
plus `helpers.ts` and a `topology.spec.ts-snapshots/` baseline-image directory. **14** task cards
across three shipped arcs (`grep -rl 'Playwright\|web/e2e' planning/tasks/*.md | wc -l`) cite
Playwright/`web/e2e` as acceptance evidence. **43** implementation reports
(`grep -ril 'playwright\|e2e' planning/reports/*.md | wc -l`) claim e2e coverage. **Zero** CI jobs
invoke Playwright: `ci.yml` runs `make check` (lint/test/govulncheck/npm audit) and `make build`
+ `make deb`; `packaging-matrix.yml` and `release.yml` run builds and packaging only — grepped all
three workflow files for `e2e`/`playwright`, no hits. **No** `make` target runs it either
(`grep -n 'e2e\|playwright' Makefile` — no hits); it is reachable only via `npm run e2e` inside
`web/`, run by hand.

**What this implies for the 43 reports:** every one of them that cites a passing e2e spec as
acceptance evidence is citing a run that happened, if at all, on that task's author's own machine
at that moment — never reproduced, never re-verified, never gated on again since. There is no
evidence any of the 31 specs, individually or as a suite, currently passes against today's `main`
build, only that a person once believed one did.

**Investigation performed this card** (characterization only, per this card's scope — repairing
the suite is its own card, not attempted here):
- The harness itself is not broken. In a quiet environment (verified: nothing on ports
  8006-8008/18006-18007/28006-28007/38006-38007/48006-48007/58006-58007, no stray
  `pvemock`/`vnproxd`/`k8smock` processes), `cd web && npm run build` succeeds, and Playwright's
  `webServer` array boots all eight mock-PVE/vnproxd/k8smock backend pairs it defines regardless
  of which single spec is targeted (confirmed via the server logs' distinct listening ports) —
  running even one spec pays the full fleet's startup cost.
- Two representative specs were run individually against this cost's base commit (`0029eb9`,
  pre-T-1805/T-1907): `npx playwright test e2e/dashboard.spec.ts` (2 passed, 33.7s) and
  `npx playwright test e2e/mgmt-redundancy.spec.ts` (2 passed, 33.2s). Both green on first
  attempt.
- This does **not** contradict a separate, independently-reported finding (from a sibling task's
  investigation, via a controlled A/B test) that `mgmt-redundancy.spec.ts` fails on a clean
  checkout of current `main` — that `main` has since merged T-1805 (schema migration 33,
  unattended-revert ticket) and T-1907 (physical-layer collapse) past this worktree's branch
  point, either of which could plausibly be the source of a regression this worktree's stale
  commit wouldn't show. The two facts together are the actual characterization: the harness
  mechanics work, but the suite is unenforced, unreproduced-by-CI, and now has at least one
  concretely reported failure against current `main` with no CI in place to have caught it at
  merge time — exactly the failure mode "43 reports claim e2e coverage" should have prevented.

**Recommendation (not implemented here — a future card's decision):**
- e2e does **not** belong in `make check`'s required, every-push gate as currently shaped: it
  needs a downloaded Chromium, boots eight full mock-PVE/vnproxd/k8smock backend pairs per run
  (real Go processes, real ports, real timeouts up to 120-180s each), and the existing specs
  total well over the "<10 min" runtime `docs/development.md` asks of `ci.yml`. Folding it into
  `check` would make the fixed, deterministic gate this card just built slow and flaky by
  association.
- The two realistic homes are **a dedicated required `e2e` CI job** (parallel to `check`, `fuzz`,
  `package`, added to branch protection's required contexts once it's proven stable) or **a
  nightly/scheduled workflow** that doesn't block individual PRs but reports failures where a
  human will see them. Given 31 specs have never been verified in aggregate even once, a nightly
  first — to establish an honest baseline pass rate and burn down whatever it finds — is the
  lower-risk sequencing; promoting to a required per-PR job is the second step once that baseline
  is credible. Either way, the job needs its own budget analysis (how long does the full suite
  actually take, cold and warm) that this card did not attempt, since running all 31 specs was
  explicitly out of scope here.
- Whichever path is chosen, it should fix `mgmt-redundancy.spec.ts` against current `main` first
  (a currently-known-red spec is not a good baseline to inherit) and should treat "does the
  aggregate 31-spec run pass at all" as its own acceptance criterion, since that has never once
  been established.

`docs/development.md` now states plainly that `web/e2e/` is currently unenforced, so the next
author doesn't assume otherwise.

---

## T-1807 · Migration upgrade-chain testing ★
**model:** sonnet-5 · **size:** M · **depends:** T-1806 · **context:** `internal/store/migrations/`, `internal/store/` (migration runner), `internal/migration/`, `docs/data-model.md`, `packaging/debian/`

**Objective:** Schema migrations are forward-only and there are 33 of them (32 shipped, plus
T-1805's), and no test walks a v1.0-era database up to current. Every upgrade in the field takes
that path untested.

**Deliverables:**
- A fixture corpus of databases at historical schema versions — generated reproducibly by running
  the migration set up to version N and seeding representative rows, not hand-built blobs.
- A table-driven test asserting every historical version opens, migrates to current, and serves:
  the store's own reads work, and no migration silently drops data. Data preservation is the part
  that matters — "it migrated without error" is a weak assertion.
- Package upgrade/downgrade semantics tested on a real install path (`podman` packaging tests
  already exist): conffile handling, service restart, key preservation across upgrade,
  purge semantics.
- `docs/deployment.md` gains a supported-upgrade-path statement: which versions can upgrade
  directly to current, and what to do if a version is older than that.

**Acceptance criteria:**
1. Every schema version from 1 to current has a fixture that opens and migrates cleanly.
2. Representative rows seeded at version N are still present and correct at current — asserted
   per table, not just "the migration returned nil".
3. A deliberately destructive migration added to the test set is caught by the data-preservation
   assertions (proving the assertions have teeth).
4. Package upgrade from the oldest supported release preserves `/etc/vnprox/keys` and the store,
   and the service comes back up — exercised in the packaging test container.
5. `docs/deployment.md` states the supported upgrade path; `make check` green.

---

### T-1807-bug-01 · vnprox's own test tooling assumes exclusive use of the machine

**Severity:** Medium — not a product defect, but a recurring, now three-times-confirmed source of
false "product is broken" signals inside exactly the kind of concurrent-worktree development this
project's own orchestration convention (`planning/implementation-plan-proven.md`) runs by design.

**Finding:** T-1807's own AC4 packaging test (`packaging/test/upgrade-service.sh`, new this card)
needs a real running `vnprox.service` under systemd to verify "the service comes back up" for
real (not a proxy like "the binary didn't crash when invoked manually" — see the file's own doc
comment for why `deb-install.sh`/`upgrade.sh` explicitly punted on this). Built and first run
against vnprox's shipped default port 8007 with `--network=host` (required in this sandbox: no
pasta/slirp4netns for rootless `podman run`'s network stack, so a bridge network can't reach
`deb.debian.org` — see `port-conflict.sh`'s identical note). Result: `vnproxd` failed to bind and
the test failed with a confusing "systemd reports the unit active(running) but nothing is
listening" symptom — root-caused (by the orchestrator, mid-card) to a concurrent Playwright run
of the coordinator's own `webServer` fleet (T-2001 verification) holding `127.0.0.1:8007` at the
same moment. This is the exact failure mode `web/e2e`'s own port scheme already had to solve for
itself: `web/playwright.config.ts` and `web/e2e/*.spec.ts` claim the entire "N8006/N8007" family
(8006-8008, 18006-18008, 28006-28007, 38006-38007, 48006-48007, 58006-58007) specifically so its
own eight concurrently-running mock-PVE/vnproxd/k8smock fixture pairs don't collide with each
other — but nothing stopped a ninth, unrelated piece of test tooling (this card's packaging test,
`make dev`, or a developer's own manual `systemctl start vnprox`) from independently reaching for
the one port every one of those schemes treats as "the" vnprox port, 8007 itself.

**Same class of finding, third occurrence this arc:**
- T-1806 hit it with `golangci-lint`'s file lock (`parallel golangci-lint is running` hard error)
  — fixed with `--allow-serial-runners`.
- T-1806-bug-01 documents the e2e fleet's own port scheme as evidence a "quiet machine" is already
  a precondition its 31 specs assume, without ever stating that requirement anywhere a human or
  agent would see it before running them.
- This card is the third: a packaging test built with the same "the machine is mine alone"
  assumption every prior one made, caught immediately because the coordinator's own concurrent
  verification made the collision impossible to miss.

**Fixed in this card, for this one script** (not a general fix — see below): `TEST_PORT`
(`packaging/test/upgrade-service.sh`) is now `61007`, chosen outside the entire "N8006/N8007"
family rather than merely a different member of it, plus an explicit preflight check
(`ss -tln | grep ":${TEST_PORT}"`) that fails with a clear "port already in use, set
`VNPROX_TEST_SERVICE_PORT`" message instead of reproducing the confusing service-start-looking
failure a second time.

**Not fixed, and out of this card's scope:**
- No repo-wide port registry exists. `web/e2e`'s family, `make dev`'s 8007, `make mockpve`'s 8006,
  and now this script's 61007 are each self-consistent in isolation but only because every author
  so far has happened to pick something that doesn't collide with what came before — there is
  nothing that would catch two future additions choosing the same port independently, the same
  way this card's first draft (8007, then briefly 28017 — close enough to the e2e fleet's own
  28006/28007 to raise the question even though it didn't actually collide) had to be caught by
  hand both times.
- `packaging/test/deb-install.sh` and `packaging/test/port-conflict.sh` also use `--network=host`
  and, in port-conflict.sh's case, deliberately bind 8007 as its own test subject (a fake listener
  to prove install.sh's conflict detection) — a genuinely fixed requirement there, not something
  to move, but one more reason two of these scripts can never safely run concurrently against each
  other either.

**Recommendation (not implemented here — a future card's decision):** a single source of truth
for "which ports does this repo's tooling ever bind during development/test" (a doc table, or a
generated `lsof`/`ss` preflight helper every script sources) would turn this class of failure from
"confusing, root-caused by hand each time" into "caught at the first line of the offending
script's own output." Given this is the third occurrence in one phase, it is worth its own small
card rather than a fourth ad hoc fix.

---

## T-1808 · Scale validation on real cluster data ★
**model:** sonnet-5 · **kind:** validation · **size:** M · **depends:** T-1801 · **context:** `docs/features/topology.md` §4 (the documented scale target), `docs/testing/topology-performance.md` (the synthetic measurement), `internal/topology/`, `web/src/topology/`

**Objective:** The documented scale target is measured synthetically. Measure it against a real
cluster's real config and publish the numbers — including where they break down.

**Deliverables:**
- A harness section capturing, from a real node: projection time, payload size, entity counts per
  layer, and the collector poll durations behind them.
- A frontend measurement pass (Playwright, `web/e2e/`) for first paint, interaction latency, and
  memory at the captured entity counts.
- `docs/testing/topology-performance.md` updated with real-hardware numbers alongside the
  synthetic ones, and an explicit statement of where the two diverge.
- A finding, filed as a bug card, wherever real numbers miss the documented target.

**Acceptance criteria:**
1. Real-hardware numbers exist for projection time, payload size, and first paint, with the node
   and PVE version recorded.
2. Synthetic and real numbers are compared side by side; divergences over 25% are called out.
3. Any miss against `topology.md` §4's target is filed as a bug card, not absorbed by editing the
   target downward.
4. The measurement is repeatable from the committed harness by someone who was not present.
5. `make check` green.

---

## T-2002-bug-01 · Frozen MCP tool payloads have no field-removal regression guard

**Severity:** Medium — caught in review before merge this time (T-2002's coordinator noticed the
coupling by hand), but the mechanism that let it get that far — a well-reasoned, fully-tested,
zero-repo-consumer removal of a field from a type that also happens to be a frozen MCP tool's
wire payload — will recur for any of the other eight tools unless something other than a human's
memory of `docs/architecture.md` §13 catches it.

**Finding:** T-2002 (phase 20, `planning/reports/T-2002.md`) removed `internal/sim.RuleRef
.RulesetRef` — unpopulated for `origin: "guest"`, the single most common deny case, and grepped
to have zero consumers anywhere in `internal/`, `cmd/`, or `web/src/`. The reasoning was sound on
every axis it checked. What it didn't check: `cmd/vnproxd/mcpwire.go`'s `mcpSimulatePath` returns
`sim.Result` **verbatim** as the `simulate.path` MCP tool's payload — one of the nine tools in the
**frozen v1 MCP manifest** (`docs/architecture.md` §13.1, decision D10), whose deprecation policy
is explicit: "no field/tool/event is ever renamed or removed... without a version bump." An
external MCP client (a T-1701 AI operator, exactly who this surface exists for) may read
`rulesetRef` today; that client's code cannot appear in this repo's own grep results, so "no
consumer found" was true and insufficient at the same time. Nothing in `internal/apicontract` (the
repo's existing contract-test package) covers MCP tool payloads at all — it is scoped to the
changeset/store lifecycle contract, a different surface entirely.

**What T-2002 did once this was flagged:** restored the field (populated correctly for all three
`RuleRef.Origin` values instead of removed — see the report), and added two narrow regression
tests: `internal/sim.TestRuleRef_JSONSchema_Stable` (golden-marshals a `RuleRef`, asserts every
documented field name survives) and `cmd/vnproxd.TestMCPSimulatePath_FrozenPayloadFields` (drives
a real deny scenario through `mcpSimulatePath` end to end and asserts the same on the actual wire
JSON). Both guard `simulate.path` specifically. Neither guards the other eight frozen tools
(`topology.get`, `findings.list`, `flows.query`, `ipam.subnets.list`, `diagnose.run`,
`changesets.diff`, `changesets.create`, `changesets.validate`), the plugin SDK's five frozen `v1`
extension-point interfaces (§13.2), or the event-stream schema (§13.3) — none of which have an
equivalent golden-shape test today, confirmed by grepping `internal/mcp`, `cmd/vnproxd/mcpwire*`,
and `internal/plugin` for `JSONSchema_Stable`/`FrozenPayload`-style test names (no hits beyond the
two this bugfix added).

**Recommendation (not implemented here — a future card's decision):**
- A systematic version of the two ad hoc tests above: one golden-shape test per frozen MCP tool
  (nine total) asserting its payload's documented field set survives a real invocation, living
  somewhere both discoverable and hard to skip — `internal/apicontract` (extended to cover MCP,
  not just changesets) or a new `internal/mcp/frozen_test.go` are both plausible homes; the choice
  is the future card's to make.
- Consider whether the plugin SDK's five §13.2 interfaces and the §13.3 event-stream schema need
  the analogous guard, or whether their existing coverage (if any) already serves this purpose —
  not verified here, out of this bug card's own scope.
- A cheaper, complementary option worth considering alongside (not instead of) the tests: a
  `docs/architecture.md` §13 cross-reference comment convention on every frozen type/field (this
  bugfix added one to `sim.RuleRef` and `simBlockingRule` by hand) — greppable, but relies on a
  human reading it before deleting the field, exactly the step that failed here. Tests don't have
  that failure mode.

---

## Card-author notes

- **These cards were written before any of them ran** (D4). T-1802 and T-1804 are expected to
  generate bug cards that land in this file as `T-1802-bug-NN` / `T-1804-bug-NN` subsections, or
  as their own `T-18NN` cards where they need dependency edges. That churn is the anticipated
  cost of planning the arc up front.
- **T-1805 is the only card here that changes product behavior.** Everything else produces
  evidence, fixtures, or CI. Reviewers should hold it to the same bar as T-1401 (key custody) and
  T-1704 (HA fencing), not to a validation card's bar.
- **The `kind: validation` marker is load-bearing**, not decoration: it changes the dispatch
  prompt (see `planning/implementation-plan-proven.md`), makes the card two-turn minimum, and
  forbids the agent from asserting hardware behavior. A card without it is ordinary work.
- **One open question for T-1805's executor:** whether `sdn.apply` can use the same sealed-ticket
  mechanism as `fw.*`. `planning/reports/T-502.md` observes that `sdn.apply`'s step is already
  excluded from `rollbackAfterFailure`'s node iteration for reasons that may be independent of
  credential availability. The card requires either equal treatment or a concrete argument for
  why not — it does not assume the answer.
