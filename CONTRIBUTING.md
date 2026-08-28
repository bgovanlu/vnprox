# Contributing to vnprox

Thanks for considering it. Read this first — it's short — then `docs/development.md` for the full
technical detail (repo layout, coding standards, CI, the mock PVE server).

## Before you file anything or open a PR

**As of 2026-08-18 (T-3302), `github.com/bgovanlu/vnprox` is public.** This paragraph used to say
the opposite — that the repo was private and an anonymous request returned 404 — and that was
worth correcting explicitly rather than quietly, since it's exactly the kind of stale claim this
project has been burned by before (see `CLAUDE.md`'s note on documents outliving their accuracy).
File bugs and feature requests as [GitHub Issues](https://github.com/bgovanlu/vnprox/issues) (see
`.github/ISSUE_TEMPLATE/`); open pull requests the ordinary GitHub way. `docs/support.md` has the
full "where to file" breakdown, including the community-forum channel once it exists.

By contributing, you agree to this project's [Code of Conduct](CODE_OF_CONDUCT.md).

## How this project actually gets built, and what that means for you

Most of vnprox's implementation has been written by AI coding agents working from detailed task
cards under `planning/tasks/`, following the rules in `CLAUDE.md` (repo root) — that file is the
literal instruction set those agents work from, not internal-only material, so it's worth reading
if you want to understand *why* the codebase is shaped the way it is (the change-engine safety
invariant, the "Proxmox is the source of truth" rule, the mock-first testing approach). A human
contribution doesn't need to follow that process, but it does need to respect the same invariants:
network changes only flow through `internal/change/` (stage → validate → diff → apply →
confirm/rollback), vnprox's SQLite store holds only app-owned data, and every feature that touches
node state has to work when that node is a cluster peer, not just localhost. `CLAUDE.md`'s "Ground
rules" section states these precisely.

## Building and testing

```bash
make dev      # backend against the mock PVE server + Vite dev server, hot reload
make build    # vnproxd binary with the embedded SPA
make test     # go test ./... && vitest run
make lint     # golangci-lint + eslint + tsc --noEmit
make check    # lint + test + govulncheck + npm audit — run this before opening a PR
```

None of this needs a real Proxmox cluster: `internal/pvemock` is a fixture-driven mock PVE API
server, and `make dev`/`make test` run against it. See `docs/development.md` §"The mock PVE
server" for how it works, and its "Definition of done" section for what a change needs before it's
considered complete (table-driven tests, `docs/api.md`/`docs/data-model.md` contract compliance
where relevant, `make check` green).

**A note on CI:** GitHub Actions is currently disabled for this repository (billing exhausted,
`release.yml`'s own header comment). `scripts/ci-local.sh` reproduces the full job matrix locally
and is the actual gate today — see `docs/development.md` §CI.

## Code standards

- **Go:** standard library first; the approved third-party dependency list is in
  `docs/development.md`'s "Tech stack" table — adding anything else needs a justification note in
  the PR description. Errors wrapped with context (`fmt.Errorf("doing X: %w", err)`); structured
  logging via `log/slog`, never `fmt.Println`.
- **TypeScript:** strict mode, no `any`, no unchecked casts. React 18 + Vite, per
  `docs/development.md` §"TypeScript standards."
- **API/data model changes** must match `docs/api.md` and `docs/data-model.md` exactly — other
  parts of the system, and anything built against the published automation contract
  (`docs/automation-contract.json`), depend on those contracts holding.

## Commit and PR conventions

- Commit style: `area: imperative summary` — e.g. `change-engine: add commit-confirm timer`.
- Keep a PR to one logical change; large or architectural changes are easier to review as an issue
  first, then a PR.
- Run `make check` before opening a PR. If a check is red for a reason unrelated to your change
  (see `docs/development.md`'s note on the `check`/`fuzz` jobs' known flakiness), say so in the PR
  description rather than leaving a reviewer to guess.

### Developer Certificate of Origin (DCO)

Every commit in a PR needs a `Signed-off-by:` trailer certifying you wrote it or otherwise have the
right to submit it under this project's license (the standard [DCO 1.1](https://developercertificate.org/)
text — the same mechanism the Linux kernel, Docker, and a large share of the CNCF use instead of a
separate CLA). Add it with:

```
git commit -s -m "area: imperative summary"
```

If you forgot on a commit you've already made: `git commit --amend -s` for the last commit, or
`git rebase --exec 'git commit --amend --no-edit -s' -i <base>` for a range. `scripts/ci-local.sh
dco` checks every commit ahead of the branch's base ref for the trailer — run it locally before
opening a PR; it's also where this check will run in CI once Actions is funded again (it has no
`ci.yml` job yet because Actions doesn't run today — see `docs/development.md`'s CI section).

This is a **sign-off**, not a cryptographic signature — GPG/SSH-signed commits (`git commit -S`)
are welcome but are a separate, optional thing from the DCO trailer above.

## Reporting a security issue

Do not open a public issue for a security vulnerability. [`SECURITY.md`](SECURITY.md) at the repo
root names the real channel — GitHub's private vulnerability
reporting (confirmed enabled on this repository) — and `docs/security-disclosure-process.md`
documents the embargo/advisory workflow from report to public disclosure. `docs/security.md`
documents the threat model and mitigations in depth; this paragraph used to say no disclosure
contact existed yet, which was true when T-3302 first wrote it (2026-08-18) and has been wrong
since the same day — corrected here rather than left to contradict `SECURITY.md` silently.
