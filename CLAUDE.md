# CLAUDE.md — instructions for implementation agents

You are implementing **vnprox**, a visual networking add-on for Proxmox VE. All product and technical decisions are already made and documented in this repository. **Do not re-litigate decisions** (language, framework, port, architecture) — if a decision blocks you, flag it in your final report instead of changing it unilaterally.

## Before writing any code

1. Read your task card in `planning/tasks/` — it names exactly which docs are context for your task. Read those.
2. `docs/architecture.md` and `docs/development.md` apply to **every** task.
3. Check `planning/implementation-plan.md` for your task's dependencies — code from dependency tasks already exists; build on it, don't duplicate it.

## Ground rules

- **Backend:** Go 1.23+, single binary `vnproxd`. Standard library first; approved third-party deps are listed in `docs/development.md`. No new major dependencies without a note in your report.
- **Frontend:** React 18 + TypeScript (strict) + Vite, in `web/`. UI components per `docs/development.md`. No `any`, no unchecked casts.
- **Proxmox is the source of truth** for network config. vnprox's SQLite store holds only app-owned data (sessions, changesets, snapshots, audit, layout). Never persist a shadow copy of PVE config as authoritative state.
- **Never apply network changes outside the change engine** (`internal/change/`). All mutations flow: stage → validate → diff → apply → confirm/rollback. This is the product's core safety guarantee.
- **Everything is cluster-aware.** Any feature that reads or writes node state must work when that node is a peer, not just localhost.
- **Testing:** every task card lists acceptance criteria; they are the definition of done. Backend: table-driven Go tests + the mock PVE server in `internal/pvemock/`. Frontend: Vitest + Testing Library for logic-bearing components. Run `make check` (lint + typecheck + tests) before declaring done.
- **Real PVE access:** you have **one** real node — `pvecube`, PVE 9.2.4, root SSH — and **no cluster**. Those are different limits and conflating them has cost this project real defects.
  - **API *shape* is observable, so observe it.** Before modelling any PVE object, run `pvesh usage <path> -v` against pvecube and check the transcript into `planning/reports/evidence/`. `pvesh ls` and `pvesh usage` are read-only. A type enum read off a running node is worth more than any amount of documentation.
  - **Never model a PVE object from `internal/pvemock/`, from `docs/`, or from Proxmox release notes.** Phase 31's scoping found all four sources agreeing that SDN Fabrics were zone types — wrong, and agreeing only because each was written from the last. A mock and the check that tests it, both derived from the same secondary source, will pass together forever.
  - **Cluster *behaviour* is still unobservable**: cross-node validation, peer round-trips, distributed rollback, drift between nodes, fabric/controller convergence. One node cannot answer any of it. Note those in your report as "needs hardware validation" and file them in `planning/reports/needs-hardware-validation.md`.
  - **Develop against `internal/pvemock/` fixtures** as before — but a fixture's job is to match what pvecube says, and when they disagree the fixture is wrong.
  - **Read-only unless the task says otherwise.** pvecube is a live host running the deployed product; a mutating `pvesh` call against it is a change to real network config, outside the change engine.

## Conventions

- Commit style: `area: imperative summary` (e.g. `change-engine: add commit-confirm timer`).
- API routes, JSON field names, and error format: follow `docs/api.md` exactly — other tasks depend on those contracts.
- Data structures: follow `docs/data-model.md` names and shapes.
- Errors are wrapped with context (`fmt.Errorf("applying changeset %s: %w", ...)`); no bare `err` returns across package boundaries.
- Log with `log/slog`, structured, no `fmt.Println`.

## Reporting back

End your work with: what you built, what you tested and how, deviations from the task card (with reasons), and anything the next agent must know.
