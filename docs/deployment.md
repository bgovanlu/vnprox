# Deployment guide

This is the complete install/upgrade/configuration reference. If you're looking for a shorter,
reader-first path (including a plain statement of what installs today vs. what's built but not
yet reachable), start at [`install.md`](install.md) instead — it links back here for everything
below.

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

### Try it without a cluster first

```bash
vnproxd --demo
```

Runs the whole product against a synthetic three-node cluster built into the
binary, with no Proxmox VE endpoint and no outbound network. It needs no
root and no configuration — everything lands under
`$XDG_STATE_HOME/vnprox-demo`. Log in with `root` / `vnprox-mock` / realm
`pam`. See docs/features/demo-mode.md.

Adding `--public-demo` (which requires `--demo`) puts a read-only edge in
front of the whole daemon: every mutating route is refused with 403 before
the router sees it, there is no login screen — each visitor gets their own
session, minted server-side — and per-visitor request and state caps apply
so one visitor cannot degrade the instance for another. That is the shape a
hosted demo would run in. **There is no hosted instance**, and the gaps
between this flag and one are listed in docs/features/demo-mode.md.

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
3. Installs the `vnprox` .deb (from the apt repo it configures, or a bundled offline .deb with `--offline <file>`), or falls back to the signed binary tarball on a host with no `apt-get` (`--tarball`, `--dist-url <url>`). **Signature verification (T-2801) is not skippable and there is no `--insecure`** — see "Signatures and trust" below.
4. Optionally installs + enables `lldpd` on all nodes (`--with-lldp`, default yes).
5. Creates the read-only PVE API token `vnprox@pve!daemon` (privilege: auditor-level on `/`), stores it root-only.
6. Generates the cluster secret in `/etc/pve/priv/vnprox/` (first node only; pmxcfs replicates it). **Correction (T-608, hardware validation):** this is under `priv/` specifically — pmxcfs only auto-restricts files under `/etc/pve/priv/` to `0600` root-only; everywhere else under `/etc/pve` it silently coerces creation-time permissions to `0640 root:www-data` and rejects `chmod()` outright, confirmed against a real PVE 9.2.4 node.
7. Writes `/etc/vnprox/vnprox.toml`, generates the session key, enables + starts `vnprox.service`.
8. Repeats 3–7 on the remaining nodes (via SSH root, same mechanism `pvecm` setups already rely on), or prints per-node instructions if SSH between nodes is unavailable.
9. Prints the URL and a first-login checklist.

### Signatures and trust (T-2801)

Every artifact the installer *downloads* is verified before anything is
unpacked or installed:

- **The apt repository's signing key is pinned by fingerprint.** The
  installer carries `VNPROX_RELEASE_KEY_FPR` and refuses a fetched key whose
  fingerprint does not match it. Without that check, "apt verified the
  repository signature" says nothing — apt verifies against whatever key it
  was given, and the key came from the same host as the packages.
- **The binary tarball is verified against a detached signature**
  (`vnprox_<version>_<arch>.tar.gz.asc`) by the same trusted key, before
  `tar` is invoked. `latest.txt` is an unsigned pointer; the versions it can
  name are limited to ones whose archive carries a valid signature, so a
  tampered pointer can select a different *genuine* release (a rollback) but
  not an artifact of an attacker's choosing. Rollback protection needs a
  signed manifest and is not claimed here.
- **A missing signature is a refusal, not a warning.** "Could not check" and
  "checked and it was wrong" are the same thing from the point of view of
  the machine about to run the binary.

There is no flag, environment variable or fallback that installs an
unverified download. `--release-key <file>` supplies a *different* trust
anchor (for air-gapped installs, and for this repository's own tests, which
sign with an ephemeral key); it changes which key is trusted, never whether
a signature is checked.

`--offline <file>` is the one path that does not *require* a signature: it
installs a local package the operator already holds and chose, and nothing
about it crossed a network on this run. It is still verified when a
`<file>.asc` sits next to it — which is what a release download unpacked by
hand looks like — and warns loudly when there is none.

