# ADR-0011: Peer wire protocol is explicitly not part of the platform freeze; stays at version 2

**D-number:** D11 (`docs/architecture.md` §10, detailed in §13.4)
**Status:** Accepted

## Context

T-1704 added `POST /api/peer/ha/replicate`, a cross-node route the optional HA active/standby pair
uses to replicate changesets, schedules, tokens, the audit-log tail, and in-flight snapshots
between an active daemon and its configured standby. This raised the question ADR-0010 had just
settled for three other surfaces: does the internal peer protocol (`internal/peer`) also become a
frozen, versioned compatibility contract?

## Decision

**No — explicitly.** `docs/architecture.md` §13.4 states this in as many words: the peer wire
protocol "is **not** part of the public platform freeze — it is an internal-only,
same-build-to-same-build contract" (`GET /api/peer/version` gates cross-node coordination on an
**exact** `ProtocolVersion` match, not a compatible-range check). For v3.0 it stays at **protocol
version 2**: `ha/replicate` is additive at version 2, not a version bump, because it is
**503-nil-safe** on any peer that doesn't serve it — the same additive-route precedent every other
observability peer route already follows. Only routes the cross-node write/coordination path
actually depends on would ever force a version bump; HA replication itself only ever targets the
operator-configured standby, never an arbitrary cluster peer, so non-HA deployments and
mixed-version rolling upgrades of ordinary peers are unaffected.

## Consequences

**What this enables.** The peer protocol can keep evolving release to release with none of
ADR-0010's ceremony — no additive-only discipline, no announced deprecation window, no dual-version
support period — because the exact-match version gate means an HA pair (and, in the general case,
any two coordinating peers) are guaranteed to be running the identical build already. There is
nothing external to protect compatibility for, so there is no cost to paying for it.

**What this costs / forecloses.** This is a **deliberate non-freeze**, and it forecloses something
real: no external tool can ever be built to speak vnprox's peer protocol directly, and — more
concretely — the protocol layer itself does not design for genuinely mixed-version peer clusters as
a first-class, ongoing state. That is not a hypothetical gap: `CLAUDE.md` documents that the actual
development cluster, `vnprox-dev`, runs exactly this condition today — `pvecube` on the current
`vnproxd` build, `pve001` on an older one, unable to be upgraded ("mixed-version peering is live,
not hypothetical"). The exact-version gate handles this the only way it can without becoming a
frozen contract: version-skew detection (§5) makes a daemon refuse to coordinate a change involving
a peer with an incompatible version, rather than corrupting state — a safe degrade, but a degrade,
not a supported cross-version peer feature set. If federation or HA topology ever wants to support
rolling upgrades across a *live* cluster rather than refusing to coordinate across one, this
decision is the one that will need revisiting first, and revisiting it means taking on everything
ADR-0010 already pays for its three frozen surfaces.

## See also

- `docs/architecture.md` §13.4 (compatibility stance), §5 (peer version-skew handling), §12 (HA
  topology, the feature that raised this question).
- `docs/api.md` "Peer API" section, `POST /api/peer/ha/replicate`.
- `internal/peer/`, `internal/ha/`.
- `CLAUDE.md` (the live mixed-version dev cluster this decision's cost is not hypothetical against).
