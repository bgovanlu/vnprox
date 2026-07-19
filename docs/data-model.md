# Data model

Two distinct models exist and must not be conflated:

1. The **inventory model** — an in-memory, typed graph of live network state, rebuilt from collectors. Never persisted as truth.
2. The **app store** — SQLite tables for vnprox-owned data (changesets, snapshots, sessions, audit, layouts, metrics).

## 1. Inventory model

### Entity kinds

```mermaid
erDiagram
    ClusterNode ||--o{ PhysNic : has
    ClusterNode ||--o{ Bond : has
    ClusterNode ||--o{ Bridge : has
    ClusterNode ||--o{ VlanIface : has
    Bond }o--o{ PhysNic : enslaves
    Bridge }o--o{ Port : "has ports"
    Port }o--|| PhysNic : "may be"
    Port }o--|| Bond : "may be"
    Port }o--|| VlanIface : "may be"
    SdnZone ||--o{ SdnVnet : contains
    SdnVnet ||--o{ SdnSubnet : contains
    SdnVnet }o--o{ Bridge : "realized on"
    Guest ||--o{ GuestNic : has
    GuestNic }o--|| Bridge : "attached to"
    GuestNic }o--|| SdnVnet : "or attached to"
    PhysNic }o--o| LldpNeighbor : "sees"
    SdnSubnet ||--o{ IpAllocation : allocates
```

### Core types (Go, `internal/inventory`)

Every entity embeds `Ref`:

```go
type Ref struct {
    Kind Kind   // "physnic","bond","bridge","vlan","ovs-bridge","ovs-bond",
                // "sdn-zone","sdn-vnet","sdn-subnet","guest","guest-nic",
                // "lldp-neighbor","fw-ruleset","node",
                // "wg-tunnel","wg-peer" (T-1401: app-owned WireGuard op
                // targets, not live-polled inventory entities),
                // "nat-rule","static-route" (T-1403: Edge & NAT op targets)
                // "vf" (T-1506: an SR-IOV virtual function's own identity —
                // "<pfName>/vf<index>" — not a merge/provenance-tracked
                // graph entity itself, see PhysNic.sriovVFs below)
                // "ceph-osd" (T-1503: a Ceph OSD's own identity — "osd<id>"
                // scoped to its hosting node — same "has a Ref, never
                // itself graph-tracked" pattern as "vf" above; internal/ceph
                // computes its bond attribution live against the graph
                // rather than ingesting OSDs as a collector-owned entity)
    Node string // "" for cluster-scoped entities (SDN, cluster firewall)
    ID   string // stable within (Kind,Node), e.g. "vmbr0", "eno1", "zone1/vnet1"
}
```

