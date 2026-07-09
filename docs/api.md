# API design

Base: `https://<node>:8007/api/v1`. JSON everywhere. This document is a **contract** — implementation tasks in different phases depend on these routes and shapes matching exactly.

## Conventions

- Auth: session cookie `vnprox_session` (HttpOnly, Secure, SameSite=Strict) + `X-VNPROX-CSRF` header on mutating requests.
- Errors: `{"error": {"code": "string", "message": "human readable", "details": {}}}` with proper HTTP status. Codes are stable identifiers (`validation_failed`, `pve_denied`, `changeset_locked`, `peer_unreachable`, ...).
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
| GET | `/topology` | full projected topology: `{nodes:[...], edges:[...], layers, generatedAt}` with optional `?layers=phys,l2,sdn,guest&node=<name>&vlan=<vid>` filters |
| GET | `/inventory/{ref}` | full detail for one entity, including raw source (interfaces stanza / PVE API object) |
| GET | `/inventory/search?q=` | fuzzy search across names, MACs, IPs, VMIDs, comments |
| GET | `/lldp` | all LLDP neighbors cluster-wide (fanned out to peers) |
| GET | `/drift` | cross-node consistency report: `[{check, severity, nodes, detail}]` |

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
| POST | `/changesets/{id}/rollback` | manual rollback (also valid on `committed` within retention) |
| DELETE | `/changesets/{id}` | discard draft |

Validation finding shape: `{severity: "error"|"warning"|"info", code, message, ref?, fix?}` where `fix` is an optional machine-applicable amendment (an `[]Op` patch the UI can offer one-click).

## Snapshots / time machine

| Method | Path | Purpose |
|---|---|---|
| GET | `/snapshots` | list (paginated, cluster-merged) |
| POST | `/snapshots` | manual snapshot `{note}` |
| GET | `/snapshots/{id}` | metadata + file list |
| GET | `/snapshots/diff?from=&to=` | unified diffs between two snapshots (or `to=live`) |
| POST | `/snapshots/{id}/restore` | creates a **changeset draft** that would restore this state (goes through normal review/apply) |

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

Client subscribes: `{"subscribe": ["topology", "changesets", "metrics:<ref>", "tasks"]}`. Server events:

| Event | Payload |
|---|---|
| `topology.delta` | `{added, updated, removed: [Ref]}` (client refetches affected) |
| `changeset.status` | `{id, status, confirmDeadline?}` — drives the confirm countdown UI |
| `metrics.sample` | `{ref, at, rates}` |
| `drift.changed` | `{count}` |

## Peer API (`/api/peer/*`, internal only)

HMAC-authenticated (cluster secret) endpoints between daemons: `GET /api/peer/host/interfaces`, `/api/peer/host/lldp`, `/api/peer/host/stats`, `POST /api/peer/host/stage-interfaces`, `POST /api/peer/host/ifreload`, `POST /api/peer/host/restore`, `GET /api/peer/health`, `GET /api/peer/version`. Never exposed in the SPA; rejected without valid HMAC + timestamp (±30s replay window).
