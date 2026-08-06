# Phase 21 — Ecosystem and reach (v4.0)

Goal: make vnprox gettable and integrable. The platform work in v3.0 built the seams — a frozen
API contract with a conformance suite, a signed blueprint bundle format, a plugin hub with
signature and capability gates — and almost none of them have anything on the other side yet.
There is no Terraform provider, no Ansible collection, no apt repository, no registry, and no
docs site. The material exists; the distribution does not.

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

**First full local run, 2026-08-06** (90 tests, 1 worker) — failures observed in the first 32:

| Spec | Count | First diagnosis |
|---|---|---|
| `a11y` | 9 | **Real defect, fixed in this change**: the nav-rail findings badge rendered white on `bg-amber-500/90` at 2.61:1 against WCAG AA's 4.5:1. It lives in the chrome, so it failed on every page — one fix, nine specs. Re-run needed to confirm all nine clear. |
| `changesets` | 2 | Untriaged |
| `conntrack` | 1 | Untriaged — locator waits on `[data-entity-ref="bridge:pve1:vmbr0"]` |
| `diagnose` | 1 | Untriaged — waits on a guest button inside a dialog |
| `federation` | 1 | Untriaged — waits on `region "Global cluster map"` |
| `command-palette` | 1 | Untriaged — `?` should open `dialog "Keyboard shortcuts"`. **Check T-2201..T-2205 first**: phase 22 touched `ShortcutHelpDialog` and added a second top-bar button, so this may be a regression that phase shipped. It was not caught because this suite was not running. |

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
