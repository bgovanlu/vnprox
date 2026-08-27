# Phase 38 — Open the source

**Arc:** *the project cannot be open sourced by pushing "public". This phase is the difference.*

**Source:** `planning/roadmap-open-source.md`, Phase 38 table (12 items, T-3801–T-3812). Sizes and
blockers below are reproduced verbatim from that table; do not add, remove, or renumber items.

## Premise

> ### ⚠️ Ground-truth check, run before writing this file — the premise needs correcting
>
> `planning/roadmap-open-source.md` frames this phase as the work that makes open-sourcing
> *possible*, as if none of the prerequisites exist yet. They partly do. Verified directly against
> the repo and against `gh api` on 2026-08-27:
>
> - **The GitHub repo is already public.** `gh repo view bgovanlu/vnprox` reports
>   `visibility: public`, `private: false`, created 2026-07-09, last pushed 2026-08-25 — and the
>   pushed `main` HEAD (`669a842c`) matches this working tree exactly. The 850-commit history,
>   including `CLAUDE.md` with `192.168.1.9`/`192.168.1.7` in it, is live on the public internet
>   **right now**. This changes T-3802 from "scrub before anyone can see it" to "an already-live
>   exposure whose scope needs to be established and, if the owner chooses a rewrite, force-pushed
>   over" — see that card.
> - **`LICENSE` (Apache-2.0), `NOTICE`, and `THIRD-PARTY-LICENSES.md` already exist**, committed
>   2026-08-06 (`license: adopt Apache-2.0, and close the agent-completable audit gaps`). The
>   license decision named in T-3801 was already made three weeks before this roadmap was written.
> - **`CONTRIBUTING.md`, `SECURITY.md`, and `.github/ISSUE_TEMPLATE/`** (`bug_report.md`,
>   `feature_request.md`, `config.yml`) already exist. `CONTRIBUTING.md` (2026-08-13) still asserts
>   the repo is private ("an anonymous request returns 404") — stale, per the point above.
>   `SECURITY.md` (2026-08-18, T-3302) already names a disclosure contact, which contradicts
>   `CONTRIBUTING.md`'s own claim that no contact exists yet. Both are corrected in the cards below
>   rather than rebuilt from scratch.
> - **The machine-readable OpenAPI contract (T-2405) already exists**: `GET /api/v1/openapi.json`
>   is generated from the live route table via `internal/apidoc`'s `Operations` table,
>   `TestOpenAPI_EveryRouteIsDescribed` gates drift in both directions, and the document is
>   committed at `docs/openapi.json` (14 979 lines) and regenerated with `make openapi`. T-3809's
>   real scope is publishing it and adding the generated-client drift tripwire — not building the
>   generator.
> - **A public docs site already exists and is live**: GitHub Pages serves `main`'s `/docs` at
>   `docs.vnprox.com` (`gh api repos/.../pages`: `status: built`, `public: true`), and Pages
>   deployments have run successfully every day this week — Pages is a free GitHub feature and is
>   unaffected by the Actions billing outage that disables `ci.yml`/`packaging-matrix.yml`
>   (confirmed: those two workflows' own runs are all `failure`/`disabled_manually` since
>   2026-08-13; only `pages build and deployment` succeeds). Several cards below publish to this
>   site rather than standing up a new one.
> - **`main` has *some* branch protection**, not none: `gh api repos/.../branches/main/protection`
>   returns `enforce_admins: true`, `allow_force_pushes: false`, `allow_deletions: false`. What is
>   genuinely absent is `required_status_checks` and `required_pull_request_reviews` — there is no
>   required-check gate, which is the specific thing T-3808 wires up. "No branch protection" (the
>   phrasing in the roadmap and in memory) overstates the gap; the precise gap is narrower and is
>   stated precisely in T-3808.
> - **Sigstore/cosign is already an approved, wired-in dependency** (T-3709, `internal/hubreg`'s
>   index signing, `docs/hub-registry.md` §"Sigstore-backed key custody") and **the installer
>   already verifies every artifact it downloads** (`docs/deployment.md`, T-2801 — "not skippable,
>   no `--insecure`"). T-3807 does not need to invent either; its real gap is SBOM generation.
>
> None of this shrinks the phase — T-3802's scrub is more urgent for being already-live, not less
> necessary — but every card below is written against what actually exists, not against a blank
> slate. Where a deliverable turned out to already exist, the card says so and scopes to what
> remains.

Every card is a **stub at Phase-37 fidelity**: a short summary of what and why, deliverable bullets
naming real files and mechanisms, and checkable acceptance criteria. A card is expanded to full
fidelity by a fresh sonnet-5 agent immediately before dispatch, grounded in the repo as it exists
*then* — see `planning/roadmap-open-source.md`'s "Execution model" section for the standing rules
(sonnet-5 implements, Opus coordinates, `make ci` gates, deploy checks against `vnprox-dev` for
anything peer-facing). Commit style for the eventual work: `area: imperative summary` (e.g.
`license: add SPDX headers and the go-licenses gate`).

**No card may weaken an existing gate.** Every CI addition below extends `scripts/ci-local.sh`'s
`ALL_JOBS` list (`check e2e cross-arm64 fuzz package packaging-matrix cluster-ssh`) with a new named
job, because Actions is down and `ci-local.sh` is the gate that actually runs today.

---

## Wave 1 — the two owner decisions

Both gate everything downstream in their lane, and T-3802 gets strictly harder the longer it waits
(more commits to scan, more time the current exposure has been live). Start both immediately.

### T-3801 · License selection & IP hygiene
**model:** owner → sonnet-5 · **size:** M · **depends:** — · **blocked on:** owner: license choice

**Owner gate, first:** Apache-2.0 was adopted 2026-08-06 (`LICENSE`, `NOTICE` already committed) —
before this roadmap existed and before the repo went public. The decision this card actually needs
from the owner is narrower than "pick a license": **ratify Apache-2.0 as final for the public
release**, given it is already the license the public repo is operating under today. If the owner
wants a different license, this card's scope grows to include re-licensing, which is a materially
bigger job (contributor re-consent, if any external commits exist yet) and should be re-scoped
before an agent starts.

**Deliverables**
- SPDX headers (`SPDX-License-Identifier: Apache-2.0`) across both trees — none exist today
  (verified: zero hits for `SPDX-License-Identifier` in `*.go`, `*.ts`, `*.tsx`).
- A `go-licenses` check (Go tree) and an npm license-compatibility check (`web/`), wired as a new
  `job_license` in `scripts/ci-local.sh`'s `ALL_JOBS`. Neither tool is in the repo today — flag both
  as new dev-time-only dependencies per `CLAUDE.md`'s "no new major dependencies without a note."
- A written policy for future third-party deps, appended to `docs/development.md`'s "Tech stack"
  section (which already states "Adding any other dependency requires a justification note in the
  task report" — this card adds the license-compatibility half of that check).
- Regenerate `THIRD-PARTY-LICENSES.md` (currently mode 600 — check whether that's deliberate or an
  accident before touching it) via whatever the new npm/go-licenses tooling produces, and diff it
  against the hand-maintained version already in the repo.

**Acceptance criteria**
1. Every first-party `.go`, `.ts`, `.tsx` file carries the SPDX header; a script counts and reports
   zero misses.
2. `job_license` runs in `scripts/ci-local.sh`, fails on an incompatible-license dependency added to
   a scratch branch, and passes on `main` today.
3. `THIRD-PARTY-LICENSES.md` regenerates byte-for-byte from tooling output (or the diff from the
   current hand-maintained file is reviewed and explained, not silently overwritten).

### T-3802 · History scrub & publication cut
**model:** owner → sonnet-5 · **size:** L · **depends:** — · **blocked on:** owner: cut method

**Owner gate, first, and urgent for a reason the roadmap didn't know when it was written:** the
repo is already public (`gh repo view`: `visibility: public`, pushed 2026-08-25, HEAD matches this
tree). This is not a pre-publication scrub; it is close-out on an exposure that has been live since
at least 2026-07-09. The owner must decide, now: **fresh-cut** (squash to a single public commit,
force-push over the current public history — breaks any existing clone URLs, stars, or forks of the
current public repo) vs. **history rewrite** (`git-filter-repo` pass over all 850 commits, preserves
authorship/history shape, force-pushed) vs. **accept and document** (if a scan finds only low-
sensitivity items — homelab RFC1918 IPs, no live credentials — the owner may decide the exposure is
acceptable and this card becomes "prove it's clean going forward," not "rewrite the past"). Any of
the three is a legitimate outcome; picking none is not.

**Deliverables**
- Scanning tooling: `gitleaks` and/or `trufflehog` run against the **full commit history**, not
  just `HEAD` — this is CLAUDE.md's standing rule for T-3802 restated: scrubbing is proven by
  scanning, never asserted. Both are new dev-time-only dependencies; flag per CLAUDE.md's
  dependency-note rule (neither ships in the built binary or CI's `check`/`e2e` path).
- A full-history scan for: the lab root password (per `CLAUDE.md`, it "lives only in scratchpad and
  must never have entered the repo" — this card's job is to prove that, not assume it), internal
  IPs (`192.168.1.x` — already known to be present in tracked files like `CLAUDE.md` at `HEAD`, so
  the interesting question is whether anything *besides* IPs is in the history), the owner's email
  (`bgovanlu@gmail.com`), and any API tokens, PVE credentials, or private keys.
- Whichever scrub mechanism the owner picks (`git-filter-repo` script, or a fresh orphan-commit
  cut), built as a repeatable script under `scripts/`, not a one-off interactive session.
- A re-scan of the resulting history proving zero hits for the categories above, checked into
  `planning/reports/evidence/` as a transcript — this is the artifact that turns "we scrubbed it"
  into a checkable claim.
- A new `job_secrets` (or similarly named) job in `scripts/ci-local.sh`'s `ALL_JOBS`, running the
  same scanner against future commits going forward, so this doesn't need re-litigating.

**Acceptance criteria**
1. A gitleaks/trufflehog transcript against the **full** history (all 850+ commits, or the rewritten
   equivalent) shows zero findings for credentials and the lab root password specifically —
   demonstrated, not asserted.
2. The owner's chosen method (fresh-cut / rewrite / accept-and-document) is recorded in writing with
   its reasoning, in `planning/` alongside this card's closeout.
3. If rewrite or fresh-cut: the force-pushed result is verified against the public
   `github.com/bgovanlu/vnprox` (not just local `.git`), and the acceptance transcript is taken
   *after* the push, not before.
4. `job_secrets` runs in `scripts/ci-local.sh` and is demonstrated to fail on a synthetic secret
   added to a scratch commit.

---

## Wave 2 — parallel infrastructure (no hard blockers)

Everything below can start once Wave 1 is underway; none of it needs T-3801/T-3802 to land first,
though T-3803 and T-3805 both correct stale text that assumes the pre-Wave-1 world.

### T-3803 · Contribution infrastructure
**model:** sonnet-5 · **size:** M · **depends:** —

`CONTRIBUTING.md` and `.github/ISSUE_TEMPLATE/` (`bug_report.md`, `feature_request.md`,
`config.yml`) already exist and are substantive — this card extends them, it does not create them
from nothing. Missing: a code of conduct, a PR template, a DCO sign-off gate, and a curated
good-first-issue pass. `CONTRIBUTING.md` also currently asserts the repo is private and 404s
anonymously — false as of this phase's ground-truth check above — and separately claims
`docs/security.md` "does not yet name a dedicated disclosure contact," which `SECURITY.md` already
contradicts (`security@vnprox.com`, added T-3302). Both need correcting, not just supplementing.

**Deliverables**
- `CODE_OF_CONDUCT.md` (Contributor Covenant or equivalent), linked from `CONTRIBUTING.md` and the
  repo README.
- `.github/PULL_REQUEST_TEMPLATE.md`.
- A DCO (`Signed-off-by:`) check wired as a new job in `scripts/ci-local.sh`'s `ALL_JOBS`, checking
  every commit in a PR's diff carries the trailer.
- A good-first-issue curation pass over `planning/tasks/`: identify existing stub- or full-fidelity
  cards genuinely approachable by an outside contributor (self-contained, no `pvecube` access
  needed, small size) and label/list them.
- Correct `CONTRIBUTING.md`'s stale "the repo is private" paragraph and its stale claim about
  `docs/security.md` lacking a disclosure contact, now that both are outdated.

**Acceptance criteria**
1. The DCO job fails against a synthetic unsigned commit and passes against a signed one,
   demonstrated both ways.
2. `CODE_OF_CONDUCT.md` exists and is linked from both `CONTRIBUTING.md` and the README.
3. At least a handful of `planning/tasks/` cards are identified and listed as good-first-issue
   candidates, with the selection criteria stated.
4. `CONTRIBUTING.md`'s private-repo and missing-disclosure-contact claims are corrected to match
   the repo's actual current state.

### T-3804 · Governance & ADR publication
**model:** sonnet-5 · **size:** M · **depends:** —

D1–D11 already exist as a decisions table in `docs/architecture.md` (§ around line 448, "Go single
binary" through "peer wire protocol stays at version 2") with prose justification scattered through
the surrounding sections, and in `docs/roadmap-proven.md`. No `docs/adr/` directory or numbered ADR
files exist yet — this card is the publication step, not the decision-making step.

**Deliverables**
- `docs/adr/0001-*.md` through `0011-*.md` (or equivalent numbering), one per D-decision, each with
  context, the decision, and consequences — extracted and expanded from `docs/architecture.md`'s
  existing table and prose, not re-litigated.
- Cross-links from `docs/architecture.md`'s D1–D11 table to the new ADR files.
- A maintainer-model doc consistent with `SECURITY.md`'s existing "single-maintainer project"
  framing, a release-cadence statement, and an LTS statement.
- Publish the above on `docs.vnprox.com` (the existing GitHub Pages site, served from `main`'s
  `/docs`) alongside the existing docs.

**Acceptance criteria**
1. 11 ADR files exist, each linked from `docs/architecture.md`'s decisions table.
2. The maintainer-model/release-cadence/LTS content is internally consistent with what
   `SECURITY.md` and `CONTRIBUTING.md` already say about the project's staffing.
3. `docs.vnprox.com` serves the new pages (verified by fetch after a Pages deploy).

### T-3805 · Security policy & coordinated disclosure
**model:** sonnet-5 · **size:** S · **depends:** —

`SECURITY.md` already exists (T-3302, 2026-08-18) with a disclosure email, a supported-versions
statement (latest release only), and a scope section. What's missing: confirming/enabling GitHub's
private vulnerability reporting feature (the doc says "if enabled... that path works too" —
conditional, unverified), a written embargo/advisory workflow, and fixing `CONTRIBUTING.md`'s stale
claim that no disclosure contact exists.

**Deliverables**
- Check and, if not already on, enable GitHub private vulnerability reporting for
  `bgovanlu/vnprox` (`gh api repos/bgovanlu/vnprox/private-vulnerability-reporting` or the repo
  Security tab) now that the repo is confirmed public.
- A written embargo/advisory workflow (draft advisory → private fix branch → coordinated release →
  public advisory), as a short doc — fold into `docs/security.md` or a new
  `docs/security-disclosure-process.md`.
- Fix `CONTRIBUTING.md`'s "does not yet name a dedicated disclosure contact" line — it already does.

**Acceptance criteria**
1. `gh api` confirms private vulnerability reporting is enabled on the repo.
2. The embargo workflow doc names concrete steps and ties into the existing `SECURITY.md` contact.
3. `CONTRIBUTING.md` no longer contradicts `SECURITY.md` about whether a disclosure contact exists.

### T-3809 · Published OpenAPI spec
**model:** sonnet-5 · **size:** L · **depends:** —

The generator already exists (T-2405): `GET /api/v1/openapi.json` is built from the router's actual
registered routes via `internal/apidoc`'s `Operations` table, `TestOpenAPI_EveryRouteIsDescribed`
gates drift in both directions (a route with no entry fails the build; a stale entry for a removed
route also fails), and the document is committed at `docs/openapi.json` (14 979 lines, regenerated
with `make openapi`). `docs/api.md` already states plainly what it does and doesn't cover (routes,
params, auth — not request/response body schemas). This card's real scope is narrower than the
roadmap line implies: publish the existing contract, and add the one piece that doesn't exist yet —
a generated TypeScript client used as a second, independent drift tripwire.

**Deliverables**
- Publish `docs/openapi.json` (or a rendered viewer — Redoc/Swagger UI as a static bundle, since
  `docs.vnprox.com` is static GitHub Pages) at `docs.vnprox.com`.
- Generate a TypeScript client (e.g. `openapi-typescript`) from `docs/openapi.json` into `web/src`
  or a dedicated package, and add a type-check-only step that fails when the generated client no
  longer matches how `web/src/api` actually calls routes — a second, independent check on top of
  `TestOpenAPI_EveryRouteIsDescribed`, not a replacement for it.
- Wire both as a new job in `scripts/ci-local.sh`'s `ALL_JOBS`.

**Acceptance criteria**
1. `docs.vnprox.com` serves the OpenAPI document or a viewer over it, fetched and verified after a
   Pages deploy.
2. The generated TS client type-checks against `web/src`'s current usage; changing a route without
   regenerating the client makes the new CI job fail — demonstrated on a scratch branch.
3. `docs/api.md`'s existing "what it does and does not cover" note is left accurate — this card does
   not claim response-body coverage that still doesn't exist.

### T-3810 · Contributor quickstart
**model:** sonnet-5 · **size:** M · **depends:** —

`CONTRIBUTING.md` already documents `make dev`/`make check` running against `internal/pvemock` with
no real PVE cluster needed. This card turns that into an actual one-command bootstrap and a guided
path for a stranger, rather than a paragraph assuming the reader already has Go/Node/nvm set up.

**Deliverables**
- A `scripts/quickstart.sh` (or `make quickstart`) that, from a clean clone with nothing but Go and
  a shell, gets to a running `make dev` against `pvemock` — installing/checking nvm and Node 22,
  `go mod download`, `npm ci` in `web/`, and reporting what it did.
- An architecture walkthrough written for a stranger — distinct from `docs/architecture.md`, which
  is dense and assumes context — covering the shape of `internal/change`, `internal/pvemock`, and
  the stage→validate→diff→apply→confirm flow at a level a first-time contributor can follow.
- A "first change" tutorial: a small, concrete, self-contained change (e.g., a new read-only field
  surfaced somewhere) that ends in a green `make check`, with every step spelled out.
- Link both new docs from `CONTRIBUTING.md`'s "Building and testing" section.

**Acceptance criteria**
1. A fresh clone plus one command reaches a running dev server against `pvemock` with zero manual
   steps beyond the one command — demonstrated on a clean checkout.
2. Following the "first change" tutorial's exact steps ends in `make check` passing — demonstrated,
   not asserted.
3. `CONTRIBUTING.md` links both new docs.

### T-3811 · Plugin developer portal
**model:** sonnet-5 · **size:** M · **depends:** —

`internal/plugin/doc.go` documents the frozen v1 SDK (five extension points — `SwitchDriver`,
`IngressDiscoverer`, `FlowIngestor`, `FindingProducer`, `DashboardTileProvider` — and the stage-only
`Stager` boundary) as a package doc comment, and `docs/hub-registry.md` covers registry/signing
mechanics, but there is no reader-facing "how to write a plugin" doc, no example-plugin template,
and no `vnproxctl plugin scaffold` — confirmed: `cmd/vnproxctl/main.go`'s command switch has no
`"plugin"` case at all today.

**Deliverables**
- `docs/plugin-development.md`, published to `docs.vnprox.com`, walking through the five extension
  points and the stage-only boundary — cross-linking `internal/plugin/doc.go`'s doc comment as the
  source of truth rather than duplicating it (the SDK is frozen at v1 per D10; the doc must not
  drift from the interfaces it describes).
- A minimal example-plugin template (in-repo under `examples/plugin-template/`, or a documented
  separate template repo) exercising `internal/plugin/plugintest`.
- A new `cmd/vnproxctl/plugincmd.go` implementing `vnproxctl plugin scaffold <name>`, wired into
  `main.go`'s command switch alongside the existing `hub`/`gitsync`/`policy` subcommands, stamping
  out the template into a new directory.
- `vnproxctl --help`'s usage text (see `main.go`'s existing per-command help lines) gains the new
  subcommand.

**Acceptance criteria**
1. `vnproxctl plugin scaffold <name>` produces a directory that builds and passes
   `internal/plugin/plugintest`'s harness, unmodified.
2. `docs/plugin-development.md` is live on `docs.vnprox.com` and does not restate SDK interface
   details that would drift from `internal/plugin/doc.go` — it links to them.
3. The example plugin installs against a dev `vnproxd` (against `pvemock`) with no code changes.

### T-3812 · Telemetry transparency page
**model:** sonnet-5 · **size:** S · **depends:** —

`internal/telemetrycollector` holds the aggregate stats server-side; `cmd/vnproxctl/telemetrycmd.go`
already implements `vnproxctl telemetry preview --report <file>` (prints the exact bytes that would
be sent, sends nothing, works whether or not telemetry is enabled) and `vnproxctl telemetry status`.
`docs/security.md` documents what's collected. Nothing public-facing renders any of this today.

**Deliverables**
- A `docs.vnprox.com` page rendering the aggregate stats `internal/telemetrycollector` holds (or, if
  no public collector endpoint should exist yet, a clearly-labeled worked example) alongside a real
  `vnproxctl telemetry preview` transcript against a sample report, so a prospective adopter can see
  the literal bytes before opting in.
- A short UX review of the opt-in flow (where and how it's presented at install/first-run) against
  OSS community norms, written up with any recommended changes flagged as follow-up — this card
  does not itself change the opt-in default.

**Acceptance criteria**
1. The page includes a real `vnproxctl telemetry preview` transcript, not a description of one.
2. The page states plainly that telemetry defaults to off, matching `telemetrycmd.go`'s "OFF by
   default" framing — no drift between what the doc claims and what the code does.
3. The opt-in review's outcome is either "no change needed" (stated why) or a filed follow-up card —
   no card in this phase silently loosens opt-in toward opt-out.

---

## Wave 3 — build & supply-chain integrity

Grouped together because they compose: reproducible builds feed SBOM/provenance, and both are what
fork-safe CI eventually verifies on every release. T-3807's SLSA half and all of T-3808 are
Actions-blocked; their SBOM/audit halves are not and should proceed now.

### T-3806 · Reproducible builds
**model:** sonnet-5 · **size:** M · **depends:** —

`internal/hubreg/vetting.go`'s `reproducibleBuildResidualNote` (always appended to every
`VetResult`) says two distinct things, and this card can only close one of them. It says vnprox
"has no source-to-artifact build pipeline" — that's the part this card fixes. It *also* says that
even with one, plugin vetting specifically has nothing to rebuild and compare, because "the registry
never receives the executable the manifest's endpoint names at all (only a `{manifest, signature}`
artifact)" — that's a structural fact about what the registry stores, not something reproducible
vnprox builds change. **Do not scope this card to also making plugin executables reproducible-
checkable; that would mean changing what the registry accepts, which is a different, larger
decision.** The card's job is vnprox's own build.

**Deliverables**
- Pinned toolchains (Go and Node versions are already pinned in `scripts/ci-local.sh` —
  `GO_VERSION_EXPECTED`, `NODE_MAJOR` — extend the same pinning into the actual release build path
  in `Makefile`/`packaging/`).
- `SOURCE_DATE_EPOCH` derived from the release commit's timestamp, `-trimpath` and matching
  `-ldflags` to strip local build paths, applied to the `.deb`/binary build.
- `scripts/verify-reproducible.sh`: builds the release artifact twice (clean checkout, or two
  different machines/times) and diffs `sha256sum` — proving byte-identical output, not asserting it.
- Update `reproducibleBuildResidualNote`'s text (and the `VetResult` note it produces) to state that
  vnprox's own build pipeline is now reproducible, while preserving the separate, still-true reason
  plugin vetting has nothing to rebuild — this is an edit, not a deletion of the note.
- Wire `verify-reproducible.sh` as a new job in `scripts/ci-local.sh`'s `ALL_JOBS`.

**Acceptance criteria**
1. Two independent builds of the `.deb` (or binary tarball) from the same commit produce identical
   `sha256sum` output — demonstrated, both hashes shown.
2. `job_reproducible` (or similarly named) runs in `scripts/ci-local.sh`.
3. `internal/hubreg/vetting.go`'s note and its test (`TestAutomatedVetChecks` or equivalent) are
   updated to reflect that vnprox's own pipeline is reproducible, and still correctly state the
   separate plugin-executable-never-received reason.

### T-3807 · SBOM + provenance on releases
**model:** sonnet-5 · **size:** M · **depends:** — · **blocked on:** Actions (partial — SLSA half
only)

Two of the three things the roadmap line names already exist: **installer verification already
works** (`docs/deployment.md`, T-2801 — every downloaded artifact is signature-checked, "not
skippable," no `--insecure` escape) and **Sigstore/cosign is already an approved dependency**
(T-3709, `internal/hubreg`'s index signing uses `sigstore-go` and `cosign sign-blob --bundle`
already). This card does not need to introduce either. What's actually missing is SBOM generation.

**Deliverables**
- CycloneDX SBOM generation for the Go tree (e.g. `cyclonedx-gomod`) and the npm tree (e.g.
  `cyclonedx-npm`), attached to release artifacts alongside the existing `.deb`/tarball. Both are
  new dev/release-time-only dependencies — flag per CLAUDE.md's dependency-note rule.
- A new job in `scripts/ci-local.sh`'s `ALL_JOBS` that generates and validates both SBOMs' shape
  (schema-valid CycloneDX) on every `package`/`packaging-matrix` run.
- SLSA provenance attestation: **stage the workflow addition** (in `.github/workflows/release.yml`,
  using the OIDC-based keyless signing pattern `publish-registry.yml` already establishes for the
  hub index) but mark it explicitly not runnable until Actions billing is restored — do not attempt
  to fake provenance from a local build; GitHub-hosted OIDC identity is the point of SLSA
  attestation and a local run cannot produce it credibly.

**Acceptance criteria**
1. A release build produces a schema-valid CycloneDX SBOM for both the Go and npm dependency trees.
2. The new SBOM job runs in `scripts/ci-local.sh` today, independent of Actions.
3. The SLSA half is present as reviewed-but-not-enabled workflow YAML with an explicit comment
   stating it's blocked on Actions billing (matching the pattern `ci.yml` already uses for its own
   disabled-state comment) — not silently dropped, not silently faked.
4. Existing installer signature verification (T-2801) is unmodified by this card.

### T-3808 · Fork-safe public CI
**model:** sonnet-5 → owner · **size:** M · **depends:** — · **blocked on:** owner: Actions billing

`.github/workflows/ci.yml` and `packaging-matrix.yml` **already exist**, fully specify all seven
`scripts/ci-local.sh` jobs (`check e2e cross-arm64 fuzz package packaging-matrix cluster-ssh`), and
already carry a comment explaining they're `disabled_manually` since 2026-08-13 with the exact
re-enable command (`gh workflow enable "<name>"`). This card is not "write the workflows"; it is
"make them safe to run on fork PRs from a now-public repo, and wire required-check branch
protection" — the agent-completable half — with the owner's Actions-restoration and the actual
re-enable as the one step nobody but the owner can do.

`main`'s branch protection today (verified via `gh api repos/.../branches/main/protection`) already
has `enforce_admins: true` and blocks force-pushes/deletions — it is not "no protection." What's
genuinely missing is `required_status_checks` and `required_pull_request_reviews`; that's the
specific gap this card closes.

**Deliverables**
- A secrets audit: enumerate every `${{ secrets.* }}` reference across `ci.yml`,
  `packaging-matrix.yml`, `release.yml`, and `publish-registry.yml`, and state which workflows/jobs
  those run in. Fork PRs must trigger only `pull_request` (read-only `GITHUB_TOKEN`, no repo
  secrets) — any privileged step (signing, publishing) must be fenced to `push`/`workflow_run` on
  `main`, never on an untrusted fork's `pull_request`.
- Fix any job found running with secrets on `pull_request` from the audit above.
- Add `required_status_checks` (at minimum `check`, ideally `e2e`) to `main`'s branch protection —
  agent-preparable as a documented `gh api` call, executed once Actions actually runs the checks
  again (a required check that never reports blocks every merge).
- Document the re-enable runbook (`gh workflow enable ...` plus the branch-protection API call) in
  `docs/development.md`'s CI section.

**Acceptance criteria**
1. The secrets audit is written down, names every secret reference and its trigger, and confirms
   (or fixes) that no privileged secret is reachable from a fork's `pull_request` event.
2. The branch-protection API call to add required status checks is prepared and tested against a
   scratch/staging setting — not applied to `main` until the owner confirms Actions billing is
   restored and the checks are actually reporting (a required check with no reports is a permanent
   red lock, worse than no protection).
3. `docs/development.md` documents the exact re-enable runbook.

---

## Sequencing

```
Wave 1   T-3801 (owner: license) ──┐
         T-3802 (owner: cut)     ──┤  start immediately; T-3802 more urgent than
                                    │  the roadmap assumed — repo is already public
                                    │
Wave 2   T-3803 ─┐                 │
         T-3804  │                 │
         T-3805 ─┤  parallel, no hard blockers on Wave 1 or each other
         T-3809  │  (T-3803/T-3805 correct text that assumed pre-Wave-1 state)
         T-3810  │
         T-3811  │
         T-3812 ─┘
                                    │
Wave 3   T-3806 ─┐  reproducible builds feeds T-3807's SBOM story
         T-3807 ─┤  SBOM half ships now; SLSA half staged, Actions-blocked
         T-3808 ─┘  secrets audit + branch-protection prep ships now;
                    actual re-enable + required-check activation is owner-gated
```

**Two owner calls dominate this phase's critical path**, restated from the roadmap: license
ratification (T-3801) and history-cut method (T-3802) — both narrower decisions than the roadmap
assumed, since Apache-2.0 was already adopted and the repo is already public. A third, T-3808's
Actions-billing restoration, blocks only the final activation step of that one card, not the whole
phase — its secrets audit and branch-protection prep are agent-completable today.

## Explicitly not in this phase

- **Re-licensing away from Apache-2.0.** T-3801 ratifies the license already in the public repo; a
  different choice is a separate, larger decision (contributor re-consent, re-scoping) not covered
  here.
- **A hosted plugin/blueprint registry, a public demo instance, or a telemetry ingestion endpoint.**
  Those are `T-3707`'s "decide the hosted-service group" territory (Phase 37, already carded) and
  Phase 40/41 items gated on DNS the owner has deferred — T-3812 documents the existing collector
  and preview output, it does not stand up a new hosted endpoint.
- **Deepening the map, integrations, or intelligence features** — Phases 39, 40, and 41 respectively.
  Nothing here adds product surface; every card in this phase makes the project adoptable, not more
  capable.
- **Re-running or fixing the Wave-0 debt gate** (`scale.spec.ts` v2-canvas hang, T-3713, tenant
  scoping, T-3712, T-3101-followup-01, T-3406-followup-02, `AlertRules.tsx`, the implementation-plan
  index) — that is prerequisite work tracked separately in `planning/roadmap-open-source.md`'s
  Wave 0, not part of the 12 cards here.
- **Actually restoring GitHub Actions billing, or force-pushing a rewritten/cut history.** Both are
  owner actions named as gates (T-3808, T-3802) that an agent stages and prepares but cannot itself
  execute.
