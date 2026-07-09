# Phase 1 — Read-only visibility

Goal: log in with PVE credentials and see the whole cluster network drawn, live. Milestone: **v0.1 private preview**.

---

## T-101 · PVE API client
**model:** sonnet-5 · **size:** M · **depends:** T-002, T-004 · **context:** `docs/architecture.md` §1 §6, `docs/api.md` (error codes), `docs/security.md` (auth)

**Objective:** Typed Go client for the PVE API surface vnprox uses, supporting both auth modes.

**Deliverables:** `internal/pve` client: ticket auth (`POST /access/ticket`, cookie + CSRF header handling, renewal), API-token auth (daemon read identity); typed methods: cluster status/resources, node network GET/PUT + reload, qemu/lxc list + config GET/PUT, SDN reads (zones/vnets/subnets/status), firewall reads (all scopes + objects), IPAM reads, task get/wait-with-backoff; consistent error mapping (PVE 401/403/5xx → typed errors incl. `pve_denied`); TLS pinned to the local pveproxy cert or system pool per config; per-call context; instrumented with slog debug.

**Acceptance criteria:**
1. Full integration test suite against pvemock (both auth modes, ticket renewal before expiry simulated with a short-TTL fixture flag).
2. 403 from pvemock surfaces as `ErrPVEDenied` carrying the PVE message.
3. Task-wait handles success, failure, and timeout (fixture-injected) correctly.
4. No PVE endpoint outside the documented surface is called (client has no generic passthrough).

---

## T-102 · Host readers
**model:** sonnet-5 · **size:** L · **depends:** T-002 · **context:** `docs/data-model.md` §1, `docs/architecture.md` §3

**Objective:** Read the node's real network state: the `interfaces(5)` file (intent) and netlink (runtime).

**Deliverables:** `internal/host`: a lossless `interfaces(5)` parser/AST (stanzas, options, comments, `source`/`source-directory` includes, `iface`/`auto`/`allow-*`) that survives round-tripping unknown options — this AST is also T-204's foundation; netlink readers (links with kind/master/slave detail for bridge/bond/vlan/veth, addresses, bond runtime from `/proc/net/bonding/*`, bridge vlan table, fdb); `/sys/class/net` stats reader; ethtool speed/duplex (netlink ethtool preferred, exec fallback); everything behind the `host.Reader` interface with `real` and `fixture` implementations (fixture format shared with T-004).

**Acceptance criteria:**
1. Parser round-trips a corpus of ≥12 real-world interfaces files (`testdata/interfaces/`: simple, vlan-aware bridges, bonds+vlans, OVS stanzas, includes, exotic-but-valid comments/options) byte-identically when unmodified.
2. Parser rejects malformed files with line-precise errors (table-tested).
3. Netlink readers produce documented data-model fields for fixture states; `real` implementation compiles on linux/amd64+arm64 and is exercised by a skippable-if-unprivileged integration test.
4. Fuzz test on the parser (Go native fuzzing) runs clean for 60s in CI.

---

## T-103 · Inventory model & graph ★
**model:** **strong (Opus/Fable-class)** · **size:** L · **depends:** T-101, T-102 · **context:** `docs/data-model.md` §1 (contract), `docs/architecture.md` §3, `docs/features/topology.md` §3–4

**Objective:** The normalized in-memory model everything reads: typed entities, edges, snapshotting, delta computation. Correctness and race-freedom here underpin every feature.

**Deliverables:** `internal/inventory`: all documented entity types + `Ref` (+ its string encoding for URLs); `Graph` with typed edges, `Snapshot()` (immutable, cheap — persistent-structure or copy-on-write), `ApplyPoll(source, entities) Delta` with per-source ownership (a PVE poll must not clobber host-poll-owned fields and vice versa; field-level merge rules documented in code); reconciliation of the same real-world object seen by multiple sources (e.g. bridge from interfaces file + netlink + PVE) into one entity with provenance; delta events (added/updated/removed with changed-field sets); guest ↔ bridge/VNet attachment resolution incl. VLAN tags.

**Acceptance criteria:**
1. Race detector clean under a stress test: 4 concurrent pollers + 8 concurrent snapshot readers, 30s.
2. Merge rules table-tested: every (source × field-group) combination has an explicit expected winner; conflicting data produces a provenance-tagged entity, never silent last-write-wins.
3. Delta correctness: golden tests feed poll sequences and assert exact delta streams (incl. no-op polls → empty delta).
4. `Snapshot()` p99 < 5ms at scale-target size (topology spec §4), benchmarked.
5. Ref string encoding round-trips all kinds incl. cluster-scoped (empty node) and IDs containing `/`.

---

## T-104 · Collectors
**model:** sonnet-5 · **size:** M · **depends:** T-103 · **context:** `docs/architecture.md` §3, `docs/deployment.md` (collect intervals)

**Objective:** The poll loops feeding inventory: PVE poller and local-host poller, with lifecycle and on-demand refresh.

