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
                // "lldp-neighbor","fw-ruleset","node"
    Node string // "" for cluster-scoped entities (SDN, cluster firewall)
    ID   string // stable within (Kind,Node), e.g. "vmbr0", "eno1", "zone1/vnet1"
}
```

Selected entities (full field lists are the implementing task's responsibility; these fields are the contract other packages rely on):

| Type | Key fields |
|---|---|
| `PhysNic` | name, mac, driver, speedMbps, duplex, linkUp, mtu, pciAddr, sriovVFs, pending |
| `Bond` | name, mode (802.3ad, active-backup, ...), slaves []string, lacpRate, xmitHashPolicy, miiStatus, activeSlave, mtu, pending |
| `Bridge` | name, kind (linux|ovs), ports []Ref, vlanAware bool, vids []VidRange, stp, mtu, addresses []CIDR, gateway, comments, pending, fdb []FDBEntry (T-306, added retroactively per docs/development.md's definition-of-done #4: `{mac, port, vlan, master bool, permanent bool, stale bool}`, host-netlink-only — excluded from the merge/provenance/delta machinery every other field here goes through, since FDB churns on every poll and has only one contributing source; see `internal/topology.FDB`/`FDBSearch` for the cluster-wide, ownership-labeled view built from it) |
| `VlanIface` | name, parent Ref, vid, addresses, mtu, pending, virt ("" \| "ovs", T-407: distinguishes a plain 802.1q VLAN sub-interface from an OVS Int Port — OVSIntPort has no dedicated `Kind` of its own the way OVSBridge/OVSBond do, so `virt` carries the distinction, mirroring `Bridge.virt`'s exact shape/precedence), trunks []VidRange (T-407, OVS-only: additional trunked VLAN ranges alongside `vid`'s single access tag, from ovs-vsctl's Port `trunks` column / the interfaces(5) `ovs_options trunks=...` token) |
| `SdnZone` | id, type (simple|vlan|qinq|vxlan|evpn), bridge, mtu, nodes []string, controller, vrfVxlan, ipam |
| `SdnVnet` | id, zone, tag, alias, vlanAware |
| `SdnSubnet` | id (cidr), vnet, gateway, snat bool, dhcpRanges, dnsZonePrefix |
| `Guest` | vmid, name, type (qemu|lxc), node, status |
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

CREATE TABLE metric_samples (
  ref TEXT NOT NULL, at INTEGER NOT NULL,
  rx_bytes INTEGER, tx_bytes INTEGER, rx_pkts INTEGER, tx_pkts INTEGER,
  rx_errs INTEGER, tx_errs INTEGER, rx_drop INTEGER, tx_drop INTEGER,
  PRIMARY KEY (ref, at)
);  -- pruned to 24h; longer horizons are out of scope for v1

CREATE TABLE kv (k TEXT PRIMARY KEY, v TEXT NOT NULL);
```

## 3. Changeset operations

`Op` is a tagged union serialized as `{"op": "<type>", "target": Ref, "params": {...}}`. The v1 op vocabulary:

| Group | Ops |
|---|---|
| iface | `iface.update` (mtu, comments, addresses, gateway, autostart), `iface.raw.replace` (node + full file content; T-208's raw editor escape hatch — target is a `node` Ref, not one entity; validated by diffing the parsed entity delta between the live file and the new content through the same referential/safety/advisory checks — see docs/features/change-management.md §7) |
| bond | `bond.create`, `bond.update`, `bond.delete` |
| bridge | `bridge.create`, `bridge.update`, `bridge.delete`, `bridge.port.add`, `bridge.port.remove` |
| vlan | `vlan.create`, `vlan.update`, `vlan.delete` |
| bridge/bond kind | OVS bridges/bonds reuse the same op names above with `target.kind` = `ovs-bridge`/`ovs-bond` (Kind alone disambiguates Linux vs OVS for these two, per Ref's closed kind set) — no separate op types. `bond.create`'s params carry an OVS-only `bridge` field (the OVS bridge this bond attaches to, rendered as `ovs_bridge`; required when `target.kind` is `ovs-bond`, ignored otherwise) — this is the "params carry ovs-specific fields" case data-model review flagged for T-407. |
| vlan kind | `KindVlan` has no dedicated OVS sibling (an OVS Int Port is a `VlanIface` with `virt: "ovs"` — see the entity table above), so `vlan.create`'s params instead carry `ovs` (bool: true selects `ovs_type=OVSIntPort`/`ovs_bridge`/`ovs_options` instead of `vlan-raw-device`; `parent` then names an OVS bridge) and `trunks` (`[]VidRange`, OVS-only, rejected when `ovs` is false: additional trunked VLAN ranges alongside `vid`'s single access tag). T-407. |
| sdn | `sdn.zone.create/update/delete`, `sdn.vnet.create/update/delete`, `sdn.subnet.create/update/delete`, `sdn.apply` |
| guest | `guest.nic.update` (reattach bridge/vnet, vid, rate, firewall flag) |
| fw | `fw.rule.create/update/delete/move`, `fw.options.update`, `fw.alias.*`, `fw.ipset.*`, `fw.group.*` |
| ipam | `ipam.alloc.create/delete` |

Each op maps to one or more **apply steps**; the planner orders steps: (1) cluster-scope PVE API calls, (2) per-node interface file staging, (3) per-node `ifreload` via PVE network reload endpoint, (4) `sdn.apply` last when present. Rollback executes the inverse from the pre-snapshot in reverse order.
