# ADR-0004: All mutations go through the change engine

**D-number:** D4 (`docs/architecture.md` §10)
**Status:** Accepted — "non-negotiable" per the decisions table itself

> `docs/roadmap-proven.md` also has its own unrelated "D4" (that arc's planning-risk decision to
> decompose all four of its phases before any of them ran). See `docs/adr/README.md`'s
> numbering-collision table.

## Context

vnprox writes to production network configuration on hypervisors — bridges, bonds, VLANs, SDN
zones/vnets/subnets, firewall rules. The product's central safety claim, stated in
`docs/architecture.md` and repeated in `CLAUDE.md`, is "if the change locks you out, it reverts
itself." That claim is only true if there is exactly one way to make a mutation, and that one way
always goes through validation, a visible diff, a pre-state snapshot, and a commit-confirm timer.

## Decision

Every mutation, without exception, is a **changeset**: an ordered list of typed operations
(`bridge.create`, `bond.update`, `sdn.vnet.create`, `fw.rule.move`, ...) that flows
**stage → validate → diff → apply → confirm/rollback** through `internal/change` and nowhere else
(`CLAUDE.md`: "Never apply network changes outside the change engine... This is the product's core
safety guarantee"). Apply produces an explicit, previewable ordered plan; a commit-confirm rollback
timer runs on the node's daemon (not the browser) after apply, and restores the pre-apply snapshot
automatically if confirmation never arrives — the Junos-style pattern that protects against a
change that severs connectivity to the operator confirming it.

## Consequences

**What this enables.** One place enforces validation (schema, referential, safety, cross-node
consistency), one place takes pre-apply snapshots, one place drives commit-confirm and rollback,
and one place writes the audit log for every mutation. Every later surface that touches
configuration — the plugin SDK's `Stager` boundary (§11), the MCP server's stage-only tools
(§13.1), git-sync's draft-changeset-only design (§ "internal/gitsync and D5"), the advisory-only
entity-lock feature (§ "internal/presence and D4") — was built to compose with this single path
rather than add a second one, and several of those boundaries are enforced **structurally**, not by
convention: a real test over the running import graph
(`presence.TestChangeEngineDoesNotImportPresence`) fails the build if `internal/change` ever
imports the presence package; a compile-time interface-surface test asserts the plugin `Stager` has
no reachable apply/confirm/rollback method; a package-load check rejects any MCP tool name matching
an apply/confirm/approve/delete/rollback/discard verb. This is what makes "non-negotiable" a
checkable claim rather than a policy statement.

**What this costs / forecloses.** Every write-shaped feature, however small, has to be modeled as
changeset ops before it can touch anything — there is no fast path for "just this one write," and
that is real, ongoing development friction each new feature pays. It is also why some features are
deliberately weaker than they otherwise would be: `internal/presence`'s entity locks are
**advisory-only** — warn, name the holder, let the operator override — specifically because a lock
that could block an apply would be a second gate on this pipeline, which this decision forbids; it
fails in the safe direction (a missed warning, never a missed apply) rather than growing a second
mutation path to be stricter. The single cluster-wide "one changeset applying at a time" interlock
(§4, a SQLite lock plus a peer advisory check) is a simplification this decision requires holding
onto rather than replacing with a more general distributed-lock service. And because this
invariant is treated as the core differentiator, any future feature proposal that would need a
second, faster mutation route (bulk import at scale, for instance) has to fit inside
stage → validate → diff → apply → confirm/rollback or be rejected outright — the decisions table
calls this non-negotiable for a reason.

## See also

- `docs/architecture.md` §4 (the change engine, full sequence diagram), §11 ("The one boundary" —
  plugins never gain an apply path), §13.1 (MCP's stage-only tools).
- `internal/change/` (the engine itself); `internal/presence/`,
  `presence.TestChangeEngineDoesNotImportPresence` (the structural boundary test).
- `docs/features/change-management.md`.