**Deliverables:** `internal/collect` (or within collectors' packages per taste): PVE poller (cluster status, resources, node networks, guest configs, SDN, firewall) and host poller (T-102 readers) on documented intervals with jitter; on-demand `RefreshNow(scope)` used post-apply; staleness tracking per source exposed on `/api/v1/health` detail; backoff on PVE errors without killing the loop; clean shutdown via run-group.

**Acceptance criteria:**
1. Against pvemock `three-node-vlan`, inventory converges to the fixture's full expected entity set (golden test) within two poll cycles.
2. PVE outage (mock stopped) degrades gracefully: staleness reported, loop recovers on mock restart without daemon restart.
3. `RefreshNow` completes a full targeted refresh in <2s against pvemock and triggers exactly one delta batch.
4. No goroutine leaks across start/stop cycles (goleak).

---

## T-105 · Auth: PVE bridge, sessions, capabilities
**model:** sonnet-5 · **size:** M · **depends:** T-101, T-003 · **context:** `docs/security.md` (the spec), `docs/api.md` (auth endpoints)

**Objective:** Login with PVE credentials; sessions; the PVE-ACL → capability mapping; auth middleware.

**Deliverables:** `internal/auth`: login handler (realm list from PVE, OTP passthrough), session create/lookup/destroy with encrypted ticket storage (T-003 helpers), ticket renewal loop, CSRF double-submit, idle + hard expiry; capability derivation from `GET /access/permissions` per the mapping table (`caps.go`, the documented single source of truth), hourly re-derivation; middleware chain: session → CSRF (mutating) → capability guard helpers used by later route registrations; login rate limiting (per-IP + per-user token bucket); `/auth/login`, `/auth/logout`, `/auth/me` per API doc; audit entries for login success/failure/lockout.

**Acceptance criteria:**
1. Full login/logout/me cycle against pvemock incl. a TOTP-required fixture user.
2. Session cookie attributes exactly per security doc (asserted); mutating request without CSRF header → 403 `csrf_required`.
3. Capability matrix test: fixture users (root, auditor, sdn-only, vm-user) yield exactly the documented caps.
4. Rate limiter: 10 rapid bad logins → 429 + audit entries; correct login unaffected from another IP.
5. Expired-ticket renewal happens transparently (short-TTL fixture); failed renewal invalidates the session cleanly.

---

## T-106 · Topology builder, API & WS hub
**model:** sonnet-5 · **size:** M · **depends:** T-104, T-105 · **context:** `docs/features/topology.md` §1 §3, `docs/api.md` (topology, WS, inventory)

**Objective:** Project inventory into the renderable topology contract; serve it; push deltas.

**Deliverables:** `internal/topology` projection (layer assignment, node grouping, status derivation incl. degraded-bond/link-down badges, guest collapse counts, VLAN filter + layer filter server-side); `GET /topology`, `GET /inventory/{ref}` (detail + raw source), `GET /inventory/search` (fuzzy across documented fields); WS hub (`/api/ws`): subscription protocol per API doc, `topology.delta` fan-out from inventory deltas, per-connection send-queue with slow-client drop; capability-gated (read caps).

**Acceptance criteria:**
1. Golden topology JSON for `single-node` and `three-node-vlan` fixtures (structure, layers, edges, badges).
2. `?vlan=20` returns only entities carrying VLAN 20 (fixture has known expectations).
3. Search finds entities by partial name, MAC, IP, VMID across fixtures; results ranked, capped.
4. WS: mutate pvemock fixture state mid-test → subscribed client receives correct delta within one poll interval; 500 concurrent WS clients sustained in a load test.
5. Unauthenticated topology/WS access → 401.

---

## T-107 · Topology UI
**model:** sonnet-5 · **size:** L · **depends:** T-106, T-005 · **context:** `docs/features/topology.md` (the spec), `docs/user-guide.md` §2 §6

**Objective:** The map: React Flow canvas, four layers, inspector, search, layouts — the product's home screen, read-only at this stage.

**Deliverables:** Topology page: React Flow + elkjs layered auto-layout per spec (node columns, layer bands, cluster-scoped SDN band); custom node components per entity kind with status painting; edge kinds with badges (VLAN ranges, enslavement); layer toggles (`1–4` keys), VLAN filter (`f`), spotlight search (`/`); hover chain-highlight; click → inspector panel (normalized fields, live status, raw source tab, related entities); guest collapse/expand pills; saved layouts (persist via layouts API — add the small API handler if T-106 didn't); delta-driven live updates via the WS bridge into query cache; empty/degraded states per spec §5; progressive disclosure at scale caps.

**Acceptance criteria:**
1. Against pvemock `three-node-vlan`: all four layers render correctly (screenshot-baseline test via Playwright or documented manual checklist executed against `make dev`).
2. Hover on a guest highlights its full path to the physical NIC; VLAN filter 20 dims everything else.
3. Search jump focuses and highlights; keyboard shortcuts work per user guide §6.
4. Live update: changing fixture link state reflects on the map without reload within one poll cycle.
5. `messy-brownfield` fixture renders without errors (degraded data tolerance); 60fps-class pan/zoom at three-node fixture scale (no dropped-frame jank on a dev machine; document measurement).
6. Vitest coverage on projection-to-props logic; `tsc`/eslint clean.
