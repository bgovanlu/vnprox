# Phase 15 — Workload & infrastructure networks (v2.3)

Goal: map the payloads Proxmox carries, not just the config it owns. Kubernetes clusters, Ceph,
and migration/backup/corosync traffic all run *on* PVE networking today invisible to it — vnprox
already knows the underlay (bridges, bonds, VLANs, flows); this phase attributes what rides on top
of it. Kubernetes and Ceph are **read-only forever** — no changeset op, no write-scoped credential,
ever, for either domain (carried-forward invariant, `docs/roadmap-universal.md`'s Phase 15
Invariants section). The phase's two write surfaces — QoS shapes and SR-IOV/VF provisioning — are
ordinary changeset ops, staged/diffed/rollback-able exactly like every other mutation this product
makes; neither introduces a second mutation path.

Dependency shape: T-1501 (Kubernetes engine), T-1504 (service attribution), T-1505 (QoS), and
T-1506 (SR-IOV/VF) are four mutually independent entry points and can be built in parallel from day
one of the phase. T-1502 (k8s overlay UX) depends only on T-1501 landing. T-1503 (Ceph awareness)
depends on T-1504's classifier (it registers Ceph's network ranges as a traffic category) in
addition to shipped inventory/flow surfaces. T-1507 (migration planner) is the phase's join: it
depends on T-1504's classifier and on Phase 13's already-shipped latency & loss mesh (T-1303) for
link-capacity data. All seven build on shipped arc-1/arc-2 machinery — inventory (T-103), flow
ingestion and the flow explorer (T-1002/T-1003), history playback (T-1007), PBS network awareness
(T-1206), the change engine (T-205), the path simulator (T-503), and the canvas/LOD renderer
(T-901/T-902) — none of it is rebuilt here.

Origin: `docs/roadmap-universal.md`'s "Phase 15 — Workload & infrastructure networks" section and
its carried-forward invariants — Proxmox (or, for k8s/Ceph, the owning system) stays the source of
truth, every mutation this phase adds flows through the ordinary change engine, service
classification uses flow metadata only (never payload inspection — not an IDS), and anything
provable only on real hardware is flagged into `planning/reports/needs-hardware-validation.md`
rather than asserted. Exit demo: a k8s service outage traced through pod → node-guest → bridge →
bond in one view; a Ceph rebalance visibly saturating a shared link raising the finding that the
cluster network should move to the idle bond — with the changeset one click away.

---

## T-1501 · Kubernetes overlay mapping engine (read-only, CNI-aware)

**model:** sonnet-5 · **size:** L · **depends:** T-103 (inventory), T-1002 (flow ingestion, for attribution) · **context:** `docs/data-model.md` §1 (entity-kind pattern, `Ref` closed kind set), `docs/api.md` (Inventory & topology, Flows), `docs/architecture.md` §7 (storage, encrypted-at-rest credential pattern), `docs/security.md` (Authentication — AES-256-GCM session-ticket construction), `internal/inventory/`, `internal/flow/`, `planning/tasks/phase-12.md` T-1206 (PBS awareness — the same "read the owning system's own knowledge, zero new write surface" pattern this card follows for k8s)

**Objective:** A **read-only** kubeconfig client that correlates k8s nodes to PVE guests, models
pod/service CIDRs, detects the common CNIs, cross-checks NodePort/LoadBalancer exposure against
PVE firewall rules, and builds the data model T-1504-style flow attribution needs to name k8s
services in the flow explorer. Zero write surface, forever — this is not a k8s management tool.

