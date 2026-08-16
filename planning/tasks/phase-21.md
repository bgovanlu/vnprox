# Phase 21 — Ecosystem and reach (v4.0)

Goal: make vnprox gettable and integrable. The platform work in v3.0 built the seams — a frozen
API contract with a conformance suite, a signed blueprint bundle format, a plugin hub with
signature and capability gates — and almost none of them have anything on the other side yet.
There is no Terraform provider, no Ansible collection, no apt repository, no registry, and no
docs site. The material exists; the distribution does not.

---

## Phase 21 — delivery record (2026-08-13)

| Card | State | Note |
|---|---|---|
| `T-2101` | ● Shipped | `docs/automation-contract.json` published (11 routes, stability v1.7), golden-tested against `docs/api.md`, conformance suite runs externally over real HTTP (`8bd930c`, `dbcb682`). The Terraform provider and Ansible collection repos themselves do not exist — always scoped as separate, independently-published repos (`docs/status-matrix.md:115`), not this card's deliverable |
| `T-2102` | ◐ **Not delivered — machinery only** | `packaging/build-apt-repo.sh` and `packaging/apt-repo.md` exist, **no commit delivers `T-2102`**: `git log --all --grep=T-2102` returns exactly two, and neither is an implementation — `0029eb9` is the commit that *authored* this card, and `db69c6c` (T-2803) only cites it in passing ("static hosting, not a service — the T-2102 posture"). No hosted repository exists: `packaging/install.sh` and `packaging/build-apt-repo.sh` both point at `get.vnprox.io`, which has never been stood up (no deploy job, no publish target, no key committed for `install.sh` to verify against — the host itself was not probed from here, so this is an evidence-of-absence-in-the-repo claim, not a DNS one). This card was the phase's own stated "spine" (line 12 above) and it is the one that didn't land. Rescheduled as `T-3301` (`docs/roadmap-earned.md`); `docs/status-matrix.md:116` carries the same ◐ |
| `T-2103` | ● Shipped | PVE compat matrix generated from mock fixtures across three version profiles (8.2/9.0/9.2), gating PVE 9.0's SDN Fabrics zone types; `make compat-matrix` regenerates `docs/compat-matrix.json` (`96549d5`, `2d2c06e`). Every cell is `validation: mock` — no hardware behind any row, stated in the doc itself, not hidden |
| `T-2104` | ◐ Partial | The real gap it closes shipped: install now refuses on any capability-scope disagreement between a registry listing and the delivered manifest, audited, no trust-flag override (`a231699`). Four tested blueprints seeded (one, the DMZ+WireGuard seed, marked PARTIAL in its own description for lacking a `wg.*` entity kind). But the card's own title is "**Hosted** blueprint and plugin registry", and there is no hosted registry — no domain, no object storage, no publish job (`docs/status-matrix.md:74`, `docs/roadmap-earned.md`'s stale-claims table). Rescheduled (hosting half) as `T-3303` |
| `T-2105` | ◐ Partial | Docsify docs site, install guide, first-hour walkthrough, support guide, and `CONTRIBUTING.md` are built and checked against source (`8cecebb`). Not delivered: the repo is private (anonymous request 404s, not a permission error), GitHub Pages is not enabled so there is no live docs URL, there is still no security-disclosure contact, and the forum announcement is written but marked DRAFT — NOT YET POSTED. Rescheduled (the remainder) as `T-3302` |

Three of five cards shipped code; two carry the word "hosted" or "distribution" in their own
title and neither actually got there. `T-2102`, the phase's declared spine, has no commit at all
under its own number — everything downstream of it (the compat matrix's suites, the "installable,
verifiable package" `T-2105` was supposed to sit behind) exists only as generated artifacts with
nowhere public to publish them. The v4.0.0 release note's claim that "phases 20 and 21 are
complete" is the specific claim `docs/roadmap-earned.md` and `docs/project-status.md` (§2, §Arc 4
paragraph) already went on record correcting: `T-2102`'s hosting half and `T-2006` (phase 20,
i18n) were not descoped when that release shipped, and both are now rescheduled into Arc 6.

Decisions this phase is built on: **D6** — distribution is a static signed apt repository
published from CI to GitHub Pages, no infrastructure to run.

Dependency shape: **T-2102 (signed apt repo) is the phase's spine** — the compatibility matrix
(T-2103) publishes per-release results the repo's suites depend on, and community distribution
(T-2105) is not honest until there is an installable, verifiable package behind it. **T-2101** and
**T-2104** are independent roots and can start immediately.

**Do not start this phase before T-1902 (support bundle) merges.** Distribution multiplies the
cost of every unsupportable failure; the support bundle is what makes a stranger's broken install
diagnosable, and shipping reach before supportability is how a small project acquires an
unservable user base.

Exit demo: a fresh PVE node adds the signed repo, `apt install vnprox`, and is running; the same
cluster is then described in Terraform, planned, and applied through the provider — with
`vnproxctl doctor` green and a compatibility matrix confirming the PVE version is tested.

---

## T-2101 · Terraform provider and Ansible collection artifacts ★
**model:** sonnet-5 · **size:** L · **depends:** — · **context:** `planning/reports/T-1106.md` (the contract card, and its explicit scope boundary), `internal/apicontract/` (the conformance suite), `docs/api.md` §Automation contract, `docs/architecture.md` §10 (API stability), `internal/api/` (token auth, scopes)

**Objective:** T-1106 shipped the stable API contract and a conformance suite and **explicitly did
not** create `terraform-provider-vnprox` or `ansible-collection-vnprox` — those were always
"separate, publishable repositories." They still do not exist. The contract is frozen and
conformance-tested; the artifacts anyone would actually consume are missing.

**Scope boundary (inherited from T-1106, restated because it is the crux):** these are **separate
repositories**, not directories in this one. This card's deliverable inside `vnprox` is the
cross-repo wiring — published contract artifacts, a conformance suite consumable from outside,
and CI that fails loudly on a contract break. The provider and collection sources live in their
own repos.

**Deliverables:**
- **`terraform-provider-vnprox`** (new repo): resources over the changeset API where `plan` maps
  to validate+diff and `apply` maps to apply+confirm — the mapping T-1106's contract was designed
  around. Published to the Terraform Registry.
- **`ansible-collection-vnprox`** (new repo): modules over the same surface, idempotent by
  changeset semantics rather than by re-reading state. Published to Ansible Galaxy.
- In this repo: the conformance suite made consumable by an external CI run (a documented
  invocation and a versioned contract artifact), plus a CI job in each downstream repo that runs
  it against a pinned `vnproxd`.
