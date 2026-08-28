# ADR-0007: Port 8007 default, with PBS conflict detection

**D-number:** D7 (`docs/architecture.md` §10)
**Status:** Accepted

> `docs/roadmap-proven.md` also has its own unrelated "D7" (that arc's decision that validation
> scripts emit machine-readable JSON for an agent to triage). See `docs/adr/README.md`'s
> numbering-collision table.

## Context

vnprox needs a default listen port for its HTTPS/WebSocket API and UI. Proxmox Backup Server
(PBS) — a very common co-install on the same hardware — already defaults to port 8007, so picking
that port outright creates a foreseeable, common conflict at install time rather than a rare edge
case.

## Decision

Default to **8007/tcp HTTPS**. The installer detects an existing listener on 8007 (or a detectably
installed PBS) and prompts for an alternative — suggested 8008 — writing the choice to
`/etc/vnprox/vnprox.toml`. TLS reuses the node's own PVE certificate
(`/etc/pve/local/pve-ssl.pem` + key, or `pveproxy-ssl.pem` for a custom cert), auto-reloaded on
renewal, so the browser's trust story matches what an operator already trusts for PVE itself;
WebSocket traffic shares the same port (`/api/ws`).

## Consequences

**What this enables.** Works out of the box on the common case (a node that isn't also running
PBS) with zero configuration, and degrades to a one-prompt fix on the case that would otherwise
silently fail to bind. Reusing PVE's certificate means there is no second self-signed cert for an
operator to click through or pin manually — the browser trust story is identical to the one they
already have for the Proxmox web UI itself.

**What this costs / forecloses.** Conflict detection only runs at install time — installing PBS
*after* vnprox is not automatically detected or re-prompted, so that ordering requires a manual
port change vnprox cannot discover on its own. 8007 as the canonical default is now baked into
documentation, screenshots, default configs, and operator muscle memory across the whole project;
changing the default later is a much bigger exercise than it looks, precisely because so much
downstream material assumes it. Sharing the PVE certificate is convenient but couples vnprox's TLS
posture to PVE's certificate lifecycle — rotation on the PVE side has to propagate correctly (the
30s reload cadence noted in §9's peer-CA-pinning discussion exists to make that hold true), which
is one more thing that has to keep working for vnprox's own listener to stay trusted.

## See also

- `docs/architecture.md` §9 (ports and coexistence).
- `docs/deployment.md` (install-time detection and prompting).
