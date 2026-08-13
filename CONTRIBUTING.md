# Contributing to vnprox

Thanks for considering it. Read this first — it's short — then `docs/development.md` for the full
technical detail (repo layout, coding standards, CI, the mock PVE server).

## Before you file anything or open a PR

**Where to file a bug or propose a change is currently unsettled** — see `docs/support.md`. As of
this writing the canonical repository, `github.com/bgovanlu/vnprox`, is **private**: it exists and
is actively pushed to, but an anonymous request returns 404, so there is no public issue tracker
and no public clone URL. This document describes the process as it's meant to work, with that
stated plainly rather than pretended away. If you're reading this from inside a working copy
someone gave you directly, ask them where they want contributions routed; this file may predate
that answer.

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

## Reporting a security issue

Do not open a public issue for a security vulnerability. `docs/security.md` documents the threat
model and mitigations in depth but, checked while writing this, does not yet name a dedicated
disclosure contact or process — that's a gap, not an oversight to route around silently. Until one
exists, treat this the same as `docs/support.md`'s general "where to file" note: there is no
confirmed public channel yet. If you've found something serious, hold it rather than posting
details publicly, and escalate through whatever channel gave you access to this repository.
