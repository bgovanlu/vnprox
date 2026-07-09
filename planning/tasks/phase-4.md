# Phase 4 — SDN & IPAM

Goal: the SDN cockpit, wizards, EVPN observability, visual IPAM, DHCP, OVS. Milestone: **v0.8**.

---

## T-401 · SDN read: cockpit tree, map overlay, pending diff
**model:** sonnet-5 · **size:** M · **depends:** T-101 (T-107 for overlay wiring) · **context:** `docs/features/sdn.md` §1 (the spec), `docs/api.md` (/sdn)

**Objective:** Full visibility of the SDN stack before any SDN writes.

**Deliverables:** SDN collector additions (zones/vnets/subnets incl. per-node status endpoints, staged-vs-running config); inventory SDN entities finalized (data model contract); `GET /sdn` per API doc (tree + per-node apply/health status); pending-state diff (staged vs. running rendered as a first-class diff view per spec); SDN cockpit UI: tree + detail panels; topology map overlay layer upgrade: VNet planes across realizing bridges, VXLAN/EVPN VTEP mesh edges with MTU annotations per spec.

**Acceptance criteria:**
1. `evpn-lab` fixture: tree renders all zone types with correct per-node status; golden `/sdn` JSON.
2. Fixture with staged-but-unapplied SDN change → pending diff renders exactly the staged delta; applied state shows "in sync".
3. Map overlay: VNet plane connects the right bridges on the right nodes; VTEP mesh renders for the VXLAN zone (fixture-verified).
4. A zone with a node reporting error status paints amber/red in tree and map consistently.

---

## T-402 · SDN ops & apply orchestration
**model:** sonnet-5 · **size:** M · **depends:** T-401, T-205 · **context:** `docs/features/sdn.md` §4, `docs/data-model.md` §3 (sdn ops)

**Objective:** SDN mutations through the change engine, with the orchestrated cluster apply.

**Deliverables:** `sdn.*` op implementations (zone/vnet/subnet create/update/delete, `sdn.apply`) with planner integration (sdn.apply ordered last per data model); SDN-specific validators (zone node coverage, bridge existence on member nodes, VXLAN MTU math per spec, tag uniqueness, deletion guards: vnet with attached guests / subnet with allocations); apply orchestration per spec §4: pre-validation, `PUT /cluster/sdn`, per-node task progress streamed, post-apply health verification, failure → node task-log deep-link; rollback semantics for SDN (pre-snapshot of `/etc/pve/sdn/*.cfg` + re-apply, from T-205 machinery); SDN editors UI (forms per entity, drawer-integrated).

**Acceptance criteria:**
1. Full lifecycle against pvemock: create VLAN zone + vnet + subnet in one changeset → plan shows sdn.apply last → apply → per-node progress → committed; fixture SDN state updated.
2. Deleting a vnet with attached guest NICs → blocked with attachment list (reattach-in-same-changeset clears it, mirroring T-203 pattern).
3. VXLAN MTU validator: underlay 1500 + vnet MTU 1500 → warning with fix patch (set 1450); fix applies clean.
4. Injected node apply failure → post-apply verification fails, changeset `failed`, rollback restores `sdn/*.cfg`, task-log link present.

---

## T-403 · Zone wizards
**model:** sonnet-5 · **size:** M · **depends:** T-402 · **context:** `docs/features/sdn.md` §2 (the spec — including the plain-English requirement)

**Objective:** The five guided zone wizards with live topology preview.

