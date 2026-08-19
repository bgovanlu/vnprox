# Phase 33 — In the world

**Roadmap:** [`docs/roadmap-earned.md`](../../docs/roadmap-earned.md) ·
**Plan:** [`../implementation-plan-earned.md`](../implementation-plan-earned.md)

Context for every card in this phase: `docs/architecture.md`, `docs/development.md`,
`docs/deployment.md`, `packaging/apt-repo.md`, `docs/hub-registry.md`, `docs/docs-site.md`.

> **This card file was authored retroactively on 2026-08-19, by the 2026-08-19 debt sweep
> (`planning/tasks/debt-sweep-2026-08-19.md`, item 5), not at the start of the phase.**
> `planning/implementation-plan-earned.md` states phase cards for 30–33 are "authored when their
> phase begins" — that did not happen here either, same as Phase 32. `T-3301` and `T-3303` are
> both recorded as "done, 2026-08-18" directly in `docs/roadmap-earned.md`'s own Phase 33 prose,
> with no card file ever written to hold acceptance criteria against. This file reconstructs what
> that card file would have said, checked against git commits, `docs/roadmap-earned.md`'s own
> Phase 33 section, `planning/reports/T-3303-demo-mode-real-host-isolation.md`, and direct
> inspection of the current tree (repository visibility via `gh api`, `docs/forum-announcement.md`,
> `SECURITY.md`, `docs/compatibility.md`) — not copied from `docs/roadmap-earned.md`'s summary
> without checking it. Where this file's findings match that summary, it is because they were
> independently verified to be true, not because the summary was trusted.

The organising rule, from the arc roadmap: **make vnprox gettable and integrable by someone who
has never met its developer.** This phase is the last of Arc 6 — it retires hosted CI in favour of
a local gate, stands up the first real hosting infrastructure the project has ever had (an apt
repo, a docs site, a demo, a plugin/blueprint registry), and makes the repository itself public.

---

## T-3301 · Distribution that works: CI decision, signed apt repo at a real host, release publishing

**Priority:** P0 · **Owns:** `.githooks/pre-push` (new), `packaging/publish-release.sh` (new),
`packaging/build-apt-repo.sh`, `packaging/apt-repo.md`, `docs/development.md` §CI, `install.sh`

**Objective, from the roadmap:** a CI decision (hosted Actions had been unfunded since
2026-08-11), a signed apt repository at a real host closing `T-2102`'s hosting gap, and a manual
release-publishing flow to replace `release.yml`'s job.

### What was actually delivered — status: **done, for everything within the project's own control; DNS is the one item still outside it**

Verified against commit `30465dda` ("build: retire hosted CI for scripts/ci-local.sh; host the
apt repo (T-3301)") and commit `c700e2d9` ("docs: repo is public, and install.sh pins the real
production key (T-3301/T-3302)").

- **CI decision, made and on the record.** Hosted GitHub Actions is retired, not paused —
  `scripts/ci-local.sh` (`make ci` for the fast subset) is the permanent gate. Enforced by a new
  `.githooks/pre-push` hook (`make install-hooks`) rather than GitHub's
  `required_status_checks`, which would be unsatisfiable now that no workflow posts them. `main`
  gained the branch protection this repo can actually use: force-push and deletion disabled,
  `enforce_admins` on. This deliberately supersedes `T-2410`'s "three consecutive green Actions
  runs" acceptance criterion rather than leaving it permanently unmeetable and unexplained.
- **The apt repo has a real host.** `apt.vnprox.com` (on `pve001`) serves a repository signed with
  a real production Ed25519 key that lives only on that host — not a GitHub Actions secret, since
  Actions no longer runs releases. Verified end-to-end against `pve001`: `build-apt-repo.sh` signs
  with the real key, nginx serves the resulting tree, and the served `Release`/keyring files are
  byte-correct (stated directly in the commit message, not merely asserted by a test).
- **`packaging/publish-release.sh`** is the manual release-cut flow replacing `release.yml`'s job:
  builds both architectures, signs and publishes the apt repo, stamps `openapi.json`/
  `automation-contract.json`, regenerates the compat matrix, and cuts a GitHub release.
- **`install.sh` pins the real production key fingerprint** (commit `c700e2d9`), replacing the
  placeholder that made every `curl | sh` install fail closed by design. The test suite was
  updated to match: `TestInstaller_RefusesWhenNoTrustAnchorIsAvailable` now forces the no-anchor
  scenario onto a pinned copy instead of relying on the shipped default (since the shipped default
  is a real key now), and `TestInstaller_PinnedFingerprintMatchesPublished` replaces the old
  placeholder-pinning test.

**Not yet done, stated plainly in the commit itself and confirmed still true 2026-08-19:**
`apt.vnprox.com` does not resolve publicly yet — pending the VPS reverse-proxy leg shared with
`T-3303`, and outside this project's own working tree to fix (`debt-sweep-2026-08-19.md` item 7:
"deferred by owner — needs VPS credentials"). The repository currently holds a dev build for
pipeline validation, not a real tagged release cut through the new flow — the repo's own version
tag is still `v4.0.0`, from before this phase.