Selected entities (full field lists are the implementing task's responsibility; these fields are the contract other packages rely on):

| Type | Key fields |
|---|---|
| `PhysNic` | name, mac, driver, speedMbps, duplex, linkUp, mtu, pciAddr, sriovVFs []VirtualFunction (T-1506, given full shape below — host-netlink-only, excluded from the merge/provenance/delta machinery exactly like Bridge.fdb below, since it is one contributing source's live hardware/driver state, not a config value multiple sources could disagree on), pending |
| `VirtualFunction` (T-1506) | ref (Kind `vf`, "<pfName>/vf<index>"), pf Ref (the owning PhysNic), macAddr, vlan, spoofCheck bool, trust bool, pciAddr, assignedGuest Ref (resolved live against guest `hostpci` config by `internal/topology.ResolveVFAssignments` — never baked into the collector-owned field, "never guessed": the zero Ref when no guest's hostpci config resolves to this VF's pciAddr) |
| `Bond` | name, mode (802.3ad, active-backup, ...), slaves []string, lacpRate, xmitHashPolicy, miiStatus, activeSlave, mtu, pending, slaveDetail []BondSlave (per-slave runtime status, host-netlink-only) |
| `BondSlave` | name, miiStatus, permHWAddr, linkFailureCount, active bool — plus (T-804) LACP actor/partner detail decoded from `/proc/net/bonding/<name>`'s "details actor/partner lacp pdu" block, opportunistically refined by netlink AD-info attributes where the kernel exposes them (`internal/host/bonding.go`, `internal/host/netlink_linux.go`): actorSystemID, actorSystemPriority, actorKey, actorSynchronized/actorCollecting/actorDistributing bool (the decoded 802.3ad port-state bits — "bond is up" vs. "bond is negotiated correctly"), partnerSystemID, partnerSystemPriority, partnerKey, lacpDetailSet bool (false — every field above at its zero value — for a bond not running 802.3ad, or an older kernel/driver that never emits the block; best-effort, not a hard requirement) |
| `Bridge` | name, kind (linux|ovs), ports []Ref, vlanAware bool, vids []VidRange, stp, mtu, addresses []CIDR, gateway, comments, pending, fdb []FDBEntry (T-306, added retroactively per docs/development.md's definition-of-done #4: `{mac, port, vlan, master bool, permanent bool, stale bool}`, host-netlink-only — excluded from the merge/provenance/delta machinery every other field here goes through, since FDB churns on every poll and has only one contributing source; see `internal/topology.FDB`/`FDBSearch` for the cluster-wide, ownership-labeled view built from it) |
| `VlanIface` | name, parent Ref, vid, addresses, mtu, pending, virt ("" \| "ovs", T-407: distinguishes a plain 802.1q VLAN sub-interface from an OVS Int Port — OVSIntPort has no dedicated `Kind` of its own the way OVSBridge/OVSBond do, so `virt` carries the distinction, mirroring `Bridge.virt`'s exact shape/precedence), trunks []VidRange (T-407, OVS-only: additional trunked VLAN ranges alongside `vid`'s single access tag, from ovs-vsctl's Port `trunks` column / the interfaces(5) `ovs_options trunks=...` token) |
| `SdnZone` | id, type (simple|vlan|qinq|vxlan|evpn), bridge, mtu, nodes []string, exitNodes []string (evpn), peers []string (vxlan/evpn underlay peer addresses), controller, vrfVxlan, ipam |
| `SdnVnet` | id, zone, tag, alias, vlanAware |
| `SdnSubnet` | id (cidr), vnet, gateway, snat bool, dhcpRanges, dnsZonePrefix |
| `Guest` | vmid, name, type (qemu|lxc), node, status, hostPci map[string]string (T-1506: raw `hostpciN` PCI-passthrough config verbatim from PVE guest config, e.g. `{"hostpci0": "0000:01:00.1,pcie=1"}` — read by `internal/topology.ResolveVFAssignments` to correlate against VF inventory; a resource-mapping form like `"mapping=<name>"` is left uncorrelated, never guessed) |
| `GuestNic` | guest Ref, key ("net0"), bridgeOrVnet Ref, vid, model, mac, firewall bool, rateMbps, linkDown bool |
| `LldpNeighbor` | localNic Ref, chassisName, chassisId, portId, portDescr, mgmtIP, vlan info, ttl |
| `FwRuleset` | scope (cluster|node|guest), ref, enabled, defaultIn/Out policy, rules []FwRule, aliases []FwAlias, ipsets []FwIPSet, groups []FwGroup |
| `FwRule` | pos, enabled, direction, action, proto, source, dest, sport, dport, iface, macro, log, comment |
| `FwAlias` (T-501) | name, cidr, comment — scoped to the FwRuleset that defines it; a cluster-scope alias is visible from every scope, node/guest-scope aliases only within their own ruleset (real pve-firewall's visibility rule) |
| `FwIPSet` (T-501) | name, comment, entries []FwIPSetEntry — same scope-visibility rule as FwAlias |
| `FwIPSetEntry` (T-501) | cidr, comment, noMatch |
| `FwGroup` (T-501) | name, comment, rules []FwRule — a reusable, cluster-scope-only security group (real PVE has no node/guest-scope groups); only ever populated on the cluster-scope FwRuleset, referenced from any rule anywhere via a rule whose direction is "group" and whose action names the group |

`pending` (added by T-305, `PhysNic`/`Bond`/`Bridge`/`VlanIface` only) mirrors PVE's own `pending` marker on `GET /nodes/{node}/network` (`""`\|`"new"`\|`"changed"`\|`"deleted"`) — a staged `interfaces.new` edit that was never applied via reload. It is exclusively `pve-network`-sourced (no other collector observes PVE's staging concept), which is what T-305's `pending_interfaces` drift check reads.

T-401 adds the identical `pending` field (same `""`\|`"new"`\|`"changed"`\|`"deleted"` marker, `pve-sdn`-sourced) to `SdnZone`/`SdnVnet`/`SdnSubnet`, for the same reason: PVE stages SDN edits until `PUT /cluster/sdn` applies them. It is structural/badge-only on these entities (topology map painting, `GET /sdn` tree rows) — the authoritative, field-level staged-vs-running diff `GET /sdn` renders (docs/api.md's `PendingDiff`) is computed live against PVE by `internal/sdn.Service`, not read off this field, since a diff view must never be stale relative to what an apply would actually do.

### Graph and deltas

`inventory.Graph` holds all entities plus typed edges (`enslaved-by`, `port-of`, `tagged-on`, `realizes`, `attached-to`, `lldp-adjacent`). It exposes:

- `Snapshot() Graph` — immutable copy for readers,
- `ApplyPoll(source, entities)` — collector ingestion, returns `Delta{Added, Updated, Removed []Ref}`,
- Deltas fan out to the WS hub as `topology.delta`.

## 2. App store (SQLite)

Schema managed by embedded migrations (`internal/store/migrations/NNNN_*.sql`). WAL mode, foreign keys on.

```sql
-- 0001_init.sql (shape contract; implementing task owns exact DDL)
CREATE TABLE sessions (
  id TEXT PRIMARY KEY, username TEXT NOT NULL, realm TEXT NOT NULL,
  pve_ticket_enc BLOB NOT NULL, csrf_token_enc BLOB NOT NULL,
  caps_json TEXT NOT NULL, created_at INTEGER, expires_at INTEGER
);

CREATE TABLE changesets (
  id TEXT PRIMARY KEY,               -- ULID
  title TEXT, author TEXT NOT NULL,
  status TEXT NOT NULL,              -- draft|validated|applying|awaiting_confirm|
                                     -- committed|rolled_back|failed|discarded
  ops_json TEXT NOT NULL,            -- ordered []Op
  findings_json TEXT,                -- validation results
  plan_json TEXT,                    -- ordered apply steps (rendered pre-apply)
  apply_log_json TEXT,               -- per-step outcomes
  confirm_deadline INTEGER,          -- unix; NULL unless awaiting_confirm
  created_at INTEGER, updated_at INTEGER
);

CREATE TABLE snapshots (
  id TEXT PRIMARY KEY, changeset_id TEXT REFERENCES changesets(id),
  taken_at INTEGER NOT NULL, kind TEXT NOT NULL,      -- pre|post|manual|scheduled
  files_json TEXT NOT NULL           -- [{node,path,sha256,content_zstd}]
);

CREATE TABLE audit_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT, at INTEGER NOT NULL,
  username TEXT NOT NULL, action TEXT NOT NULL, target TEXT,
  changeset_id TEXT, result TEXT NOT NULL, detail_json TEXT
);

CREATE TABLE layouts (
  username TEXT NOT NULL, name TEXT NOT NULL,
  layout_json TEXT NOT NULL, updated_at INTEGER,
  PRIMARY KEY (username, name)
);

CREATE TABLE annotations (           -- T-907: entity-pinned sticky notes
  id TEXT PRIMARY KEY,               -- ULID
  ref TEXT NOT NULL,                 -- pinned entity's Ref string
  content TEXT NOT NULL,             -- free text; opaque to vnproxd
  created_by TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);

CREATE TABLE metric_samples (
  ref TEXT NOT NULL, at INTEGER NOT NULL,
  rx_bytes INTEGER, tx_bytes INTEGER, rx_pkts INTEGER, tx_pkts INTEGER,
  rx_errs INTEGER, tx_errs INTEGER, rx_drop INTEGER, tx_drop INTEGER,
  PRIMARY KEY (ref, at)
);  -- pruned to 24h; longer horizons are out of scope for v1

CREATE TABLE flow_samples (        -- T-1002: internal/store/migrations/0007_flows.sql
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  at INTEGER NOT NULL, node TEXT NOT NULL,
  src_ip TEXT NOT NULL, dst_ip TEXT NOT NULL,
  src_port INTEGER NOT NULL DEFAULT 0, dst_port INTEGER NOT NULL DEFAULT 0,
  proto INTEGER NOT NULL DEFAULT 0, bytes INTEGER NOT NULL DEFAULT 0, packets INTEGER NOT NULL DEFAULT 0,
  vlan INTEGER NOT NULL DEFAULT 0,
  src_ref TEXT NOT NULL DEFAULT '', dst_ref TEXT NOT NULL DEFAULT '',
  ingress_if INTEGER NOT NULL DEFAULT 0, egress_if INTEGER NOT NULL DEFAULT 0,
  source TEXT NOT NULL              -- "sflow"|"netflow5"|"netflow9"|"ipfix"|"conntrack"
);  -- bounded: pruned to [flows] retention_minutes (default 60) AND a hard
    -- row cap ([flows] max_rows, default 2,000,000), whichever is smaller
    -- prunes first — see internal/flow's package doc comment. NOT a
    -- long-term warehouse; export to Prometheus (T-1001) or a real flow
    -- collector/TSDB is the answer for anything longer.

CREATE TABLE latency_samples (      -- T-1303: internal/store/migrations/0013_latency_samples.sql
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  link_id TEXT NOT NULL,            -- internal/latmesh.Pair.LinkID, e.g. "corosync:ring0|pve1->pve2"
  fabric TEXT NOT NULL,             -- "corosync"|"guest"
  from_node TEXT NOT NULL, to_node TEXT NOT NULL,
  at INTEGER NOT NULL,
  rtt_ms REAL NOT NULL DEFAULT 0, loss_pct REAL NOT NULL DEFAULT 0
);  -- bounded: pruned to [latmesh] retention_minutes (default 60) AND a
    -- hard row cap ([latmesh] max_rows, default 500,000), whichever is
    -- smaller prunes first — the same tick-based prune-loop pattern
    -- flow_samples already establishes. NOT a long-term warehouse.

CREATE TABLE kv (k TEXT PRIMARY KEY, v TEXT NOT NULL);

CREATE TABLE alert_rules (            -- T-1005: webhook routing rules
  id TEXT PRIMARY KEY,                -- ULID
  name TEXT NOT NULL, enabled INTEGER NOT NULL,
  source_filter_json TEXT,            -- JSON []string, NULL/[] = any source
  severity_filter_json TEXT,          -- JSON []string, NULL/[] = any severity
  target_kind TEXT NOT NULL,          -- generic|gotify|ntfy|slack
  target_url TEXT NOT NULL,
  target_secret_enc BLOB,             -- AES-256-GCM ciphertext, NULL if unset
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);

CREATE TABLE alert_deliveries (        -- T-1005: one row per delivery attempt
  id TEXT PRIMARY KEY, rule_id TEXT NOT NULL, finding_id TEXT NOT NULL,
  at INTEGER NOT NULL, attempt INTEGER NOT NULL,
  status TEXT NOT NULL,                -- retrying|delivered|failed
  error TEXT
);

CREATE TABLE finding_events (          -- T-1007: internal/store/migrations/0009_finding_events.sql
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  finding_id TEXT NOT NULL, at INTEGER NOT NULL,
  transition TEXT NOT NULL             -- "new"|"escalated"|"resolved"
);  -- pruned to the same window as metric_samples (store.MetricRetention, 24h)

CREATE TABLE capture_sessions (        -- T-1301: internal/store/migrations/0014_capture_sessions.sql
  id TEXT PRIMARY KEY,                 -- ULID, one per node-local session
  group_id TEXT NOT NULL,              -- correlation key for a multi-point capture
  target_ref TEXT NOT NULL,            -- captured entity's Ref string
  node TEXT NOT NULL,                  -- capturing node (peer-aware)
  nodes_json TEXT NOT NULL DEFAULT '[]', -- full node set of this session's group
  filter TEXT NOT NULL DEFAULT '',     -- validated BPF filter (never payload)
  caps_json TEXT NOT NULL,             -- effective, server-clamped caps
  status TEXT NOT NULL,                -- running|completed|stopped|error|purged
  started_by TEXT NOT NULL, started_at INTEGER NOT NULL, stopped_at INTEGER NOT NULL DEFAULT 0,
  file_path TEXT NOT NULL DEFAULT '',  -- on-disk .pcap path on `node`
  file_bytes INTEGER NOT NULL DEFAULT 0, packets INTEGER NOT NULL DEFAULT 0  -- accounting only
);  -- app-owned intent + accounting ONLY; captured payload lives solely in the
    -- bounded file_path .pcap, auto-purged past [capture] retention_hours.

CREATE TABLE changeset_schedules (     -- T-1103: internal/store/migrations/0010_changeset_schedules.sql
  changeset_id TEXT PRIMARY KEY,
  window_start INTEGER NOT NULL, window_end INTEGER NOT NULL,
  confirm_timeout_sec INTEGER NOT NULL,
  missed_window_policy TEXT NOT NULL,  -- "skip"|"applyImmediately"
  callback_token_hash TEXT NOT NULL,   -- sha256 hex of the one-time-delivered token, never the token itself
  status TEXT NOT NULL,                -- pending|fired|missed|blocked|failed|cancelled
  created_by TEXT NOT NULL, created_at INTEGER NOT NULL,
  fired_at INTEGER, cancelled_at INTEGER
);

CREATE TABLE api_tokens (              -- T-1104: internal/store/migrations/0011_api_tokens.sql
  id TEXT PRIMARY KEY,                 -- ULID
  name TEXT NOT NULL,
  token_hash TEXT NOT NULL,            -- hex SHA-256 of the raw bearer token
  scopes_json TEXT NOT NULL,           -- JSON []string, internal/auth.Cap names
  created_by TEXT NOT NULL, created_at INTEGER NOT NULL,
  last_used_at INTEGER, revoked_at INTEGER
);  -- UNIQUE index on token_hash

CREATE TABLE webhooks (                -- T-1104: internal/store/migrations/0011_api_tokens.sql
  id TEXT PRIMARY KEY,                 -- ULID
  url TEXT NOT NULL,
  events_json TEXT,                    -- JSON []string, NULL/[] = every event
  secret_enc BLOB NOT NULL,            -- AES-256-GCM ciphertext of the HMAC secret
  created_by TEXT NOT NULL, created_at INTEGER NOT NULL,
  consecutive_failures INTEGER NOT NULL DEFAULT 0,
  last_attempt_at INTEGER, last_success_at INTEGER, last_error TEXT
);

CREATE TABLE wireguard_tunnels (   -- T-1401: internal/store/migrations/0016_wireguard.sql
  id TEXT PRIMARY KEY, node TEXT NOT NULL, if_name TEXT NOT NULL,
  private_key_enc BLOB NOT NULL,    -- AES-256-GCM ciphertext, never returned by any API
  public_key TEXT NOT NULL,         -- base64, the exportable half
  listen_port INTEGER NOT NULL DEFAULT 0, addresses_json TEXT NOT NULL DEFAULT '[]',
  mtu INTEGER NOT NULL DEFAULT 0, carrier TEXT NOT NULL DEFAULT '',
  created_by TEXT NOT NULL, created_at INTEGER NOT NULL
);  -- UNIQUE (node, if_name)

CREATE TABLE wireguard_peers (     -- T-1401
  tunnel_id TEXT NOT NULL REFERENCES wireguard_tunnels(id) ON DELETE CASCADE,
  public_key TEXT NOT NULL, endpoint TEXT NOT NULL DEFAULT '',
  allowed_ips_json TEXT NOT NULL DEFAULT '[]',
  preshared_key_enc BLOB,           -- AES-256-GCM ciphertext, NULL when unused
  keepalive_sec INTEGER NOT NULL DEFAULT 0, external INTEGER NOT NULL DEFAULT 0,
  cluster_id TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (tunnel_id, public_key)
);

CREATE TABLE ingress_targets (     -- T-1406: internal/store/migrations/0017_ingress_targets.sql
  id TEXT PRIMARY KEY, kind TEXT NOT NULL,   -- 'haproxy'|'nginx'|'caddy'|'traefik'
  address TEXT NOT NULL,             -- the target's own status/admin endpoint base URL
  credential_enc BLOB,               -- AES-256-GCM ciphertext, NULL when unused
  added_by TEXT NOT NULL, added_at INTEGER NOT NULL
);
CREATE TABLE wan_targets (          -- T-1405: internal/store/migrations/0018_wan.sql
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  node TEXT NOT NULL, uplink TEXT NOT NULL, host TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  UNIQUE (node, uplink, host)
);

CREATE TABLE wan_probe_samples (    -- T-1405
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  link_id TEXT NOT NULL,            -- internal/latmesh.Pair.LinkID, e.g. "wan:vmbr0|pve1->1.1.1.1"
  from_node TEXT NOT NULL, uplink TEXT NOT NULL, to_node TEXT NOT NULL,
  at INTEGER NOT NULL,
  rtt_ms REAL NOT NULL DEFAULT 0, loss_pct REAL NOT NULL DEFAULT 0
);  -- bounded: pruned to [wan] retention_minutes (default 60) AND a hard
    -- row cap ([wan] max_rows, default 500,000), whichever is smaller
    -- prunes first — the same tick-based prune-loop pattern
    -- latency_samples/flow_samples already establish. NOT a long-term
    -- warehouse.
CREATE TABLE k8s_clusters (            -- T-1501: internal/store/migrations/0019_k8s_clusters.sql
  id TEXT PRIMARY KEY,                 -- ULID
  name TEXT NOT NULL,
  kubeconfig_enc BLOB NOT NULL,        -- AES-256-GCM ciphertext of the full parsed kubeconfig
  added_by TEXT NOT NULL, added_at INTEGER NOT NULL,
  cni_detected TEXT NOT NULL DEFAULT '', -- last poll's internal/k8s.CNI value, '' = never polled
  status TEXT NOT NULL DEFAULT 'unpolled' -- unpolled|ok|unreachable
);
```

**`layouts` / `annotations` (T-907: saved views & annotations, docs/api.md's "Saved views & annotations" section).** Both are strictly app-owned UI state — never a shadow copy of any PVE-authoritative config, per this doc's top-level rule. `layouts` (T-107) already held the auto-persisted canvas-position/filter blob under the reserved name `"topology"` (and `"onboarding"`'s walkthrough progress); T-907 reuses the identical mechanism for **named saved views** — a user-chosen `name` whose `layout_json` is a frontend-owned, backend-opaque blob shaped `{kind: "view", layers, vlanFilter?, zoom, viewport: {x, y}, selection?, view}` (docs/api.md documents the exact shape). The `kind: "view"` tag is how the frontend tells a saved view apart from the reserved auto-layout blobs when listing — vnproxd itself never inspects `layout_json`'s contents either way. `annotations` is a **new** table rather than a further extension of `layouts`: an entity-pinned sticky note is naturally many-rows-per-user (indeed many-rows-per-entity, shared across every user, not one blob overwritten in place), so it doesn't fit `layouts`' per-`(username, name)` single-blob shape — see `internal/store/migrations/0006_annotations.sql`'s doc comment for the full reasoning. `ref` is the pinned entity's `Ref` string (kind:node:id); `content` is free text vnproxd never interprets; `created_by` is the authoring user, kept for display/audit only — annotations are a shared team scratchpad visible to every `netRead`-capable user, not private per-user data like `layouts`.

**`alert_rules` / `alert_deliveries` (T-1005: alert routing, docs/api.md's "Alert Rules" section, migration `internal/store/migrations/0008_alert_rules.sql`).** Both app-owned per this doc's top-level rule. `alert_rules` routes findings/drift transitions (`internal/findings/notify.go`'s existing once-per-transition firing, unchanged by this task) to a webhook target; `source_filter_json`/`severity_filter_json` are JSON string arrays, `NULL`/`[]` both meaning "no filter on that dimension" — the same optional/ANDed filter convention `GET /findings`' own `?source=&severity=` uses. `target_secret_enc` is AES-256-GCM ciphertext (`internal/store/cipher.go`'s `SessionCipher` — the identical cipher/key `sessions.pve_ticket_enc` uses, not a second key pair), `NULL` when the target needs no secret. `alert_deliveries` logs one row per delivery *attempt* (not one row per logical delivery — `attempt` is the 1-based sequence number within a rule+finding retry sequence): `status` is `"retrying"` (this attempt failed, another is scheduled), `"delivered"`/`"failed"` (both terminal). Bounded by construction (a fixed max-attempt count, `internal/findings/webhook.go`'s `DefaultMaxAttempts`), so — unlike `metric_samples`, which needs an explicit prune job — this table only grows by real delivery events and needs none; note for the next agent touching this area: migration numbering skips `0007`, reserved for a sibling Phase 10 task (T-1002, flow ingestion) landing independently.

**`flow_samples` (T-1002: flow ingestion engine, docs/api.md's "Flows" section, `internal/store/migrations/0007_flows.sql`).** One row per decoded `flow.Record` (sFlow v5, NetFlow v5/v9, or IPFIX — `source` names which), observed by this node's own opt-in UDP listeners (`[flows]` in `vnprox.toml`, off by default per node). T-1004's `internal/flow/hostsample` package feeds this exact same table for nodes with no external exporter (`source = "conntrack"`, also opt-in/off-by-default via `[flows] conntrack_sampling_enabled`/`ebpf_sampling_enabled`) — no second storage path. Unlike `metric_samples`' `(ref, at)` natural key, there is no dedup key here — many distinct flow observations legitimately share the same `(node, src, dst, port, at)` tuple at one-second resolution, so `id` is a plain autoincrement surrogate, also used as `GET /api/peer/flows`' cluster-merge pagination tiebreak (docs/api.md's `FlowRecord.id`). `src_ref`/`dst_ref` are inventory `Ref` strings, populated only when `src_ip`/`dst_ip` resolves against a known bridge or SDN subnet in the live inventory graph (`internal/flow.GraphResolver`) — empty otherwise, never guessed. Bounded by **both** a retention window (`[flows] retention_minutes`, default 60) **and** a hard row cap (`[flows] max_rows`, default 2,000,000), whichever is smaller prunes first, on the same tick-based prune-loop pattern `metric_samples`' `RunPruneLoop` already establishes — this is explicitly **not** a long-term flow warehouse (docs/roadmap-next.md's carried-forward invariant); see `internal/flow`'s package doc comment. T-1504's `serviceClass` attribution (docs/api.md's Flows section) is deliberately **not** a column here: it is recomputed fresh from a row's own `src_ip`/`dst_ip`/`src_port`/`dst_port`/`proto`/`vlan` by `internal/flow.Classifier` at `GET /flows` read time, so a row classifies correctly against whichever `NetworkSource`s are registered *now* — including one registered after the row was ingested (e.g. T-1503 registering Ceph CIDRs later) — the same "recompute from live state" stance `internal/ipam`'s conflict findings already take, rather than freezing a stale classification into storage.

**`latency_samples` (T-1303: latency & loss mesh, docs/api.md's "Latency mesh" section, `internal/store/migrations/0013_latency_samples.sql`).** App-owned per this doc's top-level rule — vnprox's own continuous node-to-node probe observation, never a shadow copy of anything PVE owns. One row per probe tick per link (`internal/latmesh.Pair.LinkID` in `link_id`, e.g. `"corosync:ring0|pve1->pve2"`), written by this node's own `internal/latmesh.Service.Tick`, on `[latmesh] probe_interval_sec` (default 10). Like `flow_samples`, `id` is a plain autoincrement surrogate (no natural dedup key — a link legitimately produces a new reading every tick). `rtt_ms` is meaningless (left 0) when `loss_pct` is 100 (a probe that got no reply at all). Bounded by **both** a retention window (`[latmesh] retention_minutes`, default 60) **and** a hard row cap (`[latmesh] max_rows`, default 500,000), whichever is smaller prunes first, the identical tick-based prune-loop pattern `flow_samples` already establishes — **not** a long-term warehouse; see `internal/latmesh`'s package doc comment.

**`wan_targets` / `wan_probe_samples` (T-1405: WAN & upstream health, docs/api.md's "WAN & upstream health" section, migration `internal/store/migrations/0018_wan.sql`).** Both app-owned per this doc's top-level rule. `wan_targets` is the operator-configured reference-target list (`GET/PUT /wan/targets`) — one row per (`node`, `uplink`, `host`), `UNIQUE (node, uplink, host)` so re-`PUT`ting the same target list is idempotent. `wan_probe_samples` mirrors `latency_samples` field-for-field, with an added `uplink` column (which of a node's, possibly several, uplinks the reading belongs to — T-1405's multi-WAN visibility) and `to_node` carrying a *reference target's host* rather than another cluster node's name; `link_id` uses the exact same `internal/latmesh.Pair.LinkID` scheme, e.g. `"wan:vmbr0|pve1->1.1.1.1"` (fabric `"wan"`, label = uplink). Written by this node's own `internal/wan.Service.Tick`, which is literally `internal/latmesh.Service` reused (not forked) against this table/`[wan]` config section instead of `latency_samples`/`[latmesh]` — see `internal/wan`'s package doc comment. Bounded by **both** a retention window (`[wan] retention_minutes`, default 60) **and** a hard row cap (`[wan] max_rows`, default 500,000), the identical tick-based prune-loop pattern `latency_samples`/`flow_samples` already establish — **not** a long-term warehouse.

**`capture_sessions` (T-1301: distributed packet capture, docs/api.md's "Captures" section, migration `internal/store/migrations/0014_capture_sessions.sql`).** App-owned **intent + accounting only**, per this doc's top-level rule — emphatically **not** a place captured payload lands. The captured packets live solely in the bounded per-session `.pcap` file named by `file_path` on the capturing `node` (`[capture] root`, auto-purged past `[capture] retention_hours` by `internal/capture.Coordinator.Sweep`); this table records who started what, where, under which server-clamped `caps_json`, and running `file_bytes`/`packets` *counts* — never a byte of packet contents. One row per capturing node; a multi-point capture (the same logical flow captured on ≥2 nodes) is a set of rows sharing `group_id` — the correlation key T-1302 aligns the two nodes' decoded streams by — with `nodes_json` carrying the full participating-node list on every row. `id` is a ULID (like `annotations`/`alert_rules`, unlike `flow_samples`' autoincrement). `status` transitions `running` → `completed` (a server cap was hit) / `stopped` (operator) / `error`, or → `purged` once the sweep deletes its file. Each daemon owns its own rows and files (for a remote capture the coordinator keeps a group-bookkeeping row while the capturing peer keeps its own file-ownership row + retention sweep); there is no cross-node prune job — a bounded, self-purging table by construction.

**`finding_events` (T-1007: history playback, docs/api.md's "History" section, migration `internal/store/migrations/0009_finding_events.sql`).** App-owned per this doc's top-level rule — vnprox's own record of when its findings stream changed, never a shadow copy of PVE state. One row per finding transition (`"new"`\|`"escalated"`\|`"resolved"`), written by `internal/findings/findingevents.go`'s `FindingEventsNotifier` — a `Notifier` (the same interface `PVENotifier`/`WebhookNotifier` implement, `internal/findings/notify.go`) composed alongside them at the `cmd/vnproxd` composition root, so this table is populated from `notify.go`'s **existing** transition detection (`evaluateNotifications`/`fireNotification`, unchanged by this task) rather than a second, duplicated detector. `id` is a plain autoincrement surrogate (like `flow_samples`, unlike `metric_samples`' natural key): the same `finding_id` legitimately transitions more than once within the retention window. Bounded and pruned to the exact same window as `metric_samples` (`store.MetricRetention`, 24h) on the same cadence, per this task's card — `GET /history/events` merges this table with a changeset-lifecycle-filtered slice of `audit_log` into one timeline-marker feed for `web/src/topology/history/HistoryTimeline.tsx`'s scrubber; see that route's own doc comment (docs/api.md) for the merged shape.

**`changeset_schedules` (T-1103: scheduled changesets & maintenance windows, docs/api.md's "Scheduled changesets & maintenance windows" section, migration `internal/store/migrations/0010_changeset_schedules.sql`).** App-owned per this doc's top-level rule — one row per changeset that currently has (or most recently had) a schedule; `changeset_id` is the primary key, so scheduling a changeset again after an earlier schedule resolved (fired/missed/blocked/failed/cancelled) replaces that row rather than accumulating history — the changeset's own audit trail (`changeset.schedule_create`/`_cancel`/`_fire`/`_fire_blocked`/`_missed`) is the durable history of what happened, exactly like every other T-205 apply-engine transition. `callback_token_hash` is a sha256 hex digest of the single-use signed callback token (docs/api.md) — the token itself is never persisted anywhere, only delivered once in the creating `POST /changesets/{id}/schedule` response. `status` moves `pending` → exactly one of `fired`/`missed`/`blocked`/`failed` (stamping `fired_at`) or `cancelled` (stamping `cancelled_at`) — never back, and never more than once (`change.Service.TickSchedules`' own `WHERE status = pending` guard on every resolving write).

**`api_tokens` / `webhooks` (T-1104: event stream & automation tokens, docs/api.md's "Tokens & Webhooks" section, migration `internal/store/migrations/0011_api_tokens.sql`).** Both app-owned per this doc's top-level rule — a token is a vnprox-local, capability-scoped delegated credential a logged-in user mints (docs/security.md's Authentication section: explicitly not a second login/authentication path, distinguished from the PVE ticket bridge), and a webhook registration is purely vnprox's own delivery-target config. `api_tokens.token_hash` is a one-way hex SHA-256 of the raw bearer token — unlike `sessions.pve_ticket_enc`/`alert_rules.target_secret_enc`'s reversible AES-256-GCM encryption, nothing ever needs to recover the raw value, only prove a presented token hashes to a live row; `scopes_json` is a JSON array of `internal/auth.Cap` names (the existing capability-flag vocabulary plus the new `automation` scope), never exceeding the creating user's own derived capabilities at mint time (enforced in `internal/auth`, not by a DB constraint). `revoked_at` is set, never deleted, so a revoked token's audit trail stays intact. `webhooks.secret_enc` **does** reuse the reversible `SessionCipher` (like `alert_rules.target_secret_enc`) since delivery needs the plaintext HMAC key on every send; `events_json` mirrors `alert_rules`' optional/ANDed filter convention (`NULL`/`[]` = every event). `consecutive_failures`/`last_attempt_at`/`last_success_at`/`last_error` back the `webhook_unhealthy` finding, recomputed live from these columns each findings cycle (`internal/findings`' `WebhookProvider` seam) rather than a second persisted finding table — the same "recompute from live state" pattern `internal/ipam`'s conflict findings already use.
**`k8s_clusters` (T-1501: Kubernetes overlay mapping engine, docs/api.md's "Kubernetes" section, migration `internal/store/migrations/0019_k8s_clusters.sql`).** App-owned registration intent only per this doc's top-level rule — which k8s clusters to poll, and how to authenticate to them; **never** a shadow copy of the cluster's own live state (nodes/pods/services/overlay are always recomputed fresh by `GET /k8s/{clusterId}/overlay`, the identical boundary `ingress_targets`/`wireguard_tunnels` already establish for their own domains). Kubernetes integration is **read-only forever** (`docs/roadmap-universal.md`'s Phase 15 Invariants section): no field in this table, and no code path in `internal/k8s`, could ever back a write to the cluster itself. `kubeconfig_enc` is AES-256-GCM ciphertext (`internal/store/cipher.go`'s `SessionCipher` — the identical cipher/key `sessions.pve_ticket_enc` uses, not a second key pair) of the entire parsed kubeconfig's credential material (bearer token or client cert+key); never returned by any API response. `cni_detected`/`status` are the last poll's own best-effort cache, purely so `GET /k8s/clusters` can render a summary without a live poll on every list call — never trusted as authoritative by the overlay route itself.

## 3. Changeset operations

`Op` is a tagged union serialized as `{"op": "<type>", "target": Ref, "params": {...}}`. The v1 op vocabulary:

| Group | Ops |
|---|---|
| iface | `iface.update` (mtu, comments, addresses, gateway, autostart), `iface.rename` (newName; issue #2 — renames a logical bridge/bond/vlan in place, rewriting the stanza header, its auto/allow-* references, and every in-file reference to the old name; blocked when guests are still attached to the old name, since guest `bridge=` bindings live in PVE guest config a rename doesn't rewrite; physical-NIC/udev renames are out of scope), `iface.raw.replace` (node + full file content; T-208's raw editor escape hatch — target is a `node` Ref, not one entity; validated by diffing the parsed entity delta between the live file and the new content through the same referential/safety/advisory checks — see docs/features/change-management.md §7) |
| bond | `bond.create`, `bond.update`, `bond.delete` |
| bridge | `bridge.create`, `bridge.update`, `bridge.delete`, `bridge.port.add`, `bridge.port.remove` |
| vlan | `vlan.create`, `vlan.update`, `vlan.delete` |
| bridge/bond kind | OVS bridges/bonds reuse the same op names above with `target.kind` = `ovs-bridge`/`ovs-bond` (Kind alone disambiguates Linux vs OVS for these two, per Ref's closed kind set) — no separate op types. `bond.create`'s params carry an OVS-only `bridge` field (the OVS bridge this bond attaches to, rendered as `ovs_bridge`; required when `target.kind` is `ovs-bond`, ignored otherwise) — this is the "params carry ovs-specific fields" case data-model review flagged for T-407. |
| vlan kind | `KindVlan` has no dedicated OVS sibling (an OVS Int Port is a `VlanIface` with `virt: "ovs"` — see the entity table above), so `vlan.create`'s params instead carry `ovs` (bool: true selects `ovs_type=OVSIntPort`/`ovs_bridge`/`ovs_options` instead of `vlan-raw-device`; `parent` then names an OVS bridge) and `trunks` (`[]VidRange`, OVS-only, rejected when `ovs` is false: additional trunked VLAN ranges alongside `vid`'s single access tag). T-407. |
| sdn | `sdn.zone.create/update/delete`, `sdn.vnet.create/update/delete`, `sdn.subnet.create/update/delete`, `sdn.apply` |
| guest | `guest.nic.update` (reattach bridge/vnet, vid, rate, firewall flag) |
| fw | `fw.rule.create/update/delete/move`, `fw.options.update`, `fw.alias.*`, `fw.ipset.*`, `fw.group.*` |
| ipam | `ipam.alloc.create/delete` |
| qos (T-1505) | `qos.shape.create/update/delete` — a bridge-level tc/HTB traffic shape (params: `bridge`, optional `matchCidr`/`matchVlan`, `rateMbit`, optional `ceilMbit`/`priority`); target is a `qos-shape` Ref (node-scoped, caller-chosen id, no dedicated live-polled inventory kind of its own — like `nat-rule`/`static-route`). **Per-guest-NIC rate limiting is deliberately NOT a new op**: it already exists as `guest.nic.update`'s `rateMbps` field (the `guest` row above) — this group's only new surface is bridge-level per-service shaping. |
| wg (T-1401) | `wg.tunnel.create/update/delete`, `wg.peer.add/remove` |
| nat | (T-1403) `nat.masquerade.create/delete` (a PVE-host SNAT/MASQUERADE rule; no `update` — rotating one is delete-and-recreate, so it's always two visible ops, never a silent overwrite), `nat.portforward.create/update/delete` (a DNAT/port-forward rule: `iface`, `proto` tcp\|udp, `extPort`, `intIp`, `intPort`, `comment?`) |
| route | (T-1403) `route.static.create/update/delete` — an additional/policy static route (`iface`, `destCidr`, `gateway`, `metric?`, `comment?`); a node's *default* gateway stays owned by `iface.update`'s own `gateway` field, unchanged by this group |
| vf (T-1506) | `vf.provision` (params: `count` or explicit `vfs` []{id, macAddr?, vlan?, spoofCheck?, trust?}, top-level `vlan?`/`macAddr?`/`spoofCheck?`/`trust?` defaults — exactly one of `count`/`vfs` set, `internal/host.ResolveVFPlan`'s shared resolution) — target is the PF's own `physnic` Ref (the entity whose VF pool is being configured), not a synthetic per-VF id, since one op can provision a whole batch in one shot; there is no `vf.update`/`vf.delete` op — re-provisioning is always a fresh `vf.provision`, mirroring the "no silent overwrite of a generated rule" convention this vocabulary already uses elsewhere. Applied via the ordinary node-file post-up/post-down path (category (2)/(3) below), exactly like `bond.create`/`bridge.create` — never a second mutation mechanism. |

Each op maps to one or more **apply steps**; the planner orders steps: (1) cluster-scope PVE API calls, (2) per-node interface file staging, (3) per-node `ifreload -a` (executed directly by vnproxd's own `NodeAgent` — **correction, T-607 docs audit:** not "via PVE's network reload endpoint" as this line previously said; `cmd/vnproxd/changeagent.go` writes `/etc/network/interfaces` and execs `ifreload` itself, since vnproxd runs on the node — see `docs/architecture.md` §4 for the fuller correction), (4) `sdn.apply` last when present. Rollback executes the inverse from the pre-snapshot in reverse order.

**T-1505's `qos.shape.*` ops are node-local but NOT node-file ops**: unlike `iface`/`bond`/`bridge`/`vlan` (which mutate `/etc/network/interfaces`), a shape's on-node realization is a `tc`/HTB qdisc/class/filter invocation (`internal/qos.RenderTC`), executed by its own daemon-level `QosGateway` seam (`internal/change.QosGateway`, mirroring the `NodeAgent` seam's "no user PVE ticket needed" shape so its rollback works on the unattended commit-confirm-timeout path too) and its own apply-step kind (`qos_apply`), ordered after every node's interface stage/reload pair (a shape's bridge must already exist) and before a trailing `sdn.apply`. A shape's intent is persisted in the app-owned `qos_shapes` table (§2 above) — the live tc/HTB state itself is never shadow-copied there; `internal/qos.RenderTC` re-derives the on-node invocation from the stored row every time it is (re)applied. A `qos.shape.*` op whose `bridge` param names (or, for an update naming a new one, re-names) a node's resolved management/corosync path is `touchesMgmtPath` (`internal/change/mgmttouch.go`), inheriting T-703's mandatory-acknowledgement + 180s confirm-window-floor ceremony with no override — a shape that starves or deprioritizes the management/corosync link is exactly as dangerous as an `iface.update` on it.

**`qos_shapes` (T-1505, migration `internal/store/migrations/0020_qos.sql`).** App-owned intent only, per this doc's top-level rule: `id` (the op target's own id), `node`, `bridge`, `match_cidr`/`match_vlan` (both nullable/empty — a shape with neither set governs the bridge's whole, otherwise-unclassified egress), `rate_mbit`, `ceil_mbit`/`priority` (nullable), `created_by`, `created_at`, `updated_at`. Written exclusively by the `qos.shape.*` apply/rollback executor (`cmd/vnproxd`'s `hostQosGateway`) — there is no second mutation path.

**T-1403's `nat`/`route` op groups are node-file ops** (category (2)/(3) above, exactly like `iface`/`bond`/`bridge`/`vlan`): `internal/change/ifaces/edgeop.go` appends/replaces/removes a post-up/post-down shell-command stanza pair inside an *already-existing* iface stanza named by the op's own `iface` field — never a second file, never a second apply mechanism. `nat-rule`/`static-route` have no dedicated `inventory.Kind` entity of their own (like `fw.alias`/`ipset`/`group` above): a rule's entire state lives in its two generated lines' own trailing marker comment (`# vnprox-edge:<kind>:<url-encoded fields>`, `internal/host`'s `EncodeNat*Marker`/`DecodeNat*Marker`/`EncodeStaticRouteMarker`/`DecodeStaticRouteMarker`) — GET /edge/routes and GET /edge/nat (docs/api.md) decode it back apart on every read rather than tracking it in a second store table, so there is exactly one record of a rule anywhere: the file itself.

## 4. Blueprints (`internal/blueprint`, T-603)

A `Blueprint` (docs/api.md's Blueprints section has the full field-by-field shape) is app-owned data — a template a user wrote, captured, or copied from a starter — never a shadow copy of PVE config. The five bundled starters are compiled into the binary (`internal/blueprint.Starters`), not stored rows; only user-authored/captured/copied blueprints live in the `blueprints` table (`internal/store/migrations/0004_blueprints.sql`):

```sql
CREATE TABLE blueprints (
  id TEXT PRIMARY KEY, name TEXT NOT NULL,
  blueprint_json TEXT NOT NULL,      -- the full Blueprint (id/name kept in sync)
  created_by TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
```

Instantiation (`blueprint.Instantiate`) is a pure function of `(Blueprint, params, target nodes, inventory.Snapshot)` → `[]change.Op`: it expands each `EntityTemplate` across its node selector, substitutes `{{param}}` placeholders, then diffs each expanded entity against the snapshot — absent → a `*.create` op, present-and-matching → no op, present-and-divergent → a `*.update` op naming only the diverging fields (bridge port membership divergence is the one exception: it always becomes `bridge.port.add`/`bridge.port.remove` ops, since `BridgeUpdateParams` has no `ports` field — port membership changes go through those dedicated ops everywhere else in this codebase too). It never persists or applies anything itself; the caller (`internal/api`'s instantiate handler) hands the returned ops to `change.Service.Create`, the same "compute an op patch, let the normal changeset lifecycle own it" pattern `internal/drift`'s `FixOps` already established.

## 5. Live path probe divergence findings (`internal/store`, T-806)

Every other producer in the unified findings stream (docs/api.md's `GET /findings`) is recomputed fresh from continuously-polled state on every call — nothing needs to persist it. A `sim_divergence` finding is different: it records the outcome of a specific, user-triggered `POST /simulate/verify` call (docs/api.md's Live path probe section), and nothing re-runs that live guest-agent probe on its own, so there is no "continuously polled state" to recompute it from. It therefore gets a table of its own (`internal/store/migrations/0005_sim_divergence_findings.sql`):

```sql
CREATE TABLE sim_divergence_findings (
  id                TEXT PRIMARY KEY,   -- content-derived: "probe:sim_divergence|<src>|<dstKind>:<dstRefOrIp>|<proto>|<port>"
  src_ref           TEXT NOT NULL,      -- guest-nic Ref string (the probed src)
  dst_kind          TEXT NOT NULL,      -- guest-nic|ip|external
  dst_ref           TEXT NOT NULL DEFAULT '',  -- set iff dst_kind = guest-nic
  dst_ip            TEXT NOT NULL DEFAULT '',  -- set iff dst_kind = ip
  proto             TEXT NOT NULL,      -- tcp|icmp
  port              INTEGER NOT NULL DEFAULT 0,
  simulated_verdict TEXT NOT NULL,
  observed_outcome  TEXT NOT NULL,
  detail            TEXT NOT NULL DEFAULT '',
  created_at        INTEGER NOT NULL,   -- unix seconds, first time this tuple diverged
  updated_at        INTEGER NOT NULL    -- unix seconds, most recent divergence
);
```

Row lifecycle, driven entirely by `internal/api`'s `POST /simulate/verify` handler (`internal/store.SimDivergenceRepo`): a `diverges: true` response upserts the row keyed by `id` (re-verifying the identical tuple refreshes `updated_at`/`observed_outcome`/`detail` in place rather than accumulating a duplicate row — `created_at` is preserved across an upsert); a `diverges: false` response for a tuple that previously had a row clears it (the finding should not keep claiming a divergence the most recent live check no longer shows). `cmd/vnproxd`'s `probeFindingsAdapter` reads this table fresh on every `GET /findings` call and maps each row to the unified `Finding` shape (`Source: "probe"`, `Check: "sim_divergence"`) — the table is this producer's *source of truth*, not a cache the in-memory engine also holds a separate copy of.

## 6. Declarative cluster network spec (`internal/spec`, T-1101)

> Numbering note: the card called for a "new §5"; §5 was already taken by T-806's live-probe table by the time this landed, so the spec schema is §6.

The **Spec** is blueprints v2: one versionable YAML document capturing cluster-wide L2/SDN network intent — per-node bonds, bridges and plain 802.1q VLAN sub-interfaces, plus cluster-scoped SDN zones/vnets/subnets. It is **not** persisted anywhere as authoritative state (Proxmox remains the source of truth, D5): `Export` renders it fresh from a live `inventory.Snapshot`, and `Import` diffs a supplied document back against live. The document is the transport for a GitOps flow (commit the exported spec to git; a PR's diff is the review; `POST /spec/import` on merge produces the changeset), so two properties are load-bearing:

- **Byte-stable serialization.** `Spec` has **no embedded timestamp** and is a tree of typed structs with fixed struct-tag field order (never a `map[string]any`, whose Go iteration order is randomized). `Export` additionally sorts every collection by a stable key (nodes by name; bonds/bridges/vlans by name; SDN objects by id; set-valued fields — ports, slaves, zone nodes, dhcp ranges, vids — sorted). Two exports of identical live state are therefore **byte-identical**, which is what makes `git diff` on an unchanged cluster's committed spec empty (T-1101 AC2/AC4). Freshness comes from the changeset/commit metadata, not the document.
- **Import never applies and never prunes.** `Import(Spec, live) ([]change.Op, notInSpec []Ref, error)` returns ordinary `change.Op`s (the caller hands them to `change.Service.Create` → a **draft** changeset through the normal stage → validate → diff → apply → confirm/rollback lifecycle) plus `notInSpec`: the refs of managed-kind entities present live but absent from the document. Entities in `notInSpec` are **reported, never deleted** — there is no implicit prune (T-1101 AC5). The op set is create/update/port-add/port-remove only.

The per-entity diff mirrors `blueprint.Instantiate`'s absent→create / divergent→update / matching→noop pattern (§4), extended from one blueprint's node-selected set to every cluster-wide entity of the **managed kinds**: `bond`, `bridge`, `vlan`, `sdn-zone`, `sdn-vnet`, `sdn-subnet`. Entities of any other kind (physical NICs, guests, LLDP neighbours, firewall rulesets, **OVS** bridges/bonds and OVS Int Ports) are neither exported nor reconciled nor reported in `notInSpec` — they are outside the v1 spec's scope. **Firewall rulesets and IPAM allocations are deliberately not in the v1 spec** (firewall rule ordering/move reconcile is a separate effort the blueprint diff engine this mirrors never covered); a later `specVersion` may add them additively.

Field semantics follow the same **omitempty = "not managed by this spec"** convention `blueprint.capture`/`adapters` already use: `Export` emits only non-zero declared fields (`Bridge.DeclaredPortNames`, `Bond.DeclaredSlaves`, `*.MTUDeclared`, …), and `Import` leaves an omitted/zero field untouched on an existing entity (and takes the OS/PVE default on a created one). This applies to booleans too — a `false`/omitted `vlanAware`/`stp`/`snat`/vnet-`vlanAware` means "don't manage this flag", so only a spec value of `true` diverging from live produces an op; **reconciling a flag back to `false` is not expressible in v1** (the conservative choice keeps a partial hand-edit from silently emitting a disable op). Parent/Vid on a VLAN, Type on a zone, Zone on a vnet, and Vnet/CIDR on a subnet are identity, not editable in place — a change there is a delete+create in PVE, so the diff never emits an update for them (matching the corresponding `*UpdateParams` shapes in §3).

Schema (`specVersion: 1`; every field except identity is `omitempty`):

```yaml
specVersion: 1
nodes:                        # sorted by name
  - name: pve1
    bonds:                    # Linux bonds only; sorted by name
      - {name, mode, slaves[], lacpRate, xmitHashPolicy, mtu}
    bridges:                  # Linux bridges only; sorted by name
      - {name, ports[], vlanAware, vids[], addresses[], gateway, mtu, stp, comments}
    vlans:                    # plain 802.1q sub-interfaces; sorted by name
      - {name, parent, vid, addresses[], mtu}
sdn:                          # omitted entirely when the cluster has no SDN objects
  zones:                      # sorted by id
    - {id, type, bridge, controller, ipam, nodes[], exitNodes[], peers[], vrfVxlan, mtu}
  vnets:                      # sorted by id ("zone/vnet" path)
    - {id, zone, alias, tag, vlanAware}
  subnets:                    # sorted by id (CIDR)
    - {id, vnet, gateway, dnsZonePrefix, dhcpRanges[], snat}
```

`vids` are the inventory `VidRange` string forms (`"100"`, `"2-4094"`), sorted. `Parse` rejects any `specVersion` other than `1` (an absent field is `0` and is rejected too), so an operator never reconciles against a schema this daemon can't fully honor.

## 7. Pinned spec drift reconciliation (`internal/store`, T-1102)

The GitOps reconciler's declared desired state: an operator pins one Spec document (§6) as the reference `internal/drift`'s `spec_drift` check family (docs/features/topology.md §6, `GET /drift`'s sixth `check` value, docs/api.md's Spec pin section) diffs live state against every drift cycle, via T-1101's `spec.Import` unchanged — no second reconcile implementation. App-owned data per this doc's top-level rule: the pin is vnprox's own record of what an operator asked to reconcile toward, never a shadow copy of PVE-authoritative config — the same status docs/features/blueprints.md §2's "pin nodes to blueprint" P1 note already flagged for a related, still-unimplemented idea. A singleton row (`internal/store/migrations/0012_pinned_spec.sql`):

```sql
CREATE TABLE pinned_spec (
  id        INTEGER PRIMARY KEY CHECK (id = 1),  -- exactly one pin, cluster-wide
  content   TEXT NOT NULL,      -- the pinned YAML document, byte-for-byte
  pinned_by TEXT NOT NULL,      -- acting user at pin time
  pinned_at INTEGER NOT NULL    -- unix seconds
);
```

`POST /spec/pin` validates `content` through the same `spec.Parse` `POST /spec/import` uses (rejecting an unparseable document or a `specVersion` other than `1` before anything is stored) and upserts this one row in place — re-pinning replaces the previous pin outright, there is no history of past pins. `DELETE /spec/pin` removes the row; `internal/drift`'s spec_drift check treats "no row" identically to "never pinned" (zero findings, never an error) so unpinning cleanly clears every `spec_drift` finding on the next drift cycle. Note for the next agent: migration numbering skips `0010`/`0011`, reserved for in-flight sibling tasks (T-1103 scheduled changesets, T-1104 event stream/tokens) landing independently — the same "reserved gap" precedent `0007`'s own note above documents.

## 8. Guest interior toggles (`internal/store`, T-1304)

`guest_interior_toggles` (`internal/store/migrations/0015_guest_interior_toggles.sql`): the per-guest opt-in preference gating docs/api.md's `GET /guests/{ref}/interior` (§"Guest interior") — app-owned UI state per this doc's top-level rule (D5): it records only "has an operator opted this guest in", never a copy of any PVE-owned config or the interior read set itself, which is never persisted at all (live-read fresh on every request).

```sql
CREATE TABLE guest_interior_toggles (
  ref        TEXT PRIMARY KEY,   -- the guest's Ref string (guest:<node>:<vmid>)
  enabled    INTEGER NOT NULL,
  updated_by TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);
```

Keyed by `ref` (one row per guest, following `annotations`' ref-keyed shape rather than `layouts`' per-username shape, §2): the toggle is a shared, cluster-wide preference any `netRead`-capable operator can see and flip, not private per-user data. A guest with no row at all reads as `enabled: false` (off by default) — the table only ever grows a row the first time a guest is toggled.

## 9. WireGuard tunnels & peers (`internal/store`, T-1401)

`wireguard_tunnels` / `wireguard_peers` (`internal/store/migrations/0016_wireguard.sql`) hold **app-owned intent + audit only**, per this doc's top-level rule and `docs/architecture.md` §7's new-domain invariant — WireGuard's own on-node state (the live interface, handshake ages, transfer counters, the endpoint a peer is actually reaching us from) stays authoritative and is never shadow-copied here (`internal/wireguard.ParseDump` reads it fresh on every poll). Every WireGuard change is an ordinary `wg.*` changeset op (§3) through the normal stage→validate→diff→apply→confirm/rollback lifecycle — there is **no second mutation path**.

**Key custody.** A tunnel's private key is generated *on the owning node* via stdlib `crypto/ecdh`'s X25519 curve (`internal/wireguard.GenerateKeypair` — no new third-party crypto dependency) as part of applying `wg.tunnel.create`, written once to `private_key_enc` **AES-256-GCM-encrypted at rest using the identical `internal/store.SessionCipher` cipher/key `sessions.pve_ticket_enc` and `alert_rules.target_secret_enc` use** (not a second key pair), and is never returned by any API response, log line, or audit-log detail. Only the derived `public_key` (plaintext) is exportable (`GET /wireguard/tunnels/{id}/pubkey`). A tunnel's key is never regenerated in place by an `update` op — rotation is delete-and-recreate, always two ordinary audited changeset ops, never a silent in-place overwrite. `wireguard_peers.preshared_key_enc` is the same sealed form when a peer uses an optional preshared key.

**External peers** (`wireguard_peers.external = 1`) are endpoints vnprox does not own (a road-warrior, or a cluster vnprox does not manage): modeled read-only, config-export-only (`GET /wireguard/tunnels/{id}/peer-config`), and never targeted by an apply step of vnprox's own — their own key hygiene is explicitly out of this card's control. `cluster_id` links a federation-managed internal peer (the T-1201 seam, not yet in this repo — external/non-federated peers are the modeled shape until federation lands).

## 10. Ingress discovery targets (`internal/store`, T-1406)

`ingress_targets` (`internal/store/migrations/0017_ingress_targets.sql`) holds **app-owned intent only** — which reverse-proxy status endpoints to poll, and how to authenticate to them — per this doc's top-level rule and `docs/architecture.md` §7's new-domain invariant. A target's own discovered state (its currently configured routes/backends) is never shadow-copied here: `GET /ingress/status` (docs/api.md's Ingress visibility section) calls `internal/ingress.IngressDiscoverer.Discover` fresh against every row on every request. There is no changeset op group for this table — adding/removing a discovery target doesn't change any network config, so it is an ordinary CRUD route (`GET/POST/DELETE /ingress/targets`) audited as `ingress.target_add`/`ingress.target_remove`, the same non-changeset-managed shape `alert_rules`/`webhooks` already use for their own operator-configured, non-network-mutating settings.

`credential_enc` is the same **AES-256-GCM** sealed form `sessions.pve_ticket_enc` / `alert_rules.target_secret_enc` / `wireguard_tunnels.private_key_enc` all use (`internal/store.SessionCipher`, not a second cipher), `NULL` when a target's status endpoint needs no credential. `kind` selects which registered `internal/ingress.IngressDiscoverer` implementation (`haproxy`\|`nginx`\|`caddy`\|`traefik`) polls that row — the seam Phase 17's plugin SDK (T-1702) is expected to extend with additional `kind` values with no schema change here. Discovery iterates exactly this table: a target the operator never added is never contacted, and there is no network-range scan anywhere in `internal/ingress`.

## 11. Ceph network awareness (`internal/ceph`, T-1503)

Not app-owned data at all — **no new SQLite table**. Ceph's public/cluster network CIDRs and OSD placement are PVE's own knowledge (`GET /cluster/ceph/config`, `GET /nodes/{node}/ceph/osd`, read live via the existing `internal/pve.Client` — no new credentials), re-projected fresh against the live inventory graph on every `GET /ceph/status` call (`internal/ceph.Project`, pure and cheap — nothing here is cached as authoritative, per `docs/architecture.md` §7's new-domain invariant). **Read-only forever**: no `ceph.*` changeset op exists anywhere in `internal/change` (§3's op-group table), and no write-scoped Ceph API client exists anywhere in this codebase — PVE's own Ceph tooling (`pveceph`, the GUI's Ceph panel) keeps sole ownership of Ceph configuration.

```go
type OSD struct {
    Ref            inventory.Ref // Kind "ceph-osd", "osd<id>" — see §1's Ref.Kind enum
    Device         string
    Node           string
    ID             int
    Up, In         bool
}

type NodeAttribution struct { // one entry per node hosting >=1 OSD
    Node                                                 string
    PublicCarrier, ClusterCarrier                        inventory.Ref // the Bridge/VlanIface whose address falls in the declared CIDR — zero if unresolved
    PublicPath, ClusterPath                               []inventory.Ref // carrier -> ... -> terminal PhysNics (internal/topology.ResolvePhysicalPath, reused not re-implemented)
    PublicRidingOn, ClusterRidingOn                        inventory.Ref // the bond (if bonded) or sole bare NIC (if not) — zero if ambiguous
    PublicMTU, ClusterMTU                                  int
}

type OSDAttribution struct { OSD OSD; PublicBond, ClusterBond inventory.Ref } // denormalized per OSD, "which OSDs ride which bonds"

type Overlay struct {
    PublicNetwork, ClusterNetwork string
    Nodes                         []NodeAttribution
    OSDs                          []OSDAttribution
}
```

`Status` (`internal/ceph.Discover`'s output — `PublicNetwork`, `ClusterNetwork`, `[]OSD`) is read once at daemon startup (a Ceph network declaration changes on the order of a cluster's lifetime, the same rationale `cmd/vnproxd/serviceclassify.go` already documents for corosync's ring addresses); `Overlay` (`Project`'s output, above) is recomputed against the graph's *current* snapshot on every read — "continuously computed" means re-projecting fresh topology, not re-polling PVE's Ceph config every cycle. `PublicNetwork`/`ClusterNetwork` are registered with T-1504's `internal/flow.Classifier` (`flow.NewCIDRSource`, `NetworkSourceKindCeph`) so `GET /flows`'s `serviceClass` tags `ceph-public`/`ceph-cluster` traffic — T-1503 supplies network declarations, it implements no classification logic of its own (§ "Flows" in `docs/api.md`).