**Deliverables:** Wizard framework (step engine, param validation, live preview pane rendering the would-be topology using the real map components on synthetic entities); the five wizards per spec with their documented explanations and behaviors (simple/SNAT; VLAN with LLDP trunk cross-check when data exists; QinQ double-tag illustration; VXLAN with peer auto-suggest + explicit MTU math; EVPN with BGP session graph preview); output = changeset draft (never direct apply); copy review: every explanatory string reads for a non-networking-expert (the spec's bar), collected in one strings file for review.

**Acceptance criteria:**
1. Each wizard completed against pvemock produces a changeset whose ops match golden expectations for a scripted input set.
2. VLAN wizard with LLDP fixture showing the VID missing on the switch port → inline warning naming the port.
3. VXLAN wizard shows MTU 1450 derivation visibly; EVPN wizard preview renders the correct session graph for a 3-peer input.
4. Preview pane updates live as parameters change (<100ms perceived; debounced).
5. Abandoning a wizard leaves no draft residue.

---

## T-404 · EVPN/BGP status
**model:** sonnet-5 · **size:** M · **depends:** T-301 (T-401 for UI placement) · **context:** `docs/features/sdn.md` §3 (the spec)

**Objective:** FRR observability: peering matrix, session detail, VNI state, exit-node health.

**Deliverables:** FRR reader in `internal/host` via `vtysh -c "show bgp summary json"` / `"show evpn vni json"` (fixed argv, fixture corpus from real FRR output incl. v8/v9 format variance, absent-FRR handling), exposed on peer API; aggregation → `GET /sdn/evpn/status` per API doc; UI: peering matrix (nodes × peers, state colors), session detail (prefixes, uptime, last error), VNI list, exit-node health per spec; flapping detection → health finding (session state changes >N in window).

**Acceptance criteria:**
1. Fixture matrix renders: established/idle/active states colored correctly; session detail matches fixture JSON.
2. Absent FRR on a node → that node reports "no EVPN" cleanly, no errors.
3. Flap fixture (scripted state oscillation) raises the finding; stable sessions don't.
4. FRR JSON parse is fuzz-clean 60s; format-variant corpus all parse.

---

## T-405 · Visual IPAM
**model:** sonnet-5 · **size:** L · **depends:** T-401 · **context:** `docs/features/ipam.md` (the spec), `docs/api.md` (/ipam)

**Objective:** The IPAM views, the multi-source merge with confidence labels, conflicts, and reserve/release ops.

**Deliverables:** `internal/ipam`: PVE IPAM reads (through the plugin transparently per spec), enrichment merge (guest-agent IPs, DHCP leases when T-406 lands — interface now, ARP/neighbor via peer API) with the documented confidence labels; conflict detection (duplicate-IP, observed-unallocated, allocated-dark) as health findings with suggested resolutions; `GET /ipam/subnets` + allocations endpoints per API doc; `ipam.alloc.create/delete` ops (change-engine); UI: subnet list with utilization, allocation grid per spec §2 (/24 grid rendering, paged blocks for larger, cell detail popover), conflict styling, reserve/release into drawer, next-free picker exported as a shared component (consumed by other forms), CSV export; detected non-SDN subnets (from bridge addresses) shown read-only.

**Acceptance criteria:**
1. Fixture with pve-IPAM allocations + agent-reported IPs renders the grid with correct per-cell states incl. one of each confidence label (golden cell-state map).
2. All three conflict types detected on the brownfield fixture with correct suggested resolutions.
3. Reserve → changeset → apply (pvemock) → grid updates; release likewise.
4. Next-free picker skips allocated/observed/reserved/gateway; component reused in ≥1 other form (bridge address field) as proof of interface.
5. A /16 renders paged without jank (perf note in report); /24 grid interaction <16ms frame budget.

---

## T-406 · DHCP management (P1)
**model:** sonnet-5 · **size:** M · **depends:** T-402, T-405 · **context:** `docs/features/sdn.md` §5

**Objective:** PVE dnsmasq DHCP: ranges, reservations (as IPAM allocations), leases view.

**Deliverables:** Range editor on subnet forms (ops within `sdn.subnet.update`); static reservations bound to guest MACs (MAC picker from inventory) implemented as `ipam.alloc.create` with MAC binding per spec ("one dataset" requirement); leases reader per node via peer API (dnsmasq lease file parse, fixture-backed) merged into IPAM enrichment; leases view UI (per zone, live-ish 30s refresh) with lease↔guest correlation.

**Acceptance criteria:**
1. Range add/change round-trips through changeset → pvemock SDN state.
2. Reservation created from a guest's MAC appears in both the IPAM grid (allocated) and the DHCP view — one record, both surfaces (assert single storage row).
3. Lease file corpus (incl. malformed lines) parses defensively; leases correlate to guests by MAC.
4. Range overlapping existing allocations → validation warning listing them.

---

## T-407 · OVS support
**model:** sonnet-5 · **size:** L · **depends:** T-205 (T-102 corpus already has OVS stanzas) · **context:** `docs/features/change-management.md` §5, `docs/data-model.md` (bridge kind ovs)

**Objective:** First-class Open vSwitch: read, visualize, edit (OVSBridge/OVSBond/OVSIntPort/OVSPort).

**Deliverables:** interfaces(5) OVS stanza support hardened end-to-end (parser already round-trips; add semantic extraction to inventory: ovs bridges with their ports/bonds/intports, tags/trunks); `ovs-vsctl`-based runtime reader (fixed argv, fixture corpus) for live state (bond status, port stats) via host reader; writer/mutators for OVS ops (extend the bridge/bond op families with `kind: ovs` rather than new op types — data model note: params carry ovs-specific fields); validators (mixing Linux-bridge ports into OVS bridges and vice versa → error; OVS bond mode enums; tag/trunk consistency); editor forms extended (kind selector drives field sets per spec); map renders OVS entities with a distinguishing badge.

**Acceptance criteria:**
1. OVS fixture cluster: inventory, map, and inspector all correct (golden topology).
2. Create OVSBridge + OVSBond + tagged OVSIntPort in one changeset → golden file output → pvemock apply → committed.
3. Cross-kind port mistakes rejected with clear findings (table tests).
4. Runtime reader corpus (real `ovs-vsctl` output samples) parses; absent OVS tooling degrades to config-only view without errors.
5. T-102's byte-identity corpus still passes (no parser regressions).