**Not verifiable here, and stated plainly:** there is no published vnprox
release and no production signing key, so `VNPROX_RELEASE_KEY_FPR` is still
a documented placeholder and every download path **fails closed** today with
a message saying so. Generating the key, publishing it, and replacing that
pinned line (it carries a `vnprox-release-key-fingerprint` marker so a
release job can substitute it mechanically) is the remaining step. Until
then use `--offline <file>` or `--release-key <file>`.

### Unprivileged / air-gapped install

```bash
bash install.sh --prefix ~/.local --release-key vnprox-release.asc
```

Installs the verified binaries under `<prefix>/bin` and stops: no systemd
unit, no PVE API token, no config, no root. Useful for trying
`vnproxd --demo` (docs/features/demo-mode.md) on a machine that is not a
Proxmox node.

### Idempotence

Running the installer twice leaves the same versions and one apt sources
entry. The tarball path compares the version already installed at the prefix
against the one it is about to install and reports "already installed"
rather than re-extracting; the apt path strips any duplicate vnprox entry
from other `sources.list` files before writing exactly one of its own.

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
# Origins allowed to <iframe> the read-only /embed/* views (T-2901), in
# addition to same-origin — e.g. a wiki or NOC dashboard. Each entry must be
# an origin (scheme://host[:port]); anything else refuses startup. Empty or
# absent = same-origin embedding only. Every non-embed route stays
# unframeable regardless.
# embed_frame_ancestors = ["https://wiki.example"]

[webhooks]
# T-2905: webhook deliveries reach public https targets only, by default.
# Each override warns at every startup.
# allow_private_targets = false   # admit loopback/RFC1918/link-local targets
# allow_insecure_targets = false  # admit plain-http targets

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
snapshot_keep_days = 90    # committed-changeset snapshots are pinned a minimum of snapshot_pin_days regardless;
                           # a snapshot backing an applying/awaiting_confirm changeset is NEVER pruned, regardless
                           # of age (T-1905) — see docs/data-model.md §13
snapshot_pin_days = 7
audit_keep_days = 730      # T-1905: audit_log is a compliance artifact — see docs/data-model.md §13 for the argument
store_warn_bytes = 4294967296   # T-1905: 4 GiB — store_near_capacity finding threshold (store.DB.SizeBytes(),
                                 # main file + WAL/SHM); see "Sizing and retention" below
# snapshot_schedule_interval = "1h"  # T-2401: automatic config snapshots. OFF by default (0/absent). Covers changes
                                     # vnprox did NOT make — an ssh + vi + `ifreload -a`. Captures are de-duplicated
                                     # by content, so an idle cluster stores one row however often the timer fires.
# snapshot_schedule_keep = 48        # count-based ceiling for the above, oldest pruned first. Scoped to the
                                     # `scheduled` kind in SQL: never deletes a changeset's rollback point or a
                                     # manual snapshot.

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