### Acceptance — reconstructed and checked against evidence

1. A CI decision is made and recorded, with the superseded acceptance criterion (`T-2410`)
   explicitly named as superseded rather than left open and unexplained — **done**.
2. `main` carries branch protection appropriate to the new gate — **done**.
3. The apt repo machinery (`T-2102`) is verified against a real signing key on a real host,
   end-to-end — **done**.
4. A manual release-publishing flow exists, replacing the retired Actions job — **done**.
5. `install.sh` trusts the real production key by default — **done**.
6. The apt repo resolves publicly and a real tagged release has been cut through the new flow —
   **not done**; both are owner-gated (DNS/VPS access), tracked in `debt-sweep-2026-08-19.md`
   item 7.

---

## T-3302 · Public presence: repo, docs site, security contact, forum announcement

**Priority:** P0 · **Owns:** `SECURITY.md` (new), `CNAME` (new), `docs/index.html`,
`docs/forum-announcement.md`

**Objective, from the roadmap:** the `T-2105` remainder, all human-gated, none of it code — make
the repo public, enable the already-built docsify site, publish a security-disclosure contact, and
post the forum announcement that has sat in draft.

### What was actually delivered — status: **partial: three of four done; the announcement is still a draft**

Verified against commit `0f970685` ("docs: add a security-disclosure contact and wire up GitHub
Pages (T-3302)"), commit `c700e2d9`, `gh api repos/:owner/:repo` (visibility check), and
`gh api repos/:owner/:repo/pages` (Pages status check), both run 2026-08-19.

- **Repo is public.** `gh api repos/:owner/:repo --jq .visibility` returns `"PUBLIC"`, confirmed
  2026-08-19. `docs/support.md` was updated to point at real GitHub Issues, and private
  vulnerability reporting is enabled on the repository alongside `SECURITY.md`.
- **The docs site is live, not merely built.** `gh api repos/:owner/:repo/pages` (2026-08-19)
  returns `"status":"built"`, `"cname":"docs.vnprox.com"`, `"html_url":"http://docs.vnprox.com/"`
  — GitHub Pages is genuinely enabled and has built successfully. **Note for whoever reads
  `docs/docs-site.md` next**: that document's own "Status" section still reads "Does not exist
  yet: GitHub Pages is not enabled for this repository" — that line is now stale (written for
  `T-2105`, before this card enabled Pages) and was found by this reconstruction, not fixed by it
  (out of this sweep's file scope; flagged in the debt-sweep report instead).
- **A security-disclosure contact exists.** `SECURITY.md` publishes `security@vnprox.com` and
  documents GitHub's private-vulnerability-reporting path as a second channel. This closes a real
  audit finding — there was no way to report a vulnerability to this project at all before this
  card.

**Not done.** `docs/forum-announcement.md`'s own first line still reads, verbatim, **"DRAFT, NOT
YET POSTED"**, and the body states plainly: "Nobody in this project's current working session has
forum access; posting it... is a step for whoever has that access." Commit `c700e2d9` finalized
the draft's *content* against its own posting checklist (repo-public and install.sh items marked
done) but did not post it — the commit message itself says "DNS/VPS reachability for
`apt.vnprox.com`/`demo.vnprox.com`/`registry.vnprox.com` is still the one item to verify at actual
posting time." `docs/roadmap-earned.md`'s Phase 33 paragraph describes this card's scope as
including posting the announcement; that has not happened as of 2026-08-19.

