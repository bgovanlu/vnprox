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
1. Checks that a PVE install is present (runs `pveversion`, logs its output) and detects architecture, cluster membership, and node list. **Correction (flagged, T-607):** this doc previously said the installer "verifies" the PVE version, implying it enforces the documented 8.2+ minimum — `packaging/install.sh` only logs `pveversion`'s output today, it does not parse/enforce a minimum version. Low-risk (an incompatible architecture is still caught), but the check is weaker than "verifies" implies; follow-up: parse and enforce the minimum if this matters for a real early-PVE-version install attempt.
2. Checks port 8007 (see above); asks for the listen port if needed.
3. Installs the `vnprox` .deb (from the apt repo it configures, or a bundled offline .deb with `--offline <file>`).
4. Optionally installs + enables `lldpd` on all nodes (`--with-lldp`, default yes).
5. Creates the read-only PVE API token `vnprox@pve!daemon` (privilege: auditor-level on `/`), stores it root-only.
6. Generates the cluster secret in `/etc/pve/priv/vnprox/` (first node only; pmxcfs replicates it). **Correction (T-608, hardware validation):** this is under `priv/` specifically — pmxcfs only auto-restricts files under `/etc/pve/priv/` to `0600` root-only; everywhere else under `/etc/pve` it silently coerces creation-time permissions to `0640 root:www-data` and rejects `chmod()` outright, confirmed against a real PVE 9.2.4 node.
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

[retention]
snapshot_keep_days = 90    # committed-changeset snapshots are pinned a minimum of snapshot_pin_days regardless
snapshot_pin_days = 7

[metrics]
enabled = true             # mounts GET /metrics (Prometheus exporter, T-1001); token generated on first start
# key_file = "/etc/vnprox/keys/metrics.key"   # default
# allow_from = ["10.0.0.0/8"]                 # optional source-CIDR allowlist; default: allow any source

# [flows]                          # T-1002/T-1004: every source below is off by default, opt-in per node
# sflow_enabled = false            # UDP :6343
# netflow_enabled = false          # UDP :2055 (v5 and v9 share one port)
# ipfix_enabled = false            # UDP :4739
# conntrack_sampling_enabled = false   # periodic /proc/net/nf_conntrack poll; no extra capability needed
# ebpf_sampling_enabled = false        # needs CAP_BPF/CAP_PERFMON (docs/security.md Host footprint); setting
#                                      # this true and reinstalling/upgrading grants the unit that capability
# host_sample_interval_sec = 10        # shared poll interval for the two host-local samplers above
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

Uninstalling never touches network configuration — Proxmox remains the source of truth and keeps working exactly as configured (decision D5). The PVE token, `/etc/pve/priv/vnprox/` (cluster secret), and `/etc/pve/vnprox/` (protected-interface config) are removed on purge of the last node (prompted).

## Backup

Back up `/var/lib/vnprox/vnprox.db` (snapshots/audit/layouts) and `/etc/vnprox/` per node. Network config itself is in `/etc/network/interfaces` and `/etc/pve` — covered by normal PVE backup practice. vnprox is stateless enough that reinstall + re-setup loses only history, never configuration.

## Firewalling vnprox itself

Restrict 8007 to management networks like you (should) restrict 8006. Peer traffic uses the same port between node IPs — allow node↔node on 8007. **Correction (flagged, T-607):** the install checklist prints a generic prose reminder to do this (pointing at `docs/security.md`), not ready-to-apply pve-firewall rule syntax as this line previously implied — `install.sh`/`vnprox-setup` don't generate actual rule text. Follow-up: print copy-pasteable rule syntax if this becomes a real operator pain point.

## Troubleshooting quick refs

- `journalctl -u vnprox` — daemon logs (structured).
- `vnproxctl status` — local daemon, peer reachability, PVE API health, collector ages.
- `vnproxctl rollback-now <changeset-id>` — CLI escape hatch to trigger rollback when the UI is unreachable.
- UI unreachable after a bad change *you confirmed*: SSH in and restore the pre-snapshot: `vnproxctl snapshots list` / `vnproxctl snapshots restore <id>` (applies locally with ifreload, bypassing confirm — it *is* the recovery path).

## `vnproxctl remote`/`apply` — HTTP-backed CLI parity (T-1105)

`status`/`snapshots`/`rollback-now` above are deliberately daemon-INDEPENDENT
(direct SQLite reads, local `ifreload`) — the documented disaster-recovery
path when the daemon or UI is down. T-1105 adds a second, opposite family:
HTTP-backed commands that require the daemon up and talk exclusively to its
`/api/v1` surface with a T-1104 bearer token (`--token <token>` or
`VNPROX_TOKEN`; never a PVE username/password from this CLI). **Naming-
collision resolution:** rather than overload any of the three existing
top-level names (e.g. a second meaning for `vnproxctl snapshots list`), every
new command lives under its own `vnproxctl remote <subcommand>` namespace,
plus a standalone `vnproxctl apply` for the GitOps spec flow — the existing
three commands' names, flags, and output are unchanged.

```
vnproxctl remote topology|findings|drift|audit
vnproxctl remote changesets list|get|diff|create|validate|apply|confirm|rollback|discard
vnproxctl apply <spec.yaml> --plan     # POST /spec/import, print diff, exit 3 if changes pending
vnproxctl apply <spec.yaml> --apply    # ...then apply + poll to committed + auto-confirm
```

Every command in the binary (including the pre-existing three) supports
`-o table` (default) or `-o json`. Stable exit codes (see
`cmd/vnproxctl/exitcodes.go`): `0` success, `1` generic error, `2` usage,
`3` validation-failed/plan-pending, `4` auth (401/403 — missing/invalid/
revoked token or insufficient scope), `5` network (daemon unreachable),
`6` apply-timeout (`--apply`'s changeset never reached `committed` within
`--apply-timeout`). CI can branch on these directly, e.g. the exit-demo flow
from `planning/tasks/phase-11.md`'s Phase 11 intro: a PR to a spec repo →
`vnproxctl apply spec.yaml --plan` in CI → merge schedules/applies during a
maintenance window → `vnproxctl remote changesets get`/`remote drift`
confirms a clean result the next morning.