# [changesets]                     # T-2003: change review — approvals, comments, side-by-side diff.
#                                   # Every key below defaults to the value shown; an omitted section
#                                   # leaves apply behavior byte-identical to every pre-T-2003 deployment.
# approval_required = false        # when true, POST .../apply refuses (422 approval_required) a
#                                   # changeset with no stored "approved" review decision — see
#                                   # docs/security.md's "Change review approval" note
# allow_self_approval = true       # false forbids a changeset's own author from approving it
# approvers = []                   # empty = anyone with netWrite may record a decision;
#                                   # e.g. ["alice@pve", "bob@pve"] to name an explicit reviewer list
# policy_file = ""                 # T-2601: a declarative policy-as-code document installed into the
#                                   # cluster's policy set at startup (see docs/api.md's "Policy set"
#                                   # section and `vnproxctl policy examples`). Empty = no policy file;
#                                   # the cluster keeps whatever rule set is already installed, which
#                                   # for a fresh deployment is none, and nothing is refused that was
#                                   # not refused before. A file the daemon CANNOT PARSE IS FATAL at
#                                   # startup: vnproxd must never come up quietly enforcing a policy it
#                                   # could not read. Validate before deploying with
#                                   # `vnproxctl policy lint --policy=<path>`.
# auto_rollback_on_error = false   # T-2603: the CLUSTER DEFAULT for finding-triggered auto-rollback.
#                                   # When true, a changeset applied without an explicit
#                                   # `autoRollbackOnError` on its apply body is rolled back inside its
#                                   # commit-confirm window if a NEW `error` finding appears on an entity
#                                   # the changeset touched (its T-2404 `Impact` set). Findings already
#                                   # firing before the apply never trigger, and a finding outside the
#                                   # Impact set never does either, however severe. Off by default: a
#                                   # deployment that does not opt in behaves exactly as before, and any
#                                   # single apply can still ask for the guard on its own.
#
# [[changesets.protected_class]]   # T-2604: the ENFORCED TWO-PERSON RULE. Each entry declares one
# class = "fw.*"                    # class of change that may not be applied until N DISTINCT
# approvals = 2                     # principals have approved it (POST .../review/approve). `class` is
#                                   # an op-type glob ("fw.*", "sdn.*"), the reserved "mgmtPath"
#                                   # (anything touching a node's resolved management path), or
#                                   # "tag:<tag>" naming a T-2601 policy rule's tag. `approvals`
#                                   # defaults to 2 and is never lower.
#                                   #
#                                   # NO ENTRIES = THE RULE IS OFF, which is the default: no changeset
#                                   # is ever in a protected class and apply behaves exactly as it did
#                                   # before. A class name no op type can match is FATAL at startup —
#                                   # a deployment must never come up believing it has a gate it does
#                                   # not have.
#                                   #
#                                   # Enforcement is server-side at apply (422 two_person_required),
#                                   # never in the UI. Two API tokens belonging to one person are ONE
#                                   # approver. Emergency override: POST .../break-glass {reason},
#                                   # which requires a written reason, is audited `change.breakglass`,
#                                   # and raises an error finding that cannot be acknowledged for 24
#                                   # hours.

# [gitsync]                        # T-2701: a git repository as the source of INTENT for the
#                                   # declarative spec (docs/api.md's "Git spec sync"). Proxmox
#                                   # stays the source of TRUTH: on divergence vnprox opens a
#                                   # DRAFT changeset for a human and stops. It never applies,
#                                   # never pushes, and never decides the file wins.
# enabled = false                  # OFF BY DEFAULT. With this false (or no section at all)
#                                   # nothing is fetched, no endpoint is contacted, and no
#                                   # credential file is read.
# url = "https://github.com/org/infra"      # https only (http allowed for loopback fixtures);
#                                   # a URL that embeds credentials is REFUSED at startup —
#                                   # use token_file
# provider = "github"              # github | gitlab | raw. Omitted: inferred for github.com and
#                                   # gitlab.com, and a startup error for any other host —
#                                   # guessing an API shape is how a sync reads the wrong thing
# ref = "main"                     # branch, tag or sha
# path = "network/cluster.yaml"    # the spec document's path within the repository
# poll_interval = "5m"             # default 5m; a spec repo is edited by humans through review
# token_file = "/etc/vnprox/keys/gitsync.token"   # root:root 0600, the same on-disk-secret
#                                   # convention [oidc] client_secret_file and [pve] token_file
#                                   # use. Never inlined here, never placed in a URL, never
#                                   # logged. Omit entirely for a public repository.
# require_signed_commits = false   # true refuses any commit whose signature this daemon cannot
#                                   # verify LOCALLY against allowed_signers_file, and raises a
#                                   # finding. Fails closed in every direction: unsigned,
#                                   # OpenPGP-signed (not verifiable without a new dependency),
#                                   # unsupported algorithm, unlisted signer, and a host that
#                                   # cannot supply the signed commit object at all (GitLab, raw)
#                                   # are all refusals. vnprox verifies git's SSH-format
#                                   # signatures (`git config gpg.format ssh`).
# allowed_signers_file = "/etc/vnprox/gitsync-allowed-signers"   # an OpenSSH allowed-signers (or
#                                   # authorized_keys) file. REQUIRED when require_signed_commits
#                                   # is true — a daemon must never come up enforcing a signature
#                                   # policy whose trust anchors it could not read.
# push_token_file = "/etc/vnprox/keys/gitsync-push.token"   # T-2702, and OFF unless set: a
#                                   # separate, WRITE-scoped credential used only by
#                                   # POST /changesets/{id}/propose to push a branch and open a
#                                   # pull request. Deliberately not the same key as token_file:
#                                   # syncing needs only a read, and a deployment that never asked
#                                   # to propose anything never reads a write credential off disk.
#                                   # Unset: proposing answers 501 and nothing is contacted.
#                                   # Requires provider github or gitlab (a raw file host has no
#                                   # branch or pull-request API).

