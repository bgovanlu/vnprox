# ADR-0001: Go single binary, on-node deployment

**D-number:** D1 (`docs/architecture.md` §10)
**Status:** Accepted

> Not to be confused with `docs/roadmap-proven.md`'s own, unrelated "D1" (the apply-time revert
> ticket) — see `docs/adr/README.md`'s numbering-collision table before citing a bare "D1"
> anywhere in this project.

## Context

vnprox needs to do things no remote API client can: write `/etc/network/interfaces` directly and
exec `ifreload -a`, read LLDP neighbor data via `lldpctl`, run `ethtool`/`tc`, drive
`wg-quick`, and revert all of the above unattended when a change locks an operator out. All of
that requires **direct host access on the node itself**, not a call through the PVE API from
somewhere else. Separately, vnprox is being installed onto production hypervisors — a piece of
software with root-level network-write access needs to behave like appliance-grade infrastructure,
not a services stack an operator has to assemble and keep patched.

## Decision

Ship vnprox as **one static Go binary**, `vnproxd`, running as a single systemd service
(`vnprox.service`) installed **on every Proxmox VE node**. No runtime dependency on anything
beyond the binary itself: no separate database server (SQLite is embedded), no Node.js runtime in
production (the React SPA is embedded via Go's `embed.FS`, §8), no sidecar processes. `apt install
vnprox` is the entire install.

## Consequences

**What this enables.** Direct root-level host operations — the `/etc/network/interfaces`
write-and-`ifreload` path, LLDP reads, `wg-quick`, `tc`, the switch driver seam (§11) — happen
in-process with no second protocol or second agent to keep in sync. The install story is one
package, one service, one config file; there is nothing else to run or patch, which is what makes
the "appliance-grade ops" framing true rather than aspirational. Because it is a single binary per
node, the peer model (ADR-0006) and the on-node write path are the same process — there is no
network hop between "vnprox decided to write a file" and "the file gets written."

**What this costs / forecloses.** vnprox's own availability is coupled to each node it runs on —
there is no way to run "vnprox" once, off to the side, the way you might run a monitoring stack.
Left unaddressed, this makes the daemon itself a single point of failure per node; §12's optional
active/standby HA pair (T-1704) is the answer, but it is an *addition* bolted onto the single-binary
model (two daemons, a fenced lease, state replication over the peer channel), not a departure from
it — HA still means "two nodes each running the single binary," not a separate control-plane
process. Upgrades are per-node and must tolerate version skew across a rolling upgrade (§5: "a
daemon refuses to coordinate changes involving a peer with an incompatible schema version") — there
is no way to upgrade "the cluster" atomically. Distribution has to produce a working static binary
per target architecture (the signed apt repository, `packaging/apt-repo.md`, exists specifically
because "download a `.deb` and hope" wasn't good enough for something with this footprint). And Go
is now load-bearing for the entire backend surface: any future feature that needs host-level
manipulation is bounded by what a Go binary can exec or syscall directly, by design — that's the
tradeoff this decision makes deliberately, not an oversight.

## See also

- `docs/architecture.md` §1 (system context, the diagram), §9a/§9b (demo mode, still single
  binary), §12 (HA topology — the addition, not a reversal, described above).
- `docs/deployment.md` (install/uninstall path).
- `cmd/vnproxd/` (the single binary's entrypoint).
