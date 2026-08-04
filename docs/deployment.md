# Deployment guide

## Supported platforms

- Proxmox VE 8.2+ (Debian 12) and 9.x (Debian 13), amd64 and arm64.
- **PVE 10.x and 11.x** are the forward compatibility targets for the v3.0 arc (docs/roadmap-universal.md, "Compatibility & versioning"; carried forward from v2.0's PVE 10.x target): no PVE-10/11-specific API break is known against the surfaces vnprox reads/writes, and every Phase 13–17 feature was developed mock-first so CI never needs hardware — but a real PVE 10.x/11.x node has not been exercised in this environment. PVE 10.x/11.x support is therefore a stated target, flagged **needs-hardware-validation** (`planning/reports/needs-hardware-validation.md`) until validated on real hardware, exactly as each prior PVE major got a validation task within one phase of its release. The arc tracks new PVE SDN capabilities (fabrics, NAT zones) with a compatibility-validation task within one phase of each PVE release.
- Install **on every node** of the cluster (the installer handles this). Single-node installs are fully supported.
- Not supported: running vnprox off-cluster (a management VM elsewhere) — vnprox requires on-node deployment for host access and rollback safety. **Federation (v2.0) does not change this:** a designated *primary* vnprox instance attaches other clusters as app-owned registry entries and aggregates their *reads*, but each attached cluster still runs its own on-node vnprox for host-level operations and rollback safety — federation federates views and workflows, never config ownership (docs/security.md, "Cluster-registry credentials").

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

# [peer]                           # T-301/T-1906: the cluster-internal peer API
# secret_path = "/etc/pve/priv/vnprox/cluster.secret"   # default (pmxcfs, root-only)
# ca_file = "/etc/pve/pve-root-ca.pem"  # default: peer TLS is PINNED to the cluster's own root CA.
#                                       # The system trust store is never consulted. Nothing below is
#                                       # needed on a real PVE node — and if the file is missing, peer
#                                       # TLS fails CLOSED (every peer unverifiable) rather than
#                                       # falling back to the system pool.
# tls_trust = "cluster-ca"         # "cluster-ca" (default) | "system" | "insecure". The last two are
#                                  # escape hatches for a host that genuinely has no /etc/pve. Each
#                                  # ALSO requires its own exact tls_trust_ack literal below — one edit
#                                  # is never enough — logs a WARN naming what was given up on every
#                                  # startup, and raises a standing peer_trust_degraded finding.
#                                  # A wrong/missing ack, or an unknown value, is a fatal config error.
# tls_trust_ack = "i-accept-unpinned-peer-tls"     # required by tls_trust = "system"
# tls_trust_ack = "i-accept-unverified-peer-tls"   # required by tls_trust = "insecure"

[retention]
snapshot_keep_days = 90    # committed-changeset snapshots are pinned a minimum of snapshot_pin_days regardless
snapshot_pin_days = 7

[metrics]
enabled = true             # mounts GET /metrics (Prometheus exporter, T-1001); token generated on first start
# key_file = "/etc/vnprox/keys/metrics.key"   # default
# allow_from = ["10.0.0.0/8"]                 # optional source-CIDR allowlist; default: allow any source

# [oidc]                           # T-1207: OIDC SSO alongside the PVE ticket bridge (federated deployments)
# issuer = "https://idp.example.com/realms/vnprox"
# client_id = "vnprox"
# client_secret_file = "/etc/vnprox/keys/oidc-client-secret"   # root:root 0600, never inlined here
# redirect_url = "https://<node>:8007/auth/oidc/callback"
# scopes = ["openid", "profile", "groups"]
# groups_claim = "groups"          # ID-token claim carrying group memberships; mapped to vnprox caps,
#                                  # then intersected with the linked PVE identity's real ACLs (never additive)

# [switches]                       # T-1205: guarded switch-config push — ships DARK by construction
# enabled = false                  # daemon-level master switch; a push also needs the specific switch's own
#                                  # `enabled` row true (registered via /switches). Scoped to PVE-facing ports
#                                  # only (VLAN membership / description / LACP); every push is a changeset.

# [flows]                          # T-1002/T-1004: every source below is off by default, opt-in per node
# sflow_enabled = false            # UDP :6343
# netflow_enabled = false          # UDP :2055 (v5 and v9 share one port)
# ipfix_enabled = false            # UDP :4739
# conntrack_sampling_enabled = false   # periodic /proc/net/nf_conntrack poll; no extra capability needed
# ebpf_sampling_enabled = false        # needs CAP_BPF/CAP_PERFMON (docs/security.md Host footprint); setting
#                                      # this true and reinstalling/upgrading grants the unit that capability
# host_sample_interval_sec = 10        # shared poll interval for the two host-local samplers above

# [latmesh]                        # T-1303: always-on latency & loss mesh (no opt-in flag — a low-rate
#                                   # outbound probe carries no listening-port attack surface)
# probe_interval_sec = 10          # deliberately coarse: a mesh, not a flood
# retention_minutes = 60           # latency_samples ring: retention window AND max_rows, whichever prunes first
# max_rows = 500000

# [mtuprobe]                       # T-1306: always-on path MTU prober, built on [latmesh]'s own scheduler
# probe_interval_sec = 300         # far coarser than [latmesh] — MTU rarely changes
```

## Upgrade

```bash
apt update && apt install vnprox        # per node; any order
```

- DB migrations run automatically on daemon start; forward-only.
- Mixed versions during rolling upgrade: daemons serve, but changeset coordination involving an incompatible peer is refused with an upgrade prompt (architecture §5). Upgrade all nodes promptly.
- Config file changes are documented in release notes; unknown keys are warnings, not fatals.

### Supported upgrade path

**Every schema version vnprox has ever shipped (1 through the current latest, 33) can upgrade
directly to current in a single `apt install vnprox` — no intermediate hop through an in-between
release is ever required.** Schema version 1 (`0001_init.sql`) is the very first release's schema;
there is no vnprox install older than that, so "how far back is upgrading-directly supported"
has one answer: **all the way**, for every install that has ever run a real vnprox build.
`internal/store`'s `TestMigrate_FromEachPriorSchemaVersion` (T-1807) proves this directly rather
than by induction over intermediate hops: it freezes a database at **each** of schema versions
0 (a brand-new file, i.e. a fresh install) through 32, seeds every one of them with representative
rows in that version's own on-disk shape, migrates straight to 33, and asserts — per table, not
just "migrate() returned nil" — that every seeded row is still present and correct afterward. A
second test, `TestMigrate_DestructiveMigrationIsCaught`, injects a synthetic migration that
deliberately deletes rows and confirms the same assertions notice — evidence the data-preservation
checks above have teeth, not just that they run.

If an install predates schema tracking entirely (a database not created by a real vnproxd build —
this has never shipped, but would show up as `schema_version` being unreadable or the `kv` table
missing in a way `currentSchemaVersion` cannot resolve to a sane prior value), that database is
unsupported: back up `/var/lib/vnprox/vnprox.db` for forensics, then reinstall fresh
(`apt purge vnprox && apt install vnprox` and re-run `vnprox-setup`) — vnprox's own store is
disposable app state per the Backup section above, and Proxmox itself (the actual network
configuration) is never touched by this. The one direction that is refused outright, loudly and
by design, is pointing an **older** vnproxd binary at a database a newer build already migrated
(`ErrSchemaTooNew` — see `TestOpen_RefusesNewerSchema`): downgrading the binary without restoring
a pre-upgrade backup of the database is not supported.

### Upgrading a v1.x install to v2.0

- **The v2.0 schema is a forward-only superset of v1.x's.** The federation arc adds migrations `0021_clusters` … `0024_oidc` (cluster registry, switches, external subnets, OIDC links) on top of the v1.x schema; they run automatically on first v2.0 daemon start and touch **only** the new app-owned tables — no existing v1.x row is rewritten. This is pinned by `internal/store`'s `TestMigrate_FromEachPriorSchemaVersion`, which freezes a DB at each prior schema version (including the last pre-federation one), migrates it to the current version, and asserts every pre-existing row survives byte-for-byte.
- **Federation is additive, not a fork.** A v1.x single-cluster install that upgrades to v2.0 **with zero clusters attached** keeps serving its existing single-cluster experience unchanged: the global cluster-capsule view is skipped entirely until a *second* cluster is attached (T-1202's single-cluster-regression bar), and none of DNS/switch-push/OIDC is active unless explicitly configured (switch push additionally ships dark behind its daemon flag). Attaching federation, enabling OIDC, or registering a switch are all opt-in post-upgrade steps.
- New v2.0 config stanzas (`[oidc]`, `[switches]`) are optional; omitting them preserves v1.x behavior exactly (unknown/absent keys are warnings, not fatals).

### Upgrading a v2.x install to v3.0

- **The v3.0 schema is a forward-only superset of v2.x's.** The v2.0 → v3.0 arc adds migrations
  `0025_flow_baselines` … `0031_ha` (flow baselines, capacity samples, posture scores, changeset
  origin, plugins, tenants, and the `ha_lease` singleton) on top of the v2.x schema, followed by
  `0032_cluster_wg_tunnel` (v3.0.2, tunnel-aware federation reachability) and
  `0033_changeset_revert_ticket` (v3.0.3, sealed apply-time revert ticket for unattended
  `fw.*`/`sdn.apply` rollback) landing within the v3.0 line itself; they all run automatically on
  first daemon start at or after the release that introduced them and touch **only** new app-owned
  tables/columns — no existing row from an earlier schema is ever rewritten. This is pinned by
  `internal/store`'s `TestMigrate_FromEachPriorSchemaVersion` (freezes a DB at **every** prior
  schema version 0 through latest-1, migrates each to the current version — **33** as of this
  writing — and asserts every pre-existing row survives, per table; see "Supported upgrade path"
  above for the full guarantee this test backs).
- **Every v3.0 platform feature is opt-in / dormant until configured.** A v2.x install that upgrades
  to v3.0 keeps behaving exactly as before: the MCP server stays unmounted unless `[mcp] enabled =
  true`; the plugin registry holds no plugins until one is installed; multi-tenancy narrows nothing
  until an admin creates a tenant; HA is inert unless `[ha] enabled = true`; the hub's routes are
  unmounted unless `[hub] registry_url` is set. None of these change the single-cluster or federated
  experience of an install that leaves them alone.
- New v3.0 config stanzas (`[mcp]`, `[ha]`, `[hub]`) are optional; omitting them preserves v2.x
  behavior exactly (unknown/absent keys are warnings, not fatals). See the HA section below for the
  **standby-first** sequence an HA pair must follow.

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

## High availability (active/standby, T-1704)

vnproxd can run as an **optional** active/standby pair so the daemon is not itself a single
point of failure. It is off by default; a single-daemon install needs none of this.

Add an `[ha]` section to `/etc/vnprox/vnprox.toml` on **both** daemons. Exactly one of them sets
`bootstrap = true` so a first boot never has both claim the initial term:

```toml
[ha]
enabled = true
instance_id = "node-a"          # unique per daemon; defaults to the hostname
peer_node = "node-b"            # the standby's PVE node name (informational)
peer_address = "10.0.0.2:8007"  # the standby's host:port for the replication push
bootstrap = true                # set true on exactly ONE of the pair
mode = "vip"                    # "vip" | "dns" — the failover-announce mechanism
vip_command = "/etc/vnprox/ha-vip.sh"   # mode=vip: run on every role change, arg = "active"|"standby"
# dns_webhook = "https://dns-automation.example/repoint"  # mode=dns: POST {role,at} on role change
# lease_ttl = "15s"             # optional; sensible defaults otherwise
# renew_interval = "5s"
# fencing_margin = "15s"
# replication_lag_threshold = 500   # audit rows behind before ha_replication_degraded fires
```

Both daemons share the same cluster secret (`[peer] secret_path`) — replication rides
`internal/peer`'s existing TLS+HMAC channel — and, if any replicated state is sealed (WireGuard
keys, webhook secrets), the **same** session key file (`[storage] session_key_file`), since that
ciphertext only decrypts on the standby under the identical key.

**VIP mode** triggers `vip_command "<role>"` on every transition; the operator's script owns the
actual IP move (e.g. `ip addr add`/`del` plus a gratuitous ARP). **DNS mode** POSTs
`{"role","at"}` to `dns_webhook`; the operator's automation repoints the service record. vnprox
neither ships nor manages the VIP/ARP/DNS mechanism — it only triggers the operator-provided one.

`GET /ha/status` reports the daemon's role, lease term, lease expiry, and replication lag.

**Upgrading an HA pair:** upgrade the **standby first** (it holds no lease and drives nothing),
let it rejoin and catch up, then upgrade the active (its lease lapses and the freshly-upgraded
standby promotes, re-arming any in-flight commit-confirm timers to their original absolute
deadlines). Migrations are forward-only, so a newer standby reading the pair's replicated state
is safe; never point an older binary at a store a newer one has migrated.
