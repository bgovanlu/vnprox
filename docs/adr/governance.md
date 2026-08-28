# Governance

This is a solo-maintained open-source project. This document says that plainly, rather than
implying a foundation, a steering committee, or a company that don't exist — the same posture
`SECURITY.md` and `docs/support.md` already take ("vnprox does not have a support team, an SLA,
or a paid tier. It is maintained by whoever is maintaining it at any given time, best-effort").

## Maintainer model

- **One maintainer, best-effort, no committee.** There is no core team, no working groups, and no
  formal decision-making body beyond the maintainer. `docs/support.md` states this for support
  requests; it is equally true for design and release decisions. If that changes — a second
  regular committer earns write access, for instance — this document is the place that update
  belongs, not an inference from commit history.
- **Architecture decisions (this ADR set) are made by the maintainer**, historically recorded as
  settled at specific points (e.g. `docs/roadmap-proven.md`'s own decisions were "settled before
  decomposition, 2026-07-30" — a single dated commitment, not a vote). Reopening a locked decision
  (`docs/architecture.md` §10 says exactly this: "Do not re-litigate decisions... if a decision
  blocks you, flag it in your final report instead of changing it unilaterally") requires the
  maintainer's sign-off, not a PR merge.
- **Contribution review** follows `CONTRIBUTING.md`: PRs are reviewed by the maintainer (or anyone
  the maintainer has since added). There is no requirement for multiple approvals because there is,
  today, only one person positioned to give one.
- **This is a cost, stated rather than hidden.** A single maintainer is a bus-factor-one project:
  response times are best-effort (see `SECURITY.md`'s "acknowledged... within a few business days"
  language, deliberately not an SLA), review bandwidth gates how fast contributions land, and there
  is no succession plan on file. Anyone relying on vnprox in production should read `docs/status-matrix.md`
  and `planning/reports/needs-hardware-validation.md` with that staffing reality in mind, not just
  the feature list.

## Release cadence

- **No fixed calendar cadence.** vnprox does not promise "a release every N weeks." Releases are
  cut at the end of a roadmap arc or phase, once its exit criteria are demonstrated — the pattern
  every arc to date has followed (`CHANGELOG.md`'s phase-to-version map: v1.0 end of Arc 1's
  milestones, v2.0 end of Arc 2, v3.0 the platform cut of Arc 3, v3.5.0 the cut of Arc 5, v4.0.0
  the end-of-arc cut of Arc 4). `CHANGELOG.md` is explicit that this map "has always been a plan
  rather than a ledger" — `v3.1`, `v3.2`, and `v3.3` were reserved by the roadmap and never
  tagged, because the work that would have justified them shipped inside other version lines
  instead. Read the phase-to-version mapping in a roadmap document as *intent*, and `CHANGELOG.md`
  as the actual ledger of what was tagged and when.
- **Versioning is semantic** (`CHANGELOG.md`'s own header: "vnprox uses semantic versioning; the
  SQLite schema migrates forward-only"). A breaking change to a frozen platform surface (ADR-0010)
  mints a new major surface version with the deprecation window that ADR describes; that is
  independent of vnprox's own release *number*, which follows ordinary semver against the whole
  product.
- **What gates a release** is exit criteria stated per-phase in the relevant roadmap document
  (each phase's own "Exit demo" section), plus, per `CLAUDE.md`, that `make check`/`make ci` is
  green — there is no separate release-approval process beyond the maintainer deciding the arc's
  exit criteria are actually met.

## Supported versions / LTS

- **Latest release only — restated here, matching `SECURITY.md` verbatim.** `SECURITY.md`'s
  "Supported versions" section already says this: "The latest release only.
  `docs/deployment.md`'s upgrade flow (`apt-get update && apt-get install vnprox`) is how you get
  a fix — there is no backport process for older versions." This document does not weaken or
  extend that: there is no LTS line, no maintained N-1 release, and no backport branch, because a
  solo maintainer cannot honestly commit to maintaining more than one line at a time.
- **The SQLite schema migrates forward-only** (`CHANGELOG.md`, `docs/data-model.md`), which is the
  concrete mechanism behind "no backport": there is no supported downgrade path once a node has
  upgraded, so staying current is the only supported way to receive a fix, not a policy preference.
- **PVE compatibility is a separate axis from vnprox's own release support.** ADR-0009 (target PVE
  8.2+ and 9.x, extended per phase) states which *Proxmox* versions the current vnprox release
  targets; it does not mean older vnprox releases against those PVE versions are still supported —
  only the latest vnprox release is, against whichever PVE versions `docs/compatibility.md`
  currently lists.
- **If this ever needs to change** — a second maintainer taking on an LTS line, for instance — the
  change belongs in this document and in `SECURITY.md` together, not in one without the other;
  `docs/adr/README.md`'s "D-numbers referenced in code" table is a working example of what happens
  when two documents describe the same thing independently and drift.

## See also

- `SECURITY.md` — disclosure contact, embargo posture, scope.
- `docs/support.md` — what response to expect, where to file, known maturity gaps.
- `CONTRIBUTING.md` — how a PR gets reviewed and merged.
- `docs/architecture.md` §10 — the locked decisions this ADR set publishes; "no re-litigating"
  applies to governance the same way it applies to architecture.