**Deliverables:**
- New `internal/k8s` package, deliberately **not** `client-go`: a minimal kubeconfig parser (reuses
  T-1101's `gopkg.in/yaml.v3` dependency rather than adding a second YAML library) plus a
  `net/http`-only REST client issuing exclusively `GET` requests against `/api/v1/nodes`,
  `/api/v1/pods`, `/api/v1/services`, and `/apis/apps/v1/namespaces/kube-system/daemonsets` (for
  CNI detection). **Flagged new-dependency decision** (per CLAUDE.md): client-go's transitive graph
  was rejected in favor of this hand-rolled reader, stated in the report as the resolution to the
  design doc's "flag any new dependency, e.g. a k8s client" note.
- New app-store table `k8s_clusters` (id, name, kubeconfig_enc, added_by, added_at, cni_detected,
  status) — credential material encrypted at rest with the same AES-256-GCM primitive
  `docs/security.md`'s session-ticket store uses, never a shadow copy of k8s's own state.
- `GET /k8s/clusters` / `POST /k8s/clusters {name, kubeconfig}` (netWrite+CSRF, audited
  `k8s.cluster.add`) / `DELETE /k8s/clusters/{id}` (audited `k8s.cluster.remove`) — registering a
  read source, not a network mutation; documented in a new `docs/api.md` Kubernetes section.
- `CNIDetector`: Flannel (VXLAN backend annotation on nodes), Calico (`calico-node` DaemonSet in
  `kube-system`), Cilium (`cilium` DaemonSet) — best-effort, a fourth/unknown CNI is reported as
  `unknown` rather than guessed.
- Node↔guest correlation: k8s node `status.addresses` (InternalIP) matched against IPAM
  allocations / guest-agent-reported addresses already in inventory — the same "observed, never
  guessed" resolution gap `docs/api.md`'s Flows section documents for guest-NIC IP resolution;
  unmatched nodes surface as `unmatched`, never a wrong guess.
- `GET /k8s/{clusterId}/overlay` (pod CIDR / service CIDR / node correlation / detected CNI) — the
  model T-1502 renders and T-1504-style attribution consumes.
- `K8sResolver`: resolves a flow's `srcIp`/`dstIp` against a cluster's pod/service CIDRs to a k8s
  service ref, mirroring `internal/flow.GraphResolver`'s shape so T-1502 can compose it identically.
- Finding: `k8s_nodeport_exposed_without_fw_rule` (source `k8s`, new enum value — extends
  `docs/api.md`'s `GET /findings` `source` list) when a NodePort/LoadBalancer service's port has no
  covering PVE firewall allow rule on the backing guest/node.
- Fixtures: mock kubeconfig files + a mock k8s API server (`internal/k8smock`, mirroring
  `internal/pvemock`'s httptest-server convention) driven by YAML fixtures under `testdata/k8s/`
  covering Flannel, Calico, and Cilium variants.

**Acceptance criteria:**
1. `internal/k8s.DetectCNI` against each of the three fixture variants (Flannel/Calico/Cilium)
   returns the correct value; a fourth fixture with no recognizable CNI markers returns `unknown`
   — table test.
2. Node→guest correlation against a fixture where a k8s node's InternalIP matches a known
   IPAM-allocated guest address resolves the correct guest Ref; an unmatched node returns
   `unmatched`, never a wrong Ref — table test.
3. `k8s_nodeport_exposed_without_fw_rule` fires for a fixture NodePort service with no covering
   firewall rule and is silent when a matching allow rule exists — table test, both directions.
4. Regression: every method on `internal/k8s.Client` is asserted `GET`-only (reflection or a static
   method-inventory test) — no code path can issue a mutating call against a k8s API server.
5. Kubeconfig credential ciphertext never contains the plaintext client cert/token (assert on
   stored bytes) — targeted encrypted-at-rest test.
6. `docs/api.md` (new Kubernetes section, `source: "k8s"` finding) and `docs/data-model.md`
   (`k8s_clusters` table) updated; `make check` green.

---

## T-1502 · Kubernetes overlay map layer & service-flow attribution UX

**model:** sonnet-5 · **size:** M · **depends:** T-1501, T-1003 (flow explorer/map), T-902 (LOD renderer) · **context:** `docs/features/topology.md` §1 (layers) §2 (interactions), `docs/api.md` (new Kubernetes section from T-1501, Flows section), `web/src/topology/` (T-902 canvas), `web/src/flows/` (T-1003's `FlowExplorer.tsx`)

**Objective:** Render T-1501's pod/service CIDR model as a topology overlay layer, attribute
flow-explorer traffic to k8s services, and give the "why is this pod unreachable" view that
includes the underlay half nobody else shows — pod → node-guest → bridge → bond, in one panel.

**Deliverables:**
- New "Kubernetes" layer toggle (`web/src/topology/layers/`, following the existing layer-toggle
  convention `docs/features/topology.md` §1 documents) rendering pod/service CIDR regions and
  node↔guest correlation lines from `GET /k8s/{clusterId}/overlay`.
- Flow explorer gains a `k8sService` column populated via T-1501's `K8sResolver`, alongside the
  existing `srcRef`/`dstRef` columns — attribution is display-only, never a filter that hides
  unresolved rows.
- `PodDrilldown.tsx`: selecting a pod/service on the overlay opens a panel tracing pod → its
  correlated node-guest → the guest's attached bridge → that bridge's bond, reusing T-902's
  existing hop-rendering primitives rather than a new trace renderer.
- Vitest + Testing Library for overlay rendering and flow-attribution logic; `web/e2e` Playwright
  (`web/e2e/k8s-overlay.spec.ts`) for toggle → select pod → drill-down.

**Acceptance criteria:**
1. Vitest: the Kubernetes layer renders correct pod/service CIDR shapes and node-correlation lines
   for a fixture cluster returned from `GET /k8s/{clusterId}/overlay`.
2. Vitest: flow explorer rows against a fixture with flow IPs inside k8s pod/service CIDRs display
   the correct `k8sService` attribution; an IP outside every CIDR shows no attribution, not a wrong
   guess.
3. `web/e2e/k8s-overlay.spec.ts`: toggle the Kubernetes layer on a fixture cluster → select a pod →
   drill-down panel renders the full pod → node-guest → bridge → bond chain.
4. Regression: no PATCH/PUT/DELETE/POST call site in `web/src/topology/layers/k8s*` or
   `PodDrilldown.tsx` targets any `/k8s/*` route beyond T-1501's own read/registration routes — no
   k8s object management affordance exists anywhere in the UI (grep-verifiable, stated in report).
5. `docs/features/topology.md` §1 gains the new layer's entry; `make check` (incl. `tsc --noEmit`)
   green.

---

## T-1503 · Ceph network awareness

**model:** sonnet-5 · **size:** M · **depends:** T-103 (inventory), T-1003 (flow explorer), T-1504 (service attribution) · **context:** `docs/data-model.md` §1 (entity-kind extension pattern), `docs/features/topology.md` §1 §6, `docs/api.md` (`GET /findings` finding shape, health-check pack precedent), `internal/pve/`, `internal/topology/` (NIC-path resolution), `planning/tasks/phase-12.md` T-1206 (the same "read PVE's own knowledge, zero new credentials" pattern)

**Objective:** Render Ceph's public/cluster networks as first-class map layers, attribute which
OSDs ride which bonds, classify replication-vs-client Ceph traffic in the flow explorer (via
T-1504's classifier), and raise findings for the classic footguns — Ceph and corosync sharing a
saturated link, cluster-network MTU mismatch, single-NIC Ceph. Read-only; PVE's own Ceph tooling
keeps ownership of Ceph configuration.

**Deliverables:**
- New `internal/ceph` package: discovery reads Ceph's public/cluster network CIDRs and per-OSD
  node placement from PVE's own Ceph config (`GET /cluster/ceph/*` / `/nodes/{node}/ceph/osd` via
  the existing `internal/pve` client) — no new Ceph API client, no new credentials, exactly
  mirroring T-1206's "read the owning system's own knowledge of itself" boundary.
- OSD→bond attribution: for each discovered OSD, resolves the physical bond/NIC its traffic rides
  by reusing `internal/topology`'s existing NIC-path resolution (the same resolver T-1206's
  datastore sizing hint calls).
- Map layer: Ceph public/cluster network overlay + OSD↔bond highlighting, following the same
  layer-toggle convention T-1502 establishes.
- Traffic attribution: registers Ceph's public/cluster CIDRs with T-1504's classifier so flow
  records crossing them are tagged `ceph-public`/`ceph-cluster` (replication vs client) — T-1503
  does not implement its own classification logic, it supplies T-1504's engine with Ceph's network
  declarations.
- Findings (source `health`, extending T-803's pack — same continuously-computed, hysteresis-gated
  convention): `ceph_corosync_shared_link` (Ceph cluster network and the corosync ring resolve to
  the same physical link, with combined utilization risking saturation), `ceph_cluster_mtu_mismatch`
  (cluster-network MTU diverges across OSD-hosting nodes, reusing the `internal/xnode` comparison
  T-801's cross-node validator already shares), `ceph_single_nic` (Ceph public and cluster traffic
  both ride the same single, unbonded NIC — no redundancy).
- Fixtures: Ceph status fixtures (`testdata/ceph/`) — one clean topology, one per named footgun.

**Acceptance criteria:**
1. A Ceph-config fixture produces the correct map-layer projection (public/cluster CIDRs, OSD↔bond
   attribution) — golden projection test.
2. Flow explorer attributes a synthetic flow crossing Ceph's public/cluster CIDRs to
   `ceph-public`/`ceph-cluster` correctly (table test against T-1504's classifier).
3. Each of the three named findings fires against its own dedicated fixture and stays silent
   against the clean topology fixture — table test, one row per finding.
4. Regression: no `ceph.*` changeset op type exists anywhere in `internal/change` (grep-verifiable
   over `internal/change/op.go`) and no Ceph API client with write scope exists in `internal/ceph`
   — zero write surface, stated in the report.
5. `docs/data-model.md`, `docs/api.md` (three new health-check names), and `docs/features/topology.md`
   §1 updated; `make check` green.

---

## T-1504 · Service-network attribution

**model:** sonnet-5 · **size:** M · **depends:** T-1002/T-1003 (flow), T-1007 (history playback), T-1206 (PBS awareness), T-904 (dashboard) · **context:** `docs/api.md` (Flows section — `flow.Record` shape, History section — `HistoryEvent`), `planning/tasks/phase-12.md` T-1206 (backup-path edges this card classifies against), `planning/tasks/phase-9.md` T-904 (dashboard tile convention), `internal/flow/`

**Objective:** Classify migration, backup (PBS), Ceph, and corosync traffic in the flow explorer
and history playback using flow **metadata only** — never payload inspection, this is not an IDS —
and surface a `service_traffic_on_wrong_network` finding when a service's traffic strays onto a
network it shouldn't share. This is the classifier T-1503 (Ceph) and T-1507 (migration) build on.

**Deliverables:**
- `internal/flow` gains a `Classifier` (`internal/flow/classify.go`): given a `flow.Record` + live
  inventory, assigns a `serviceClass` of `migration`\|`backup`\|`ceph-public`\|`ceph-cluster`\|
  `corosync`\|`unclassified`, using only metadata already on `flow.Record` (refs, ports, VLAN) plus
  known-network declarations: corosync's ring addresses, PVE's configured migration network,
  T-1206's node→PBS `backup-path` edges, and (once registered) T-1503's Ceph CIDRs. A registration
  interface (`RegisterNetworkSource(kind, source)`) lets T-1503/T-1507 extend the input set without
  touching this card's core logic — the design's explicit "classifier other cards build on"
  requirement.
- `serviceClass` added to `GET /flows`' `flow.Record` and `GET /history/events`' changeset-adjacent
  entries where applicable — additive fields, documented in `docs/api.md`'s Flows and History
  sections.
- New home-dashboard tile (extends T-904's tile set): per-`serviceClass` bytes/sec breakdown over
  the retained flow window.
- Finding `service_traffic_on_wrong_network` (source `flow`, new enum value — extends
  `docs/api.md`'s `GET /findings` `source` list): a classified flow observed on a VLAN/subnet
  outside its service's declared network (e.g. Ceph-classified traffic on the guest VLAN) —
  hysteresis-gated like the existing health-check pack.

**Acceptance criteria:**
1. Table test: synthetic flow records matching corosync ports, the configured migration network,
   PBS backup-path edges, and (via a registered test source) a declared Ceph CIDR each classify
   correctly; an unmatched record classifies `unclassified`.
2. `GET /flows` and `GET /history/events` responses carry `serviceClass` against a mixed-traffic
   fixture — golden test.
3. `service_traffic_on_wrong_network` fires when a classified flow's VLAN falls outside its
   service's declared network and stays silent when traffic matches its declared network — table
   test, both directions.
4. Dashboard tile renders the correct per-`serviceClass` byte/sec breakdown against a fixture flow
   corpus (Vitest).
5. Regression: `internal/flow/classify.go` has no import of any capture/payload package — flow
   metadata only, stated in the report as the classifier's honesty-contract proof.
6. `docs/api.md` (Flows, History, Findings sections) and `docs/features/monitoring.md` updated;
   `make check` green.

---

## T-1505 · QoS & traffic shaping

**model:** sonnet-5 · **size:** L · **depends:** T-205 (change engine), T-503 (path simulator engine), T-901 (renderer) · **context:** `docs/data-model.md` §3 (Changeset operations table — the op-group pattern new `qos.*` ops extend), `docs/api.md` (Changesets, Path simulator sections), `internal/change/` (`params_*.go` convention), `docs/features/change-management.md`

**Objective:** Manage per-guest-NIC rate limits (PVE's existing `rate` knob, already a
`guest.nic.update` field) and per-service tc/HTB shapes on bridges, entirely through the change
engine — staged, diffed, and rollback-able exactly like every other mutation. Teach the path
simulator shape-awareness so a shaped hop is disclosed, not silently ignored.

**Deliverables:**
- **Design decision, stated in the report:** per-guest-NIC rate limiting is *not* a new op — it
  already exists as `guest.nic.update`'s `rateMbps` field (`docs/data-model.md` §3); this card
  documents that boundary explicitly rather than duplicating it. The genuinely new surface is
  bridge-level per-service shaping.
- New `internal/qos` package (mirrors `internal/sdn`/`internal/fw`'s "service package the change
  engine's executor calls into" shape): renders tc/HTB configuration for a named shape (bridge Ref,
  optional match CIDR/VLAN, rate/ceil/priority).
- New `qos.*` op group in `docs/data-model.md` §3: `qos.shape.create`/`qos.shape.update`/
  `qos.shape.delete` (params: bridge Ref, matchCidr?, matchVlan?, rateMbit, ceilMbit?, priority?).
  `internal/change` planner integration: a new apply-step kind ordered alongside the existing
  per-node ifreload step; rollback restores the pre-snapshot shape config and re-applies it,
  following T-205's existing inverse-order rollback contract.
- Validation: schema (`rateMbit <= ceilMbit`, positive values), referential (bridge exists), safety
  (a shape on a management/corosync-path bridge is `touchesMgmtPath` — reuses T-703's ceremony
  unmodified, no override path).
- `internal/sim` (T-503) gains shape-awareness: a hop crossing a shaped bridge/NIC adds a new
  `Caveat` (`code: "qos-shaped"`) to `SimulateResult` rather than silently ignoring the shape.
- Map: a shaping-active badge on bridges/guest NICs carrying an applied shape (reuses T-901's
  badge-rendering convention).

**Acceptance criteria:**
1. `qos.shape.create` against `three-node-vlan` stages, validates, diffs, applies, and rolls back
   cleanly — golden ops + apply/rollback e2e test, mirroring T-402's SDN e2e pattern.
2. Schema validation rejects `rateMbit > ceilMbit` and non-positive values — table test.
3. A `qos.shape.*` op touching a management-path bridge carries `touchesMgmtPath: true`, inherits
   the 180s confirm-window floor and typed-ack ceremony, and has no override path anywhere in the
   API or schema — test + doc cross-reference.
4. `POST /simulate/path` across a shaped bridge surfaces the `qos-shaped` caveat; an unshaped path
   does not — table test.
5. Map renders the shaping-active badge for a fixture with an active shape, absent otherwise
   (Vitest).
6. Regression: shape config is only ever written by `internal/change`'s apply/rollback executor —
   no other code path invokes tc or writes shape state (grep-verifiable, stated in the report).
7. `docs/data-model.md` §3 (new `qos.*` group) and `docs/api.md` updated; `make check` green.

---

## T-1506 · SR-IOV & accelerated NIC lifecycle

**model:** sonnet-5 · **size:** M · **depends:** T-103 (inventory), T-205 (change engine) · **context:** `docs/data-model.md` §1 (`PhysNic.sriovVFs` — already-documented field this card gives real shape), `docs/development.md` (`host.Reader` real/fixture split), `planning/reports/needs-hardware-validation.md`, `internal/host/`, `internal/change/`

**Objective:** Inventory PF/VF topology, map today-invisible passthrough VFs to the guests using
them, validate VLAN/MAC-spoof-check consistency between VF config and the equivalent bridge policy,
and stage VF provisioning through the change engine. **Flagged needs-hardware-validation from day
one** — no acceptance criterion here may require real SR-IOV hardware.

**Deliverables:**
- Extend `internal/inventory`'s already-documented `PhysNic.sriovVFs` field into a full
  `VirtualFunction` type (id, pf Ref, macAddr, vlan?, spoofCheck bool, trust bool, assignedGuest
  Ref?, pciAddr), collected via `internal/host`'s existing netlink/PCI reader — same
  `host.Reader` real/fixture split (`docs/development.md`) every other host-level collector uses.
- Guest↔VF correlation: a guest's PCI-passthrough (`hostpci`) config, read via `internal/pve`,
  correlated against VF inventory — surfaced in the inspector as an attached entity, where today
  it's invisible.
- Validation (both as a changeset-validate-time check on staged `vf.*` ops and as a standing drift
  finding for already-diverged state): `vf_spoofcheck_mismatch` — a VF's configured VLAN/MAC
  spoof-check setting diverges from the equivalent bridge's own VLAN-awareness/VID-set policy.
- New `vf.*` op group in `docs/data-model.md` §3: `vf.provision` (params: PF Ref, count or explicit
  VF list, vlan?, macAddr?, spoofCheck?, trust?) — staged/diffed/applied/rolled-back through the
  ordinary changeset lifecycle, exercised only against the fixture `host.Reader`.
- Fixtures: PF/VF topology fixtures (`testdata/sriov/`) extending `internal/host`'s fixture Reader.
- `planning/reports/needs-hardware-validation.md` gains entries for real VF creation/kernel-driver
  behavior and firmware-level spoof-check enforcement — named in the card, not just discovered
  post-hoc.

**Acceptance criteria:**
1. A PF/VF topology fixture produces the correct `PhysNic`/`VirtualFunction` inventory projection
   — golden test, no real SR-IOV hardware involved.
2. Guest↔VF correlation: a fixture guest with `hostpci` passthrough config correlates to the
   correct VF in the inspector — table test.
3. `vf_spoofcheck_mismatch` fires when a fixture VF's VLAN/spoof-check diverges from its PF's
   bridge policy and stays silent when consistent — table test, both as a changeset validation
   error and as a drift finding.
4. `vf.provision` stages, validates, diffs, applies, and rolls back against the fixture
   `host.Reader` — golden ops + apply/rollback test.
5. `planning/reports/needs-hardware-validation.md` gains the two named entries (real VF creation,
   firmware spoof-check enforcement), cross-referenced from the report per the day-one-flag
   requirement.
6. `docs/data-model.md` (`VirtualFunction`, `vf.*` ops) and `docs/api.md` updated; `make check`
   green.

---

## T-1507 · Migration network planner

**model:** sonnet-5 · **size:** M · **depends:** T-1303 (latency mesh, Phase 13 — shipped ahead of this phase), T-1504 (service attribution) · **context:** `docs/api.md` (new Migration planner section this card adds, Latency mesh section from Phase 13's T-1303), `internal/probe/` (T-1303's mesh package), `internal/flow/classify.go` (T-1504)

**Objective:** A pre-flight, purely advisory check for live migrations and evacuations —
bandwidth headroom on the migration network versus guest RAM size and a best-effort dirty-rate
estimate — warning before a Friday-night evacuation saturates the corosync link. It never triggers
or blocks a migration; its verdict shape is a pinned interface Phase 16's failure-impact simulator
(and, through it, the already-shipped T-1103 maintenance scheduler) will consume.

**Deliverables:**
- New `internal/migration` package: `Plan(guest Ref, targetNode string) Assessment` computing
  headroom from T-1303's mesh link-capacity/utilization data on the migration network minus current
  T-1504-classified `migration` traffic volume, against the guest's configured RAM size and a
  best-effort dirty-rate estimate derived only from PVE's own guest config (explicitly flagged
  `bestEffort: true` in the response — no live guest instrumentation this arc).
- `POST /migration/preflight {guest: Ref, targetNode}` (netRead-gated): returns
  `{headroomMbps, estimatedTransferSec, verdict: "ok"|"tight"|"insufficient", bestEffort: bool,
  caveats: [string]}` — documented in a new `docs/api.md` Migration planner section, with the
  schema explicitly pinned as the stable interface Phase 16's `T-1604` failure-impact simulator
  will call.
- Guest inspector integration: a pre-flight check surfaced on the guest's migration action, called
  before the operator confirms a migration in PVE itself — vnprox never triggers or manages the
  migration; this stays a read-only advisory call.
- Explicit non-goal, enforced by regression test: no code path in `internal/migration` calls any
  PVE migration-trigger API.

**Acceptance criteria:**
1. `POST /migration/preflight` against a fixture with ample migration-network headroom returns
   `verdict: "ok"` with correct `headroomMbps`/`estimatedTransferSec` arithmetic — golden test.
2. The same route against a fixture with saturated mesh link data (reusing a T-1303 synthetic
   latency/loss fixture) and a large guest RAM size returns `verdict: "insufficient"` with an
   explanatory caveat.
3. The dirty-rate estimate is `bestEffort: true` whenever derived only from PVE config — table
   test asserting the flag is always set given this arc's data sources.
4. Verdict-shape stability test: the response matches its documented JSON schema exactly
   (schema/golden test) — the contract Phase 16's failure-impact simulator will depend on.
5. Regression: `internal/migration` has zero calls to any PVE migration-start/evacuate endpoint —
   grep/mock-assertion test, stated in the report as the advisory-only proof.
6. `docs/api.md`'s new Migration planner section documents the pinned response schema explicitly;
   `make check` green.

---

## Card-author notes

No conflicts found between `docs/roadmap-universal.md`'s Phase 15 section and the design
document's §1 Phase 15 task list — task IDs, titles, P0/P1 markers, sizes, and dependencies all
match, with one dependency-ID correction: the design document cites `T-501 (path simulator)` for
T-1505, but arc-1's T-501 is "Firewall read" — the path simulator engine is **T-503**; corrected in
T-1505's `depends:` line above, matching phase-14/phase-16's identical correction (all seven cards
are `model: sonnet-5`; none of Phase 15's cards appear in the design's §2
strong-executor table or its §3 heavyweight-review-checkpoint list, consistent with §2's stated
rationale: T-1501/1503 are large but strictly read-only, T-1504/1505/1506/1507 extend already-proven
machinery rather than defining a new safety core).

Doc references verified against the current repo: `docs/data-model.md` §1's `PhysNic.sriovVFs`
field already exists (T-1506 gives it real shape rather than inventing it), and §3's op-group table
already documents `guest.nic.update`'s `rateMbps` field (T-1505's stated boundary against
duplicating it as a new op). `docs/api.md`'s `GET /findings` `source` enum is currently
`drift`\|`lldp`\|`ipam`\|`health`\|`probe`; this phase adds two new values (`k8s` — T-1501,
`flow` — T-1504) plus three new checks under the existing `health` source (T-1503) — each is an
explicit deliverable on its introducing card, not a silent doc gap. No doc section referenced above
was invented; all are existing headings in `docs/architecture.md`, `docs/api.md`,
`docs/data-model.md`, `docs/development.md`, or `docs/features/topology.md` as of this writing.
