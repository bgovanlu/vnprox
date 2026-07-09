# Phase 3 — Cluster & discovery

Goal: true multi-node: the peer mesh, LLDP physical discovery, cluster fan-out, drift detection, and distributed rollback safety.

---

## T-301 · Peer API: secret, HMAC, client/server
**model:** sonnet-5 · **size:** M · **depends:** T-002 (review checkpoint: security boundary) · **context:** `docs/security.md` (Transport, peer auth — the spec), `docs/api.md` (Peer API), `docs/architecture.md` §5

**Objective:** The authenticated intra-cluster channel every cluster feature rides on.

**Deliverables:** `internal/peer`: cluster secret handling (generate-if-absent under `/etc/pve/vnprox/`, 0600, load + watch); HMAC-SHA256 request signing per security doc (method, path, body hash, timestamp; ±30s window; constant-time verify); server: all documented `/api/peer/*` endpoints — host reads (interfaces, lldp placeholder until T-302, stats, fdb), host writes (stage-interfaces, ifreload, restore), health, version — bound to the same listener, rejecting unsigned/foreign requests before any handler logic; client with peer discovery from PVE cluster status, per-peer circuit breaker, timeouts; version exchange + incompatibility surfacing (architecture §5); single-node mode: zero peers, everything short-circuits locally through the same interface (callers never special-case).

**Acceptance criteria:**
1. Two daemon instances (test harness, distinct fixture-backed hosts) exchange all read endpoints successfully.
2. Security suite: missing/garbage/expired-timestamp/replayed/wrong-secret signatures each → 401 before handler execution (asserted via handler spy); SPA-session cookies grant nothing on peer routes.
3. Circuit breaker: dead peer → fast-fail + recovery on return; caller-visible error `peer_unreachable`.
4. Version mismatch surfaces per architecture §5 (incompatible peer → coordination refusal error).
5. Fuzz the signature parser/verifier 60s clean.

---

## T-302 · LLDP collection, switch merging, ports table
**model:** sonnet-5 · **size:** M · **depends:** T-301 · **context:** `docs/features/lldp-discovery.md` (the spec)

**Objective:** The physical layer: lldpd integration, cross-node switch entity merging, the VLAN cross-check, and the ports table.

**Deliverables:** `lldpctl -f json` reader in `internal/host` (fixed argv, defensive parsing of the full documented field set, fixture-backed variant with real captured lldpctl output in testdata incl. CDP-decoded neighbors and adversarial/truncated JSON); 30s collector locally + via peer API cluster-wide; inventory integration: `LldpNeighbor` entities, chassis-ID merge into unified switch entities per spec §2, staleness lifecycle per spec §3; VLAN cross-check findings (bridge/bond expected VIDs vs. switch-advertised); topology map physical layer upgrade (switch nodes, NIC→port edges); Ports table view + CSV export; graceful absence: lldpd-missing hint + guided install flow per spec §1 (peer-API apt install with confirmation + audit).

**Acceptance criteria:**
1. Fixture with two nodes seeing the same chassis ID → one switch entity with both links; distinct switches stay distinct (incl. same-name/different-ID edge case).
2. VLAN cross-check golden tests: matching, missing-on-switch, missing-on-bridge each produce the documented finding.
3. Staleness: TTL-expired neighbor greys then drops from map per spec timings (clock-injected test).
4. Malformed/hostile lldpctl JSON never panics (fuzz + adversarial corpus); absent lldpd renders the degraded state per spec.
5. CSV export matches the table (golden).

---

## T-303 · Cluster fan-out
**model:** sonnet-5 · **size:** M · **depends:** T-301, T-104 · **context:** `docs/architecture.md` §1 §5 §7, `docs/features/topology.md` §5

**Objective:** Make every read surface truly cluster-wide: remote host data via peers, merged queries, per-node staleness.

**Deliverables:** Host-poller extension: local `host.Reader` for self, peer-API-backed reader for every other node, uniform ingestion into inventory (node-tagged, source-tagged per T-103 merge rules); peer staleness/unreachability → topology degraded-band state per spec §5 + health detail; audit/snapshot list fan-out with merge + per-peer-failure tolerance (partial results flagged, never silent); `GET /lldp` and `/drift` (T-305 fills logic) fan out likewise.

