# ADR-0006: Peerless symmetric cluster design, no elected leader

**D-number:** D6 (`docs/architecture.md` §10)
**Status:** Accepted, amended (T-1704's HA lease is a scoped exception, not a reversal)

> **Flagged inconsistency (see `docs/adr/README.md`'s numbering-collision table for the full
> picture):** `docs/roadmap-proven.md` has its own unrelated "D6" — the decision that vnprox's
> distribution channel is a static, signed apt repository on GitHub Pages
> (`packaging/apt-repo.md`). That decision has nothing to do with cluster topology. Worse,
> `docs/community-repo-assessment.md` (lines 18, 67, 75) cites that *other* D6 as a bare "D6"
> with no document qualifier, which reads — to anyone who hasn't found the second registry — as a
> citation of *this* ADR. This ADR does not correct that citation (out of this task's scope); it
> is recorded here as exactly the kind of drift `CLAUDE.md` warns about.

## Context

Proxmox VE already provides cluster membership and a replicated config store (`corosync`,
`pmxcfs`) — building a second, vnprox-level cluster-membership or leader-election protocol on top
would duplicate a solved problem and add a new class of failure mode (split-brain, quorum logic)
that PVE itself already has to get right.

## Decision

vnproxd runs on every node with **no elected leader**. Any node's daemon can coordinate a
changeset — it is simply whichever node's `vnproxd` the user's browser happens to be talking to.
Peer discovery reads the node list straight from the PVE API (`/cluster/status`); peers are reached
at `https://<node-ip>:8007/api/peer/...`, authenticated with an HMAC of the request over a cluster
secret distributed via `/etc/pve/priv/vnprox/` (pmxcfs-replicated, root-only) plus TLS. A
single-node cluster works identically with zero peers configured.

## Consequences

**What this enables.** No consensus protocol to design, implement, or debug — vnprox rides
entirely on corosync/pmxcfs's existing membership and replication guarantees. Whichever node an
operator's browser reaches is a fully capable coordinator, matching how Proxmox's own admin UI
already works (any node administers the cluster). Peer requests degrade gracefully by design: a
peer that fails auth and a peer that simply doesn't answer produce distinguishable findings
(`peer_untrusted` vs `peer_unreachable`), and version-skew detection refuses to coordinate a change
involving a peer running an incompatible schema version rather than corrupting state across a
rolling upgrade.

**What this costs / forecloses.** "Peerless" turned out to need a scoped, deliberate exception:
T-1704's optional active/standby HA pair (`docs/architecture.md` §12) introduces a **fenced leader
lease** (`ha_lease`: `holder`, `term`, `expiresAt`) so that within a two-daemon HA pair, exactly one
daemon may drive apply/confirm/rollback at a time. This is documented explicitly as *not*
reopening this decision — `docs/data-model.md`'s `ha_lease` note: "deliberately distinct from
decision D6's 'peerless symmetric cluster' model: D6 governs cluster-wide read/write
*coordination* (still symmetric, still no cluster leader); `ha_lease` governs only *daemon*
failover in an optional active/standby pair." The distinction is real but narrow enough to be worth
recording as a cost of this ADR rather than pretending it doesn't touch it: a future feature that
wants a cluster-wide (not just HA-pair) coordinator for some other reason now has one working
precedent to point at, which is exactly the kind of pressure a "no leader, ever" decision has to
keep resisting. Separately, this design means there is no single node that can serve a strongly
consistent "the" answer to a cluster-wide question — the one cluster-wide apply interlock (§4) is a
SQLite lock plus a peer advisory check, not a distributed lock service, which is a real
simplification with real edge cases at its boundary (documented, not hidden, in §4). And every peer
route has to be written to be safe when a peer that doesn't implement it is asked anyway
(503-nil-safe), because there is no leader to guarantee every peer speaks the same optional
extensions.

## See also

- `docs/architecture.md` §5 (cluster model), §12 (HA topology — the scoped exception).
- `docs/data-model.md` (`ha_lease` table note, the explicit D6-vs-HA-lease distinction).
- `internal/peer/`, `internal/ha/`.
