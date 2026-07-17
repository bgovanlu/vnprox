# Phase 12 — Beyond the cluster (v2.0)

Goal: turn vnprox from "the visual network manager for a PVE cluster" into the all-in-one visual
networking tool for Proxmox, across any number of clusters. T-1201 is the federation core every
other federation-touching card depends on; T-1204 (DNS), T-1205 (switch push), and T-1206 (PBS
awareness) are independent of federation and of each other — they extend existing single-cluster
surfaces and can be built in parallel with T-1201. T-1202/T-1203/T-1207 depend on T-1201 landing
first. T-1208 closes the phase and depends on T-1201–T-1203.

Origin: `docs/roadmap-next.md`'s "Phase 12 — Beyond the cluster" section and its Invariants —
federation federates *views and workflows*, never config ownership; Proxmox stays each cluster's
own source of truth; every mutation flows through the change engine (or, for the two surfaces
that sit outside it by nature, an explicit staged/audited mirror of the same contract).

---

## T-1201 · Federation core
**model:** strong (Opus/Fable-class) · **size:** L · **depends:** T-301, T-105 · **context:** `docs/architecture.md` §1 §5 §7, `docs/security.md` (Authentication, Authorization), `docs/api.md` (Auth, Audit, peer API sections), `docs/data-model.md` §2, `internal/peer/`, `internal/auth/`, `internal/pve/`

**Objective:** One vnprox instance (or a designated primary) attaches multiple PVE clusters as
app-owned registry entries and aggregates reads across them with per-cluster failure isolation.
Config ownership stays strictly per-cluster — there is no cross-cluster mutation primitive.
Everything downstream in this phase (global topology/search, cross-cluster IPAM, OIDC, the v2.0
release) builds on this card's registry and aggregator.