### Acceptance — reconstructed and checked against evidence

1. The repository is public — **done**, verified via the GitHub API directly.
2. The docs site resolves and serves real content via GitHub Pages — **done**, verified via the
   GitHub API directly (`status: built`).
3. A security-disclosure contact is published — **done**.
4. The forum announcement is posted — **not done**; the draft is finalized and ready, but posting
   is gated on the DNS/VPS work `T-3301`/`T-3303` also wait on, and on someone with forum access.

---

## T-3303 · Hosted instances + ecosystem: demo, registry, Terraform/Ansible

**Priority:** P1 · **Owns:** `internal/publicdemo/`, `internal/auth/ratelimit.go`,
`internal/certs/service.go` (`resolveCertsRoot`), `internal/blueprint/` (`KindWgTunnel`),
`terraform-provider-vnprox` (external repo, seeded), `ansible-collection-vnprox` (external repo,
seeded)

**Objective, from the roadmap:** stand up the hosted demo and the hosted signed registry (closing
`T-2802`/`T-2803`'s hosting gaps), close the DMZ+WireGuard blueprint seed's missing `wg.*` entity
kind, and seed the Terraform provider / Ansible collection repositories.

### What was actually delivered — status: **done, for everything within the project's own control; DNS is the shared outstanding item, same as `T-3301`**

Verified against commit `ac3a7c3f` ("demo: real hosted public demo (T-3303), plus a bug it found
live"), commit `7b9d7133` ("blueprint: add the wg-tunnel entity kind; publish the registry for
real (T-3303)"), and `planning/reports/T-3303-demo-mode-real-host-isolation.md`.

- **The hosted demo is live** at `demo.vnprox.com` (`pve001`, `vnprox-demo-public.service`).
  Resolves `T-2801-followup-01`'s app-level half: `POST /simulate/path` and `POST /diagnose` now
  execute for real in plain `vnproxd --demo` instead of answering "would have" — audited
  handler-by-handler, not guessed from route names. `internal/publicdemo`'s hosted edge stays
  deliberately method-blind (a decision re-confirmed, not silently overridden, now that a real
  instance exists). The login rate limiter gained a real per-username override
  (`internal/auth.RateLimitByUsername`, `[server] login_rate_username_capacity`), closing the
  other named demo-mode gap: a public demo mints every visitor's session against the same shared
  fixture credential, so the per-username bucket previously throttled the whole instance's
  visitor onboarding globally rather than per abusive IP.
- **Standing up a real instance on a genuine PVE node found a real, previously-unknown bug**,
  exactly the kind of thing this project's hardware-first discipline exists to catch:
  `certs.Service` scanned `pve001`'s actual `/etc/pve` regardless of demo mode, so the first time
  demo mode ran anywhere `/etc/pve` genuinely exists, it leaked real node names into a supposedly
  synthetic public demo's findings. Fixed (`resolveCertsRoot`) and regression-tested. Full account
  in `planning/reports/T-3303-demo-mode-real-host-isolation.md`, including what's contained by
  deployment posture rather than fixed at the code level yet.
- **The hosted signed registry is live** at `registry.vnprox.com` (`pve001`, static nginx, same
  posture as the apt repo) with all four `T-2104` seed blueprints actually published through the
  real `vnproxctl hub publish`/`hub index` pipeline — not a copy of a test fixture. The registry's
  Ed25519 index-signing key lives only on that host.
- **The DMZ+WireGuard seed's missing `wg.*` blueprint entity kind is closed.**
  `blueprint.KindWgTunnel` closes the gap the seed's own doc comment used to name — the DMZ seed
  now provisions the local WireGuard tunnel interface, not just the bridge. Still deliberately
  partial: the remote peer needs a public key exchanged out of band, so `wg.peer.add` stays a
  separate step, stated in both doc comments rather than silently assumed.
- **`terraform-provider-vnprox` / `ansible-collection-vnprox` are seeded** — real repositories,
  contract-pointer READMEs, Apache-2.0 licensed. No provider/module code exists yet; that
  implementation is real, separate future work, deliberately not rushed alongside standing up the
  public infrastructure.

**Not yet done, stated in the commit and still true 2026-08-19:** `demo.vnprox.com` and
`registry.vnprox.com` don't resolve publicly yet — the same VPS reverse-proxy leg `T-3301`'s apt
repo is waiting on, deferred by the owner (`debt-sweep-2026-08-19.md` item 7).

### Acceptance — reconstructed and checked against evidence

1. A real hosted demo instance exists and runs on a real PVE node — **done**.
2. The demo's read-only edge posture is preserved on a real host, and a real-host-only bug found
   in the process is fixed and tested — **done**.
3. A real hosted signed registry exists, with real blueprints published through the real
   publishing pipeline (not a test fixture) — **done**.
4. The WireGuard blueprint seed's stated gap is closed, with the remaining scope boundary (peer
   exchange) stated rather than assumed — **done**.
5. Terraform provider / Ansible collection repositories exist, seeded but not implemented, stated
   as such — **done**.
6. All three new hosted services resolve publicly — **not done**; owner-gated, shared with
   `T-3301`.

---

## Phase 33 — delivery record (reconstructed 2026-08-19)

| Card | State | Note |
|---|---|---|
| `T-3301` | ● Done (DNS excepted) | CI decision made and enforced; apt repo hosted, signed, verified end-to-end against a real host. Not yet done, and outside this project's control: public DNS resolution and a real tagged release cut through the new flow |
| `T-3302` | ◐ Partial | Repo public and docs site live — both verified directly against the GitHub API 2026-08-19, not assumed. Security contact published. **The forum announcement is still an unposted draft** — `docs/forum-announcement.md`'s own header says so; this is the one clearly incomplete item in this card |
| `T-3303` | ● Done (DNS excepted) | Hosted demo and hosted registry both live on real hardware, with a real host-only bug found and fixed along the way; WireGuard blueprint gap closed; Terraform/Ansible repos seeded. Not yet done, same shared reason as `T-3301`: public DNS resolution |

**What this reconstruction found that needed independent verification rather than trust:** the
repo-public and docs-site-live claims in `docs/roadmap-earned.md`'s Phase 33 prose were verified
directly against the GitHub API (not merely re-stated) and both hold. The forum-announcement claim
implied by that same prose ("post the forum announcement that has sat in draft") does **not**
hold — the file itself still says DRAFT, NOT YET POSTED, and no commit in this project's history
posts it (posting a forum thread is outside any repository commit's reach by construction). Anyone
closing this phase out fully needs to either post the announcement or explicitly re-scope T-3302
to exclude it.

**Cross-reference:** the public-DNS gap common to `T-3301`/`T-3303` (`apt.vnprox.com`,
`demo.vnprox.com`, `registry.vnprox.com`) is tracked as `debt-sweep-2026-08-19.md` worklist item 7
and marked there as a 2026-08-19 owner decision ("Deferred — owner will supply VPS credentials
later"), not as work either card failed to do.
