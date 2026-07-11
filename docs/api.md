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

**`GET /drift` finding shape** (added by T-305; documented here per docs/development.md's definition-of-done #4 — the base `{check, severity, nodes, detail}` fields are the original contract, `id`/`refs`/`fixable` are additive): `{id, check, severity, detail, nodes: [string], refs?: [string], fixable}`. `check` is one of `bridge_divergence`\|`mtu_consistency`\|`sdn_realization`\|`pending_interfaces`\|`file_runtime_divergence` (docs/features/topology.md §6's five families); `severity` is `error`\|`warning`\|`info`, reusing the changeset finding vocabulary. `id` is a stable key (derived from `check` plus the finding's affected refs/nodes, never random or time-based) — the same finding on an unchanged cluster keeps the same `id` across drift cycles. `fixable` is true iff `POST /drift/{id}/fix` will succeed for `id` right now (bridge-property harmonization and MTU alignment are the only two families with a computable fix as of T-305; presence/SDN-realization/pending/file-vs-runtime findings are detection-only). Cluster-wide fan-out (peer nodes' own drift findings, mirroring `GET /audit`/`GET /snapshots`) is not yet implemented — today's response covers only this daemon's own view of the cluster-wide inventory graph, which is already complete for every node the PVE API and this daemon's own host poller can see (see `GET /topology`'s staleness note for the same host-poller-locality caveat).

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

HMAC-authenticated (cluster secret) endpoints between daemons: `GET /api/peer/host/interfaces`, `/api/peer/host/lldp`, `/api/peer/host/stats`, `/api/peer/host/links`, `/api/peer/host/fdb`, `POST /api/peer/host/stage-interfaces`, `POST /api/peer/host/ifreload`, `POST /api/peer/host/restore`, `POST /api/peer/host/discard-staged`, `POST /api/peer/host/lldp/install`, `GET /api/peer/health`, `GET /api/peer/version`, `GET /api/peer/audit`, `GET /api/peer/snapshots`, `POST /api/peer/timer/arm`, `POST /api/peer/timer/cancel`, `GET /api/peer/timer/status`. Never exposed in the SPA; rejected without valid HMAC + timestamp (±30s replay window). Protocol version 2 (T-304 added `discard-staged` and the `timer/*` routes below — a peer still advertising protocol 1 cannot serve them, so a coordinator refuses to route multi-node steps to it per the version-skew rule in `docs/architecture.md` §5).

`GET /api/peer/host/links` (added by T-303, documented here retroactively per that task's report note): a node's netlink-equivalent link state (`{links: [LinkState]}`, one entry per physical NIC/bond/bridge/VLAN sub-interface) — the remote-node counterpart of `internal/host.Reader.Links`, letting a peer's daemon fan its host poller out cluster-wide instead of only ever seeing its own node's netlink state.

`GET /api/peer/host/fdb` (added by T-306, per that task's card — T-301's report flagged its absence as this task's deliverable): a node's bridge forwarding-database tables, flattened and bridge-tagged (`{entries: [{bridge, mac, port?, vlan?, master?, permanent?, stale}]}`) out of the same data `Links()` already carries in each entry's `Bridge.FDB` — this route exists as its own endpoint so a caller that only wants the FDB (the MAC/FDB browser, `GET /fdb` above) doesn't have to pull every bridge's full VLAN table and every physical NIC's link state over the wire to get it. `internal/collect`'s host poller does not call this route: bridge FDB reaches the inventory graph (and therefore cluster-wide `GET /fdb`) via the existing `Links()`-based host poll, exactly like every other netlink-observed field — `handleFDB` derives its response from the same `Links()` read `GET /api/peer/host/links` uses, not a second collection path.

`GET /api/peer/audit` and `GET /api/peer/snapshots` (added by T-303): each node's own local page of its `/audit`/`/snapshots` data — `{items: [...], nextCursor?}`, the same shapes as the public `/audit`/`/snapshots` list items, minus the cluster-fan-out `partial`/`failedNodes` fields (a peer only ever reports its own node-local page here; the fan-out/merge happens on the calling daemon, inside `GET /audit`/`GET /snapshots` themselves). `/api/peer/audit` accepts the same filter query params as `GET /audit` (`user`, `action`, `target`, `result`, `changesetId`, `from`, `to`) plus `limit`/`cursor`; `/api/peer/snapshots` accepts `limit`/`cursor` only.

T-304's local-timer protocol (`docs/features/change-management.md` §4: "each node arms its own local timer at step start"): `POST /api/peer/timer/arm` `{changesetId, node, content, deadline}` — persists `content` as the node's pre-apply state to restore and arms a real rollback timer for `deadline` (unix seconds); `POST /api/peer/timer/cancel` `{changesetId, node}` — stops it (idempotent); `GET /api/peer/timer/status?changesetId=&node=` — returns the current record, or 404 `timer_not_found` if this node never armed one for that key. All three return `{record: {changesetId, node, status, deadline, armedAt, resolvedAt?, error?}}`, where `status` is one of `armed|cancelled|rolled_back|rollback_failed`.
