# Security design

vnprox runs as root on hypervisor hosts and can reconfigure the network of an entire cluster. Security posture is therefore conservative by design.

## Authentication

- **No vnprox-local accounts.** Users authenticate with Proxmox credentials; vnproxd forwards to PVE `POST /access/ticket` (supports any PVE realm: PAM, PVE, LDAP/AD, OIDC, and TOTP/second factor passthrough via `otp`).
- On success vnproxd creates a server-side session: random 256-bit id in an `HttpOnly; Secure; SameSite=Strict` cookie. The PVE ticket + CSRF token are stored encrypted at rest (AES-256-GCM; key in `/etc/vnprox/keys/session.key`, root:root 0600, generated at install).
- PVE tickets expire at 2h; vnproxd renews at ~1h30 while the session is active. vnprox sessions idle out at 2h, hard cap 12h.
- CSRF: double-submit — mutating requests require `X-VNPROX-CSRF` matching the session record.
- Login rate limiting: per-IP and per-username token bucket; lockout events audited.

## Authorization

Two enforcement layers, both required:

1. **PVE-enforced (primary):** all PVE API writes use the *user's own ticket* — vnprox cannot exceed the user's PVE ACLs, and PVE remains the audit point of record for those calls.
2. **vnprox-enforced (for host-level ops):** operations that bypass the PVE API (reading LLDP, staging `/etc/network/interfaces`, ifreload, snapshot restore) are gated on capability flags derived from the user's PVE ACLs at login (and re-derived hourly): `Sys.Audit` on `/nodes/{node}` → read; `Sys.Modify` on `/nodes/{node}` → node network write; `SDN.Allocate` → SDN write; `VM.Config.Network` → guest NIC edits; firewall caps from `Sys.Modify`/relevant paths. The mapping table lives in `internal/auth/caps.go` and is the single source of truth. When `[server] read_only = true` (docs/features/blueprints.md §3's "observe-only until you trust it" toggle), every derived capability's write flags are additionally forced false regardless of the user's PVE ACLs (`internal/auth.forceReadOnly`, applied inside `deriveCapabilities`) — since every `RequireCap`-gated mutating route in `internal/api` gates on these same flags, this makes `read_only` a real, server-enforced restriction, not merely a UI affordance the frontend is trusted to hide.

There is **one privileged internal identity**: a PVE API token `vnprox@pve!daemon` (created at install, stored root-only) used exclusively for *read* polling by collectors — never for writes. Writes without a user context do not exist, with one exception: automatic rollback of an unconfirmed changeset, which restores from snapshot using host-level file operations and is attributed in the audit log to `system:rollback` with the originating user recorded.

## Transport

- TLS everywhere; no plaintext listener. Reuses the node's PVE certificate by default (see architecture §9), so admins keep one cert story.
- HSTS, `X-Content-Type-Options`, `X-Frame-Options: DENY`, and a strict CSP (self-only; no inline script; WS to self).
- Peer API: TLS + HMAC-SHA256 over (method, path, body hash, timestamp) with the cluster secret; ±30s replay window; constant-time compare.

## Host footprint

- Systemd unit hardening: `ProtectSystem=strict` with explicit `ReadWritePaths` (`/var/lib/vnprox`, `/etc/network`, `/run`), `ProtectHome=yes`, `PrivateTmp=yes`, `NoNewPrivileges=yes`, capability bounding to what netlink/ifreload require (v1 runs as root — capability reduction is a tracked hardening task, T-604).
- vnproxd never shells out with user-supplied strings; the few external commands (`lldpctl -f json`, `ethtool`) use fixed argv arrays and interface names validated against the kernel's interface list.
- SQLite DB and key files are `root:root 0600`; snapshots contain network configs (not secrets) but are treated as sensitive.

## Safety interlocks (availability is security here)

- The change engine refuses (hard error, no override in UI; override only via config flag `allow_dangerous_ops`) any changeset that would: remove/re-address the interface carrying the node's management IP, take down the interface used by corosync links, or delete a bridge with running guests attached (must first present guest reattachment ops).
- Commit-confirm auto-rollback (architecture §4) bounds the blast radius of every applied change.
- Cluster-wide apply lock prevents concurrent conflicting changesets.

## Audit

Every mutation attempt (including denied and rolled-back) is written to the audit log with user, source IP, changeset id, op summaries, and result. Audit entries are append-only at the API layer; there is no delete endpoint.

## Threat model summary

| Threat | Mitigation |
|---|---|
| Credential stuffing on 8007 | rate limits, PVE lockout semantics, TOTP passthrough |
| Session theft | HttpOnly+Secure+SameSite cookies, short idle timeout, encrypted server-side tickets |
| Privilege escalation via vnprox | user-ticket writes (PVE enforces), read-only daemon token, cap mapping for host ops |
| Rogue peer / lateral movement | cluster-secret HMAC + TLS, pmxcfs-distributed root-only secret, replay window |
| Malicious/buggy change bricks cluster | validators + safety interlocks + commit-confirm rollback + time machine |
| Supply chain | pinned deps, `make check` includes `govulncheck` + `npm audit` gates in CI |
| XSS → config change | strict CSP, no inline script, CSRF header, framework-escaped rendering only |