# [telemetry]                      # T-2503: opt-in compatibility reporting. OFF by default, and the
#                                   # shipped vnprox.toml has this whole section commented out.
# enabled = false                  # With this false (or no section at all) no payload is built, the
#                                   # store is not read for an install-id, and no endpoint is
#                                   # contacted by anything.
# endpoint = "https://collector.example/vnprox"   # REQUIRED when enabled, https only. vnprox ships
#                                   # NO default: there is no vnprox telemetry service, so opting in
#                                   # means naming the collector yourself. enabled = true with no
#                                   # endpoint is a FATAL config error, not a quiet no-op — an
#                                   # operator who opted in must never be silently sending nothing.
#                                   #
#                                   # What would be sent: a `vnproxctl verify` run reduced to check
#                                   # ids and verdicts, the vnprox/PVE/kernel versions, the NICs' PCI
#                                   # vendor:device ids, and a node COUNT. Never a hostname, address,
#                                   # MAC, guest name or cluster name — docs/security.md
#                                   # ("Compatibility telemetry") lists every field, and
#                                   # `vnproxctl telemetry preview --report <file>` prints the exact
#                                   # bytes. The install-id correlator is a random local ULID,
#                                   # thrown away by `vnproxctl telemetry reset-id`.
```

### Git spec sync operating notes (T-2701)

- **A remote that is down is never a startup failure.** An unreachable or refusing remote degrades
  to a `gitsync_unreachable` finding and a retry on the next poll; the daemon starts, serves, and
  every other subsystem is untouched. The only `[gitsync]` conditions that are fatal at startup are
  ones that make the remote *undescribable* — a malformed URL, an unguessable provider, an
  unreadable allowed-signers file — because a daemon that came up looking configured while
  reconciling nothing is the worst of the available states.
- **One open sync changeset at a time.** A second detected divergence updates the existing draft
  rather than accumulating drafts. Check it with `vnproxctl gitsync status`, apply it like any other
  changeset.
- **Proposing (T-2702) is a separate opt-in with a separate credential.** Setting `push_token_file`
  is what turns on `POST /changesets/{id}/propose`; syncing keeps working with a read-only
  `token_file` and never pushes. vnprox opens a pull request and stops — it does not merge, gate or
  poll one, and whatever happens to the request comes back through the ordinary sync above. A
  proposal that could not be completed removes the branch it created, so a failed call leaves no
  orphan branch behind.
- **Nothing is pushed on this path.** The sync is a read-only fetch of one file at one ref, over
  plain HTTPS — there is no `git` binary dependency and no git library in the .deb.

## Upgrade

```bash
apt update && apt install vnprox        # per node; any order
```

- DB migrations run automatically on daemon start; forward-only.
- Mixed versions during rolling upgrade: daemons serve, but changeset coordination involving an incompatible peer is refused with an upgrade prompt (architecture §5). Upgrade all nodes promptly.
- Config file changes are documented in release notes; unknown keys are warnings, not fatals.

### Supported upgrade path

**Every schema version vnprox has ever shipped (1 through the current latest, 34) can upgrade
directly to current in a single `apt install vnprox` — no intermediate hop through an in-between
release is ever required.** Schema version 1 (`0001_init.sql`) is the very first release's schema;
there is no vnprox install older than that, so "how far back is upgrading-directly supported"
has one answer: **all the way**, for every install that has ever run a real vnprox build.
`internal/store`'s `TestMigrate_FromEachPriorSchemaVersion` (T-1807) proves this directly rather
than by induction over intermediate hops: it freezes a database at **each** of schema versions
0 (a brand-new file, i.e. a fresh install) through 33, seeds every one of them with representative
rows in that version's own on-disk shape, migrates straight to 34, and asserts — per table, not
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
  `0032_cluster_wg_tunnel` (v3.0.2, tunnel-aware federation reachability),
  `0033_changeset_revert_ticket` (v3.0.3, sealed apply-time revert ticket for unattended
  `fw.*`/`sdn.apply` rollback), and `0034_changeset_review` (v3.3, T-2003: per-op/changeset review
  comments and the review-approval gate) landing within the v3.0 line itself; they all run automatically on
  first daemon start at or after the release that introduced them and touch **only** new app-owned
  tables/columns — no existing row from an earlier schema is ever rewritten. This is pinned by
  `internal/store`'s `TestMigrate_FromEachPriorSchemaVersion` (freezes a DB at **every** prior
  schema version 0 through latest-1, migrates each to the current version — **34** as of this
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

## Sizing and retention

`/var/lib/vnprox/vnprox.db` accrues data with no natural ceiling of its own — audit rows, snapshots,
flow/latency/WAN samples, capacity aggregates, and `.pcap` captures all grow unless something bounds
them. `vnproxd` bounds every one of them itself (a "no operator action required" default), and warns
before the disk actually fills. Full per-class defaults and the argument for each are in
`docs/data-model.md` §13 ("Retention, rotation, and compaction"); this section is the operator-facing
summary of what to expect and what to tune.

**Defaults, at a glance:**

| Class | Kept for | Tunable |
|---|---|---|
| Audit log | 2 years | `[retention] audit_keep_days` |
| Rollback snapshots | 90 days (7-day floor for a committed changeset's manual-rollback window; **never** pruned while a changeset is still `applying`/`awaiting_confirm`, regardless of age) | `[retention] snapshot_keep_days` / `snapshot_pin_days` |
| Automatic config snapshots (T-2401) | **off by default**; when enabled, the newest 48 captures | `[retention] snapshot_schedule_interval` / `snapshot_schedule_keep` |
| Flow / latency / WAN samples | 60 minutes or a hard row cap, whichever is smaller | `[flows]`/`[latmesh]`/`[wan] retention_minutes`/`max_rows` |
| Capacity forecasting data | ~13 months (a downsampled daily rollup, not raw samples) | `[capacity] aggregate_retention_days` |
| Packet captures (`.pcap`) | 6 hours | `[capture] retention_hours` |

**How much disk vnprox itself needs:** for a typical single-cluster deployment with default
retention, the store (`vnprox.db` plus its `-wal`/`-shm` sidecars) is expected to stay in the low
hundreds of megabytes to low single-digit gigabytes — dominated by snapshot content (each apply's
pre/post file captures, content-addressed so identical content across snapshots is stored once) and,
if flow ingestion is enabled, the flow-sample ring at its row cap. Packet captures live as separate
`.pcap` files under `[capture] root`, purged independently of the store and not counted in the
store's own footprint — budget for these separately if capture is used routinely (a single busy
session can be tens of MB before it hits its own caps).

**The `store_near_capacity` finding** (`internal/findings`, source `store`) warns in the findings
stream once the store's own on-disk size (main file + WAL/SHM, the same figure `GET /metrics`'s
`vnprox_store_size_bytes` reports) reaches `[retention] store_warn_bytes` (default 4 GiB). This is a
warning about vnprox's own footprint specifically, not a general disk-space check — an operator
concerned about the root filesystem as a whole should also monitor it independently (e.g. node
exporter's `node_filesystem_avail_bytes`), since vnprox's own store is rarely the only thing writing
to `/`.

**Compaction:** pruning stops the store from growing further; a separate background compaction pass
(`internal/store`'s `EnsureIncrementalVacuum`/`Compact`, SQLite's incremental auto-vacuum) reclaims
the space pruning frees, without blocking reads. On first startup after upgrading to a version that
includes this, an **existing** store pays a one-time cost proportional to its current on-disk size (a
single `VACUUM` to enable incremental auto-vacuum, logged at `INFO` with how long it took) — a fresh
or already-converted store sees no delay. If your store is unusually large (tens of GB — well outside
the sizing expectations above, and worth investigating on its own), consider timing an upgrade for a
maintenance window rather than an unattended `apt upgrade`.

**If you need to tune retention down** (a smaller root partition, tighter compliance minimums that
happen to be shorter, or the reverse — a longer audit requirement): edit `[retention]`/`[flows]`/
`[latmesh]`/`[wan]`/`[capacity]`/`[capture]` in `vnprox.toml` and restart. There is no "0 = keep
forever" value for any of these — an unbounded table is exactly the failure mode this exists to
prevent, so "keep longer" always means configuring a larger, still-finite number.
## Support bundles

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

## `vnproxctl doctor` — self-check (T-1904)

Start here when something is wrong and you do not yet know what. `doctor` runs ten checks and, for
anything that is not healthy, names the file, port, privilege, or command involved.

```
vnproxctl doctor                        # human-readable, worst first
vnproxctl doctor -o json                # schema-stable, for CI and support bundles
vnproxctl doctor --config /path/to.toml # point at a config before it is installed
vnproxctl doctor --live                 # T-2406: also ask the running daemon (needs --token/VNPROX_TOKEN)
```

It is **read-only** — safe to run as root, mid-incident, against a live daemon — and it works with
the daemon **down** and before first setup.

| Check | What it catches |
|---|---|
| `config` | Missing or unparseable `vnprox.toml`; a listen address that is not `host:port` |
| `key_files` | Session key or PVE token absent, or readable beyond its owner |
| `pmxcfs` | `/etc/pve` missing — usually `pve-cluster` is not running, and nothing else will work |
| `schema_version` | Store written by a **newer** vnprox (fail — downgrade is unsupported), or behind (warn — the daemon migrates on start) |
| `disk_headroom` | Snapshot/capture growth heading toward a full filesystem on a hypervisor |
| `port_conflict` | Something else on the listen port — including Proxmox Backup Server's own 8007, which it names |
| `pve_reachable` | pveproxy down, wrong API URL, expired token |
| `pve_privileges` | The token is missing a privilege vnprox uses, with what each one unlocks |
| `peer_secret` | Nodes disagree on the cluster secret — why peer calls 401 while every node looks healthy alone |
| `clock_skew` | Drift against PVE past half (warn) or all (fail) of the ±30s peer replay window |

**Exit code:** non-zero if any check **fails**. Warnings do not gate — a two-node cluster with one
node down for maintenance should not fail a script.

**`skip` is not `pass`.** A check that could not run says so, with the reason. Before first setup
most of the PVE checks skip, which is correct and expected — "we did not look" and "we looked and
it was fine" are different facts.

**`--live` (T-2406).** Four checks need a credential only the running daemon holds. `--live` asks
it (`GET /doctor/live`, bearer token, `audit` capability) and merges the verdicts over the local
skips. Without the flag, behaviour is unchanged.

| Check | With `--live` |
|---|---|
| `pve_reachable` | **Answered.** The daemon makes a real authenticated call, so a pass means "reachable *and* the token authenticates" — not merely that a port is open |
| `pve_privileges` | **Answered**, against the same privilege list `internal/auth` uses, so the two cannot drift |
| `clock_skew` | Still skips — see below |
| `peer_secret` | Still skips — see below |

**If the daemon cannot be reached, `--live` reports `skip`, never `fail.`** A stopped service does
not mean PVE is unreachable or that the token is wrong, and a `fail` here would send you to look at
the wrong thing. The reason names the daemon, and is also written to stderr so it is visible when
the report itself is JSON on stdout.

**Known limitations, stated rather than hidden:**

- `clock_skew` needs PVE's own clock, and neither the `internal/pve` client nor `internal/pvemock`
  exposes a server-time surface today. `--live` therefore still skips it. `T-2406-followup-01`.
- `peer_secret` compares the cluster secret **across nodes**, and no peer-API route reports another
  node's digest. A probe returning only the local digest would be **worse** than skipping: the
  check reads a one-entry map as "single-node cluster; nothing to agree with" and would report
  **pass** on a five-node cluster whose secrets disagree. `T-2406-followup-02`.
- With `--live` on a single node, `doctor` therefore answers **8 of 10** checks rather than 6.
- `capture_root` and the PVE API URL are read from the packaged defaults rather than the config,
  because they are outside the daemon-independent config subset `doctor` loads. If you have moved
  either, the disk check reports on the default location.
- `install.sh` runs `doctor` as a post-install verification and prints its report, but does **not**
  abort on failure — aborting after the package and cluster rollout have run would leave a
  half-configured cluster. Making it a hard gate is `T-1904-followup-01`.

## `vnproxctl verify` — hardware validation, executed (T-2501)

`doctor` asks whether this daemon is healthy. `verify` asks a different question: **does this
cluster actually behave the way vnprox claims it does?**

It exists because of the number in [`status-matrix.md`](status-matrix.md) §5.3. Almost everything
vnprox does has only ever been tested against `internal/pvemock`, because validating a behaviour on
real hardware meant a human reading a checklist line, doing the thing, and writing down what
happened. That does not scale, does not repeat, and — most importantly — cannot be handed to a user
who has a cluster and would like to help. `verify` is that checklist as a command.

```
vnproxctl verify --list                       # what the suite will ask of your hardware, before it asks
vnproxctl verify --suite=hardware             # read-only; safe on a production cluster
vnproxctl verify --suite=multinode            # needs 2+ nodes; skips loudly otherwise
vnproxctl verify --suite=destructive --i-understand
vnproxctl verify --only=lldp.neighbors_match_pve_interfaces
vnproxctl verify --suite=hardware --out report.json   # signed artifact to attach to an issue
```

**If you have a Proxmox cluster and want to help this project, this is the command to run.**
`--suite=hardware` changes nothing, and the report it prints (or writes with `--out`) is the single
most useful thing anyone outside the project can contribute.

### What a result means

| Status | Meaning |
|---|---|
| `PASS` | The behaviour was observed on your hardware, and it was what we claim. The evidence is in the report. |
| `FAIL` | The behaviour was observed, and it was not what we claim. This is a bug in vnprox — please send the report. |
| `SKIP` | The behaviour could **not** be observed here, with the reason and the hardware it would take. **Never** counted as a pass. |

**A run in which everything skipped exits non-zero and says `0 passed`.** "We did not look" and "we
looked and it was fine" are different facts, and a suite that conflated them would make the
hardware-validated figure fiction rather than merely small.

**Every `PASS` and every `FAIL` carries its evidence** — the API response, the command output, the
file contents the verdict rests on. This is enforced structurally (`verify.Report.Validate`), not
by convention: a verdict with no evidence is a malformed report and the command refuses to print
it. The point is that you can read the working and disagree with the verdict.

### It refuses to run against a mock

```
$ vnproxctl verify --pve-url https://127.0.0.1:8899
vnproxctl verify: refusing to run the hardware validation suite against https://127.0.0.1:8899:
the endpoint identifies itself as internal/pvemock (X-Pvemock: server).
A green run against a mock is indistinguishable from a green run against real Proxmox, and would
raise the hardware-validated count in docs/status-matrix.md without validating any hardware.
Point --pve-url at a real cluster, or pass --allow-mock to run against the mock anyway — the report
will be stamped as a mock run and is not hardware evidence.
```

`--allow-mock` exists for development. A report produced with it carries `environment.mock: true`
and the signal that identified the endpoint, so the stamp travels with the document rather than
living only in the terminal of whoever ran it. A cassette **replay** server counts as a mock too:
the traffic was recorded from real PVE, but replaying it is not exercising a live cluster.

### The suites

| Suite | Needs | Changes anything? |
|---|---|---|
| `hardware` | one real PVE node | **No.** Read-only throughout. |
| `multinode` | two or more online nodes (or, for federation, two clusters) | No. |
| `destructive` | `--i-understand`, and a cluster you can disrupt | **Yes** — it interrupts applies, lets commit-confirm windows expire, provisions VFs, and stops the active daemon. |

The destructive interlock is structural: without `--i-understand` the command does not construct a
write client at all, so those checks find no way to mutate and skip naming the flag. It is not a
rule the checks are trusted to follow.

### The report artifact

`--out` writes a signed, timestamped JSON document naming the vnprox version, PVE version, kernel
and NIC models the run observed, plus every result and its evidence. It is compact single-line JSON
on purpose — that is its canonical form, and the parser rejects anything that is not byte-identical
to it, so re-indenting the file (`jq . report.json`) invalidates it exactly as editing a value
would.

The signature is Ed25519 with the public key embedded, which means **integrity, not provenance**: a
verified signature says "these are the bytes that were signed", and says nothing about who signed
them unless you already trust the fingerprint. `--sign-key` points at a key whose fingerprint a
reader knows; without it the run uses an ephemeral key, which still detects any later edit.

### Exit codes

`0` only when at least one check passed and none failed. `1` on any failure **or** on a run that
validated nothing. `2` for a usage problem, including the mock refusal and an unknown `--only` id.
`5` when the PVE endpoint could not be reached at all.

### Before you send a report to a stranger

**Read it first.** Evidence is verbatim by design — that is what makes a verdict checkable — so a
report carries real API responses and real command output from your cluster: node names, IP
addresses, bridge and VNet names, LLDP neighbour identities, certificate subjects.

The one check that deliberately handles a live credential (`supportbundle.contains_no_secret`, which
must read your session key in order to search for it) redacts that key from its own evidence,
including on the branch where the bundle leaked it — a report written to prove a secret did not
escape must not be the thing that lets it escape. That is enforced by a test.

**No general redaction pass runs over the other checks' evidence.** vnprox's own routes are built
not to return secrets (`GET /config` excludes every secret-bearing value; `GET /wireguard/tunnels`
never returns a private key — both are checked by this suite), so the exposure is inventory
metadata rather than credentials. But it is your cluster's metadata, and it is your call. Use
`vnproxctl support-bundle` instead when you need a redaction guarantee; that command is built for
it and this one is not.

### Known limitations, stated rather than hidden

- **Host reads are local-only.** `verify` reads `/proc`, `/sys` and `/etc/pve` on the machine it
  runs on. Asked for another node's host state it reports that limitation, which the affected
  checks turn into a skip — never a silent read of the wrong node. Run the suite on each node.
- **The `HW` column is not yet regenerated automatically.** `verify.HWFromReport` computes what a
  row's mark should be from a report, and nothing yet writes it back into
  [`status-matrix.md`](status-matrix.md). Until it does, the column is maintained by hand.
- **Many checks skip on a healthy, lightly-used cluster** — no drift to report, no capture ever
  run, no external IPAM configured. Each says what to do to make itself run. That is the honest
  state: a suite that reported `pass` for "we looked and there was nothing to look at" would be
  back to the problem this command exists to solve.

## `vnproxctl telemetry` — opt-in compatibility reporting (T-2503)

One cluster validated by us is an anecdote. A hundred clusters reporting which `verify` checks pass
on which PVE version, kernel and NIC is a compatibility matrix. This command family is how you help
with that, if you want to — **it is off, it has no endpoint, and it sends nothing until you
configure both** (see `[telemetry]` in the configuration reference above).

```bash
vnproxctl verify --suite=hardware --out report.json   # produce a report first
vnproxctl telemetry preview --report report.json      # print the exact bytes that would be sent
vnproxctl telemetry status                            # on/off, endpoint, install-id
vnproxctl telemetry send --report report.json         # submit one report (requires the opt-in)
vnproxctl telemetry reset-id                          # throw away the correlator
```

- **`preview` is the point.** It prints the same buffer `send` posts — not an equivalent rendering;
  the payload is marshalled once and both paths read that one allocation, which a test asserts by
  capturing both and comparing. What you read is what leaves.
- **What is collected** is listed field by field in [`security.md`](security.md) under
  "Compatibility telemetry", and that list is compared against the code on every build. Never a
  hostname, address, MAC, guest name or cluster name; a payload containing one is refused before it
  is sent, and the refusal names the rule and the value.
- **The install-id** is a random ULID generated locally on first preview or send, and is the only
  correlator. `reset-id` replaces it; the old one is deleted and recorded nowhere.
- **`verify` may send in the background** once you have opted in, and never waits for it: a
  collector that hangs cannot delay or fail a verify run. That also means a send still in flight
  when the command exits is abandoned — `telemetry send` is the path that waits and tells you what
  happened.
- **A run against a mock PVE endpoint is never sent**, whatever the config says. It is not hardware
  evidence, and a matrix polluted with mock runs would look larger than it is.

## Troubleshooting quick refs

- `vnproxctl doctor` — **start here.** Ten checks, each failure naming its own fix. Read-only; works daemon-down.
- `vnproxctl verify --suite=hardware` — does this cluster behave the way vnprox claims? Read-only, evidence-carrying, and the most useful thing you can send us if you have hardware we do not.
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
