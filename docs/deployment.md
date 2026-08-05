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

## Backup and disaster recovery

Network configuration itself is never vnprox's — `/etc/network/interfaces` and `/etc/pve` belong
to Proxmox and are covered by ordinary PVE backup practice. What only vnprox holds is its own
app-owned state: **every changeset and its diff, every pre/post rollback snapshot, the full audit
trail, saved layouts, tenants and blueprint state** — precisely the artifacts you most want *after*
an incident. Lose the box and you lose the record of what was changed and every snapshot you would
have rolled back to.

`vnproxctl backup` and `vnproxctl restore` (T-1901) are the supported way to keep and recover that.
Like `status`/`snapshots`/`rollback-now`, they are **daemon-independent**: they read `vnprox.toml`
and touch SQLite directly, with no HTTP API involved, so they work when the daemon (or its
certificate, or the UI) is the broken thing.

### Taking a backup

```bash
vnproxctl backup                                   # -> /var/lib/vnprox/backups/vnprox-backup-<node>-<UTC>.tar.gz
vnproxctl backup --out /mnt/nas/pve1-vnprox.tar.gz # exact path
vnproxctl backup --keep 14                         # also prune to the newest 14 archives here
vnproxctl backup -o json                           # machine-readable result
```

**Safe to run against a running daemon.** The store copy is taken with SQLite's `VACUUM INTO`, a
consistent point-in-time snapshot from a second connection — not `cp vnprox.db`, which in WAL mode
silently omits every commit still sitting in `vnprox.db-wal`.

The archive is a gzipped tar whose first entry is a manifest recording the format version, the
node, the UTC timestamp, the store's **schema version**, whether key material is included, and a
SHA-256 for every entry. It is written `0600`.

**What is in it**

| Entry | Contents |
|---|---|
| `store/vnprox.db` | the consistent store snapshot |
| `config/vnprox.toml` | this node's config, verbatim |
| `keys/…` | **only** with `--include-keys` (see below) |
| `readme.txt` | a plain-text description of the archive for whoever finds it later |
| `manifest.json` | format/version/node/schema/digests |

### Key material is opt-in, and loud

A backup taken **without** `--include-keys` contains **no key material at all**. Every credential
vnprox holds lives in the store as AES-256-GCM ciphertext sealed with
`/etc/vnprox/keys/session.key` (docs/security.md, "Authentication"), and that key is *not* in the
archive — so the archive is safe to copy to a NAS, an object store, or a colleague's laptop, and is
useless to anyone who obtains it. **This is the right default and should stay the default.**

`--include-keys` produces something categorically different: an archive that, on its own, is a
complete compromise of every PVE credential, federation credential, WireGuard private key, webhook
secret and sealed revert ticket this installation holds. It therefore:

- prints a warning **naming every class** it is about to include, before writing anything;
- requires an interactive `include-keys` confirmation (or an explicit `--yes` for automation, which
  still prints the warning);
- marks the manifest `includesKeyMaterial: true`; and
- names the file `…-with-keys.tar.gz`, so the marking is visible in an `ls` without opening it.

Treat such a file exactly as you treat `/etc/vnprox/keys` itself.

The **peer cluster secret** (`/etc/pve/priv/vnprox/`) is never collected under any flag: it is
cluster-shared state that pmxcfs replicates, not this node's to hand out.

### Scheduled backups

vnprox ships a systemd timer, **installed but not enabled**:

```bash
systemctl enable --now vnprox-backup.timer     # daily 02:30 ±30m, keeps 14 archives
systemctl list-timers vnprox-backup.timer
```

It runs `vnproxctl backup --out-dir /var/lib/vnprox/backups --keep 14` — no `--include-keys`, on
purpose. The randomised delay matters on a cluster: without it every node would snapshot its store
at the same instant. A cron line is equivalent if you prefer one:

```cron
30 2 * * *  /usr/bin/vnproxctl backup --out-dir /var/lib/vnprox/backups --keep 14
```

There is deliberately **no scheduler inside vnproxd**: a daemon that schedules its own backups
cannot back itself up when the daemon is the thing that is broken. `/var/lib/vnprox/backups` is
local storage on a hypervisor's root filesystem — replicate it somewhere else (PBS, rsync, an
object store) if you want it to survive the node.

### Restoring

```bash
systemctl stop vnprox                                     # required — see below
vnproxctl restore --dry-run /path/to/vnprox-backup-….tar.gz   # decide first
vnproxctl restore          /path/to/vnprox-backup-….tar.gz
systemctl start vnprox
```