**Deliverables:**
- New package `internal/federation`: a cluster registry service — CRUD over a new `clusters`
  app-store table (id, name, PVE API URL, credential material encrypted at rest per
  docs/security.md's AES-256-GCM session-ticket pattern, status, addedBy, addedAt) — and an
  `Aggregator` that fans reads out to N clusters' own `internal/pve.Client`s concurrently.
- Failure isolation: an unreachable/erroring cluster's contribution to any aggregate response is
  flagged (`partial`, `failedClusters: [string]`, mirroring the existing `partial`/`failedNodes`
  pagination-envelope convention docs/api.md already uses for peer fan-out) — it never blanks or
  errors the whole response; other clusters' data still renders.
- Per-cluster changeset scoping: changesets gain a `clusterId` field (additive; a single-cluster
  deployment's changesets keep working with an implicit default cluster). `internal/change`
  rejects any op whose target Ref belongs to a different cluster than the changeset's `clusterId`
  at validation time. No op type or API surface allows a changeset to span clusters.
- Global audit trail: extends T-303's existing node-dimension fan-out (`GET /audit`,
  docs/architecture.md §7) with a cluster dimension — each row tagged `clusterId`; unreachable
  clusters behave like unreachable peers do today (`partial`/`failedNodes`), plus new
  `failedClusters`.
- New routes: `GET/POST /federation/clusters`, `GET/PUT/DELETE /federation/clusters/{id}`
  (netWrite-gated for mutations) — documented in `docs/api.md`. New credential-storage flow
  documented in `docs/security.md` (one encrypted-at-rest credential set per cluster, same primitive
  as the session-ticket store, not a new one).
- `docs/data-model.md`: `clusters` table shape.
- pvemock: `internal/pvemock` gains the ability to run as N independently-addressed mock clusters
  within one test process (distinct servers/fixtures), so the aggregator can be tested against
  several simulated clusters, one made unreachable mid-test.

**Acceptance criteria:**
1. `internal/federation.Service` cluster CRUD is table-driven tested; credential ciphertext never
   contains the plaintext token (assert on stored bytes).
2. pvemock exposes running N distinct mock clusters in one test; a test attaches three fixtures
   (`single-node`, `three-node-vlan`, `evpn-lab`) to one `Aggregator` and confirms all three
   aggregate with correct `clusterId` tagging.
3. Failure-isolation test: one of three attached mock clusters is killed/timed out mid-aggregation
   → the response still carries the other two clusters' full data, `partial: true`,
   `failedClusters` names the unreachable one — no error blanks the whole response.
4. A changeset created against one `clusterId` whose op targets a ref belonging to a different
   attached cluster is rejected at validation with a stable error code; table test.
5. `GET /audit` merges rows across ≥2 attached clusters, newest-first, each tagged `clusterId`;
   one cluster unreachable → `partial`/`failedClusters` present, the other cluster's rows unaffected.
6. `docs/api.md`, `docs/data-model.md`, `docs/security.md` updated for the new routes, shape, and
   credential flow; `make check` green.

---

## T-1202 · Global topology, search & palette
**model:** sonnet-5 · **size:** L · **depends:** T-1201, T-902, T-903 · **context:** `docs/features/topology.md` §1 §2 §3, `docs/api.md` (topology, WS), T-902's map LOD renderer, T-903's command palette, `internal/topology/`

**Objective:** A global map with per-cluster drill-down (cluster capsules at the outermost LOD
level, reusing T-902's summary-capsule primitive) and a global search/command palette spanning
clusters with namespaced results. A single attached cluster renders its existing topology
unchanged — federation must be invisible until a second cluster is attached.

**Deliverables:**
- Frontend: a "Global" view mode at the map's outermost zoom, one capsule per attached cluster
  (name, aggregate findings count, drift status, unreachable indicator) built on T-902's LOD
  renderer; clicking a capsule drills into that cluster's ordinary topology view, unchanged.
- `GET /federation/topology` (backed by T-1201's `Aggregator`): per-cluster summary stats always;
  a given cluster's full `GET /topology` payload is fetched lazily on drill-down, not inlined.
- Global search: `GET /federation/search?q=` fans `/inventory/search` out per attached cluster via
  the aggregator, namespacing each result with `clusterId`/`clusterName`; T-903's palette groups
  results by cluster and gains a "switch to cluster X" action for changing context.
- Single-cluster regression: with exactly one cluster attached, the capsule view is skipped
  entirely — the existing per-cluster topology is the landing view, byte-identical to pre-Phase-12.
- Tests: Vitest + Testing Library for the capsule component, cluster-namespaced search results,
  and the palette's cluster-switch action. `web/e2e` Playwright: (a) attach two mock clusters,
  land on the global view, drill into cluster B, confirm its topology (layer toggles,
  Switch/Graph toggle, saved layouts) behaves exactly as the single-cluster baseline; (b) palette
  search finds an entity in a non-active cluster and switches context to it.

**Acceptance criteria:**
1. `GET /federation/topology` against N attached fixture-backed clusters returns a summary per
   cluster; one unreachable → its capsule renders degraded/greyed, others intact — Vitest test.
2. The capsule view renders only with ≥2 clusters attached; with exactly one, a DOM/snapshot
   comparison confirms the topology page is unchanged from the pre-Phase-12 baseline.
3. `GET /federation/search?q=` against ≥2 mock clusters returns namespaced results; the palette
   groups by cluster (Testing Library test).
4. Playwright scenario (a) above passes against two pvemock fixtures.
5. Playwright scenario (b) above passes: search jumps to the correct cluster+entity and switches
   active context.
6. `docs/api.md` updated for `/federation/topology` and `/federation/search`; `make check` green.

---

## T-1203 · Cross-cluster IPAM, external subnets & bidirectional sync
**model:** sonnet-5 · **size:** L · **depends:** T-1201, T-405, T-805 · **context:** `docs/features/ipam.md`, `docs/api.md` (Firewall/SDN/IPAM section), `docs/data-model.md`, `internal/ipam/`

**Objective:** External (non-PVE) subnets become first-class IPAM records; a cross-cluster IPAM
view surfaces the same subnet used in two attached clusters as a conflict finding; the
NetBox/phpIPAM bridge is upgraded from read-merge to bidirectional sync. Sync writes to the
external IPAM system are outside the change engine's scope (external IPAM is not Proxmox network
config) but get their own staged preview and audited confirm step mirroring change-engine
semantics — because "outside the change engine" must never mean "unstaged and unaudited."

**Deliverables:**
- New app-store table `external_subnets` (cidr, label, source: `manual`\|`netbox`\|`phpipam`,
  description, createdBy, createdAt, updatedAt). `GET /ipam/subnets`'s `source` enum grows a third
  value `external` alongside `sdn`/`bridge`; external rows are read/write via dedicated
  `/ipam/external-subnets` CRUD routes, never via `ipam.alloc.*` changeset ops (they are not PVE
  SDN subnets).
- `GET /federation/ipam/conflicts` (backed by T-1201's aggregator across attached clusters'
  `GET /ipam/subnets`): the same or overlapping CIDR allocated in two clusters surfaces as a new
  `Conflict.type` value `cross_cluster_duplicate_subnet`, reusing the existing `Conflict` shape
  with a cluster-pair field added.
- Bidirectional sync: `internal/ipam`'s NetBox/phpIPAM bridge gains a diff engine (vnprox-side
  allocations vs. the external system's own records) with `POST /ipam/external-sync/preview`
  (dry-run, never writes) and `POST /ipam/external-sync/apply {confirm: true}` (mirrors
  `/lldp/install`'s explicit-confirm ceremony; `confirm` false/omitted → `400`, no write). Every
  sync write is audit-logged (`ipam.external_sync`) with before/after per record.
- Sync findings flow into the unified stream (`source: "ipam"`) with new `check` values
  `external_ipam_drift`/`external_ipam_conflict`.
- Docs: `docs/api.md` new routes; `docs/data-model.md` `external_subnets` table + `Conflict`
  extension; `docs/features/ipam.md` updated (today's "out of scope v1: external subnets as pure
  records" / "read-merge" language is superseded) with an explicit design note on why external-IPAM
  writes sit outside `internal/change` yet mirror its stage/review/confirm/audit contract.
- Test doubles: a fake NetBox/phpIPAM HTTP double (`internal/ipam` test fixtures) with
  controllable write acceptance/rejection for sync tests.

**Acceptance criteria:**
1. External subnet CRUD produces `source: "external"` rows in `GET /ipam/subnets` alongside
   sdn/bridge rows; table test.
2. `GET /federation/ipam/conflicts` against two mock clusters sharing an overlapping CIDR returns
   one `cross_cluster_duplicate_subnet` finding naming both clusters; no overlap → empty.
3. Sync preview against the fake external-IPAM double surfaces additions/removals/conflicts
   without writing (double's state asserted unchanged); `apply {confirm:true}` writes and audits
   `ipam.external_sync` with before/after; `confirm` false/omitted → `400`, no write.
4. A sync conflict (vnprox and external IPAM disagree on one address) surfaces as an
   `ipam`-source finding, `check: external_ipam_conflict`, `fixable: false`, `docsLink` set.
5. No `ipam.external_sync.*` (or any) changeset op type exists — a regression test asserts the
   sync write path never touches `internal/change`.
6. `docs/api.md`, `docs/data-model.md`, `docs/features/ipam.md` updated; `make check` green.

---

## T-1204 · DNS management
**model:** sonnet-5 · **size:** M · **depends:** T-402 · **context:** `docs/features/sdn.md` §4 §6, `docs/api.md` (SDN apply orchestration), `docs/data-model.md` §3, `internal/sdn/`, `internal/pvemock/sdn.go`

**Objective:** Surface and edit PVE SDN's DNS plugin (PowerDNS-backed): zone/record visibility,
guest names on the map, and record edits as `sdn.*`-style changeset ops routed through the
existing SDN apply orchestration — PVE stages and applies DNS plugin config exactly like
zones/vnets/subnets, so it belongs in the same `sdn_stage`/`sdn_apply` plan, not a new mechanism.

**Deliverables:**
- `GET /sdn/dns?zone=`: `records` (PVE-config-sourced, authoritative, from `/etc/pve/sdn/dns.cfg`)
  and `resolved` (live PowerDNS API read where reachable) — mirrors `GET /sdn/dhcp`'s
  Reservation/Lease duality (config-truth vs. live-observed).
- Guest names on the map: topology projection gains a `dnsName` field on Guest/GuestNic nodes
  (badge/label, not a new entity kind), correlated by guest IP/hostname to the matching DNS
  record — same correlation pattern `Reservation.guestRef`/`Lease.guestRef` already use.
- New changeset ops (`docs/data-model.md` §3, `sdn.*` group): `sdn.dns.zone.create/update/delete`,
  `sdn.dns.record.create/update/delete` — routed through the existing `sdn_stage`/`sdn_apply` plan
  steps (docs/api.md's SDN apply orchestration section), not a separate apply path.
- pvemock: a PowerDNS-shaped mock (zones/records CRUD, since real PVE's DNS plugin calls PowerDNS
  directly per-record) plus mock `dns.cfg` plugin config endpoints, following
  `internal/pvemock/sdn.go`'s existing pattern.
- Docs: `docs/features/sdn.md` §6 (today "DNS record management — P2, display status only") is
  superseded with the editable model; `docs/api.md`/`docs/data-model.md` updated for the new
  routes/ops.

**Acceptance criteria:**
1. `GET /sdn/dns?zone=` against a fixture with a configured DNS plugin returns matching zone/record
   data; an unconfigured fixture returns empty, not an error.
2. A guest whose IP matches a DNS record shows a `dnsName` badge in the map projection — golden
   test against an extended fixture.
3. `sdn.dns.record.create` validates (schema: valid hostname/FQDN; referential: zone exists) and
   applies against the pvemock PowerDNS mock, verified by a follow-up `GET /sdn/dns` read;
   `sdn.dns.zone.delete` with existing records requires cascading record deletion in the same
   changeset (blocking finding otherwise) — table tests.
4. A changeset mixing `sdn.dns.*` ops with other `sdn.*` ops produces exactly one `sdn_apply` step,
   ordered last, per the existing plan contract — plan-shape test.
5. pvemock's PowerDNS mock rejects malformed records with PVE-style `400`s where the real shape is
   known; unverified shapes are flagged, not guessed.
6. `planning/reports/needs-hardware-validation.md` gains entries for real PowerDNS behavior (exact
   error shapes, TTL defaults, zone notify/transfer semantics); docs updated; `make check` green.

---

## T-1205 · Guarded switch config push
**model:** strong (Opus/Fable-class) · **size:** L · **depends:** T-302, T-205, T-703 · **context:** `docs/security.md` (Safety interlocks), `docs/architecture.md` §4, `docs/features/change-management.md`, `docs/features/lldp-discovery.md` §1 §2, planning/tasks/phase-7.md's T-703 (rigor bar), `internal/change/protected.go`, `internal/topology/`

**Objective:** The read-write physical step beyond LLDP-read: driver-based (OpenConfig/gNMI
first, vendor drivers behind the same interface), scoped strictly to ports facing PVE nodes (VLAN
membership, port descriptions, LACP settings), every push an ordinary changeset with the mgmt-path
interlocks extended to the uplink ports carrying the management path. Per-switch explicit opt-in;
ships dark (feature-flagged off) until enabled per switch. This is the highest-risk card in the
arc — a switch push can sever connectivity to hardware vnprox cannot itself recover, unlike a
node-side change where the node's own daemon can always roll itself back locally.

**Safety analysis (required, T-703-level rigor):**
- **What rollback means for a switch port.** Before any write, the driver reads and stores the
  port's current config (VLAN membership, description, LACP settings) as a pre-image snapshot,
  stored exactly like a changeset's file snapshots (docs/data-model.md §2). Rollback re-pushes the
  pre-image. Unlike node rollback, there is no local recovery agent living *on* the switch — if the
  switch becomes unreachable after a push, rollback cannot execute remotely. This is a genuine,
  unsolved residual risk, not something this card can engineer away; it must be stated plainly to
  the operator (opt-in copy, `docs/security.md`) and the changeset must land in a distinguishable
  "rollback incomplete — needs manual intervention" state rather than being silently marked
  `rolled_back` when the switch can't be reached to revert it.
- **Ordering when a changeset touches both a switch port and the node side.** Switch-side
  additions (adding a VLAN to a trunk) are safe to apply before the corresponding node-side change
  because they are additive and don't remove existing connectivity; the plan therefore always
  applies switch-port steps before node-network steps within one changeset. If the switch push
  succeeds but the node-side step then fails validation/apply, the switch is left carrying an
  unused VLAN — harmless, and surfaced as an informational drift-style finding, not silently
  ignored. If the node-side step succeeds but then rolls back (e.g. commit-confirm timeout because
  connectivity broke), the changeset's rollback plan reverts the switch port to its pre-image in
  the same pass as the node-side rollback, audited as one rollback record; if the switch is
  unreachable at that moment, that failure surfaces distinctly (see above) rather than being
  folded into a generic "rolled back" status.
- **LLDP-verified port identity before any write.** Immediately before every write, the driver
  re-reads the target port's live LLDP neighbor (via gNMI or the driver's own read path) and
  aborts the push if it doesn't match the last-known PVE-node neighbor recorded when the port was
  scoped as PVE-facing — protection against a cable having moved since the last poll. This check
  is mandatory and cannot be bypassed by any op; a mismatch is a hard abort, not a warning.

**Deliverables:**
- `internal/switchdrv` (new package): a `SwitchDriver` interface scoped to exactly VLAN
  membership, port description, and LACP settings — no other port operations, no full-config push
  — with an OpenConfig/gNMI implementation; vendor drivers are a documented future extension point
  behind the same interface, not implemented this task.
- App-store table `switches` (id, name, mgmtAddr, driverType, credentials encrypted at rest same
  as PVE tokens/peer secret, `enabled` bool default `false`, addedBy, addedAt); a daemon-level
  `[switches] enabled = false` config flag. No push is possible unless both the daemon flag and the
  specific switch's `enabled` are true — ships dark by construction.
- Port scoping: only ports whose LLDP-observed neighbor is a known PVE node's PhysNic (per
  `internal/topology`'s existing LLDP-neighbor merge) are writable; any other port target is
  rejected at validation before any driver call.
- New op group `switch.port.update` (vlanMembership, description, lacp params only), new Ref kind
  `switch-port` (docs/data-model.md addition), routed through the ordinary
  stage→validate→diff→apply→confirm/rollback lifecycle with a new switch-port apply-step type.
- Mgmt-path interlock extension: `internal/topology.ResolveMgmtPaths` (T-702) extends one hop
  further onto the LLDP-identified switch port carrying a node's management path — a
  `switch.port.update` touching that port is `touchesMgmtPath: true` (T-703's ceremony applies)
  and is hard-blocked (`safety.protected_switch_port`, no override, mirroring T-703's "no override
  in UI") if its net effect would strip the management VLAN from that port.
- Mock switch: a gNMI test double (`internal/switchmock` or under `internal/pvemock`) implementing
  enough OpenConfig (interfaces/vlan/lacp paths) for create/update/read cycles, plus
  LLDP-consistent and LLDP-inconsistent neighbor scenarios.
- Docs: `docs/api.md` `/switches` CRUD + new op docs; `docs/data-model.md` new op group/Ref
  kind/table; `docs/security.md` credential-storage note plus an explicit statement that switch
  push is opt-in, feature-flagged, and scoped — an extension of the change-engine invariant, not
  an exception to it.

**Acceptance criteria:**
1. Daemon flag off (default) → no switch route accepts a write; table test.
2. A registered switch with `enabled: false` rejects any `switch.port.update` targeting it with a
   stable error code; enabling it allows the same op.
3. A `switch.port.update` targeting a port whose LLDP neighbor is not a known PVE PhysNic is
   rejected at validation before any driver call — table test against mock-switch fixtures with
   both PVE-facing and non-PVE-facing ports.
4. Mock-switch scenario where a port's neighbor changed since last poll: the pre-write identity
   check aborts the push, asserted by zero writes reaching the mock.
5. A `switch.port.update` on the uplink port feeding a node's resolved mgmt path is
   `touchesMgmtPath: true` and gets T-703's ceremony; an op whose net effect strips the mgmt VLAN
   from that port is blocked with `safety.protected_switch_port` — golden test against
   `three-node-vlan` extended with a mock-switch topology.
6. Rollback proof: a changeset with both node-side and switch-port ops, node-side commit-confirm
   times out → both node-side and switch-port state roll back to their pre-images (mock switch
   config matches its pre-apply snapshot). A second test makes the mock switch unreachable during
   that rollback and asserts the changeset lands in a distinguishable "rollback incomplete" state,
   not silently `rolled_back`.
7. Ordering test: a changeset adding a VLAN to both a switch port and a node bridge applies the
   switch-port step before the node-network step (plan-order assertion); a tamper test (T-703
   AC2-style) mutates ops to target a non-PVE-facing port directly — blocked, and the UI cannot
   construct that state through its own op builder.
8. Report enumerates hardware-validation gaps at minimum: real gNMI vendor behavior variance, real
   LACP negotiation against physical hardware, rollback timing/atomicity on vendor firmware, and
   MLAG/stacked-switch topologies. `make check` green.

---

## T-1206 · PBS network awareness
**model:** sonnet-5 · **size:** S · **depends:** T-103 · **context:** `docs/features/topology.md` §1 §2, `docs/data-model.md` §1, `internal/inventory/`, `internal/pve/`

**Objective:** Read-only: PBS hosts appear on the map with their interfaces, the backup traffic
path (node → PBS) is highlighted, and the inspector shows datastore-network sizing hints. Zero
write surface — no new changeset ops, no PBS credentials stored.

**Deliverables:**
- Discovery: PBS storages are read from PVE's own storage config (`storage.cfg` type `pbs`) via
  the existing `internal/pve` client — no new credentials or PBS API client needed; this is
  read-only discovery of PVE's own knowledge of its PBS storages, not a PBS API integration.
- New entity kind `pbs-host` (docs/data-model.md addition): name/address, port, datastores
  `[]string`, fingerprint — projected as a topology node, connected to backing-up nodes via a new
  edge kind `backup-path` (docs/data-model.md edge-kind addition).
- Map: a "backup path" paint/highlight mode (mirrors the existing traffic-paint mode,
  docs/features/topology.md §2) lighting up node→PBS edges for nodes with a configured backup job
  targeting that storage (read from PVE's backup job config).
- Inspector: a plain-English datastore-network sizing hint combining backup job schedule/volume
  (best-effort from PVE's own config, not Phase 10's flow/metrics data) with the resolved network
  path's link speed (reusing `internal/topology`'s existing NIC-path resolution).

**Acceptance criteria:**
1. A pvemock fixture extended with a PBS-type storage entry produces a `pbs-host` node in
   `GET /topology` with correct fields — golden projection test.
2. `backup-path` edges exist for nodes with a backup job targeting that storage, absent otherwise
   — table test.
3. Inspector sizing-hint output is deterministic given fixture inputs (golden test); flagged as a
   heuristic estimate, needs-hardware-validation for real-world accuracy.
4. No new changeset op, write route, or credential-bearing store table is introduced — an explicit
   regression test/review note confirms zero write surface for PBS.
5. `docs/data-model.md` updated for the new entity/edge kind; `docs/api.md`'s `/topology` note
   updated; `make check` green.

---

## T-1207 · OIDC SSO
**model:** sonnet-5 · **size:** M · **depends:** T-1201, T-105 · **context:** `docs/security.md` (Authentication, Authorization), `docs/api.md` (Auth section), `internal/auth/`

**Objective:** OIDC login alongside the existing PVE ticket bridge, for federated deployments
where per-cluster PVE credentials stop scaling. Group→role mapping. The precise split: **OIDC
authenticates the human to vnprox; PVE authorization still gates every cluster-scoped action per
cluster** — OIDC-derived capabilities are never additive beyond what the linked PVE identity's own
ACLs allow, mirroring docs/security.md's existing "vnprox cannot exceed the user's PVE ACLs"
invariant.

**Deliverables:**
- `internal/auth` gains an OIDC authorization-code+PKCE flow alongside the PVE ticket bridge
  (`GET /auth/oidc/login`, `POST /auth/oidc/callback` — docs/api.md), config in `vnprox.toml`
  (`[oidc] issuer, clientId, clientSecretFile, scopes`).
- Per-cluster PVE authorization linkage: an OIDC identity has no PVE ticket by itself, so a
  cluster-scoped action still needs a resolved PVE authorization for that cluster — either an
  admin-configured OIDC-group→PVE-user/token mapping per cluster (stored in T-1201's cluster
  registry) or a first-use prompt for PVE credentials when no mapping exists. If this split cannot
  be resolved cleanly without inventing new PVE-side capability, flag it in the report rather than
  papering over it.
- Group→role mapping: OIDC group claims map to vnprox capability bundles, extending
  `internal/auth/caps.go`'s existing mapping-table pattern — capped by the linked PVE identity's
  actual PVE ACLs (same enforcement point `forceReadOnly` already uses).
- Session lifecycle: OIDC sessions follow docs/security.md's existing session model (HttpOnly/
  Secure/SameSite cookie, encrypted-at-rest tokens, idle/hard-cap timeouts), with OIDC
  refresh-token renewal where the PVE-ticket-bridge path uses ticket renewal.
- Mock OIDC provider for tests, issuing tokens with configurable group claims.
- Docs: `docs/security.md` new Authentication subsection stating the authn/authz split precisely;
  `docs/api.md` new routes; `docs/data-model.md` if new store fields are needed.

**Acceptance criteria:**
1. OIDC login against the mock provider issues a vnprox session cookie with the same security
   properties (HttpOnly/Secure/SameSite, encrypted-at-rest identity token) as a PVE-ticket-bridge
   session — integration test.
2. A mock OIDC token carrying group X maps to capability bundle Y; a user with no PVE
   authorization linkage for a given cluster is denied cluster-scoped writes despite holding the
   OIDC-mapped capability bundle — table test proving the authn/authz split.
3. An OIDC-mapped capability bundle exceeding the linked PVE user's actual PVE ACLs is capped at
   the PVE-derived value — test against `internal/auth/caps.go`'s enforcement pattern.
4. Idle timeout, hard cap, and refresh behave per docs/security.md's existing session contract,
   applied to OIDC sessions.
5. `planning/reports/needs-hardware-validation.md` gains entries for real-IdP claim-shape variance
   (Okta/Keycloak/Azure AD) and refresh-token edge cases.
6. `docs/security.md`, `docs/api.md`, `docs/data-model.md` updated; `make check` green.

---

## T-1208 · v2.0 release: performance, docs, packaging
**model:** sonnet-5 · **size:** M · **depends:** T-1201, T-1202, T-1203 · **context:** `docs/performance.md`, `docs/deployment.md`, `docs/security.md`, planning/tasks/phase-6.md's T-607 (release-checklist precedent), `docs/roadmap-next.md` (Compatibility & versioning)

**Objective:** The v2.0 cut: a multi-cluster genscale performance pass, a docs freeze covering
this arc's new features, packaging/upgrade-path testing from the v1.x line, a security pass over
the phase's new write/credential surfaces, and a release checklist mirroring v1's T-607.

**Deliverables:**
- Define a multi-cluster genscale profile (extends `testdata/genscale`; docs/features/topology.md
  §4's existing target was single-cluster) — state the actual chosen numbers (e.g. N clusters ×
  the existing per-cluster target) as an explicit output of this task, then run a scale/perf pass
  against it: federation aggregator latency, `GET /federation/topology`/`/federation/search`
  response times, memory for N attached clusters.
- Docs freeze: user guide chapters for federation, cross-cluster IPAM/external subnets, DNS
  management, and switch push, matching the existing user-guide chapter structure/tone.
- Packaging/upgrade-path testing: an apt upgrade from the v1.x line onto v2.0 exercised against a
  v1.x-schema DB fixture (forward-only migrations, docs/data-model.md §2); a v1.x single-cluster
  install upgrading with zero attached federation clusters must continue serving its existing
  single-cluster experience unchanged — the same "federation is additive, not a fork" bar T-1202's
  AC2 established, proven here at the release level.
- Security pass: `docs/security.md`'s threat-model-summary table gains rows for this arc's new
  surfaces (switch credentials, OIDC tokens/IdP responses, cluster registry credentials, rogue/
  compromised attached cluster) with stated mitigations; each new credential type confirmed
  encrypted at rest.
- Release checklist mirroring T-607's structure, extended with federation-specific gates (e.g.
  single-cluster regression suite green, multi-cluster failure-isolation suite green).
- Compatibility: confirm the PVE 9.x/10.x target (docs/roadmap-next.md's versioning section) holds
  across every Phase 12 feature.

**Acceptance criteria:**
1. The multi-cluster genscale profile is defined (numbers stated) and checked into `testdata/` or
   documented in `docs/performance.md`; a scale run against it meets a stated latency/memory bound
   for the global topology/search endpoints, numbers recorded in the report.
2. A v1.x-schema SQLite fixture DB migrates cleanly to v2.0's schema (forward-only migration
   test); a smoke test confirms the daemon starts and serves its existing single-cluster surface
   unchanged with zero clusters attached.
3. User guide chapters exist for federation/IPAM/DNS/switch-push, matching existing structure.
4. `docs/security.md`'s threat-model table has new rows for the v2.0 surfaces; each new credential
   type (switch, OIDC, cluster registry) has a targeted encrypted-at-rest test.
5. A release checklist mirroring T-607's format is produced, covering the federation-specific
   gates listed above.
6. `make check` green across the full v2.0 surface; PVE 8.2+/9.x/10.x compatibility notes updated
   per docs/roadmap-next.md's versioning section.
