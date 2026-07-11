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
| GET | `/drift` | cross-node consistency report: `[{check, severity, nodes, detail}]` |

**`GET /topology` staleness.** The response carries an optional top-level `staleness` object (omitted when the daemon has no collector status, e.g. collectors failed to initialize) describing how fresh the data behind the map is, per collector source — the feature spec's greyed-band + staleness-banner state (docs/features/topology.md §5):

```json
"staleness": {
  "stale": false,
  "sources": [
    {"name": "pve", "stale": false, "lastSuccess": 1720512345},
    {"name": "host", "node": "pve1", "stale": false, "lastSuccess": 1720512345},
    {"name": "lldp", "node": "pve1", "stale": false, "lastSuccess": 1720512345}
  ]
}
```

- `name` is the collector loop: `pve` (all PVE-derived data, cluster-wide), `host` (netlink + interfaces-file data), `lldp`.
- `node` scopes the source to one cluster node's band (`host`/`lldp` only poll the daemon's local node); absent = cluster-wide.
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

Paginated list response envelope (`/snapshots`, `/audit` below): `{items: [...], nextCursor?: "<opaque>"}` — `nextCursor` is present iff there is a further page; pass it back verbatim as `?cursor=` to fetch it. An empty `?cursor=` (or omitted) starts from the newest item.

Snapshot shape (list item and, with an added `files` array, the `/snapshots/{id}` detail response): `{id, kind: "pre"|"post"|"manual"|"scheduled", changesetId?, note?, takenAt, nodes: [string], files?: [{node, path, sha256}]}` — file *content* is never inlined (it lives in the content-addressed, zstd-compressed blob store, deduplicated by sha256); fetch it via `/snapshots/diff`.

`/snapshots/diff` response: `{files: [{node, path, unified, changed}]}` — only entries with `changed: true` carry a non-empty `unified` diff (mirrors the changeset diff's `FileDiff` shape, minus the op summaries, since a snapshot diff is raw file state, not changeset ops).

## Audit

| Method | Path | Purpose |
|---|---|---|
| GET | `/audit?user=&action=&target=&result=&changesetId=&from=&to=&limit=&cursor=` | filtered, paginated audit log (docs/features/change-management.md §8) |

Requires the `audit` capability (viewing vnprox's own audit log), not `netRead`. Every filter param is optional and ANDed together; `from`/`to` are unix seconds, inclusive. Response: `{items: [{id, at, username, action, target?, changesetId?, result, detail?}], nextCursor?}` — `detail` is the action-specific structured detail object (e.g. `{"stepCount":3}`), opaque JSON. Every T-205 apply-engine lifecycle action (`changeset.apply`, `changeset.confirm`, `changeset.rollback`, `changeset.timer_rearm`, `changeset.recover`, `changeset.safety_override`) and this task's `snapshot.create` / `snapshot.restore` appear here; each row's `changesetId` (when present) links to that changeset and, transitively, its snapshots.

## Firewall, SDN, IPAM (read views; writes are changeset ops)

| Method | Path | Purpose |
|---|---|---|
| GET | `/firewall/rulesets?scope=` | cluster/node/guest rulesets, resolved with group expansion |
| GET | `/firewall/objects` | aliases, ipsets, security groups |
| GET | `/sdn` | zones → vnets → subnets tree + per-node apply/health status |
| GET | `/sdn/evpn/status` | FRR/BGP peering state per node (peers, prefixes, up/down) |
| GET | `/ipam/subnets` | subnets with utilization counts |
| GET | `/ipam/subnets/{cidr}/allocations` | allocation grid data |

## Path simulator

| Method | Path | Purpose |
|---|---|---|
| POST | `/simulate/path` | `{src: EndpointSpec, dst: EndpointSpec, proto, port}` → `{verdict: allow|deny|unreachable, hops:[...], blockingRule?, missing?}` where `EndpointSpec` is a guest NIC ref, an IP literal, or "external" |

## Metrics

| Method | Path | Purpose |
|---|---|---|
| GET | `/metrics/live?refs=a,b,c` | current rates for entities |
| GET | `/metrics/history?ref=&fromTs=&toTs=` | 24h ring data |

## Blueprints

| Method | Path | Purpose |
|---|---|---|
| GET/POST | `/blueprints` | list / save (parameterized topology template JSON) |
| POST | `/blueprints/{id}/instantiate` | `{params}` → changeset draft |

## WebSocket `/api/ws`

One connection multiplexes all topics; every frame (both directions) is a JSON text message.

**Client → server (subscribe).** The only client message is `{"subscribe": ["topology", "changesets", "metrics:<ref>", "tasks"]}`. Each subscribe message **replaces** the connection's entire topic set (it is not additive); an empty array unsubscribes from everything. Malformed messages are ignored.

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

## Peer API (`/api/peer/*`, internal only)

HMAC-authenticated (cluster secret) endpoints between daemons: `GET /api/peer/host/interfaces`, `/api/peer/host/lldp`, `/api/peer/host/stats`, `POST /api/peer/host/stage-interfaces`, `POST /api/peer/host/ifreload`, `POST /api/peer/host/restore`, `POST /api/peer/host/discard-staged`, `GET /api/peer/health`, `GET /api/peer/version`. Never exposed in the SPA; rejected without valid HMAC + timestamp (±30s replay window). Protocol version 2 (T-304 added `discard-staged` and the `timer/*` routes below — a peer still advertising protocol 1 cannot serve them, so a coordinator refuses to route multi-node steps to it per the version-skew rule in `docs/architecture.md` §5).

T-304's local-timer protocol (`docs/features/change-management.md` §4: "each node arms its own local timer at step start"): `POST /api/peer/timer/arm` `{changesetId, node, content, deadline}` — persists `content` as the node's pre-apply state to restore and arms a real rollback timer for `deadline` (unix seconds); `POST /api/peer/timer/cancel` `{changesetId, node}` — stops it (idempotent); `GET /api/peer/timer/status?changesetId=&node=` — returns the current record, or 404 `timer_not_found` if this node never armed one for that key. All three return `{record: {changesetId, node, status, deadline, armedAt, resolvedAt?, error?}}`, where `status` is one of `armed|cancelled|rolled_back|rollback_failed`.