`--dry-run` validates the archive completely, prints exactly what would happen, and changes
nothing. Its plan is generated by the same code path as the real run, so the two cannot drift.

Four refusals, all of them deliberate:

1. **A running daemon.** Restoring would swap the store file out from under a daemon holding open
   descriptors onto it. Detected two ways — an advisory lock the daemon holds on
   `/var/lib/vnprox/vnprox.db.lock` for its whole lifetime, and a probe of `[server] listen` (which
   also catches a pre-v3.2 daemon that takes no lock). Either one refuses.
2. **A store from a newer vnprox.** Forward migration is supported and runs automatically; the
   downgrade direction is refused with a message naming both versions. Install the newer vnprox
   first, then restore. The check is made against the archive's manifest *and* re-made against the
   store actually inside it, so an edited manifest cannot smuggle a newer store past it.
3. **A support bundle.** `vnproxctl support-bundle`'s output shares this archive format but is
   redacted by construction; restoring one would install a deliberately incomplete store.
4. **A malformed or hostile archive.** The archive is untrusted input: entry names come from a
   strict allowlist, only regular files are accepted (no symlinks, hardlinks or devices), every
   read is bounded by absolute byte and entry-count budgets, and every entry must match the
   manifest's declared size and SHA-256. All of this is checked in a pass that writes nothing to
   disk at all, before extraction begins.

**The restore is atomic.** The archive is extracted into a private directory *next to* the target
store, forward-migrated there, and only then swapped in by two renames within one directory. If
anything fails at any point, the live store is untouched — and if the swap itself fails, the
previous store is put back. On success the previous store is **kept**, not deleted, at
`/var/lib/vnprox/vnprox.db.pre-restore-<UTC>`; remove it by hand once you are satisfied.

### Restoring onto different hardware

Supported and expected — the archive is not tied to the machine it came from.

**Carries over:** every changeset and diff, every pre/post rollback snapshot, the whole audit
trail, layouts, tenants, blueprints, and every app-owned table.

**Must be re-established:**

| Thing | Why, and what to do |
|---|---|
| Node identity (hostname) | Snapshots are keyed per node name. `vnproxctl snapshots restore` resolves the local host's name against what the snapshot captured, so if the replacement host has a different name, pass `--node`. |
| Peer cluster secret | Lives on pmxcfs (`/etc/pve/priv/vnprox/`) and is never in the archive. It reappears when the node rejoins a cluster; a standalone rebuild regenerates one on first start. |
| Sealed credentials | Unless the archive was taken with `--include-keys` **and** restored with `--restore-keys`, the store's sealed columns will not decrypt under the new node's own session key. Re-enter PVE credentials, federation cluster credentials, switch credentials, webhook secrets, and re-create WireGuard tunnels (rotation is delete-and-recreate by design — docs/security.md). |
| PVE API token | Re-created by `vnprox-setup`, or restored with the keys. |
| `vnprox.toml` | **Not** installed unless you pass `--restore-config`: an archive from another node carries that node's listen address and certificate paths. The archived copy is always available inside the tarball for you to diff. |

`--restore-config` and `--restore-keys` both move any existing file aside to
`<path>.pre-restore-<UTC>` rather than overwriting it.

### If you cannot restore

vnprox's store is disposable app state, by design (decision D5): reinstalling loses history, never
configuration. Proxmox itself — the actual network configuration — is untouched by any of this and
keeps working exactly as configured.

## Support bundles (T-1902)

`vnproxctl support-bundle` produces **one redacted archive that lets someone diagnose this install
without logging into it.** It is the thing to attach to a bug report, a forum thread, or a message
to whoever is helping you.

```bash
vnproxctl support-bundle --dry-run       # print exactly what would be collected; write nothing
vnproxctl support-bundle                 # -> /var/lib/vnprox/support/vnprox-support-<node>-<UTC>.tar.gz
vnproxctl support-bundle --no-probe      # make no outbound connection at all
vnproxctl support-bundle -o json         # machine-readable
```

Like `backup`/`restore`, it is **daemon-independent** — that is the point. A bundle is most needed
when vnproxd will not start, so nothing in it requires a healthy daemon: the store is read
**read-only** (SQLite `query_only`, so inspecting a store with a failed migration cannot migrate
it), peers come from `corosync.conf` rather than from a live peer client, and the daemon's own
`/api/v1/health` is one probe among many rather than the source of everything.

