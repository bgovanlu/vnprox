# API design

Base: `https://<node>:8007/api/v1`. JSON everywhere. This document is a **contract** — implementation tasks in different phases depend on these routes and shapes matching exactly.

## Conventions

- Auth: session cookie `vnprox_session` (HttpOnly, Secure, SameSite=Strict) + `X-VNPROX-CSRF` header on mutating requests.
- Errors: `{"error": {"code": "string", "message": "human readable", "details": {}}}` with proper HTTP status. Codes are stable identifiers (`validation_failed`, `pve_denied`, `changeset_locked`, `peer_unreachable`, `peer_incompatible`, ...).
- IDs: entities use `Ref` triplets `kind:node:id` in URLs, URL-encoded (cluster-scoped: empty node, `sdn-vnet::zone1/vnet1`).
- Pagination: `?limit=&cursor=` on list endpoints that can grow (audit, snapshots).
- All timestamps are unix seconds UTC.

## Auth

| Method | Path | Purpose |
|---|---|---|
| POST | `/auth/login` | `{username, password, realm, otp?}` → sets cookie, returns `{user, caps}` |
| POST | `/auth/logout` | destroy session |
| GET | `/auth/me` | current user + capability flags `{caps: {netRead, netWrite, sdnRead, sdnWrite, fwRead, fwWrite, guestNet, audit}}` per node |

## Inventory & topology

