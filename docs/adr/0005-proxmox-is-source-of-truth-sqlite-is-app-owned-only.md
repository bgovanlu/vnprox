# ADR-0005: Proxmox configs remain source of truth; SQLite holds app-owned data only

**D-number:** D5 (`docs/architecture.md` §10)
**Status:** Accepted

> `docs/roadmap-proven.md` also has its own unrelated "D5" (that arc's decision that agents build
> validation harnesses while a human runs them against real hardware). See `docs/adr/README.md`'s
> numbering-collision table.

## Context

vnprox is an add-on, not a replacement, for Proxmox VE's own config machinery (`pmxcfs`,
`pvecfg`, `/etc/pve/*`). Two properties follow from that positioning that a design has to protect
deliberately: vnprox must never disagree with PVE about what the live network config *is*, and
uninstalling vnprox must leave a fully working cluster behind, with nothing missing.

## Decision

vnprox's SQLite store (`/var/lib/vnprox/vnprox.db`, embedded, WAL mode, one DB per node) holds
**only app-owned data it is the sole author of**: sessions, changesets, snapshots, audit log,
layouts, metrics rings, kv, ingress targets. It **never** persists a shadow copy of PVE network
config as authoritative state (`CLAUDE.md`: "vnprox's SQLite store holds only app-owned data...
Never persist a shadow copy of PVE config as authoritative state"). The in-memory inventory model
that the topology map renders from is rebuilt from live reads on every startup and is explicitly a
cache, never truth (`docs/architecture.md` §3: "The inventory is in-memory and rebuilt on startup —
it is a cache of live state, never persisted as truth").

## Consequences

**What this enables.** Uninstalling vnprox is safe by construction: `docs/deployment.md` states
plainly that uninstalling "never touches network configuration — Proxmox remains the source of
truth and keeps working exactly as configured," and only vnprox's own credential/config files
(`/etc/pve/priv/vnprox/`, `/etc/pve/vnprox/`) are removed. Because the store is disposable app
state by design, "reinstalling loses history, never live config" (`docs/deployment.md`) is a true
statement rather than a hope. It also gives every feature that might have been tempted to cache PVE
config a forcing function to not: the Blueprint spec export/import explicitly renders fresh from a
live inventory snapshot rather than persisting the blueprint as authoritative
(`docs/data-model.md` §"Spec"), and `internal/gitsync` treats a linked git repository as a source
of *intent* that only ever produces a draft changeset through the ordinary
`Parse`/`Import`/`Export` path — it "never becomes authoritative over live config" and "has no
apply path" (`docs/architecture.md` §"internal/gitsync and D5").

**What this costs / forecloses.** Every read that matters is a live read — there is no authoritative
local cache to answer from cheaply, which bounds performance to the PVE API's and host's own
response times and pushes real work onto collector poll cadences (10s PVE API, 5s local host, 30s
LLDP, §3). A feature cannot "remember" what PVE's config was between polls the way it could with a
trusted local mirror; anything wanting a longer memory of state has to build its own bounded,
explicitly app-owned record instead (e.g. `finding_events`, `digest_schedules` — both deliberately
re-derive their payload from live surfaces at read/send time rather than storing a rendered copy,
per `docs/data-model.md`'s notes on each). This is a discipline that has to be actively held, not
just declared: `docs/architecture.md` calls out `internal/presence` and `internal/gitsync` by name
as the packages "most able to break" this invariant, and each carries its own explicit boundary
statement and (for presence) a structural test rather than trusting convention alone.

## See also

- `CLAUDE.md` (ground rules), `docs/architecture.md` §7 (storage), §3 (read path / inventory as
  cache).
- `docs/deployment.md` (uninstall guarantees).
- `docs/data-model.md` §"Spec" (Blueprint export/import), "internal/gitsync and D5" note in
  `docs/architecture.md`.
