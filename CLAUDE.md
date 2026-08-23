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
- **Real PVE access:** you have a **real, quorate two-node PVE cluster** — `vnprox-dev`, formed 2026-08-18. Know which node you are talking about, because your access to the two is not the same:
  - `pvecube` (192.168.1.9), PVE 9.2.4, **root SSH**. Runs the deployed vnproxd. This is the node you can log into.
  - `pve001` (192.168.1.7), PVE 9.2.4, **no SSH credentials, no authorisation to modify it**. Observable through pvecube's own `pvesh` (`pvesh get /nodes/pve001/...`) and reachable as a vnprox peer, which is enough for most validation. It runs an *older* vnproxd, so mixed-version peering is live, not hypothetical — and we cannot upgrade it. Anything requiring a change on `pve001` is blocked on the owner.
  - This paragraph previously read "one real node, and **no cluster**", and stayed wrong for five days after the cluster was formed — long enough for an audit to classify 17 features as unprovable and file 156 validation items against a limit that did not exist. **If a limit here blocks you, verify it on the node before you accept it** (`pvecm status` takes a second). That is the same rule as the two bullets below, applied to this file.
  - **API *shape* is observable, so observe it.** Before modelling any PVE object, run `pvesh usage <path> -v` against pvecube and check the transcript into `planning/reports/evidence/`. `pvesh ls` and `pvesh usage` are read-only. A type enum read off a running node is worth more than any amount of documentation.
  - **Never model a PVE object from `internal/pvemock/`, from `docs/`, or from Proxmox release notes.** Phase 31's scoping found all four sources agreeing that SDN Fabrics were zone types — wrong, and agreeing only because each was written from the last. A mock and the check that tests it, both derived from the same secondary source, will pass together forever.
  - **Cluster *behaviour* is now mostly observable — go and observe it.** Cross-node validation, peer round-trips, drift between nodes and fabric/controller convergence can all be exercised against `vnprox-dev` today. Three things still cannot, and only these three earn a "needs hardware validation" note in `planning/reports/needs-hardware-validation.md`: anything **destructive** (partition, quorum loss, killing a node mid-rollback — `vnprox-dev` is live and must not be broken; use a disposable nested lab, see T-3704); anything needing **three or more nodes** (quorum with a survivor); and anything needing **real NICs or a physical switch** (bond failover, LACP). File under one of those three, never under a bare "needs hardware".
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
