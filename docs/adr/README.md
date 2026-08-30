# Architecture decision records

This directory publishes vnprox's eleven load-bearing architecture decisions — referred to
throughout the codebase and docs as **D1 through D11** — as numbered ADRs (Architecture Decision
Records) a newcomer can actually read. For 0001–0011 this was a **publication step, not a
decision-making step**: every one of those already existed, locked, in `docs/architecture.md` §10
("Key decisions (locked)") before this directory existed. Nothing in them is re-litigated; each
extracts and expands the existing table row plus the prose justification scattered through the
surrounding sections, and cites the task/report that made or amended the call.

**From 0012 onward that is no longer the whole story.** ADR-0012 is the first record here that
*makes* a decision rather than publishing one — T-4016 was opened to take it, and it binds three
integrations that have no shared code. Such an ADR carries **no D-number**, because the D-series is
the eleven architecture decisions and this is not one of them; the `D#` column reads `—`. The
distinction is worth keeping visible: a reader who assumes everything here was settled elsewhere
would take a live decision for a summary of one.

## Index

| ADR | D# | Title | Status |
|---|---|---|---|
| [0001](0001-go-single-binary-on-node-deployment.md) | D1 | Go single binary, on-node deployment | Accepted |
| [0002](0002-react-typescript-react-flow-frontend.md) | D2 | React + TypeScript + React Flow frontend, embedded | Accepted |
| [0003](0003-pve-api-writes-use-the-users-ticket.md) | D3 | PVE API writes use the user's ticket, never a privileged daemon identity | Accepted, amended |
| [0004](0004-change-engine-is-the-sole-mutation-path.md) | D4 | All mutations go through the change engine (stage → validate → diff → apply → confirm/rollback) | Accepted |
| [0005](0005-proxmox-is-source-of-truth-sqlite-is-app-owned-only.md) | D5 | Proxmox configs remain source of truth; SQLite holds app-owned data only | Accepted |
| [0006](0006-peerless-symmetric-cluster-no-elected-leader.md) | D6 | Peerless symmetric cluster design, no elected leader | Accepted, amended |
| [0007](0007-port-8007-default-with-pbs-conflict-detection.md) | D7 | Port 8007 default, with PBS conflict detection | Accepted |
| [0008](0008-support-bridges-ovs-and-the-full-sdn-stack.md) | D8 | Support Linux bridges, OVS, and the full PVE SDN stack | Accepted |
| [0009](0009-target-pve-8-2-plus-and-9-x.md) | D9 | Target PVE 8.2+ and 9.x, with a forward target for each new major | Accepted, revised each phase |
| [0010](0010-platform-api-freeze-at-v3-0.md) | D10 | Platform API freeze at v3.0 (MCP manifest, plugin SDK v1, WS events envelope) | Accepted |
| [0011](0011-peer-wire-protocol-not-frozen-stays-at-version-2.md) | D11 | Peer wire protocol is explicitly **not** part of the platform freeze; stays at version 2 | Accepted |
| [0012](0012-stage-only-integrations-never-imply-liveness.md) | — | A stage-only integration reports intent recorded, never network converged | Accepted |

`docs/architecture.md` §10's decisions table links to each ADR directly (see that section);
this index is the canonical entry point for reading them as a set.

## What "Status" means here

None of these eleven has been reversed — there is no "Superseded" in the strict sense (a later
decision replacing an earlier one wholesale) among them. Three carry **"Accepted, amended"**
because a later task narrowed or extended the original call without contradicting it, and the
ADR's Consequences section says so explicitly with the task ID:

- **D3** was amended by T-1805, which added the apply-time sealed revert ticket after a real
  incident (`planning/reports/T-502.md`) showed the original "writes use the user's ticket" design
  had no way to revert `fw.*`/`sdn.*` changes once the request that applied them had ended.
- **D6** was amended by T-1704's HA active/standby lease, a narrowly-scoped fenced leader for
  daemon failover only — explicitly documented as *not* reopening D6's cluster-wide peerless
  model (`docs/data-model.md`'s `ha_lease` note).
- **D9** is a moving target by design: "PVE 8.2+ and 9.x" is a floor and a currently-supported
  ceiling, not a fixed range, and the decision itself says each new PVE major gets a validation
  pass within one phase of its release.

## D-numbers referenced in code and docs map to these ADRs — with one exception

Grep for `D1`–`D11` across `internal/`, `docs/`, and `planning/` and most hits are citations of
*this* registry — `docs/architecture.md` §10's locked decisions table, the one this directory
publishes. Cross-reference each ADR against the running code with the file paths its "See also"
section lists.

**The one exception, found while assembling this directory and worth stating plainly rather than
quietly working around: `docs/roadmap-proven.md` has its own, separate "## Decisions" section**
(settled 2026-07-30, scoped to Phase 18–21, "Proven in production") that **also numbers its items
D1 through D7**, and — because both registries started counting from D1 for unrelated reasons —
**every one of D1 through D7 collides**: the same label names two different decisions depending on
which document you're reading.

| Label | `docs/architecture.md` §10 (this directory) | `docs/roadmap-proven.md` §Decisions |
|---|---|---|
| D1 | Go single binary, on-node deployment | Capture a revert ticket at apply time (T-1805) |
| D2 | React + TS + React Flow frontend | Hardware for that arc is `pvecube` only, single node |
| D3 | PVE API writes use the user's ticket | The multi-node gap gets a blocked register + a harder mock |
| D4 | Change engine is the sole mutation path | Decompose all four phases now (accepted planning risk) |
| D5 | Proxmox is source of truth; SQLite app-owned only | Agents build validation harnesses; the human runs them |
| D6 | Peerless symmetric cluster, no leader | Distribution is a static signed apt repo on GitHub Pages |
| D7 | Port 8007 default, PBS conflict detection | Validation evidence protocol: scripts emit JSON, an agent triages |
| D8–D11 | (see index above) | *(roadmap-proven.md has no D8+)* |

Most of the prose in `docs/architecture.md` and `docs/data-model.md` that cites the
`roadmap-proven.md` registry is careful to say so explicitly — e.g. "`docs/roadmap-proven.md`
decision **D1**" rather than a bare "D1" (see `docs/architecture.md` lines 339, 357;
`docs/api.md` line 225; `docs/data-model.md` line 739). **`docs/community-repo-assessment.md`
is the one place that does not**: it cites `roadmap-proven.md`'s D6 (the apt-repo distribution
decision) as a bare "D6" three times (its own lines 18, 67, 75) with no qualifying document name,
which reads as a citation of *this* directory's D6 (peerless cluster) to anyone who hasn't
independently discovered the second registry. This ADR set does not fix that citation — it is
flagged here, and in ADR-0006's own notes, rather than silently corrected, per this task's
instruction to surface a D-number inconsistency rather than pick one.

There is also a **third, unrelated reuse of the `D1, D2, D3...` label** worth knowing about so it
is never mistaken for either decision registry: several implementation reports under
`planning/reports/` (e.g. `T-1906.md`, `T-1902.md`, `T-1805.md`, `T-1901.md`) number their own
"Deviations from the card" lists `D1`, `D2`, `D3`... as a local, per-report convention. Those are
implementation notes about one task, not architecture decisions, and they do not belong to either
registry above — a stray grep hit on `D1`–`D9` inside `planning/reports/` is very likely one of
these, not a decision citation.

## Governance

Maintainer model, release cadence, and the supported-versions/LTS statement are in
[`governance.md`](governance.md), consistent with what `SECURITY.md` and `CONTRIBUTING.md`
already say about this project's staffing.