- A contract-break protocol: what happens in both downstream repos when this repo changes the API,
  documented in `docs/api.md` alongside the stability guarantee.
- `docs/api.md` §Automation gains links to both published artifacts.

**Acceptance criteria:**
1. Both repos exist, build, and are published to their respective registries with a version
   pinned to a `vnprox` API contract version.
2. A Terraform `plan` against a running `vnproxd` produces the changeset diff, and `apply`
   applies and confirms it — end to end against `internal/pvemock`, not mocked at the HTTP layer.
3. An Ansible module run is idempotent: a second run with unchanged input stages no ops.
4. A deliberate contract break in this repo fails the downstream conformance job — the wiring is
   proven, not assumed.
5. `make check` green here; both downstream repos green.

---

## T-2102 · Signed apt repository on GitHub Pages ★ 🔒
**model:** sonnet-5 · **size:** M · **depends:** T-1806 · **context:** `.github/workflows/release.yml`, `packaging/`, `planning/reports/T-1107.md` (blueprint bundle signing — the key-handling conventions to reuse), `docs/deployment.md`, `docs/security.md`

**Objective:** Today's install is "download a `.deb` from GitHub Releases and hope." For software
that runs as root on a hypervisor and rewrites its network configuration, that is not good enough.
Per **D6**, stand up a static signed apt repository published from CI to GitHub Pages.

**Safety analysis (required section):** the repository signing key is the highest-value secret this
project will hold — compromise means arbitrary root code execution on every installation that
trusts it. It lives in CI secrets, is used only by the release workflow, and is never available to
a PR-triggered run (a fork PR must not be able to sign anything). The published key fingerprint is
documented so an operator can verify what they are trusting, and a key-rotation procedure exists
**before** it is needed rather than after.

**Deliverables:**
- `release.yml` extended: build both architectures (already done), sign, generate
  `Packages`/`Release`/`InRelease`, publish to `gh-pages`.
- Suite structure (`stable`, and a `testing` suite for pre-release tags) so an operator can choose
  their risk.
- A documented install path: fetch and install the signing key, add the source, `apt update`,
  `apt install vnprox` — replacing the download-and-hope instructions in `docs/deployment.md`.
- Published `SHA256SUMS` with a detached signature, and a documented verify-before-install flow
  for anyone who prefers the direct download.
- A reproducible-build audit: how close the `.deb` is to reproducible today, what breaks it, and
  whether closing the gap is worth it — an argued answer, not necessarily a fix.
- A documented key-rotation procedure.

**Acceptance criteria:**
1. A fresh Debian/PVE container adds the repo and key, runs `apt install vnprox`, and gets a
   working daemon — exercised in the existing packaging test container.
