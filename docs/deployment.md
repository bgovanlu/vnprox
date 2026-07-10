# Deployment guide

## Supported platforms

- Proxmox VE 8.2+ (Debian 12) and 9.x (Debian 13), amd64 and arm64.
- Install **on every node** of the cluster (the installer handles this). Single-node installs are fully supported.
- Not supported: running vnprox off-cluster (a management VM elsewhere) — v1 requires on-node deployment for host access and rollback safety.

## Ports

| Port | Use |
|---|---|
| 8007/tcp | HTTPS UI + API + WebSocket + peer API (default) |

**Proxmox Backup Server conflict:** PBS's web UI also uses 8007. The installer checks for a listener on 8007 and an installed `proxmox-backup-server` package; if found it prompts for an alternative (suggested **8008**) and writes it to config. All nodes in a cluster must use the same port (installer enforces).

## Install

### Quick install (script)

```bash
# On any one node; the script offers to roll out to all cluster nodes via SSH.
curl -fsSL https://get.vnprox.io/install.sh -o install.sh
less install.sh   # you're piping root on a hypervisor — read it
bash install.sh
```

The installer:
1. Verifies PVE version and architecture; detects cluster membership and node list.
2. Checks port 8007 (see above); asks for the listen port if needed.
3. Installs the `vnprox` .deb (from the apt repo it configures, or a bundled offline .deb with `--offline <file>`).
4. Optionally installs + enables `lldpd` on all nodes (`--with-lldp`, default yes).
5. Creates the read-only PVE API token `vnprox@pve!daemon` (privilege: auditor-level on `/`), stores it root-only.
6. Generates the cluster secret in `/etc/pve/vnprox/` (first node only; pmxcfs replicates it).
7. Writes `/etc/vnprox/vnprox.toml`, generates the session key, enables + starts `vnprox.service`.
8. Repeats 3–7 on the remaining nodes (via SSH root, same mechanism `pvecm` setups already rely on), or prints per-node instructions if SSH between nodes is unavailable.
9. Prints the URL and a first-login checklist.

### Manual install (per node)

```bash
apt install ./vnprox_<version>_amd64.deb
vnprox-setup            # steps 2 and 5–7 above, interactive; --answers file for automation
systemctl enable --now vnprox
```

### Configuration file — `/etc/vnprox/vnprox.toml`

```toml
[server]
listen = "0.0.0.0:8007"
# tls_cert / tls_key: default = reuse PVE's node certificate
read_only = false          # observe-only mode
confirm_timeout_default = 120

[pve]
api_url = "https://127.0.0.1:8006"
token_file = "/etc/vnprox/keys/pve-token"

[safety]
allow_dangerous_ops = false   # see docs/security.md interlocks
# protected_path = "/etc/pve/vnprox/protected.json"   # default (pmxcfs, cluster-wide)

[storage]
db_path = "/var/lib/vnprox/vnprox.db"
session_key_file = "/etc/vnprox/keys/session.key"

[collect]
pve_interval = "10s"
host_interval = "5s"
lldp_interval = "30s"
```

## Upgrade

```bash
apt update && apt install vnprox        # per node; any order
```

- DB migrations run automatically on daemon start; forward-only.
- Mixed versions during rolling upgrade: daemons serve, but changeset coordination involving an incompatible peer is refused with an upgrade prompt (architecture §5). Upgrade all nodes promptly.
- Config file changes are documented in release notes; unknown keys are warnings, not fatals.

## Uninstall

```bash
apt remove vnprox          # keeps /var/lib/vnprox (snapshots, audit) and config
apt purge vnprox           # removes those too
```

Uninstalling never touches network configuration — Proxmox remains the source of truth and keeps working exactly as configured (decision D5). The PVE token and `/etc/pve/vnprox/` are removed on purge of the last node (prompted).

## Backup

Back up `/var/lib/vnprox/vnprox.db` (snapshots/audit/layouts) and `/etc/vnprox/` per node. Network config itself is in `/etc/network/interfaces` and `/etc/pve` — covered by normal PVE backup practice. vnprox is stateless enough that reinstall + re-setup loses only history, never configuration.

## Firewalling vnprox itself

Restrict 8007 to management networks like you (should) restrict 8006. Peer traffic uses the same port between node IPs — allow node↔node on 8007. The install checklist prints suggested pve-firewall rules.

## Troubleshooting quick refs

- `journalctl -u vnprox` — daemon logs (structured).
- `vnproxctl status` — local daemon, peer reachability, PVE API health, collector ages.
- `vnproxctl rollback-now <changeset-id>` — CLI escape hatch to trigger rollback when the UI is unreachable.
- UI unreachable after a bad change *you confirmed*: SSH in and restore the pre-snapshot: `vnproxctl snapshots list` / `vnproxctl snapshots restore <id>` (applies locally with ifreload, bypassing confirm — it *is* the recovery path).