| Method | Path | Purpose |
|---|---|---|
| GET | `/topology` | full projected topology: `{nodes:[...], edges:[...], layers, generatedAt, staleness?}` with optional `?layers=phys,l2,sdn,guest&node=<name>&vlan=<vid>` filters |
| GET | `/inventory/{ref}` | full detail for one entity, including raw source (interfaces stanza / PVE API object) — see shape below |
| GET | `/inventory/search?q=` | fuzzy search across names, MACs, IPs, VMIDs, comments |
| GET | `/lldp` | all LLDP neighbors cluster-wide (fanned out to peers): `{items: [{ref, node, localIface, protocol, chassisName, chassisId, chassisIdType?, portId, portIdType?, portDescr?, mgmtIps?, pvid?, taggedVlans?, speedMbps?, speedDescr?, ttl?, lastSeen?}]}`. Cluster fan-out to peer nodes lands with T-303; today's response covers whichever node(s) this daemon's own collector has polled (the local node; single-node clusters are already complete). |
| GET | `/lldp/vlan-check` | VLAN cross-check findings (docs/features/lldp-discovery.md §2): `{items: [{bridgeRef, neighborRef, code, severity, message, expected: [string], advertised: [string], missing?: [int], extra?: [int]}]}`. `code` is one of `vlan_cross_check_ok`\|`vlan_cross_check_missing_on_switch`\|`vlan_cross_check_missing_on_bridge`. Added by T-302 (not in the original contract; documented here per docs/development.md's definition-of-done #4). |
| GET | `/ports` | flat ports table (docs/features/lldp-discovery.md §2): `{items: [{node, nic, switch, port, speedMbps?, speedDescr?, pvid?, taggedVlans?, lastSeen?, stale}]}`; `?format=csv` returns the same rows as `text/csv` (`Content-Disposition: attachment`) with columns `node,nic,switch,port,speedMbps,pvid,taggedVlans,lastSeen,stale`. `stale` is true once a neighbor has greyed (2×TTL) or aged past 10 minutes — dropped-from-map entries (docs/features/lldp-discovery.md §3) still appear here, tagged stale, for troubleshooting unplugged links. Added by T-302 (not in the original contract; documented here per docs/development.md's definition-of-done #4). |
| GET | `/fdb` | MAC/FDB browser (docs/features/lldp-discovery.md §4): `{items: [{node, bridge, bridgeRef, mac, port?, vlan?, master?, permanent?, stale, owner, ownerRef?, ownerLabel?, score?}]}`, cluster-wide. With no `?mac=`, every learned entry (sorted node/bridge/mac); with `?mac=<query>` (full or partial, separators optional), ranked partial-match results (`score` populated, exact > prefix > substring > fuzzy — same scoring as `/inventory/search`), capped at 50. `owner` is `guest` (`ownerRef` is the owning Guest's ref), `vnprox-known` (`ownerRef` is a PhysNic elsewhere in the cluster — a recognized infra MAC, not a guest), or `unknown` (no match — typically an external device on an uplink/trunk port). Added by T-306 (not in the original contract; documented here per docs/development.md's definition-of-done #4). |
| GET | `/drift` | cross-node consistency report: `[{check, severity, nodes, detail}]` |
| POST | `/drift/{id}/fix` | create a fixing changeset draft from a fixable finding's computed op patch → returns the changeset (same shape as `POST /changesets`); `404 not_found` if `id` no longer names a current, fixable finding |
| GET | `/findings` | T-602's unified findings stream, spanning drift + LLDP mismatch + IPAM conflicts + health checks: `{items: [Finding]}`. Optional `?source=&severity=&node=` filters, each independently optional and AND-combined (never a 400 for an unrecognized value — it just matches nothing for that filter). |
| POST | `/findings/{id}/fix` | same contract as `POST /drift/{id}/fix`, generalized across every producer: creates a fixing changeset draft from a fixable unified finding's computed op patch; `404 not_found` if `id` no longer names a current, fixable finding. Only drift-sourced findings have a computable fix today (bridge-property harmonization, MTU alignment); every other producer's findings are detection-only. |

**`GET /drift` finding shape** (added by T-305; documented here per docs/development.md's definition-of-done #4 — the base `{check, severity, nodes, detail}` fields are the original contract, `id`/`refs`/`fixable` are additive): `{id, check, severity, detail, nodes: [string], refs?: [string], fixable}`. `check` is one of `bridge_divergence`\|`mtu_consistency`\|`sdn_realization`\|`pending_interfaces`\|`file_runtime_divergence` (docs/features/topology.md §6's five families); `severity` is `error`\|`warning`\|`info`, reusing the changeset finding vocabulary. `id` is a stable key (derived from `check` plus the finding's affected refs/nodes, never random or time-based) — the same finding on an unchanged cluster keeps the same `id` across drift cycles. `fixable` is true iff `POST /drift/{id}/fix` will succeed for `id` right now (bridge-property harmonization and MTU alignment are the only two families with a computable fix as of T-305; presence/SDN-realization/pending/file-vs-runtime findings are detection-only). Cluster-wide fan-out (peer nodes' own drift findings, mirroring `GET /audit`/`GET /snapshots`) is not yet implemented — today's response covers only this daemon's own view of the cluster-wide inventory graph, which is already complete for every node the PVE API and this daemon's own host poller can see (see `GET /topology`'s staleness note for the same host-poller-locality caveat).

**`GET /findings` finding shape** (T-602, docs/features/monitoring.md §5 — internal/findings.Finding): `{id, source, check, severity, detail, nodes: [string], refs?: [string], fixable, docsLink?}`. `source` is one of `drift`\|`lldp`\|`ipam`\|`health` — which producer emitted the finding; `check` is that producer's own check name (drift's five families unchanged; lldp's `vlan_cross_check_missing_on_switch`\|`vlan_cross_check_missing_on_bridge` — `vlan_cross_check_ok` matches are not surfaced as findings; health's own: `error_drop_rate`, `bond_slave_down`, `mtu_consistency` (via the drift adapter — MTU path-mismatch is drift's own check, source `drift`, folded in unchanged), `stp_topology_burst`, `bridge_no_carrier`, `service_down`, `stale_pending_interfaces`). `id` is globally stable and unique across every producer (`"<source>:<producer-id>"` for drift/lldp, `"health:<check>|<refs-or-nodes>"` for health checks) — never random or time-based. `docsLink` is a relative path into this repo's docs (e.g. `docs/features/monitoring.md#5-health-checks`) pointing at remediation guidance; present on every finding that isn't `fixable` (the "remediation: a fixing changeset where computable, docs link otherwise" contract). IPAM (`source: "ipam"`) is not yet populated — T-405 is still a stub package as of T-602; the stream simply has zero IPAM-sourced findings until it lands. Health checks apply hysteresis (debounce) before firing/clearing, so a finding's presence lags a raw threshold breach/clear by a few cycles — see docs/features/monitoring.md §5 and internal/findings' health_*.go doc comments for each check's specific window.

**`GET /topology` finding badge.** The `"drift"` node/edge badge (docs/features/topology.md §2: "drift = dashed outline") is, since T-602, painted from the unified `/findings` stream when wired (any node named by any producer's `refs`, not only drift's) rather than from `/drift` alone — the wire badge name is unchanged for frontend/doc backward compatibility, but its meaning is now "this entity is named by at least one open finding of any kind."

**`GET /topology` staleness.** The response carries an optional top-level `staleness` object (omitted when the daemon has no collector status, e.g. collectors failed to initialize) describing how fresh the data behind the map is, per collector source — the feature spec's greyed-band + staleness-banner state (docs/features/topology.md §5):

```json
"staleness": {
  "stale": false,
  "sources": [
    {"name": "pve", "stale": false, "lastSuccess": 1720512345},
    {"name": "host", "node": "pve1", "stale": false, "lastSuccess": 1720512345},
    {"name": "host", "node": "pve2", "stale": false, "lastSuccess": 1720512340},
    {"name": "host", "node": "pve3", "stale": true, "lastSuccess": 1720512200, "lastError": "peer_unreachable"},
    {"name": "lldp", "node": "pve1", "stale": false, "lastSuccess": 1720512345}
  ]
}
```

- `name` is the collector loop: `pve` (all PVE-derived data, cluster-wide), `host` (netlink + interfaces-file data), `lldp`.
- `node` scopes the source to one cluster node's band; absent = cluster-wide. Since T-303 (documented here retroactively per that task's report note), `host` carries one entry **per cluster node** this daemon can reach — itself directly, every peer through the peer API (docs/architecture.md §1, §5) — so a single unreachable peer's band degrades independently (docs/features/topology.md §5's "greyed from last-known data with a staleness banner and timestamp") without affecting any other node's; `lldp` is still local-node-only pending T-302's own cluster-wide collection.
- `stale` per source flips true after 3 consecutive poll failures (≈ data 3 poll intervals old); top-level `stale` is true iff any source is stale. `lastSuccess` (unix seconds, omitted if no poll has ever succeeded) is the "data as of" timestamp for the banner; `lastError` (string, present while a source is failing) is the most recent poll error.

**`GET /inventory/{ref}` response shape.** `{ref, kind, node, label, fields, provenance, rawSource?, related, generatedAt}`:

- `fields` — the resolved entity's canonical fields (JSON object).
- `provenance` — per resolved field: `{"<field>": {"owner": "<source>", "conflicts": [{"source", "value"}]}}` (which source won the merge, and any dissenting values).
- `rawSource` — the raw source text behind the entity, keyed by contributing source name; omitted when no source retained raw text. Every value is a **string**: for `host-interfaces` the verbatim interfaces(5) stanza (byte-identical to the file); for `pve-network`/`pve-sdn`/`pve-guest`/`pve-firewall`/`pve-cluster` the pretty-printed JSON of the PVE API object; for `host-netlink`/`host-lldp` compact JSON of the observed state. Example: `"rawSource": {"host-interfaces": "auto vmbr0\niface vmbr0 inet static\n...", "pve-network": "{\n  \"iface\": \"vmbr0\", ...\n}"}`.
- `related` — edges incident to the entity: `[{ref, edgeKind, direction: "from"|"to"}]`.

## Changesets (the only write path)

| Method | Path | Purpose |
|---|---|---|
| GET | `/changesets?status=` | list |
| POST | `/changesets` | create draft `{title, ops:[Op]}` → changeset with validation findings |
| GET | `/changesets/{id}` | full changeset incl. findings, plan, apply log |
| PUT | `/changesets/{id}` | replace ops on a draft (revalidates) |
| POST | `/changesets/{id}/validate` | re-run validation, returns findings |
| GET | `/changesets/{id}/diff` | rendered diff: per-file unified diffs + structured op summaries |
| POST | `/changesets/{id}/apply` | `{confirmTimeoutSec: 120}` → `202`, status `applying` → `awaiting_confirm` |
| POST | `/changesets/{id}/confirm` | commit within the window |
| POST | `/changesets/{id}/rollback` | manual rollback (also valid on `committed` within the 7-day rollback window — beyond it: 409 `rollback_window_expired`; the window matches `[retention].snapshot_pin_days`) |
| DELETE | `/changesets/{id}` | discard draft |

Validation finding shape: `{severity: "error"|"warning"|"info", code, message, ref?, fix?}` where `fix` is an optional machine-applicable amendment (an `[]Op` patch the UI can offer one-click).

**SDN apply orchestration (added by T-402; documented here retroactively per docs/development.md's definition-of-done #4).** A changeset carrying `sdn.zone/vnet/subnet.*` ops and a trailing `sdn.apply` renders a plan whose cluster-scope SDN steps (`kind: "sdn_stage"`, one per op) come first and whose `sdn_apply` step is always last (docs/data-model.md §3). Each `apply_log` step gains an optional `taskUpid` field: for the `sdn_apply` step, the PVE task's UPID, populated as soon as the task starts (even on failure) so the UI can deep-link to that task's log on its node (`node` on the same step entry). If the underlying `PUT /cluster/sdn` task itself succeeds but T-402's post-apply per-zone health check (`GET /cluster/sdn/zones/{zone}/status`) finds a member node unhealthy, `POST /changesets/{id}/apply` fails with a `422` and the stable code `sdn_zone_unhealthy`, `details: {zone, node, status, detail, taskUpid, taskNode}`. `POST /changesets/{id}/rollback` on an SDN-carrying changeset additionally reverts the SDN portion (pre-apply zone/vnet/subnet config diffed against current and re-applied) — this needs the requesting user's own PVE ticket exactly like apply does, so a rollback with no live session (the auto commit-confirm-timeout path) skips the SDN portion and records it in `apply_log.rollback` rather than failing outright; the node-file portion still rolls back.

### Raw interfaces editor (T-208)

| Method | Path | Purpose |
|---|---|---|
| GET | `/nodes/{node}/interfaces/raw` | current live `/etc/network/interfaces` content + hash: `{node, content, sha256}` — the raw Monaco editor's "open" call |
| POST | `/interfaces/lint` | `{content}` → interfaces(5) syntax check: `{errors: [{line, message}]}` (empty array when content parses cleanly); pure and node-less — no changeset or node state involved |

Saving the raw editor creates a changeset whose single op is `iface.raw.replace` (target: a `node` Ref, i.e. `node:<node>:<node>` — the op replaces the whole file, not one entity; params: `{content, baseHash?}`). `baseHash` is the `sha256` the editor read at open time: `POST /changesets`/`PUT /changesets/{id}` compares it against the node's live file at validation time and, on a mismatch, returns the changeset with a blocking `raw.hash_conflict` finding (client should prompt the user to reload the file and reapply). `GET /changesets/{id}/diff` renders the usual full-file unified diff for this op; `POST /changesets/{id}/validate` runs the normal T-202/T-203 pipeline against the *entity delta* between the live file and the new content (e.g. a raw edit that removes the management bridge's stanza produces the same `safety.protected_interface`/`safety.guest_bearing_bridge` findings a `bridge.delete` op would).

Decode errors on POST/PUT bodies return `400 validation_failed` with `details.path` identifying the offending field, op-indexed for multi-op bodies (e.g. `ops[3].params.mtu`).

### Protected interfaces (safety-interlock configuration)

Added by T-203 (documented here retroactively per that task's report note; pinned by `internal/api` tests):

| Method | Path | Purpose |
|---|---|---|
| GET | `/protected-interfaces` | current confirmed set: `{nodes: {"<node>": ["<ref>", ...]}, updatedBy?, updatedAt, version}` |
| GET | `/protected-interfaces/suggest` | detection-suggested set (inventory + corosync.conf): `{nodes: {...}}`, same shape PUT accepts |
| PUT | `/protected-interfaces` | replace the set `{nodes: {...}}` (netWrite + CSRF); refs must parse and each ref's node must match its map key → else `400 validation_failed` with `details.refs` |

## Snapshots / time machine

| Method | Path | Purpose |
|---|---|---|
| GET | `/snapshots?limit=&cursor=` | list (paginated, cluster-merged) |
| POST | `/snapshots` | manual snapshot `{note}` |
| GET | `/snapshots/{id}` | metadata + file list |
| GET | `/snapshots/diff?from=&to=` | unified diffs between two snapshots (or `to=live`) |
| POST | `/snapshots/{id}/restore` | creates a **changeset draft** that would restore this state (goes through normal review/apply) |

Paginated list response envelope (`/snapshots`, `/audit` below): `{items: [...], nextCursor?: "<opaque>", partial?: boolean, failedNodes?: [string]}` — `nextCursor` is present iff there is a further page; pass it back verbatim as `?cursor=` to fetch it. An empty `?cursor=` (or omitted) starts from the newest item. Added by T-303 (retroactive doc note per that task's report): `partial`/`failedNodes` are cluster-fan-out fields (docs/architecture.md §7 — both tables are node-local app data, so a cluster-wide list re-queries every peer and merges the pages) — `partial` is present and `true` iff one or more peers (or peer discovery itself) could not be reached for this page, and `failedNodes` names them; both are omitted entirely on a fully-successful page, including every single-node deployment (zero peers) and every pre-T-303 caller's exact original response shape.

Snapshot shape (list item and, with an added `files` array, the `/snapshots/{id}` detail response): `{id, kind: "pre"|"post"|"manual"|"scheduled", changesetId?, note?, takenAt, nodes: [string], files?: [{node, path, sha256}]}` — file *content* is never inlined (it lives in the content-addressed, zstd-compressed blob store, deduplicated by sha256); fetch it via `/snapshots/diff`.

`/snapshots/diff` response: `{files: [{node, path, unified, changed}]}` — only entries with `changed: true` carry a non-empty `unified` diff (mirrors the changeset diff's `FileDiff` shape, minus the op summaries, since a snapshot diff is raw file state, not changeset ops).

## Audit

| Method | Path | Purpose |
|---|---|---|
| GET | `/audit?user=&action=&target=&result=&changesetId=&from=&to=&limit=&cursor=` | filtered, paginated audit log (docs/features/change-management.md §8) |

Requires the `audit` capability (viewing vnprox's own audit log), not `netRead`. Every filter param is optional and ANDed together; `from`/`to` are unix seconds, inclusive. Response: `{items: [{id, at, username, action, target?, changesetId?, result, detail?}], nextCursor?, partial?, failedNodes?}` (the last two per the pagination envelope note above) — `detail` is the action-specific structured detail object (e.g. `{"stepCount":3}`), opaque JSON. Every T-205 apply-engine lifecycle action (`changeset.apply`, `changeset.confirm`, `changeset.rollback`, `changeset.timer_rearm`, `changeset.recover`, `changeset.safety_override`) and this task's `snapshot.create` / `snapshot.restore` appear here; each row's `changesetId` (when present) links to that changeset and, transitively, its snapshots.

Cluster fan-out (T-303): each node's audit log is node-local (docs/architecture.md §7), so `GET /audit` re-queries every reachable peer (via `GET /api/peer/audit` below) with the same filter/cursor/limit and merges the pages with the local one, newest-first, filtered per peer before merging (not fetched-then-filtered) — see the pagination envelope note for the `partial`/`failedNodes` fields this produces. A peer-supplied row is not otherwise distinguished from a local one in the response shape.

## Firewall, SDN, IPAM (read views; writes are changeset ops)

| Method | Path | Purpose |
|---|---|---|
| GET | `/firewall/rulesets?scope=` | cluster/node/guest rulesets, resolved with group expansion |
| GET | `/firewall/objects` | aliases, ipsets, security groups |
| GET | `/firewall/log?node=&vmid=&direction=&action=&limit=` | cluster-wide pve-firewall log tail, filtered, correlated to rules where determinable |
| GET | `/firewall/effects?group=` | rule-effects preview: guests a security group's rules actually reach |
| GET | `/sdn` | zones → vnets → subnets tree + per-node apply/health status |
| GET | `/sdn/evpn/status` | FRR/BGP peering state per node (peers, prefixes, up/down) |
| GET | `/ipam/subnets` | subnets with utilization counts |
| GET | `/ipam/subnets/{cidr}/allocations` | allocation grid data |

**`GET /sdn` response shape** (added by T-401; documented here per docs/development.md's definition-of-done #4 — the original contract was the one-line purpose above): `{zones: [Zone], generatedAt}`. `Zone` is `{id, type, bridge?, controller?, nodes?, exitNodes?, peers?, mtu?, vrfVxlan?, pending?, nodeStatus: [NodeStatus], pendingDiff?: PendingDiff, vnets: [Vnet]}`; `Vnet` is `{id, zone, alias?, tag?, vlanAware?, pending?, pendingDiff?: PendingDiff, subnets: [Subnet]}`; `Subnet` is `{id, vnet, cidr, gateway?, dhcpRangeStart?, dhcpRangeEnd?, snat?, pending?, pendingDiff?: PendingDiff}` (`id` is the CIDR, per docs/data-model.md's `SdnSubnet.ID` doc comment). `pending` mirrors PVE's own staged-edit marker (`""`\|`"new"`\|`"changed"`\|`"deleted"`).

`NodeStatus` is `{node, status, detail?}` — `status` is `"ok"`\|`"pending"`\|`"error"` (docs/features/sdn.md §1: "every level shows per-node realization status").

`PendingDiff` renders docs/features/sdn.md §1's "staged-vs-running as a first-class diff": `{state, changedFields?: [string], running?: {...}, staged?: {...}}`. `state` is `"new"`\|`"changed"`\|`"deleted"`; an in-sync object (`pending` is empty) carries no `pendingDiff` field at all — omitted entirely, not present-but-empty. `changedFields`/`running` are only populated for `state: "changed"` (a "new" object has nothing to diff against; a "deleted" object's fields haven't changed, only its existence is about to). `running`/`staged` are the two full objects (JSON-shaped, `id`/`pending` stripped) the UI renders the delta from.

`GET /sdn` reads PVE directly and live on every request (not the poll-cached inventory graph `GET /topology` renders) via `?running=1` on the underlying `GET /cluster/sdn/{zones,vnets,vnets/{v}/subnets}` calls — a mock-server-only extension (`internal/pvemock`) mirroring real PVE's own running-vs-pending query convention, added alongside this route so the staged-vs-running diff is never stale relative to what an apply would actually do. SDN configuration is cluster-scoped PVE data (replicated via pmxcfs), so the local node's own PVE API always has the complete view regardless of which node's vnproxd the browser is talking to — no peer fan-out.

**`GET /ipam/subnets` / `GET /ipam/subnets/{cidr}/allocations` response shapes** (added by T-405; documented here per docs/development.md's definition-of-done #4 — the original contract was the one-line purposes above). Gated on the `sdnRead` capability (there is no dedicated `ipamRead` flag in the documented `/auth/me` capability set — IPAM is an SDN-adjacent read view, and vnprox reads through PVE's own configured IPAM plugin transparently per docs/features/ipam.md §1); flagged here as the smallest reasonable extension where the contract didn't fully specify one.

`GET /ipam/subnets` → `{items: [Subnet], generatedAt}`. `Subnet` is `{cidr, zone?, vnet?, gateway?, node?, source: "sdn"|"bridge", readOnly?, dhcpEnabled?, total, allocated, observed, conflicts, utilization}` — one row per currently-configured SDN subnet (`source: "sdn"`, live from PVE) plus one row per detected non-SDN subnet derived from a bridge address (`source: "bridge"`, `readOnly: true`, `node` set to the node the bridge was observed on — docs/features/ipam.md §2). `allocated`/`observed` each count every address `GET .../allocations` would render with that source contributing (not a mutually-exclusive partition — an address both allocated and observed, e.g. a "both"-confidence cell, counts toward both); `utilization` is `allocated/total`.

`GET /ipam/subnets/{cidr}/allocations[?block=][&format=csv]` → `{cidr, prefix, total, paged, blocks?: [BlockSummary], block?, cells?: [Cell], conflicts: [Conflict], readOnly?, generatedAt}`. For a subnet with <=256 addresses (a /24 or smaller), `paged` is `false` and `cells` carries every address in the subnet. For a larger subnet, the default (no `?block=`) response has `paged: true` with `blocks` (one summary per /24-sized — or, for IPv6, /120-sized — block) and no `cells`; passing `?block=<cidr>` (one of `blocks`' own `cidr` values) returns that one block's full `cells` instead, with `block` echoing the requested block. `?format=csv` returns `text/csv` (`Content-Disposition: attachment`) instead of JSON: one row per non-free address across the *whole* subnet regardless of paging (columns `ip,state,confidence,hostname,mac,vmid,sources`), not a literal dump of the address space.

`Cell` is `{ip, state, confidence?, hostname?, mac?, vmid?, guestRef?, sources?: [string]}`. `state` (docs/features/ipam.md §2's grid legend) is `"free"`\|`"allocated"`\|`"reserved"`\|`"observed"`\|`"gateway"`\|`"conflict"` — the cell's render color. `confidence` (docs/features/ipam.md §1's multi-source-merge label) is `"allocated"`\|`"observed"`\|`"both"`\|`"conflict"`, omitted for a free or pure-gateway cell — a related but distinct axis from `state`: e.g. an allocated-but-dark address (docs/features/ipam.md §2) renders `state: "conflict"` (it needs attention) while `confidence` stays `"allocated"` (it genuinely is PVE-IPAM-sourced only). `sources` lists which collector(s) contributed (`"pve-ipam"`, `"guest-agent"` today; `"neighbor"`/`"dhcp-lease"` are reserved for the ARP/peer-API and T-406 DHCP-lease enrichment sources — see this task's report). `BlockSummary` is `{cidr, total, allocated, observed, conflicts, utilization}`.

`Conflict` (docs/features/ipam.md §2: "Each conflict is a health finding with suggested resolution") is `{type, severity, ips: [string], message, suggestion}`. `type` is `"duplicate_ip"` (two+ guests, or an IPAM record and reality, disagree about who owns an address), `"observed_unallocated"` (an address is actively in use with no IPAM reservation — a squatter), or `"allocated_dark"` (an IPAM reservation whose VMID/MAC corresponds to no currently-known guest — a stale record); `severity` mirrors the changeset finding vocabulary (`error`\|`warning`\|`info`).

Like `GET /sdn`, both `/ipam/*` routes read PVE directly and live on every request (every configured IPAM plugin's current allocation set, via PVE's `/cluster/sdn/vnets/{vnet}/ips` write route for reserve/release — mock-server-only, mirrors real PVE's own vnet-scoped IPAM plugin API) plus the cluster's own inventory graph (for guest-agent-reported IPs and bridge-derived subnet detection), so a reserve/release apply is reflected immediately, never waiting on the next poll cycle.

**`GET /sdn/evpn/status` response shape** (added by T-404; documented here per docs/development.md's definition-of-done #4 — the original contract was the one-line purpose above): `{nodes: [NodeStatus], exitNodes: [ExitNodeHealth], findings: [Finding], generatedAt, partial?, failedNodes?}`.

`NodeStatus` (reusing the name but *not* the shape of `GET /sdn`'s own `NodeStatus` above — these are two distinct route-local types) is `{node, frrInstalled, routerId?, asn?, peers: [Peer], vnis: [VNI], error?}`. `frrInstalled` is `false` (with `peers`/`vnis` empty and `error` unset) for a node running no FRR daemon at all — the documented clean "no EVPN" case (docs/features/sdn.md §3), distinct from `error` being set (a real read/parse failure on a node that *does* run FRR, e.g. an unreachable peer). `Peer` is `{peerAddr, peerNode?, addressFamily?, state, stateReason?, remoteAs?, pfxRcd?, pfxSnt?, uptimeSecs?, flapTransitions?}` — one entry per distinct BGP neighbor address, preferring the `l2VpnEvpn` address-family observation when a peer negotiates more than one (the underlying TCP session is the same; EVPN is what this view is about). `state` is FRR's own FSM vocabulary (`Idle`\|`Connect`\|`Active`\|`OpenSent`\|`OpenConfirm`\|`Established`) — the peering matrix's node×peer color (`Established`→green, `Connect`/`Active`/`OpenSent`/`OpenConfirm`→amber, `Idle`→red); `stateReason` is FRR's parenthetical qualifier for a down session when present (e.g. `Idle (Admin)` renders as `state:"Idle", stateReason:"Admin"`) — the closest FRR's summary JSON comes to a "last error", surfaced as such in the session detail panel. `VNI` is `{vni, type, vxlanIf?, tenantVrf?, numMacs?, numArpNd?}`, `type` is `"L2"`\|`"L3"`.

`ExitNodeHealth` is `{zone, node, healthy, detail?}` — one entry per (EVPN zone, exit node) pair from `GET /sdn`'s own zone tree (docs/features/sdn.md §2's exit-node model): `healthy` is true iff the node has FRR installed and every one of its observed peer sessions is `Established`.

`Finding` is `{id, code, severity, node, peerAddr, detail}`, reusing `GET /drift`'s `{id, severity, detail}` finding vocabulary (`severity` is currently always `"warning"` for this route). `code` is `"evpn_bgp_flapping"`: a session whose observed state has changed 3+ times within a trailing 10-minute window (docs/features/sdn.md §3: "Flapping sessions raise a health finding") — `Peer.flapTransitions` is the same session's live transition count, so a UI can render both the raised finding and its current count. Flap detection needs repeated observations over time, which a single live snapshot can never show by itself: the backing `internal/evpn.Service` is long-lived within one daemon process and accumulates a short rolling per-session state history across calls to this route (poll it regularly, e.g. via the same interval-based refetch `GET /sdn` itself would use, for the finding to become and stay accurate).

`partial`/`failedNodes` mirror `GET /audit`/`GET /snapshots`' cluster-fan-out convention: `partial` is true iff peer discovery or an individual peer's FRR read failed (as opposed to that node cleanly reporting no FRR, which is `frrInstalled:false` with no failure at all); `failedNodes` names them. `GET /sdn/evpn/status` fans out across the cluster via the peer API (`GET /api/peer/host/frr/{bgp-summary,evpn-vni}` below) exactly like `internal/collect`'s host poller does for netlink/LLDP data — unlike `GET /sdn`, EVPN/BGP state is node-local FRR daemon state, not cluster-scoped PVE config, so it cannot be read from any single node's PVE API and does need this fan-out.

**`GET /firewall/rulesets?scope=` response shapes** (added by T-501; documented here per docs/development.md's definition-of-done #4 — netRead-gated, read-only). `scope` is `cluster`\|`node`\|`guest`.

- `scope=cluster`: one `RulesetView` — `{ref, scope, node?, enabled, defaultIn?, defaultOut?, rules: [RuleView], banners?: [Banner]}`. 404 `not_found` if the cluster firewall config has not been observed yet.
- `scope=node`: with `?node=<name>`, that node's `RulesetView`; without it, `{items: [RulesetView]}` — every node this daemon has observed a ruleset for (hierarchy navigation).
- `scope=guest`: with `?ref=<guest ref>` (a `guest:<node>:<vmid>` Ref triplet, URL-encoded), `{ruleset: RulesetView, resolved: ResolvedView}` — the guest's own raw ruleset plus its full resolved evaluation order in one payload; without `ref`, `{items: [RulesetView]}` (raw rulesets only, one per observed guest).
- Any other `scope` value: 400 `validation_failed`.

`RuleView`: `{pos, enabled, direction, action, proto?, source?, dest?, sport?, dport?, iface?, macro?, macroExpansion?, log?, comment?}` — mirrors `internal/inventory.FwRule` field-for-field; `macroExpansion` (`[{proto?, dport?}]`) is populated whenever `macro` names a macro this build's built-in catalog knows (docs/features/firewall.md §2's "expansion preview").

`Banner`: `{scope, message}` — the "Datacenter firewall is OFF: none of these rules are active" footgun warning (docs/features/firewall.md §2) and its cascaded node/guest-scope variants, computed by `internal/fw.ScopeBanners`. Present on every `RulesetView` (and, for guest scope, additionally on `ResolvedView.gates`) whenever some scope's own toggle — or an ancestor scope's — means none of that ruleset's rules are actually enforced.

`ResolvedView`: `{guest, active, gates?: [Banner], rules: [ResolvedRuleView], defaultIn: DefaultPolicy, defaultOut: DefaultPolicy}` — a guest's full effective evaluation order per docs/features/firewall.md §1 ("cluster rules → security groups → guest rules → default policies"), computed by `internal/fw.Resolve`. `active` is false iff some gate in `gates` makes every rule inert; `rules` is still populated in that case (transparency — the UI shows what's configured even when it's not enforced). `ResolvedRuleView`: `{origin, groupName?, rule: RuleView, pos}` where `origin` is `cluster`\|`group`\|`guest` (`group` when this entry came from a security group's own rule list, spliced in at the position a `"type":"group"` reference rule named it — `groupName` names it either way). `DefaultPolicy`: `{direction, policy, origin}` where `origin` additionally allows `default` (pve-firewall's own hardcoded fallback, DROP in/ACCEPT out, when neither the guest's own ruleset nor the cluster's set an explicit policy).

**`GET /firewall/objects` response shape** (added by T-501): `{aliases: [ObjectUsage], ipsets: [ObjectUsage], groups: [ObjectUsage], macros: [Macro]}`. `ObjectUsage`: `{kind, scope, name, comment?, count, referencedBy?: [{scope, ref, pos}]}` — `kind` is `alias`\|`ipset`\|`group`; `scope` is the scope the object is *defined* in (cluster-scope objects are referenceable from any rule anywhere; node-/guest-scope objects only from their own ruleset's rules — real pve-firewall's alias/ipset visibility rule); `count`/`referencedBy` are docs/features/firewall.md §2's "referenced by N rules — view" usage tracking, computed by `internal/fw.UsageCounts`. `Macro`: `{name, comment?, ports: [{proto?, dport?}]}` — the built-in service-macro catalog's expansion preview (a representative subset of pve-firewall's own macros, not exhaustive; see `internal/fw`'s `KnownMacros`).

**`GET /firewall/log` response shape** (added by T-505, docs/features/firewall.md §4; documented here per docs/development.md's definition-of-done #4 — netRead-gated, read-only). Every filter param is optional and ANDed together, mirroring `GET /audit`'s convention: `node` (exact match), `vmid` (positive integer), `direction` (`in`\|`out`), `action` (case-insensitive, e.g. `DROP`); `limit` defaults to 500. Response: `{items: [FwLogEntry], droppedTotal, unavailableNodes?}`. `items` is oldest-first (so it reads top-to-bottom the same way subsequent `firewall.log.batch` WS pushes append). `droppedTotal` is the cumulative count of lines the server-side rate cap has dropped since this daemon started (the storm indicator — see the WS section below); `unavailableNodes` (omitted when empty) names any cluster node whose most recent log fetch attempt failed, so the UI can flag that node's contribution as possibly stale/incomplete rather than silently under-reporting it.

`FwLogEntry`: `{seq, node, vmid, guestRef?, direction?, action?, proto?, source?, dest?, sport?, dport?, at?, raw, correlation: Correlation}`. `seq` is a server-assigned, strictly increasing sequence number (stable React key / de-dup id). `guestRef` (a `guest:<node>:<vmid>` Ref triplet) is present iff the line's chain was a recognized guest tap/veth chain. `at` (unix seconds) is omitted when the line's own timestamp could not be parsed. `raw` is the original log line verbatim.

`Correlation`: `{status, rule?: RuleRef, candidatePositions?: [int], reason?}`. `status` is one of `rule` (correlated — `rule` names the deep-link target), `default_policy` (matched a default-policy fallthrough, not a specific rule — by design, not a parsing failure), `ambiguous` (2+ rules match; `candidatePositions` lists their positions, informational only), `unmatched` (no rule matches, e.g. the ruleset changed since the line was logged), `unknown_chain` (not a recognized guest chain — node/cluster-scope log correlation is not evaluated, see `internal/fwlog`'s package doc comment), or `no_guest_data` (this daemon hasn't observed any firewall data for that guest yet). `reason` is a human-readable explanation, always present except for `status: "rule"`. `RuleRef`: `{guestRef, origin, groupName?, pos}` — `origin` is `cluster`\|`group`\|`guest` (mirrors `ResolvedRuleView.origin` above); deep-linking is by `guestRef`+`pos`+`origin` (never DOM position), so it survives the firewall rule-table UI's independent evolution.

Rule correlation is a best-effort heuristic (direction+action match against the guest's resolved evaluation order, narrowed by protocol/port when available), not a re-simulation of pve-firewall's actual evaluation (that is `internal/sim`'s job, T-503): real pve-firewall log lines do not embed a rule position at all (checked against the upstream `pve-firewall` source — see `internal/fwlog`'s package doc comment), contradicting this feature's original premise that "PVE logs include rule references in most drop/reject cases". Ambiguous or unmatched lines are always labeled honestly, never guessed.

**`GET /firewall/effects?group=<name>` response shape** (added by T-502; docs/features/firewall.md §2 P1's "rule effects preview" for a security-group reference): `{group, guests: [Ref]}` — every guest ref (`guest:<node>:<vmid>`) whose resolved evaluation order actually splices in `group`'s own rules, computed by `internal/fw.MatchingGuests` (which calls the same `fw.Resolve` the guest resolved view uses). Given this build's documented cluster-cascade simplification (`internal/fw`'s `Resolve` doc comment: cluster rules apply directly to every guest's resolved view), a group referenced by the *cluster* ruleset matches every observed guest, not just ones that reference it themselves. `group` naming zero, or a nonexistent, group returns `{group, guests: []}` (200), not a 404 — "no matches" is itself the answer a preview needs. `group` missing from the query is 400 `validation_failed`. For a *guest-scope* group reference, the matching set is trivially that one guest — the UI computes that locally without calling this route.

## Path simulator

| Method | Path | Purpose |
|---|---|---|
| POST | `/simulate/path` | `{src: EndpointSpec, dst: EndpointSpec, proto, port}` → `SimulateResult` where `EndpointSpec` is a guest NIC ref, an IP literal, or "external" |

**Response shapes** (added by T-503; netRead-gated — a read-only static analysis over the poll-cached inventory snapshot, it mutates nothing). Documented here per docs/development.md's definition-of-done #4 (the route's one-line purpose above didn't pin a schema).

- `EndpointSpec`: `{kind, ref?, ip?}` — `kind` is `guest-nic`\|`ip`\|`external`. `ref` (a `guest-nic:<node>:<vmid>/<key>` Ref triplet) is required for `guest-nic`; `ip` (a literal address) is required for `ip`; `external` needs neither.
- `proto` is `tcp`\|`udp`\|`icmp`\|omitted (any); `port` is the destination port (omit/0 = any).
- `SimulateResult`: `{verdict, src, dst, proto?, port?, hops: [Hop], blockingRule?, missing?, caveats: [Caveat]}`.
  - **`verdict`** is `allow`\|`deny`\|`unreachable`\|**`indeterminate`**. *Flagged deviation from the original one-liner:* T-503 adds `indeterminate` because the spec's honesty contract (docs/features/firewall.md §6, AC5) forbids a confident allow/deny/unreachable when the engine could not fully evaluate the path — an unknown/unsupported entity kind, an unresolvable firewall alias/ipset/macro, or a firewall decision that would depend on a guest IP the inventory does not carry. An `indeterminate` result always carries at least one `blocker`-severity caveat explaining why, and the UI (T-504) must surface it as "could not determine", never as a pass/fail.
  - `src`/`dst` (`ResolvedEndpoint`): `{kind, ref?, guest?, node?, ip?, ipSource?, attachment?, vid?, zone?, vnet?, subnet?, description?}` — how the engine resolved each endpoint. `ipSource` is `literal`\|`static`\|`ipam`\|`guest-agent`\|omitted (unknown).
  - `Hop`: `{ref?, kind, node?, label, detail?}` — one step of the traced path (guest NIC → bridge/VNet → fabric/overlay/router/exit-node/gateway → …), for T-504's map rendering. `ref` is an inventory Ref string where the hop is a real entity, else a synthetic id (`external`, a fabric segment).
  - `blockingRule` (present only for `deny`): `{enforcementPoint, rulesetRef, origin, groupName?, direction, action, pos, rule}` — `enforcementPoint` is `source-guest-out`\|`dest-guest-in`; `origin` is `cluster`\|`group`\|`guest`; `rule` is a `RuleView` (same shape as `GET /firewall/rulesets`, macro expansion included) for the editor deep-link.
  - `missing` (present only for `unreachable`): `{code, message, atRef?, atNode?}` — `message` is the operator-facing break description (e.g. `"VLAN 30 is not trunked on bond0 of node pve2"`, `"no route between subnets … — different zones without exit node"`).
  - `Caveat`: `{code, severity, message, feature?}` — the honesty-contract disclosures (docs/features/firewall.md §5/§6), **always present** (every result carries at least the standing "simulated"/conntrack/guest-internal-firewall notes). `severity` is `info`\|`warning`\|`blocker`; `code` includes `simulated`, `conntrack`, `snat-asymmetry`, `guest-agent-ip`, `guest-ip-unknown`, `not-evaluated`, `fw-cluster-host-guest-simplification`, `node-firewall-not-on-path`, `ovs-l2`, `lldp-trunk-cross-check`. `feature` names the un-evaluated PVE/SDN feature for `code: not-evaluated`.

The engine is pure (`internal/sim`, no I/O) and reuses `internal/fw.Resolve` (T-501) as its firewall substrate; the API layer passes the live `*inventory.Graph`'s snapshot. Note: guest IPs (IPAM allocations / guest-agent reports) are **not** currently carried in the inventory graph, so a guest endpoint's IP resolves unknown at this route — any firewall rule that restricts by address then yields `indeterminate` with a `guest-ip-unknown` caveat (see the report's honesty inventory). Supplying resolved guest IPs is a documented follow-up.

## Metrics

| Method | Path | Purpose |
|---|---|---|
| GET | `/metrics/live?refs=a,b,c` | current rates for entities |
| GET | `/metrics/history?ref=&fromTs=&toTs=` | 24h ring data |

Added by T-601 (documented here retroactively per docs/development.md's definition-of-done #4). `Rates` (shared by both routes and the `metrics.sample` WS event below): `{rxBps, txBps, rxPps, txPps, rxErrsPerSec, txErrsPerSec, rxDropPerSec, txDropPerSec}` — `*Bps` are bits/sec, everything else is events/sec.

**`GET /metrics/live`** response: `{items: [LiveMetric]}`. `refs` is a comma-separated list of `Ref` strings; a blank/omitted `refs` returns `{items: []}` without erroring. `LiveMetric`: `{ref, at, rates: Rates, speedMbps?, rxUtilPct?, txUtilPct?, utilizationPct?, slaves?: [SlaveRate]}` — `at` is unix seconds; `speedMbps`/`*UtilPct` are omitted when the entity's link speed isn't known (e.g. a bond with no active slave yet); `slaves` (per-slave balance, docs/features/monitoring.md §1) is present only for a Bond ref: `SlaveRate` is `{ref, active, rates: Rates}`. A ref the sampler hasn't observed at least twice yet (no rate computable) is simply absent from `items` — not an error.

**`GET /metrics/history`** response: `{ref, items: [HistoryPoint]}`. `HistoryPoint`: `{at, rates: Rates}` — one entry per stored 30s-downsampled sample after the first (a rate needs a predecessor to diff against), `at` the later sample's unix-second timestamp. `fromTs`/`toTs` default to "no bound on that side" when omitted/unparsable.

## Blueprints

| Method | Path | Purpose |
|---|---|---|
| GET/POST | `/blueprints` | list / save (parameterized topology template JSON) |
| POST | `/blueprints/{id}/instantiate` | `{params}` → changeset draft |

**Blueprint shape** (T-603): `{blueprintVersion: 1, id, name, description?, readOnly?, nodeSelector: {mode: "all"|"single"}, params: [ParamDef], entities: [EntityTemplate], createdBy?, createdAt?, updatedAt?}`. `ParamDef`: `{name, type: "string"|"int"|"bool"|"cidr"|"ip"|"vid"|"vidList"|"iface"|"nodeList", label?, description?, default?, required?, addressSuggest?, subnet?}` — `addressSuggest` (only valid on `cidr`/`ip` params) marks it eligible for `GET /blueprints/{id}/suggest` below; `subnet` is the CIDR pool searched (falls back to the containing network of `default` for a `cidr` param). `EntityTemplate`: `{kind: "bridge"|"bond"|"vlan"|"sdn-zone"|"sdn-vnet"|"sdn-subnet", idTemplate, nodeSelector?, fields: {...}}` — `fields` keys are exactly the corresponding `change.*CreateParams` JSON field names; values may be literal, a `"{{param}}"` placeholder (whole-value substitution preserves the param's JSON type), or the builtin `"{{__nodes__}}"` token (the instantiate request's target node list). `readOnly: true` marks the five bundled starters (`GET /blueprints` always includes them; they cannot be saved-over or deleted — `403 blueprint_read_only`).

**`POST /blueprints/{id}/instantiate` body** is additive to the documented `{params}` shape (per docs/development.md's definition-of-done #4): `{params: {...}, nodes?: [string], title?: string}`. `nodes` is the target cluster node list for `nodeSelector.mode: "all"` expansion and for entities' `"{{__nodes__}}"` substitution; omitted/empty defaults to every node currently in inventory. `title` overrides the default changeset title (`"blueprint: <name>"`). The response is the same shape `POST /changesets` returns (a draft changeset) — instantiation only ever produces a draft; nothing is applied.

Additive routes (T-603, not in the original contract; documented here per docs/development.md's definition-of-done #4):

| Method | Path | Purpose |
|---|---|---|
| GET | `/blueprints/{id}` | single blueprint detail (starter or saved) — also the export/download source |
| DELETE | `/blueprints/{id}` | remove a saved blueprint; `403 blueprint_read_only` for a starter id |
| POST | `/blueprints/capture` | `{node}` → an unsaved Blueprint captured from that node's live bonds/bridges/VLAN interfaces (addresses turned into named, address-suggest-eligible params); save it via `POST /blueprints` to persist |
| GET | `/blueprints/{id}/suggest?param=` | next-free-address suggestion for one of the blueprint's `addressSuggest` params: `{address}` |

Import/export is file-level, not a dedicated route: export is `GET /blueprints/{id}`'s JSON body saved to a file; import is that same JSON re-posted to `POST /blueprints` (with `id` cleared so a new blueprint is created, or set to overwrite an existing saved one — never a starter id).

## WebSocket `/api/ws`

One connection multiplexes all topics; every frame (both directions) is a JSON text message.

**Client → server (subscribe).** The only client message is `{"subscribe": ["topology", "changesets", "metrics:<ref>", "tasks", "firewall.log"]}`. Each subscribe message **replaces** the connection's entire topic set (it is not additive); an empty array unsubscribes from everything. Malformed messages are ignored.

**Server → client (events).** Each event is a **flat** JSON object carrying the event name in an `"event"` field with the payload fields alongside it at the top level — there is no nested payload wrapper. Example `topology.delta` push:

```json
{"event": "topology.delta", "added": [], "updated": ["node/pve1/iface/vmbr0"], "removed": []}
```

(Producers: `internal/topology/hub.go` `deltaEvent`, `internal/change/service.go` `statusEvent`; consumer: `web/src/api/ws.ts`. All future event producers must keep this envelope.)

| Event | Payload fields (alongside `event`) |
|---|---|
| `topology.delta` | `{added, updated, removed: [Ref]}` — Refs as strings; client refetches affected |
| `changeset.status` | `{id, status, confirmDeadline?}` — `confirmDeadline` is unix seconds, omitted unless `awaiting_confirm`; drives the confirm countdown UI |
| `metrics.sample` | `{ref, at, rates}` |
| `drift.changed` | `{count}` |
| `firewall.log.batch` | `{entries: [FwLogEntry], droppedTotal}` — pushed by `internal/fwlog.Service`'s poll loop whenever new log lines were parsed since the previous tick; `entries` is already rate-capped (see the `GET /firewall/log` section above for both shapes) — a client subscribed to `firewall.log` should append `entries` to its own bounded, client-side-capped view and show `droppedTotal` as the storm/drop indicator, never render an unbounded list |
| `findings.changed` | `{count}` — T-602's unified-stream counterpart of `drift.changed`; fires whenever the unified findings ID set changes between the engine's ~30s cycles (subscribe to WS topic `"findings"`). Additive: `drift.changed` keeps firing independently on its own (drift-only) cycle. |

## Peer API (`/api/peer/*`, internal only)

HMAC-authenticated (cluster secret) endpoints between daemons: `GET /api/peer/host/interfaces`, `/api/peer/host/lldp`, `/api/peer/host/stats`, `/api/peer/host/services`, `/api/peer/host/links`, `/api/peer/host/fdb`, `/api/peer/host/frr/bgp-summary`, `/api/peer/host/frr/evpn-vni`, `GET /api/peer/firewall/log`, `POST /api/peer/host/stage-interfaces`, `POST /api/peer/host/ifreload`, `POST /api/peer/host/restore`, `POST /api/peer/host/discard-staged`, `POST /api/peer/host/lldp/install`, `GET /api/peer/health`, `GET /api/peer/version`, `GET /api/peer/audit`, `GET /api/peer/snapshots`, `POST /api/peer/timer/arm`, `POST /api/peer/timer/cancel`, `GET /api/peer/timer/status`. Never exposed in the SPA; rejected without valid HMAC + timestamp (±30s replay window). Protocol version 2 (T-304 added `discard-staged` and the `timer/*` routes below — a peer still advertising protocol 1 cannot serve them, so a coordinator refuses to route multi-node steps to it per the version-skew rule in `docs/architecture.md` §5); T-404's two `frr/*` routes, T-505's `firewall/log` route, and T-602's `host/services` route below are additive reads at the same protocol version 2, following the precedent T-303's `links` and T-306's `fdb` routes already set (new read-only observability routes do not themselves force a version bump — only routes the write/coordination path depends on do, per those tasks' own reports).

**`GET /api/peer/firewall/log?node=&cursor=&maxLines=`** (added by T-505): the requesting daemon's own polling loop (`internal/fwlog.Service.Tick`) calls this once per known peer, per poll tick, exactly like it calls its local `Source.Tail` — `node` names which node's log to read (the peer serves only its own node, mirroring `host/*`'s "Real only ever serves its own node" convention), `cursor` resumes a previous call's `nextCursor` (opaque; omit for an initial tail), `maxLines` defaults to 500 and clamps to 5000 (a higher ceiling than the audit/snapshot pagination routes', since a real log-storm burst needs a single fetch able to catch up rather than trickling in). Response: `{lines: [string], nextCursor}` — raw, unparsed log lines; the requesting daemon parses/correlates them itself (this route has no knowledge of firewall rules, only bytes).

`GET /api/peer/host/services` (added by T-602): `{services: {"dnsmasq": true, "frr": false}}` — host.WatchedServices' unit set; a unit never installed on the node is simply absent from the map, not reported as down. Like the other host/* reads, it does not bump the protocol version since it is additive and callers degrade gracefully (internal/findings' service-down check simply has nothing to report from a peer still on an older build that lacks the route, the same "quietly absent" treatment every other optional producer input gets).

`GET /api/peer/host/links` (added by T-303, documented here retroactively per that task's report note): a node's netlink-equivalent link state (`{links: [LinkState]}`, one entry per physical NIC/bond/bridge/VLAN sub-interface) — the remote-node counterpart of `internal/host.Reader.Links`, letting a peer's daemon fan its host poller out cluster-wide instead of only ever seeing its own node's netlink state.

`GET /api/peer/host/frr/bgp-summary` and `GET /api/peer/host/frr/evpn-vni` (added by T-404): a node's raw `vtysh -c "show bgp summary json"` / `"show evpn vni json"` output, each wrapped as `{available, content?}` rather than passed through raw (unlike `/host/lldp`) — `available` is `false` (`content` omitted) when the node runs no FRR daemon at all, so "FRR entirely absent" travels over the wire as a clean, distinguishable 200 instead of an error status; `content` is the verbatim vtysh JSON otherwise, parsed by the caller (`internal/host.ParseBGPSummary`/`ParseEVPNVNI`). Both accept `?node=`, same convention as every other `/host/*` read. These back `GET /sdn/evpn/status`'s cluster fan-out above.

`GET /api/peer/host/fdb` (added by T-306, per that task's card — T-301's report flagged its absence as this task's deliverable): a node's bridge forwarding-database tables, flattened and bridge-tagged (`{entries: [{bridge, mac, port?, vlan?, master?, permanent?, stale}]}`) out of the same data `Links()` already carries in each entry's `Bridge.FDB` — this route exists as its own endpoint so a caller that only wants the FDB (the MAC/FDB browser, `GET /fdb` above) doesn't have to pull every bridge's full VLAN table and every physical NIC's link state over the wire to get it. `internal/collect`'s host poller does not call this route: bridge FDB reaches the inventory graph (and therefore cluster-wide `GET /fdb`) via the existing `Links()`-based host poll, exactly like every other netlink-observed field — `handleFDB` derives its response from the same `Links()` read `GET /api/peer/host/links` uses, not a second collection path.

`GET /api/peer/audit` and `GET /api/peer/snapshots` (added by T-303): each node's own local page of its `/audit`/`/snapshots` data — `{items: [...], nextCursor?}`, the same shapes as the public `/audit`/`/snapshots` list items, minus the cluster-fan-out `partial`/`failedNodes` fields (a peer only ever reports its own node-local page here; the fan-out/merge happens on the calling daemon, inside `GET /audit`/`GET /snapshots` themselves). `/api/peer/audit` accepts the same filter query params as `GET /audit` (`user`, `action`, `target`, `result`, `changesetId`, `from`, `to`) plus `limit`/`cursor`; `/api/peer/snapshots` accepts `limit`/`cursor` only.

T-304's local-timer protocol (`docs/features/change-management.md` §4: "each node arms its own local timer at step start"): `POST /api/peer/timer/arm` `{changesetId, node, content, deadline}` — persists `content` as the node's pre-apply state to restore and arms a real rollback timer for `deadline` (unix seconds); `POST /api/peer/timer/cancel` `{changesetId, node}` — stops it (idempotent); `GET /api/peer/timer/status?changesetId=&node=` — returns the current record, or 404 `timer_not_found` if this node never armed one for that key. All three return `{record: {changesetId, node, status, deadline, armedAt, resolvedAt?, error?}}`, where `status` is one of `armed|cancelled|rolled_back|rollback_failed`.