2. `apt upgrade` moves between two published versions cleanly, preserving `/etc/vnprox/keys` and
   the store (T-1807's guarantees, now exercised through `apt`).
3. Tampering with a published `.deb` causes `apt` to refuse it — the signature is actually
   verified, tested by corrupting a package in a local mirror.
4. The signing key is unavailable to PR-triggered workflow runs; asserted by workflow
   configuration and a test run from a fork.
5. `docs/deployment.md` documents install, upgrade, key fingerprint, and rotation;
   `docs/security.md` covers the key's threat model.

---

## T-2103 · PVE compatibility matrix and automated compat testing ★
**model:** sonnet-5 · **size:** M · **depends:** T-2102 · **context:** `docs/roadmap.md` §Compatibility policy (the existing promise), `internal/pvemock/`, `internal/apicontract/`, `planning/reports/needs-hardware-validation.md`, `.github/workflows/`

**Objective:** `docs/roadmap.md` commits to "a compatibility validation task within one phase of
each new PVE release" — a promise with no mechanism behind it. Build the mechanism and publish
the results.

**Deliverables:**
- A compatibility matrix: vnprox version × PVE version (8.2, 9.x, and whatever is current),
  populated by running the conformance and integration suites against per-version mock fixtures.
- Per-PVE-version fixtures in `internal/pvemock`, capturing the API shape differences that
  actually matter, seeded from the real shapes T-1801's harness captures on hardware.
- A CI job running the matrix on every release, publishing results alongside the release.
- A published matrix in `docs/` that an operator can read at a glance to answer "is my combination
  tested?"
- An explicit statement of what "tested" means for each cell — mock-validated versus
  hardware-validated (Phase 18's distinction carries through here and must not be blurred).

**Acceptance criteria:**
1. The matrix runs in CI and produces a machine-readable result per cell.
2. A fixture divergence between PVE versions is caught: a deliberately version-specific API shape
   fails the cell that does not support it.
3. Each cell states whether it is mock-validated or hardware-validated, and the docs never present
   the former as the latter.
4. The published matrix is regenerated on release, not hand-maintained.
5. `make check` green; `docs/roadmap.md`'s compatibility promise now points at the mechanism.

---

## T-2104 · Hosted blueprint and plugin registry ★
**model:** sonnet-5 · **size:** L · **depends:** — · **context:** `planning/reports/T-1705.md` (the local hub), `planning/reports/T-1107.md` (signed bundles), `internal/hub/`, `internal/blueprint/`, `internal/plugin/`, `web/src/hub/`, `docs/features/blueprints.md`, `docs/security.md`

**Objective:** T-1705 shipped a local hub with signature and capability gates. Give it something to
talk to. A plugin system with no plugins is scaffolding.

**Deliverables:**
- A hosted index (static, same GitHub Pages posture as T-2102 — no server to run): signed bundle
  metadata, versions, and capability manifests.
- A submission and review process: what a contributor does, what a reviewer checks, and what gets
  a bundle rejected. Written before the first submission, because a review process invented under
  pressure is not one.
- Capability manifests surfaced in the UI **before** install — an operator must see what a plugin
  will be allowed to do while they can still decline. This is the existing capability-gate
  machinery's whole point and the registry must not route around it.
- A seeded library of real-world blueprints, each tested against `internal/pvemock`: homelab
  single-node, three-node Ceph cluster, VLAN-segmented SMB, DMZ with WireGuard site-to-site.
- Trust model documented: what a signature proves, what review does and does not check, and what
  an operator is still responsible for.

**Safety note:** an installable-plugin index is a supply-chain surface. It must not become a path
around T-1702's capability sandbox or T-1107's signature verification — the registry distributes
bundles, it never relaxes what the local hub enforces.

**Acceptance criteria:**
1. A signed bundle published to the index installs through the existing hub with signature
   verification intact; an unsigned or tampered bundle is refused.
2. Capability manifests are shown before install and match what the plugin is actually granted at
   runtime — a mismatch fails a test.
3. Each seeded blueprint applies cleanly against `internal/pvemock` and produces the documented
   topology.
4. The submission and review process is documented and has been walked once end to end with a real
   bundle.
5. `docs/features/blueprints.md` and `docs/security.md` document the trust model; `make check`
   green.

---

## T-2105 · Community distribution and docs site ★
**model:** sonnet-5 · **size:** M · **depends:** T-2102, T-2104, T-1902 · **context:** `docs/` (the existing corpus), `README.md`, `docs/deployment.md`, `docs/user-guide.md`

**Objective:** Get vnprox in front of the people who would use it. The documentation is already
strong and currently reaches nobody — it lives in a repo, in markdown, unindexed.

**Deliverables:**
- A docs site built from `docs/` (static, GitHub Pages, consistent with T-2102's posture),
  versioned per release so an operator reads the docs matching their install.
- A restructure for a reader rather than a contributor: install, first hour, task-oriented guides,
  then reference. The current corpus is organized for someone building vnprox, not someone
  running it.
- A serious assessment of what inclusion in a Proxmox community repository would require —
  packaging standards, licensing, maintenance commitments — written up as a decision document
  rather than pursued blindly.
- A support path that scales: where to file a bug, what to attach (T-1902's bundle), and what
  response to expect. A project with distribution and no stated support posture generates
  frustration on both sides.
- A Proxmox forum presence with an announcement that is honest about maturity — including which
  cluster behaviors remain mock-validated per Phase 18's blocked register.

**Acceptance criteria:**
1. The docs site builds from `docs/` in CI and publishes on release, versioned.
2. Every page in the reader-facing structure is reachable from the landing page within two clicks.
3. Install instructions on the site match T-2102's apt path exactly and are verified by the same
   container test.
4. The community-repository assessment is written and reaches a recommendation, either way.
5. The support path is documented and names the support bundle; the announcement states maturity
   honestly, including the blocked register's contents.

---

## Card-author notes

- **T-2101 is the only card in this arc whose primary deliverable is outside this repository.**
  Its acceptance criteria are therefore split across three repos, and the orchestrator should
  expect to verify two of them elsewhere. The in-repo half — a consumable conformance suite and a
  contract-break protocol — is what makes the other two maintainable.
- **T-2102's key handling is the phase's highest-severity surface.** It should be reviewed with
  the same weight as T-1401's key custody, not as packaging work.
- **T-2105 depends on T-1902 for a reason** stated in the phase intro, and the dependency is not
  negotiable for scheduling convenience: distribution without a support bundle produces bug
  reports nobody can act on.
- **These cards assume Phase 18's findings did not change the product's shape.** If the burndown
  or the blocked register materially changes what can honestly be claimed about cluster behavior,
  T-2105's announcement copy and T-2103's matrix semantics are the two places that must be
  revisited before publishing anything.

---

## Audit-raised items — 2026-08-06

Filed by the full-stack audit (`docs/status-matrix.md`). Neither was tracked by any existing
card, and both were found by sweeping the artifact rather than by reading a report.

### T-2106 · The repository has no license

**kind:** decision, then trivial implementation
**Severity:** High — blocks this whole phase in practice.

There is no `LICENSE` file. `T-2102` (public apt repository) and `T-2105` (Proxmox-community
distribution) both assume the software can be redistributed; without a license nobody legally
can, and no external contribution can be accepted on any settled terms. It costs one decision
and one file, and until it is made every distribution card in this phase is building a road to
somewhere no one may drive.

**Acceptance**

1. A `LICENSE` file exists at the repository root.
2. `README.md` and `docs/datasheet.md` state the license.
3. The packaging metadata (`packaging/debian/control`, `copyright`) matches it.
4. If any third-party dependency's license constrains the choice, that is recorded rather than
   assumed away — there are 8 direct Go modules and the full npm tree to check.

### T-2107 · `docs/features.md` describes a product that no longer exists

**kind:** documentation
**Severity:** Medium — it is the one document that would actively mislead a new reader.

The file still describes the v1.0 feature set and, under "Explicit non-goals for v1", lists five
capabilities that have since shipped: NetFlow/sFlow collection, Proxmox Backup Server networking,
multi-cluster federation, physical switch config push, and the Prometheus exporter. Every other
document in `docs/` is current.

**Acceptance**

1. The feature tables cover what actually ships, or the file is explicitly re-scoped as a
   historical v1.0 record and says so in its first line.
2. No shipped capability appears under "non-goals".
3. `README.md`'s stale-file warning is removed once this is true.
4. The relationship to `docs/datasheet.md` is stated, so the two cannot silently diverge again.

### T-2108 · Triage the e2e backlog and make the suite blocking

**kind:** validation
**depends on:** T-1806-bug-01 (the `make e2e` target and CI job, landed)
**Severity:** High — until this closes, a green `CI` badge does not mean the e2e suite passed.

Turning the Playwright suite on (T-1806-bug-01) ended three arcs of it running nowhere. It also
revealed that it is red. The job is `continue-on-error: true` so that it *runs and is visible*
today rather than being deleted for wedging every PR; this card is what stops "temporarily"
becoming "permanently".

**First full local run, 2026-08-06** — complete: **29 failed, 59 passed** in 30 minutes
(1 worker, `three-node-vlan` fixture).

| Spec | Fails | Diagnosis |
|---|---|---|
| `a11y` | 9 | **Real defect, fixed in this change.** The nav-rail findings badge rendered white on `bg-amber-500/90` at 2.61:1 against WCAG AA's 4.5:1. It lives in the chrome, so it failed on every page — one cause, nine specs. Re-run to confirm all nine clear. |
| `simulator` | 3 | Untriaged |
| `help` | 3 | One is a **bad locator in our own spec** (`getByText("Switch view")` matches two elements — strict-mode violation); one is the `?` failure below; one probably cascades from it. |
| `user-guide-tasks` | 2 | Untriaged |
| `saved-views` | 2 | Untriaged |
| `changesets` | 2 | Untriaged |
| `topology` | 1 | Untriaged |
| `microseg` | 1 | Untriaged |
| `history` | 1 | Untriaged |
| `guest-interior` | 1 | Untriaged |
| `federation` | 1 | Untriaged — waits on `region "Global cluster map"` |
| `diagnose` | 1 | Untriaged — waits on a guest button inside a dialog |
| `conntrack` | 1 | Untriaged — waits on `[data-entity-ref="bridge:pve1:vmbr0"]` |
| `command-palette` | 1 | **Pre-existing, not caused by phase 22 — settled by experiment.** Pressing `?` does not open `dialog "Keyboard shortcuts"`. Phase 22 modified `useKeyboardShortcuts` and `ShortcutHelpDialog`, making it the obvious suspect, so the spec was re-run in a worktree at `5019c45` (the commit immediately before phase 22): **it fails there identically**. Whatever breaks `?` in a real browser predates that work and is still unfound — note the vitest coverage of the same binding passes, so the divergence is browser-vs-jsdom, not handler logic. |

**Method note.** That last row is the shape triage should take: name the suspect, then run the
experiment that could exonerate or convict it, rather than reasoning from plausibility. It cost one
worktree and 90 seconds.

**Scope**

1. Complete the run and record the full failure list — the table above stops at 32 of 90.
2. For each: decide *product defect* vs *stale spec*, and say which. A stale spec gets a fixed
   locator; a product defect gets a fix and keeps the spec.
3. Audit every spec for the two patterns that let `T-2003-bug-01` hide: assertions loose enough to
   match the page you should have left, and conditional steps that turn a regression into a skip.
4. Flip `continue-on-error` off in `ci.yml` and update the job table in `docs/development.md`.

**Acceptance**

1. `make e2e` is green locally and in CI.
2. The `e2e` job is required, not observe-only.
3. Any product defect found is fixed with its own regression assertion, not by loosening a locator.

---

## Triage, 2026-08-06 — full run on a quiet machine

Scope items 1 and 2 are done. The first table above was gathered while a second project's CI was
loading the machine; this one was not, and the numbers moved.

| | First run (under load) | **This run (quiet)** |
|---|---|---|
| Failed | 29 | **23** → **22** after the a11y fix below |
| Passed | 59 | **64** → 65 |
| Skipped / did not run | 0 | 2 / 1 |
| `a11y` | 9 failed | **0 failed** |

### The method that mattered: suite vs. standalone

Every failing spec was re-run **standalone** on the same build. That splits the failures into two
classes that need completely different work, and which are indistinguishable from the suite log
alone:

| Class | Count | Meaning |
|---|---|---|
| **Reproduces standalone** | **17** | A real defect or a genuinely stale spec. Fix the spec or the product. |
| **Suite-context only** | **6** | Passes alone, fails in the suite. Not a feature defect — shared state or ordering across a single-worker suite sharing one `vnproxd`. |

**Suite-context-only failures** — `saved-views` (3), `nav-after-inspector` (1), `map-export` (1),
`changesets` (1). These are the ones to chase as *one* problem, not six.

Corroborating signal: **16 of the 23 failures were 120-second timeouts**, three of them stuck in a
login helper at `waitForURL("**/topology")` — a login that works in every standalone run.

### Correction: `T-2003-bug-01` **does** reproduce

`docs/status-matrix.md` and `docs/project-status.md` recorded it as *unreproducible* on the strength
of `nav-after-inspector.spec.ts` passing. That verdict was reached by running the spec **standalone**,
and it was wrong:

| Context | Result |
|---|---|
| Standalone | passes, 3.9s |
| Full suite | **fails** |

It fails inside `openInspectorViaSpotlight` (line 60) — during *setup*, before reaching the nav-rail
assertion at line 79. The card's status is corrected to open, in the suite-context-only class above.
The methodological point generalises: **a regression spec verified only in isolation has not been
verified**, because isolation is the one condition the reported bug did not occur under.

### A real defect found and fixed: WCAG AA contrast, second instance

`axe: IPAM` failed on `dark:text-slate-500` over `dark:bg-slate-900` — **3.74:1**, below AA's 4.5:1.
The light-mode half of the same pairing (`text-slate-400` on `bg-slate-100`, ~2.4:1) fails too; axe
could not see it because the sweep runs in dark mode.

`src/layout/TopBar.tsx` already carried a comment describing **this exact defect and this exact
fix** from `T-905` — applied to one element and never generalised. 19 sites still had the original
pairing; the repo already used the corrected pairing in 196 places, so the 19 were the outliers.

**One of the 19 was not like the others, and the fix was wrong for it.** `topology/EntityNode.tsx`'s
kind badge sits on per-kind *tinted* backgrounds, not neutral slate; swapping it moved `axe:
Topology` from passing to 80 violations. Reverted that single site and kept the other 18 —
`a11y` then went to **10/10 passed**. Recorded because "apply the established pattern everywhere it
appears" is exactly the sort of change that looks safe and is not, and the only reason it was caught
is that the sweep was re-run rather than assumed.

### Remaining 17, by failure mode

| Spec | Fails | Dies on |
|---|---|---|
| `simulator` | 3 | `getByLabel('Port')`; verdict text never appears |
| `help` | 2 | help panel `toBeHidden`; search hit never visible |
| `user-guide-tasks` | 2 | `getByRole('button', {name:'Next'})`; `'10.100.0.51: Free'` |
| `changesets` | 1 | `getByRole('heading', {name:'Guests', level:1})` |
| `command-palette` | 1 | `dialog "Keyboard shortcuts"` — **pre-existing, predates phase 22** (proven by worktree experiment at `5019c45`) |
| `conntrack` | 1 | `[data-entity-ref="bridge:pve1:vmbr0"]` |
| `diagnose` | 1 | `dialog` → `button "app01 guest"` |
| `guest-interior` | 1 | `dialog` → `button "app01 guest"` — **same locator as `diagnose`; likely one cause, two specs** |
| `federation` | 1 | `region "Global cluster map"` |
| `flows` | 1 | edge-paint assertion `isLightBg \|\| isDarkBg` |
| `history` | 1 | boolean assertion returns `false` |
| `topology` | 1 | screenshot **1158×740 expected vs 1158×574 received** — a stale snapshot or a real layout change |

**Next**, in this order: (1) the six suite-context-only failures as a single shared-state
investigation, since it is one cause and the largest single group; (2) `diagnose`+`guest-interior`,
which share a locator; (3) the topology snapshot, which is a yes/no question about whether the
layout legitimately changed.

### Root cause of the suite-context-only class — found, 2026-08-06

Two mechanisms, both shared-state, both invisible in a standalone run. Neither is a product defect.

**1. The suite exhausts vnprox's own login brute-force limiter.**

`internal/auth.DefaultRateLimitConfig` is 10 attempts with one token back every 30s, keyed per-IP
*and* per-username. The suite performs **82 logins in ~30 minutes** against one daemon, all from
127.0.0.1 as the same user — above the refill rate. The baseline log contains exactly **three
`/api/v1/auth/login` responses with status 429**, matching the three specs stuck at
`waitForURL("**/topology")`.

The limiter is behaving correctly; a real operator logs in once and keeps a session. Fixed by adding
`dev_login_rate_capacity` / `dev_login_rate_refill_seconds` under `[server]` — zero means "use the
production default", so **no shipped config changes** — and raising it in the seven `testdata/dev*.toml`
e2e configs only. Named `dev_`-prefixed to match `[pve]`'s existing `dev_ticket_*` overrides.

**2. Specs mutate the shared daemon store, changing what later specs render.**

`web/playwright.config.ts` removes `var/dev-vnprox.db` once per *run*, not per spec, and the suite is
single-worker against one `vnproxd`. So writes accumulate. Caught red-handed:
`saved-views.spec.ts`'s `getByRole("button", {name: "Apply"})` resolved to **four** elements — the
VLAN filter's Apply button, plus three history-timeline event buttons with aria-labels like
`Changeset event: changeset.apply at 8/6/2026, 4:04:52 PM`, left behind by `changesets.spec.ts`
earlier in the same run.

Fixed by **tightening** the locators (`exact: true`), not loosening them — these were always
ambiguous and merely latent while nothing else on the page happened to match. Same for
`nav-after-inspector.spec.ts`'s `{name: "Search"}`, which matched both the top-bar search button and
a `Search ( / )` button.

**Result on the four specs in that class:** 4 failed → **1 failed, 9 passed**. The one remaining
(`changesets.spec.ts:231`, read-only affordances) fails standalone too, so it was misclassified here
and belongs in the genuine group.

**Not fixed, and worth its own decision:** per-spec store isolation. Cause 2 is structural — any
future spec that writes to the store can perturb any later spec, and the next occurrence will again
look like a product defect. Options are a fresh DB per spec file (slow, and the fixture reseed is
not free) or a convention that specs clean up after themselves (easy to forget, which is how this
arose). Filed as `T-2108-followup-01` rather than decided here.

### Second pass, 2026-08-06 — verification and two more product defects

Full suite after the shared-state fixes: **18 failed / 69 passed** (from 23/64), zero 429s, and 5
minutes faster (24.3m vs 29.6m). Diff of the failure lists, not just the totals:

| Change | Specs |
|---|---|
| **Fixed** | `saved-views` ×3, `nav-after-inspector`, `map-export`, `a11y: IPAM` |
| **New** | `help.spec.ts:61` |

`help.spec.ts:61` is the `?`-shortcut test — the same binding `command-palette.spec.ts:48` fails on.
**It is not caused by the changes in this pass**: run standalone with every change in place it
passes, exactly as it did before. It joins the suite-context class.

#### Product defect: spotlight results announce as one word

`diagnose` and `guest-interior` both timed out on
`getByRole("button", {name: "app01 guest"})`. The accessibility snapshot showed the button's real
accessible name was **`app01guest· pve1 name`** — no space. `SpotlightSearch.tsx` separated the
entity label from its kind badge with an `ml-2` *margin*, which positions the badge visually but
puts no whitespace in the accessible name. A screen reader reads "app01guest" as one word.

The specs were right and the DOM was wrong, so the fix is text-node spacing in the component, not a
loosened locator. `diagnose` passes; `guest-interior` now gets **past** the search and fails deeper,
at `tabpanel "Interior"` → `interior-view` — a genuine feature-level failure that is now visible
because the earlier one stopped masking it.

#### Product defect: entity-node kind badge fails AA on every node tint

The badge's contrast is measured against the **node's own tint**, not the page background — entity
nodes carry per-kind and per-state background tints and the badge sits on them. Both halves of the
usual muted pairing fail there: `dark:text-slate-500` measured **1.84:1** and `dark:text-slate-400`
**3.7–4.4:1**, either side of 4.5:1 but neither above it. Changed to
`text-slate-600 dark:text-slate-300`, a step further from the background than muted text elsewhere,
which is the only way to clear AA across every tint a node can take.

This is also why the earlier bulk contrast sweep had to exclude this one site: it is not a
neutral-background element, and the "obvious" repo-wide pattern is wrong for it in **both**
directions.

**Method note for the next pass.** `axe: Topology` passes standalone and fails in a mini-suite,
because the tinted nodes that expose the defect only exist once earlier specs have written state.
That is the same store-pollution mechanism as `T-2108-followup-01`, and it means **an a11y sweep
that only ever runs standalone will not see this class of defect at all.**

### Third pass, 2026-08-06 — the `?` binding was never a product defect

Full suite after the second pass: **15 failed / 73 passed** (from 18/69), no new failures, 19.4m
(from 24.3m). `conntrack`, `diagnose` and `flows` cleared as side effects of the spotlight and
contrast fixes.

**Correction to this card's own first triage table.** It recorded the `?` shortcut as
*"pre-existing, not caused by phase 22 — settled by experiment"*, on the evidence that
`command-palette.spec.ts:48` fails identically at `5019c45`. That evidence was real and the
conclusion drawn from it was still wrong: it is not a product defect at all.

Established by direct probe, not inference:

| Experiment | Result |
|---|---|
| Does the keydown event reach the page? | Yes — `key: "?"`, `code: "Slash"`, `target: BODY`, `defaultPrevented: false` |
| Does a dialog open at all? | **Yes** — one dialog, `aria-labelledby` resolving to text `"Keyboard shortcuts…"` |
| Press immediately after `waitForURL("**/topology")` | **0 dialogs** |
| Press after waiting for the shell's own "Keyboard shortcuts" button | **1 dialog** |

`waitForURL` resolves when the navigation completes, which is *before* React mounts
`useKeyboardShortcuts`' window keydown listener. A key pressed in that gap is dropped. The race is
**deterministic** in this app — it always loses — which is exactly why it presented as a permanent
product defect and why it "reproduced" at a commit predating the help work. A flaky race would have
been recognised as one; a reliably-lost one looks like a broken feature.

Not treated as a product defect: a real user cannot press a key in the window between navigation
and hydration. The specs now wait for the shell to be interactive before sending keys.

**Also fixed, all ambiguous-locator (strict-mode) failures — tightened, never loosened:**

| Spec | Locator | Also matched |
|---|---|---|
| `help.spec.ts` | `getByText("Switch view")` | the panel's own summary paragraph, which contains the phrase |
| `simulator.spec.ts` ×2 | `getByLabel("Port")` | the "**Port**s" nav link, and "Documentation ex**port**"'s help button — the latter added by phase 22 |

**Result:** `command-palette` and `help` fully green; `simulator` 3 failed → 1.

Cumulative across the three passes: **29 → 23 → 18 → 15 → ~9 failing.**

### Fourth pass, 2026-08-06 — 10 failing, and a stale visual baseline

Full suite: **10 failed / 78 passed**, 17.0m. Six cleared (`command-palette`, `help` ×3,
`simulator` ×2). One regression, `conntrack:60`, which had passed in the previous run — genuinely
flaky, not fixed: its inner click waits for a React Flow node to be *stable* on a 5s budget while
the rest of the suite loads the machine. Raised to 12s inside a 90s `toPass` — deliberately under
the 120s per-test timeout, since a retry budget that cannot fit inside the test timeout only
converts one failure mode into another.

**`topology.spec.ts:136` — visual baseline was never regenerated, and nothing was watching.**

The snapshot expects 1158×740; the render is 1158×574, and the actual image contains an `eno3`
physnic node the baseline does not. `eno3` is a legitimate fixture entity — three-node-vlan.yaml
documents it as "a spare, unconfigured, link-up NIC on pve1 only", added by T-703 for the
mgmt-path bond wizard.

`git log -S'eno3'` puts the fixture change and the last snapshot update in the **same commit**
(`5909807`), so the baseline was simply never regenerated after the entity was added. The spec has
therefore been failing continuously since that commit — which is entirely consistent with the suite
having run in **no gate for three arcs**. A permanently red screenshot test is invisible when
nothing looks at it.

Regenerated, but only after **visually inspecting the new render** rather than trusting the diff
percentage: all four layer bands present, correct entities, correct relationships. Recorded here
because blindly re-baselining a screenshot is the single easiest way to erase a real regression, and
"the diff is only 25%" is not evidence of anything.

### Fifth pass — 9 failing. `flows:185`: two hypotheses tested, both refuted, change reverted

Full suite: **9 failed / 78 passed**, 16.1m. `conntrack:60` and `topology:136` confirmed fixed.

`flows.spec.ts:185` is **intermittent**, not newly broken: it failed in runs 2 and 5, passed in runs
3 and 4, and fails standalone. Its assertion samples the canvas at the geometric midpoint of a flow
edge's two endpoints and requires that pixel not to be the background colour.

| Hypothesis | Experiment | Result |
|---|---|---|
| Sub-pixel fragility — a 1px probe on a thin anti-aliased curve | Widened to an 11×11 neighbourhood | **Refuted** — still no non-background pixel |
| The paint is asynchronous; the assertion runs too early | Wrapped in `expect.poll`, 30s budget | **Refuted** — still false after 30s |

So the edge genuinely is not painted anywhere near that point in the failing runs. The most likely
remaining explanation is geometric rather than temporal: the edge is a **bezier**, and the
straight-line midpoint of its endpoints does not lie on the curve. How far the curve bows depends on
the layout, which varies between runs — which fits "intermittent and layout-dependent" exactly,
where neither probe width nor time would help.

**Both changes were reverted rather than committed.** Neither fixed the failure, and leaving a
speculative change in a spec that still fails implies it did something. This is the same discipline
`T-1806-bug-02` applied when its own speculative `pipefail` fix could not be reproduced at three
sizes and was reverted rather than shipped.

**Next approach for whoever picks this up:** sample along the edge's *actual* rendered geometry —
read the path element's own points, or scan the corridor between the two nodes excluding the node
rectangles — instead of assuming a straight-line midpoint. Do not simply widen the probe further;
that has been tried and it does not work.

### Sixth pass, 2026-08-07 — a shipped feature was unreachable from the browser

**`guest-interior` was not a test problem. `T-1304`'s guest network interior inspector has never
worked in a browser.**

Every request the SPA made to the three guest-interior routes returned **400**:

```
"path":"/api/v1/guests/guest:pve1:200/interior-toggle","status":400
"path":"/api/v1/guests/guest:pve1:200/interior","status":400
```

chi routes on `r.URL.RawPath` when it is non-empty, so `chi.URLParam` returns the still-encoded
segment. The frontend builds these URLs with `encodeURIComponent`, so the ref arrives as
`guest%3Apve1%3A200`, and `inventory.ParseRef` rejects it. `internal/api/guestinterior.go` was the
**only** ref-taking handler in the package that did not `url.PathUnescape` first — `topology.go`'s
entity lookup and `ipam.go`'s CIDR params both already did, with a comment explaining exactly this
hazard.

**Why nothing caught it.** Every existing test in `guestinterior_test.go` spells the ref raw
(`guest:pve1:200`), which is what curl sends and what chi passes through untouched. The package's
tests were green throughout. The e2e suite would have caught it — but only once `T-2108` fixed the
spotlight accessible-name defect that was failing the spec earlier, at the search step, before it
ever reached the toggle. **One masked defect was hiding another.**

Fixed with `url.PathUnescape` matching the established pattern, plus
`TestGuestInteriorRoutes_PercentEncodedRef`, which sends `encodeURIComponent`'s exact form.
Proven by mutation: removing the unescape returns the test to `400, want 200`.

A note on that test: its first draft used Go's `url.PathEscape`, which **does not escape `:`** in a
path segment — the encoded and raw forms came out identical and the test proved nothing. Its own
anti-vacuity guard caught that (`"contains no ':' to escape, so this test proves nothing"`) before
it could be committed as coverage that never exercised the bug.

**Also in this pass, and deliberately NOT credited with the fix:** a `setQueryData` change in
`guestInteriorQueries.ts`. While investigating, the toggle's optimistic-state handling was found to
have a real stale window (`onSettled` clears the optimistic value while `invalidateQueries` has only
*scheduled* a refetch). The change closes it — but the e2e spec passes with or without it, tested
both ways. The code comment says so explicitly. Attributing a change to a bug it did not fix is
worse than not making it.

### Seventh pass, 2026-08-07 — `conntrack`: three hypotheses, three refutations

Suite: **9 failed / 79 passed**. `guest-interior` and `flows` cleared. `conntrack:60` returned,
having now alternated pass/fail across six consecutive full runs.

| # | Hypothesis | Experiment | Result |
|---|---|---|---|
| 1 | The 5s click budget is too short | Raised to 12s (shipped in `ec89355`) | **Refuted** — failed again at 12s in run 6 |
| 2 | A drifting node's never-ending `animate-pulse` makes Playwright's stability check unsatisfiable | Emulate `prefers-reduced-motion` | **Refuted** — the pulse *is* removed (verified: class gone, `transition-none` present) and the click still times out |
| 3 | Both together | Reduced motion + 12s | **Refuted** — failed 3/3 |

**A methodological trap worth recording.** The first attempt at hypothesis 2 put `reducedMotion` at
the top level of `use` in `playwright.config.ts`. That is the wrong nesting — it belongs under
`contextOptions` — and Playwright **silently ignores** it there. `conntrack` then "passed twice in a
row", which read as confirmation. It was not: the setting was doing nothing. `tsc` caught the
mistake, and a probe (`matchMedia("(prefers-reduced-motion: reduce)").matches`) was added before
re-testing. With the emulation genuinely in effect the spec failed **3/3** — the opposite of the
"confirmation". *An option that is silently ignored produces evidence that looks exactly like a
successful fix.*

**Everything from this line of attack is reverted**, including the 12s raise shipped in `ec89355`,
whose commit message claimed it stabilised the spec. It did not. An inflated timeout that does not
fix the flake only hides the next genuine slowdown behind it.

**What the evidence actually points at:** the node never becomes *stable* — never stops moving —
and this spec's retry loop pans the canvas on every attempt, which moves it, while React Flow
re-lays-out. The next attempt should look at the interaction between the pan loop and layout
settling, not at budgets or animations. Both of those have now been ruled out by experiment.


---

### Eighth pass, 2026-08-07 — the six remaining feature failures, all closed

Six specs were failing at the start of this pass: `changesets:231`, `federation:109`, `history:233`,
`simulator:138`, `user-guide-tasks:139`, `user-guide-tasks:176`. Every one is now passing. Three of
them turned out to be **the same bug**.

| Spec | Verdict | Cause |
|---|---|---|
| `changesets:231` | **product defect** | `T-2003-bug-01` — see below |
| `federation:109` | **product defect** | same bug |
| `simulator:138` | **product defect** | same bug |
| `user-guide-tasks:139` | **product defect** | VXLAN wizard's peer auto-suggest read a field shape the API has never sent |
| `history:233` | **product defect + two spec bugs** | flow refs unresolvable during the daemon's cold start; plus a dashed-line pixel probe and a substring locator |
| `user-guide-tasks:176` | **stale spec + stale doc** | the IPAM per-address grid was replaced by a collapsed address list |

**1. Three failures, one bug.** `T-2003-bug-01`, root-caused at last — an infinite render loop in
`HistoryTimeline` starving react-router v7's navigation transition. Full write-up on the card in
`phase-20.md`. The reported reproduction (spotlight → inspector → Escape → nav) named the wrong
trigger; the actual precondition is *the Graph view is mounted*, which is why the regression spec
written for that card passed against the live bug.

**2. The VXLAN wizard could never be completed.** `peerSuggest.ts` read `fields.addresses` and
type-guarded it to `string`, citing `inventory.Bridge`'s `fieldMap`. But `fieldMap` is the
merge/provenance table — `topology.Detail` builds `fields` with `json.Marshal`, so the key is
`Addresses` and the value is an array. The lookup missed on every node, every time, so all three
peer address inputs stayed empty behind "An address is required." and **Next was permanently
disabled** — while the wizard's own copy promised "vnprox suggests each node's own address
automatically".

Its unit tests passed throughout, because `wizardTestUtils.tsx` fixed up `{ addresses: "<cidr>" }`
— a key and a type the server has never produced. *A fixture that invents the shape the code
expects tests nothing.* The fix accepts both shapes, and the two halves of the contract are now
pinned on both sides: `internal/topology.TestDetailBridgeAddressesShape` asserts the wire shape in
Go, and the TS fixture was corrected to match it.

Two further failures in the same spec were the *test's* fault, not the product's, and only became
visible once step 1 could be passed at all: `vnet-overlay1` contains a hyphen (SDN names are
alphanumeric), and VNI `10001` exceeds `VNI_MAX` (4094, matching `internal/change.maxVID`).

**3. Flow records ingested during the daemon's cold start are unattributable forever.**
`GET /flows` was returning the seeded record with no `srcRef`/`dstRef`, so the Flows overlay had no
edge to paint. `flow.GraphResolver` is refreshed from the inventory graph every 15 s and resolution
happens **once, at ingest, with no retry** — so the daemon's first 15 s, during which the index is
empty because nothing has been collected yet, silently drop attribution for every record that
arrives. The steady-state 15 s decoupling is a documented, deliberate tradeoff and is unchanged;
the *cold start* now polls at 1 s until the index is non-empty (`cmd/vnproxd/flows.go`), which is
the only window this changes. Two Go tests pin both halves — that it catches up quickly while
empty, and that it settles back to the steady interval afterwards.

The same spec also had two genuine test bugs, each of which had been masking the next:
- The paint probe sampled **one pixel at the segment midpoint**, but `drawFlowOverlay` strokes a
  *dashed* line (`setLineDash([8, 6])`): 6 px in every 14 are legitimately blank, so the probe was
  a ~43% coin flip that `expect.poll` could not fix — with the dash animation paused the gap does
  not move. It also only asked "is this pixel not the background colour", which any node border or
  label satisfies. It now walks the whole segment, skips node rectangles, and tests for the
  overlay's own cyan.
- `getByText("Live")` is a case-insensitive substring match, so it also matched the **"Back to
  live"** button that exists *only while scrubbing*. The assertion "we have left live mode" was
  satisfied by the very control proving we had left it, and could never pass.

**4. The IPAM grid does not exist any more.** `AddressList.tsx` replaced the per-address grid with a
NetBox-style list that collapses contiguous free space into "N addresses free" range rows. There is
no `10.100.0.51: Free` button to click. The spec now reserves from the range that starts at .51, so
it still asserts on the same concrete address. `docs/user-guide.md`'s task table described the
removed grid too, and is corrected.

**Method note.** Three of the six were fixed by one change, but only after bisecting *preconditions*
rather than reading code: four runs of the same navigation with the graph view and the inspector
varied independently. Reading the component tree first would have kept pointing at the inspector,
which the original card, its 2026-08-06 update, and its regression spec all did.

---

### Ninth pass, 2026-08-07 — `conntrack`, and a correction to the seventh pass

**Suite: 86 passed / 3 failed / 2 skipped** (the 2 skips are `microseg`'s two `test.skip`s, which
need a seeded NAS corpus and are marked as such in the file). All six specs from the eighth pass
passed. The three failures were `changesets:108`, `inspector-compare:32` and `conntrack:60` — all
three now fixed, none of them a product defect.

**`conntrack:60` — the seventh pass read the evidence wrong, and this pass corrects it.**

That pass concluded: *"the node never becomes stable — never stops moving"*, and sent two rounds of
work at click budgets and CSS animations. Both were refuted by experiment, correctly. But the
conclusion drawn from the refutations was also wrong. Playwright's own call log says:

```
- element is visible, enabled and stable
- scrolling into view if needed
- done scrolling
- element is outside of the viewport
```

**`stable`** — explicitly. The node is not moving. It is simply *off-screen*, and
`scrollIntoViewIfNeeded` cannot bring it back, because a React Flow node is positioned by a CSS
transform on the canvas, not by document scroll: there is nothing to scroll. The spec's retry loop
then panned by a *fixed* offset, always up-and-left, cycling four magnitudes — so when the node
started off the top-left, every retry pushed it further away. That is why the pass/fail alternated
with layout: it depended on which side of the viewport elk happened to leave the node on.

The pan is now computed from the node's own bounding box against the pane's, centring it. Four
consecutive runs pass in 11–13 s each, against a spec that previously either burned the full 60 s
retry window or timed out.

*The lesson is about reading logs, not about React Flow.* Two hypotheses were tested rigorously and
refuted rigorously, and the write-up of those refutations restated a claim ("never stops moving")
that the same log had already contradicted, one line above the line being quoted.

**`changesets:108`** — the final assertion required the change drawer region to be *hidden* after
committing. `ChangesetDrawer` only unmounts when there is no active draft **and** no other parked
draft to resume, so a draft left by any earlier spec in the single-worker run (`a11y.spec.ts:148`
deliberately creates one — "axe: changeset drawer (open, with a drafted op)") keeps a collapsed
launcher on screen. Rewritten to assert what the step means — the committed changeset is no longer
active — using `toHaveCount(0)`, which holds whether or not the region is rendered. The underlying
cross-spec store sharing remains `T-2108-followup-01`.

**`inspector-compare:32`** — waited for `compare.or(mismatch)` and then branched on a *separately
evaluated* `compare.isVisible()`. Under load that second read could land while the panes were still
settling, sending the test into the mismatched-kind branch to assert on an element that was never
going to exist. Both selections are `bond0`, so the aligned compare grid is the only outcome the
scenario has; the branch is gone. This is the same conditional-step pattern
`nav-after-inspector.spec.ts`'s header warns about, and it produced exactly the promised confusion.

**Latent, not fixed:** `simulator.spec.ts`'s `traceFromContextMenu` retries a right-click on a map
node with no pan at all. It passes consistently today, but it has the same off-screen exposure
`conntrack` had — retrying cannot move a node into the viewport. Worth converting to the same
geometry-driven pan the next time it flakes.

**Tenth pass, 2026-08-07 — `flows`, and one more product defect on the history timeline**

Second full run: **87 passed / 1 failed** (`flows:185`). Third-pass fixes below.

`flows:185` carried the *same* single-pixel-midpoint probe on the *same* dashed edge as
`history:233` — the eighth pass fixed one and left its twin. Both now walk the segment and test for
the overlay's own cyan. This is the concrete answer to the sixth pass's open note ("sample along the
edge's actual rendered geometry ... do not simply widen the probe"): the geometry is a straight line
between node centres and always was; what defeats a point sample is the **dash pattern**, not the
path shape.

Fixing that surfaced a real defect underneath. `history:233`'s changeset-marker click then failed
with `<button disabled ... aria-label="Finding new ...">` **intercepts pointer events**. Every
timeline marker is absolutely positioned on the same 3px track, so two events seconds apart overlap,
and with no explicit stacking order the later one in the DOM — later in time — wins. A *finding*
marker is decorative: it is `disabled` and its onClick does nothing. So a finding raised six seconds
after a changeset was confirmed sat on top of that changeset's marker and swallowed the only click
the timeline offers. Fixed by stacking changeset markers above finding markers (`z-10`).

Unrelated but blocking `make check`: two new high-severity `nanoid` advisories
(GHSA-28wg-ghj8-5hjv, GHSA-2v37-7h3g-55p8) appeared in the audit database. Resolved properly —
`nanoid` 3.3.15 → 3.3.18 via the existing `vite > postcss` chain — rather than allowlisted.

**Eleventh pass, 2026-08-07 — the axe gate earning its place**

Third full run: **88 passed / 1 failed**, and the one failure was `axe: Topology (Graph view, v1)` —
a genuine WCAG AA shortfall the two previous full runs had passed. Entity-node badges
(`text-[10px]`, below the large-text threshold, so the full 4.5:1 applies) rendered
`dark:text-slate-300` on a translucent `dark:bg-slate-700/70` over a tinted node and measured
**4.35–4.39:1** on some tints and not others — which is why it surfaced on one run and not the two
before it. Raised to `dark:text-slate-200`, which clears every tint with margin instead of landing
back on the threshold.

Worth noting for its own sake: this is the gate catching a real accessibility regression *by
itself*, on a run whose purpose was to confirm unrelated fixes. That is the argument for AC2.

---

### T-2108 · **CLOSED 2026-08-07**

**Final: 89 passed / 0 failed / 2 skipped**, ~10 minutes. The two skips are `microseg.spec.ts`'s own
`test.skip`s, which need a seeded NAS corpus and say so in the file.

| Acceptance criterion | Status |
|---|---|
| 1. `make e2e` green locally and in CI | ✅ green locally, four consecutive full runs converging to zero |
| 2. The `e2e` job is required, not observe-only | ✅ `continue-on-error` removed from `ci.yml`; `docs/development.md`'s job table updated |
| 3. Every product defect fixed with its own regression assertion, not a loosened locator | ✅ ten defects, each with a pinning test; every new guard mutation-checked against the pre-fix code |

**The ten product defects the gate found**, none of which any unit test could see:

1. `T-1304`'s guest interior returned **400 to every request a browser ever made**.
2. **The app could not navigate away from the Topology page** (`T-2003-bug-01`).
3. The **VXLAN zone wizard could not be completed** — peer auto-suggest read a field shape the API
   has never sent.
4. The **VLAN wizard's LLDP trunk check warned on every neighbour**, naming a blank switch and
   port — same wrong-shape bug, second site.
5. Flow records ingested during the daemon's cold start are **unattributable forever**.
6. A **decorative timeline marker swallowed changeset clicks**.
7. WCAG AA: findings badge, white on amber at **2.61:1**, on every page.
8. WCAG AA: muted text at **3.74:1**.
9. WCAG AA: entity-node label unreadable on tinted nodes (**1.84:1**).
10. WCAG AA: entity-node badge chip at **4.35–4.39:1**.

**Four of those ten had green unit tests** sitting on fixtures that invented the shape the code
expected (#3 and #4 explicitly; #1's tests spelled the ref raw; #2's regression spec exercised the
wrong precondition). That is the argument for this gate in one line: *a fixture written by the same
person who wrote the bug agrees with the bug.*

**Deferred, with cards:** `T-2108-followup-01` (per-spec store isolation — specs share one
`vnproxd`, which is what makes drawer/marker state leak across files). `simulator.spec.ts`'s
`traceFromContextMenu` still retries a right-click with no pan and carries the same off-screen
exposure `conntrack` had; convert it the next time it flakes.