### What is in a bundle

| Entry | Contents | Redaction |
|---|---|---|
| `readme.txt` | what this bundle holds and what it deliberately omits — generated from the code, not written by hand | generated text |
| `environment.json` | node, build, kernel, OS, clock/UTC offset, and the existence + mode of every path vnprox depends on | typed fields, allowlisted |
| `config/vnprox.redacted.json` | `vnprox.toml` **key by key**; a key not on the bundle's allowlist keeps its name and loses its value | per-key allowlist |
| `store/summary.json` | schema version vs. this binary's, migration state, size, `integrity_check`, per-table row counts | derived facts only |
| `changesets/recent.json` | the last N changesets with ops, plan and apply log | typed fields + JSON key-walk redaction |
| `findings/events.json` | stored finding transitions (new/escalated/resolved) | typed fields, scrubbed |
| `host/network.json` | `/etc/network/interfaces` as **structure** — stanzas and allowlisted options | per-option allowlist |
| `peers.json` | cluster peers from `corosync.conf`, each `ok` / `unreachable` / `untrusted` | typed fields, scrubbed |
| `probes.json` | is the listen port taken, is the daemon answering, do the declared key files exist and with what mode | typed fields, scrubbed |
| `logs/daemon.log`, `logs/summary.json` | the tail of `journalctl -u vnprox` (or `--log-file`) | every line through the redactor |

### What is deliberately not in a bundle

- **The store itself.** `store/vnprox.db` is never included: it carries the whole audit trail,
  every rollback snapshot, and the ciphertext of every sealed credential. Derived facts only.
- **`vnprox.toml` verbatim** and **`/etc/network/interfaces` verbatim.** Both are re-emitted through
  explicit allowlists — an interfaces(5) file can legitimately carry a WireGuard private key.
- **Any key file's contents.** The declared key files are reported by existence and mode only; the
  bundle never opens them.
- **Anything matching a credential shape** in a log line, an error string, a changeset title or a
  changeset's ops — replaced with `[REDACTED-BY-VNPROXCTL-SUPPORT-BUNDLE]`, so you can grep for what
  was removed rather than wondering whether it was removed or simply never there.

There is **no `--include-keys` and no equivalent.** A bundle's collectors implement an interface
with no way to declare an emitted secret class at all, so `secretClasses` in its manifest is empty
by construction rather than by default. See `docs/security.md`, "Support bundles".

`vnproxctl restore` **refuses** a support bundle: it shares the archive format but is deliberately
incomplete, and restoring one would install a store with no history in it.

### Decide before you produce one

`--dry-run` runs the identical collection, prints exactly what the archive would contain, and then
throws the staging area away without writing anything. It is the same code path as a real run — the
plan is built from what was actually collected, not from a prediction of it — so what it prints
cannot disagree with what a real bundle holds.

### It is still a map of your network

A bundle contains no credential. It does describe node names, interface names, IP addressing, VLAN
ids and changeset titles. **Read `readme.txt` inside it before you attach it to anything public.**

## Firewalling vnprox itself

Restrict 8007 to management networks like you (should) restrict 8006. Peer traffic uses the same port between node IPs — allow node↔node on 8007. **Correction (flagged, T-607):** the install checklist prints a generic prose reminder to do this (pointing at `docs/security.md`), not ready-to-apply pve-firewall rule syntax as this line previously implied — `install.sh`/`vnprox-setup` don't generate actual rule text. Follow-up: print copy-pasteable rule syntax if this becomes a real operator pain point.

## Troubleshooting quick refs

- `journalctl -u vnprox` — daemon logs (structured).
- `vnproxctl status` — local daemon, peer reachability, PVE API health, collector ages.
- `vnproxctl rollback-now <changeset-id>` — CLI escape hatch to trigger rollback when the UI is unreachable.
- UI unreachable after a bad change *you confirmed*: SSH in and restore the pre-snapshot: `vnproxctl snapshots list` / `vnproxctl snapshots restore <id>` (applies locally with ifreload, bypassing confirm — it *is* the recovery path).
- `vnproxctl backup` / `vnproxctl restore` — vnprox's own state (changesets, snapshots, audit, layouts). See "Backup and disaster recovery" above; `restore` refuses to run while the daemon is up.
- `vnproxctl support-bundle` — one redacted archive someone else can diagnose this install from without SSH. Works with the daemon down. `--dry-run` first if you want to see what it collects. See "Support bundles" above.

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