**Acceptance criteria:**
1. Three-daemon harness: any daemon's `/topology` shows all nodes' host-level detail (bond runtime, stats presence) identically (golden, modulo generatedAt).
2. Kill one peer: its band degrades with last-known + staleness timestamp within one poll cycle; merged audit queries return partial-result flag; peer's return heals everything without restarts.
3. Merged audit/snapshot pagination is stable (no duplicates/gaps across pages with interleaved timestamps — property test).
4. No cross-node data attribution bugs: entity node-tags always match originating peer (asserted in harness).

---

## T-304 · Distributed rollback: per-node local timers
**model:** sonnet-5 · **size:** M · **depends:** T-301, T-205 · **context:** `docs/architecture.md` §4 (mid-apply failure paragraph), `docs/features/change-management.md` §4

**Objective:** Extend T-205's safety to multi-node applies: each node arms its own local timer so no node's safety depends on cluster connectivity.

**Deliverables:** Apply-plan execution for multi-node changesets via peer API (stage → reload per node in plan order); local-timer protocol: coordinator sends each affected node its pre-state + deadline *before* that node's first mutating step; node-local daemon persists and arms it; confirm fans out cancellations; missing cancellation at deadline → local restore; coordinator-side reconciliation on reconnect (statuses merge into the changeset apply log); mid-apply peer loss handling per architecture (abort remaining, roll back completed, unreachable node self-rolls-back); `changeset.status` events reflect per-node states.

**Acceptance criteria:**
1. Three-daemon harness, changeset touching all nodes: apply → confirm cancels all three local timers (asserted via logs/DB on each).
2. Partition test: cut coordinator↔node C after C's steps ran, don't confirm → A/B roll back at coordinator deadline, C rolls back independently at its local deadline; post-heal reconciliation marks the changeset `rolled_back` with per-node detail.
3. Peer dies before its steps start → abort path: completed nodes roll back, changeset `failed`, untouched node stays untouched.
4. Coordinator dies mid-window → every node still rolls back locally (the T-205 restart-survival property, now per-node).
5. Property across injected failure points: every node converges to pre-state or committed state — never a mix on one node.

---

## T-305 · Drift detection
**model:** sonnet-5 · **size:** M · **depends:** T-303 · **context:** `docs/features/topology.md` §6 (the spec)

**Objective:** The cross-node consistency engine and its surfacing.

**Deliverables:** `internal/drift` (or within inventory): the five documented check families (same-named bridge divergence, MTU path consistency, SDN realization vs. membership, pending `interfaces.new`, file-vs-runtime divergence) running on a 30s cycle over inventory snapshots; findings with severity, plain-English detail, affected refs; "create fixing changeset" for computable fixes (start with: bridge-property harmonization and MTU alignment — each fix is an op patch through the normal drawer); `GET /drift` per API doc; map dashed-outline painting + nav count badge + `drift.changed` WS event; findings stream UI (shared component — T-602 reuses it).

**Acceptance criteria:**
1. `messy-brownfield` fixture produces exactly its documented expected findings (the fixture exists to be this test).
2. Each check family has targeted golden tests incl. a clean-cluster no-findings case.
3. A generated fixing changeset, applied (pvemock), clears the finding on the next drift cycle (closed loop test).
4. File-vs-runtime check catches a fixture-simulated manual `ip link` change.
5. No finding flapping: hysteresis or stable-key dedup verified across repeated cycles on unchanged state.

---

## T-306 · MAC/FDB browser (P1)
**model:** sonnet-5 · **size:** S · **depends:** T-303 · **context:** `docs/features/lldp-discovery.md` §4

**Objective:** Cluster-wide "where does this MAC live" search.

**Deliverables:** FDB reader (T-102's netlink fdb) exposed via peer API and merged; correlation with guest NIC MACs from inventory (label entries: guest / vnprox-known / unknown); Tools → MAC search UI: query any MAC/partial → per-node bridge/port hits + owning guest deep-link; inspector integration (bridge detail shows its FDB with owner labels).

**Acceptance criteria:**
1. Fixture MAC lookups: guest MAC → its bridge+port+guest link; unknown MAC on an uplink port → labeled unknown with port shown.
2. Partial-MAC search returns ranked matches cluster-wide.
3. Stale FDB entries (fixture-aged) marked per staleness rules.
