# Phase 8 — Verified networking

Goal: prove the network does what the config says. v1 shows intent (config) and observed state
(drift, findings) as two separate stories; Phase 8 closes the loop — catching cross-node breakage
**before** apply instead of after, and testing the live data path instead of only the configured
one. This phase promotes every verification item flagged at T-607 and in `docs/roadmap-next.md`'s
Phase 8 section. T-801 and T-802 are independent and can run in parallel; T-803 depends on T-801's
cluster-fold helper, T-806 depends on T-802's probe endpoint, T-804/T-805 are each independent
extensions of existing collector/enrichment seams.

Invariants that bind every card here (carried from `CLAUDE.md` and `docs/roadmap-next.md`):
Proxmox stays the source of truth; nothing in this phase writes network config outside the change
engine — T-802's probes and T-805's neighbor collection are read/diagnostic only, but every action
that executes something (a probe inside a guest) is still audited; everything is cluster-aware.

---

## T-801 · Cross-node consistency validator class
**model:** strong (Opus/Fable-class) · **size:** L · **depends:** T-202, T-303, T-305 · **context:** `docs/features/change-management.md` §2 (the marked-unassigned insertion point and its "real, currently-accepted gap" note), `docs/features/topology.md` §6 (the five drift check families this class must not re-diverge from), `docs/api.md` (`GET /drift` finding shape, changeset validation finding shape, `sdn.*` finding codes), `docs/data-model.md` §3

**Objective:** Implement the validator class `internal/change/validate.go` explicitly marks as
unassigned ("a future task's cross-node consistency class ... would insert here, after safety and
before advisory"). Unlike every existing validator class, it must fold the changeset's *projected*
effect across **every** cluster node (not just the node(s) an op targets) before comparing — the
same shape of comparison `internal/drift` already runs against *live* state, run instead against
*what the state would become*. `internal/drift` already imports `internal/change` (for fix-op
types), so `internal/change` cannot import `internal/drift` back — the three comparison families
below must be factored so both packages call the same logic without a cycle; do not reimplement
bridge/MTU/SDN-realization comparison logic a second time under a different name, since that is
exactly the "same problem under two names" failure this task exists to prevent.

**Deliverables:**
- New validator class (`internal/change/validate_crossnode.go`), wired into `ValidateWithSafety`
  at the marked insertion point, running after safety and before advisory, short-circuiting the
  advisory class on any error exactly like the other classes. It builds a per-node projected
  snapshot (extending the existing `projection` folding every other class already uses) and
  compares across nodes:
  1. **Bridge/VLAN divergence** — same-named bridge presence, VLAN-awareness, and VID-set
     divergence introduced or left uncorrected by this changeset (a VLAN carried on one node's
     trunk but not a same-named bridge elsewhere in the cluster — every cluster node is a
     potential migration target). Reuses the comparison logic behind
     `drift.CheckBridgeDivergence`; code `crossnode.bridge_divergence`, `severity: error`, `fix`
     patch restoring parity when the divergence is a VID-set (not a presence) mismatch, mirroring
     drift's own fixable/not-fixable split.
  2. **MTU asymmetry** — the cross-node half of `drift.CheckMTUConsistency` (same-named
     bridge/bond MTU divergence across nodes), run against the projected state; code
     `crossnode.mtu_consistency`, `severity: error`, `fix` aligning outliers to the majority MTU
     (same alignment strategy drift's own fixable case uses).
  3. **SDN zone/bridge coverage gap** — an SDN zone's node-membership vs. actual bridge
     realization (`drift.CheckSDNRealization`'s comparison), evaluated against **every** known
     zone in the projected graph, not only zones this changeset's own `sdn.*` ops name (closing
     the gap `sdnValidate`'s `zoneBridgeExistenceFindings` leaves: a plain `bridge.delete`/
     `iface.rename` op that silently breaks an *untouched* zone's realization on one node is
     invisible to T-402's SDN class today). Code `crossnode.sdn_realization`, `severity: error`,
     detection-only (no `fix`, matching drift's own stance — creating a bridge needs a physical
     port decision this validator cannot make safely).
- Whatever package split resolves the import-cycle constraint (e.g. relocating the three pure
  comparison functions out of `internal/drift` into a lower-level package both `internal/drift`
  and `internal/change` import) is this task's call — the constraint is binding, the file layout
  is not.
- `docs/features/change-management.md` §2 updated: retract the "not a pre-apply validator class
  ... real, currently-accepted gap" paragraph now that class 4 exists; document its position and
  the three codes above.
- `docs/api.md`: the three new finding codes, documented per docs/development.md's
  definition-of-done #4.

**Acceptance criteria:**
1. Golden table test against `three-node-vlan`: a changeset op that removes a VID from one node's
   trunk while the same VID stays tagged elsewhere in the projected cluster state →
   `crossnode.bridge_divergence` error with a fix patch restoring the VID; applying the fix
   revalidates clean.
2. Golden table test: a changeset op setting one node's bond/bridge MTU divergent from the
   cluster's same-named bridge → `crossnode.mtu_consistency` error with a majority-alignment fix;
   applying the fix revalidates clean.
3. Golden test against `evpn-lab`: a changeset containing only a `bridge.delete` (no `sdn.*` ops)
   that removes a zone-realizing bridge on one member node → `crossnode.sdn_realization` error,
   no `fix`. A companion negative test proves `sdnValidate` alone (T-402's class, unmodified) does
   **not** catch this shape — demonstrating the gap this task closes, not a redundant check.
4. A test asserting the three `crossnode.*` comparison functions and `internal/drift`'s
   `CheckBridgeDivergence`/`CheckMTUConsistency`/`CheckSDNRealization` share one implementation
   (not merely equivalent-looking code) — e.g. both call into the same exported function from a
   shared package, verified by the test importing that package directly.
5. Negative-path coverage: a changeset that changes every node's same-named bridge/MTU in lockstep
   (no divergence introduced) validates with zero `crossnode.*` findings, against `three-node-vlan`
   and `evpn-lab`.
6. `make check` green; `internal/change` stays at its documented ≥90% coverage bar.

---

## T-802 · Guest-agent live path probes (engine + API)
**model:** sonnet-5 · **size:** M · **depends:** T-101, T-503 · **context:** `docs/features/firewall.md` §5 (the "verify live" P2 mention and the honesty contract it must not violate), `docs/api.md` (Path simulator section, Peer API/audit conventions), `docs/data-model.md` (Guest/GuestNic)

**Objective:** Execute a real, explicit probe (ICMP ping, TCP handshake) from a source guest via
the QEMU guest agent toward a destination, and report the observed outcome alongside the path
simulator's verdict for the identical `src→dst/proto/port` tuple plus a divergence flag. This is a
diagnostic action, never automatic and never a network-config mutation — it still goes through
audit like every other action that reaches into a guest.

**Deliverables:**
- `internal/pve`: guest-agent exec methods (`AgentExec`, `AgentExecStatus`) wrapping real PVE's
  `POST /nodes/{node}/qemu/{vmid}/agent/exec` / `GET .../agent/exec-status` — qemu-only, matching
  `GetGuestAgentInterfaces`'s existing qemu-only precedent (no LXC guest-agent equivalent).
- A probe engine (new package, e.g. `internal/probe`) that, given a `guest-nic` src (only —
  `ip`/`external` sources cannot host a probe) and a dst/proto/port, execs the appropriate in-guest
  command via guest-agent exec, polls exec-status to a bounded timeout, and classifies the outcome
  (`reachable`\|`unreachable`\|`timeout`\|`error`). Exact in-guest command choice (portable across
  guest OS/toolchains) is a needs-hardware-validation item — flag it rather than guessing.
- New audited endpoint `POST /simulate/verify` (sibling to `POST /simulate/path`, same
  `{src, dst, proto, port}` body restricted to a `guest-nic` src) → `{simulated: SimulateResult,
  observed: {outcome, detail?, execError?}, diverges: bool}`. `diverges` is true iff the simulated
  verdict makes a confident reachability claim (`allow`/`deny`/`unreachable`) that disagrees with
  the observed outcome; an `indeterminate` simulated verdict or an `error` observed outcome never
  diverges (neither makes a claim to contradict). Audited as `probe.verify` (target: src guest ref,
  result ok/error) per `docs/api.md`'s audit action vocabulary. Document the route in `docs/api.md`
  per definition-of-done #4.
- `internal/pvemock`: guest-agent `exec`/`exec-status` handlers with a fixture-scriptable outcome
  table (declare "src/dst/proto/port → outcome" per test case), added to the `sim-lab` fixture the
  simulator already uses so probe scenarios can be matched directly against existing `Simulate`
  cases.

**Acceptance criteria:**
1. Table-driven Go tests in the probe engine covering outcome classification for ICMP
   reachable/unreachable/timeout and TCP open/refused/timeout, against a fake `AgentExec`.
2. pvemock: the new exec/exec-status handlers serve a scripted outcome for a declared tuple; every
   existing `sim-lab`-based simulator test still passes unchanged (byte-compatible fixture load).
3. API test: `POST /simulate/verify` against `sim-lab` with a tuple matching an existing `allow`
   `Simulate` case and a scripted `reachable` outcome → `diverges: false`; the same tuple with a
   scripted `unreachable` outcome → `diverges: true`.
4. Audit test: a successful and a failed probe call each produce exactly one `probe.verify` audit
   row.
5. Validation: an `ip`/`external` src → `400 validation_failed`; a qemu guest with an unreachable
   guest agent → `200` with `observed.outcome: "error"` and `execError` set (not a 5xx — the
   attempt itself is the answer), `diverges: false`.
6. `docs/api.md` gains the route/shapes; `planning/reports/needs-hardware-validation.md` gains an
   entry for the exact in-guest probe command per OS family (untested against a real guest agent).
7. `make check` green.

---

## T-803 · Health-check pack 2
**model:** sonnet-5 · **size:** M · **depends:** T-602, T-801 · **context:** `docs/features/monitoring.md` §5 (stable-id/hysteresis/docsLink contract), `docs/features/sdn.md` §2 §3 (EVPN anycast gateway, exit nodes), `docs/api.md` (`GET /findings` shape and existing check vocabulary)

**Objective:** Five new checks in `internal/findings`. **Scoping correction, flagged for the
record:** `docs/roadmap-next.md`'s "path-MTU asymmetry" item is *already shipped* — plain L2
NIC→bond→bridge MTU mismatches, both within-node and cross-node, flow into the unified stream today
via `drift.CheckMTUConsistency` (see `internal/findings/mtupath_test.go`) and are additionally
caught pre-apply by T-801. Re-implementing that would be exactly the "two names, one problem"
failure T-801 exists to prevent. This task instead promotes the one genuinely uncovered MTU gap:
`validate_advisory.go`'s VXLAN/EVPN encapsulation-overhead sanity check (`checkVxlanMTU`) only runs
at changeset-validate time — nothing re-checks it continuously, so a physical underlay MTU that
degrades *after* apply (no changeset involved) goes undetected. That live/continuous form is this
task's "path-MTU asymmetry" check.

**Deliverables:** New checks, each following `health_mgmtpath.go`'s pattern (stable id, `docsLink`,
detection-only unless noted, standard hysteresis debounce per `hysteresis.go` unless the check is
structural):
- `vxlan_underlay_mtu` — a VXLAN/EVPN zone whose configured MTU doesn't leave room for the
  encapsulation overhead against the *observed* underlay path MTU (same overhead constant
  `validate_advisory.go` uses), evaluated continuously rather than only at changeset time.
- `orphan_vnet` — an `SdnVnet` whose `Zone` no longer resolves to any known `SdnZone` (the zone was
  deleted out from under it).
- `evpn_gw_inconsistency` — an EVPN zone's anycast subnet gateway realized differently (present on
  some member/exit nodes, absent or different on others); reuse T-801's cluster-fold helper for the
  cross-node comparison rather than writing a second one. Needs-hardware-validation entry required
  (exact per-node anycast-gateway realization in `/etc/network/interfaces` is unverified).
- `corosync_link_degraded` — a corosync ring reporting faulty/no-link status. Requires a new
  `internal/host.Reader` capability (e.g. shelling to `corosync-cfgtool -s`, real + fixture
  implementations) since only ring *addresses* are read today (`internal/host/corosync.go`).
  Needs-hardware-validation entry required (exact `corosync-cfgtool` output format/version).
- `trunk_unused_vlans` (informational) — a VLAN-aware trunk's tagged/allowed VID set contains a VID
  no guest NIC on that bridge/VNet actually uses. Detection-only, never auto-narrows the trunk.

**Acceptance criteria:**
1. Table-driven tests per check against a fixture-constructed graph (extend `evpn-lab` for
   `orphan_vnet`/`evpn_gw_inconsistency`, `three-node-vlan` for `corosync_link_degraded`/
   `trunk_unused_vlans`, `sim-lab` or `evpn-lab` for `vxlan_underlay_mtu`), covering both the firing
   and clean cases.
2. Each check's `id` is stable across two evaluations of unchanged input (no random/time-based
   component) and clears when the underlying condition is fixed.
3. Hysteresis behavior matches its documented window (or is explicitly structural/hysteresis-exempt
   with a doc comment justifying it, `mgmt_single_path`-style — a decision, not a default).
4. `GET /findings` golden test: all five new `check` values appear with `source: "health"`,
   correct `docsLink`, and `fixable: false` throughout (none of these five has a computable fix).
5. `docs/features/monitoring.md` §5 and `docs/api.md`'s health-check vocabulary list updated with
   the five new `check` names.
6. `planning/reports/needs-hardware-validation.md` gains the two entries above.
7. `make check` green.

---

## T-804 · LACP actor/partner state in the bond inspector
**model:** sonnet-5 · **size:** S · **depends:** T-102, T-104, T-602 · **context:** `docs/features/change-management.md` §5 (the flagged gap — `BondEditor.tsx` self-documents it), `docs/data-model.md` (Bond/BondSlave key fields)

**Objective:** Parse 802.3ad actor/partner system ID, key, and per-slave sync/collecting/
distributing state from `/proc/net/bonding/<name>` (netlink AD-info attributes opportunistically,
where the running kernel exposes them — best-effort, not a hard requirement), turning "bond is up"
into "bond is *negotiated correctly*". Surface it in the bond inspector's live LACP section and a
new `lacp_partner_mismatch` health check.

**Deliverables:**
- `internal/host/bonding.go`: extend `parseBondingProc`/`applySlaveField` to decode each slave's
  "details actor lacp pdu"/"details partner lacp pdu" block (system ID, system priority, key, and
  the port-state bitmask decoded into synchronized/collecting/distributing bools) into new fields
  on `host.BondSlave`; prefer a netlink-sourced read when the kernel exposes it, falling back to
  `/proc` parsing otherwise.
- `internal/inventory.BondSlaveState` gains the same actor/partner fields; `Bond.fieldMap()`/
  `clone()` updated so they flow through the existing merge/provenance machinery to
  `GET /inventory/{ref}`'s `fields` with no route change needed.
- `internal/findings`: `lacp_partner_mismatch` (new `health_lacpmismatch.go`, following
  `health_bondslave.go`'s pattern) — one finding per bond whose slaves disagree on partner system
  ID/key (split-brain aggregation), or whose actor state isn't
  synchronized+collecting+distributing on a bond netlink reports as up. Standard hysteresis
  debounce (this is live negotiation state, not a structural fact like `mgmt_single_path`).
- Frontend: the bond inspector's live LACP section (`web/src/topology/InspectorPanel.tsx`) renders
  actor/partner system ID/key and a per-slave sync/collecting/distributing indicator, visually
  distinct for a mismatched vs. negotiated-correctly bond.
- `docs/data-model.md`'s Bond/BondSlave field table amended with the new fields (definition-of-done
  #4).

**Acceptance criteria:**
1. Parser table tests: a golden `/proc/net/bonding` sample with matched actor/partner state on both
   slaves, and a second sample with a desynced slave, decode correctly.
2. Inventory golden test against `three-node-vlan`'s bonded interface: actor/partner fields appear
   in `GET /inventory/{ref}`'s `fields`.
3. `lacp_partner_mismatch` golden test: a fixture bond with mismatched partner system IDs across
   its slaves → exactly one hysteresis-debounced finding; a matched-state fixture bond → none.
4. Vitest + Testing Library test on the inspector's LACP section rendering both states from fixture
   data; a `web/e2e` Playwright scenario opens the bond inspector against `make dev` + pvemock and
   asserts the LACP section renders.
5. `docs/data-model.md` updated; needs-hardware-validation entries for the exact `/proc/net/bonding`
   actor/partner block format across kernel versions and netlink AD-info attribute availability.
6. `make check` green.

---

## T-805 · ARP/neighbor tables as IPAM enrichment
**model:** sonnet-5 · **size:** M · **depends:** T-303, T-405 · **context:** `docs/features/ipam.md` §1 (the flagged gap — `NeighborSource` interface exists, unimplemented), `docs/api.md` (Peer API conventions, `Cell.sources`' reserved `"neighbor"` value), `docs/architecture.md` §5 §7 (peer API / cluster fan-out)

**Objective:** Implement the `internal/ipam.NeighborSource` seam the codebase already reserved a
slot for: a per-node ARP/neighbor-table collector, fanned in cluster-wide, merged into the IPAM
address list as `observed` records — never authoritative, matching guest-agent's existing
confidence-labeling contract exactly.

**Deliverables:**
- `internal/host.Reader` gains a `Neighbors(ctx, node)` method (real: `/proc/net/arp` for IPv4,
  the IPv6 neighbor table; fixture: backed by fixture data) returning `{ip, mac, iface, state}`,
  filtered to resolved states (REACHABLE/STALE/PERMANENT) — FAILED/INCOMPLETE entries excluded.
- New peer route `GET /api/peer/host/neighbors?node=` (additive, protocol-version-2-compatible,
  following the `links`/`fdb` precedent — no version bump).
- `internal/ipam.NeighborSource` implemented: local via `host.Reader`, peers via the new route,
  fanned out the same way `internal/topology`'s cluster-wide reads already fan out T-303-style;
  produces `Observation{Source: "neighbor", ...}` merged by the existing `merge.go` pipeline. Wired
  into `internal/ipam.Service.Config.Neighbors`.
- `Cell.sources`' already-documented but previously-unreachable `"neighbor"` value goes live — no
  API shape change, this activates a reserved value.
- No new conflict types: squatters and stale allocations reuse the existing `observed_unallocated`/
  `allocated_dark` `Conflict` types (`docs/api.md`) — this task supplies the missing data source,
  it does not invent new finding vocabulary.

**Acceptance criteria:**
1. `host.Reader.Neighbors` table tests (real parser + fixture) against a golden `/proc/net/arp`
   sample; FAILED/INCOMPLETE entries excluded.
2. Peer route test: `GET /api/peer/host/neighbors` returns fixture data, HMAC-gated like every
   other peer route.
3. `ipam-lab` fixture extended with a neighbor-observed IP with no PVE-IPAM allocation → the
   address list shows `confidence: "observed"`, `sources: ["neighbor"]`, and an
   `observed_unallocated` conflict.
4. `ipam-lab` case: an allocated address with a corroborating neighbor observation →
   `confidence: "both"`.
5. Cluster fan-out test (three-node fixture): a neighbor observed only on a peer node still merges
   into the cluster-wide subnet view; one peer unreachable degrades that peer's contribution only
   (matches the existing "quietly absent" contract other optional producers get), not a fatal
   error.
6. `GET /findings?source=ipam` golden test: the new squatter/stale findings use the existing
   `ipam:<type>|<cidr>|<sorted-ips>` id convention unchanged.
7. `docs/features/ipam.md` §1's "known gap" paragraph and `docs/api.md`'s `Cell.sources` "reserved
   for" wording updated to reflect `NeighborSource` is now implemented.
8. `make check` green.

---

## T-806 · "Verify live" simulator UX + divergence findings
**model:** sonnet-5 · **size:** M · **depends:** T-802, T-504 · **context:** `docs/features/firewall.md` §5 §6 (honesty contract — a divergence must never be presented as a silent correction of the simulated verdict), `docs/features/topology.md` §2 (the path overlay this reuses), `docs/api.md` (simulator + findings sections, T-802's `/simulate/verify` contract)

**Objective:** The path simulator UI gains an explicit "verify live" action wired to T-802's
endpoint: gate it on guest-agent availability with plain-English preconditions, render
observed-vs-simulated side by side on the existing path overlay, and raise a persisted
`sim_divergence` finding on mismatch that links back to the simulator with the tuple pre-filled.

**Deliverables:**
- `web/src/simulator/SimulatorPage.tsx`: a "Verify live" action on a completed result, enabled only
  for a `guest-nic` src resolved to a qemu guest with a reachable guest agent (grey out + a
  plain-English reason otherwise — "verify live requires a QEMU guest source with the guest agent
  running" style copy, per T-403's non-expert-copy bar). On click, calls
  `POST /simulate/verify` and renders the observed outcome alongside the simulated verdict on the
  existing path overlay (reusing the hop rendering — an observed-outcome marker distinct from the
  simulated-verdict styling), with a clear divergence callout when `diverges: true`.
- Backend: a `diverges: true` response from `POST /simulate/verify` persists one `sim_divergence`
  finding — new `Source` value `"probe"` (additive to the documented `drift`\|`lldp`\|`ipam`\|
  `health` enum) since this is a user-triggered diagnostic action, not a continuous background
  check, and deserves to be distinguishable as such. Persisted (survives a daemon restart — a new
  migration/table, `internal/store/migrations/`, since this isn't derivable from live/polled state
  the way every other producer's findings are) with a `docsLink`/deep-link carrying the exact
  src/dst/proto/port tuple back into the simulator.
- `GET /findings?source=probe` supported, following the existing AND-combined optional-filter
  contract (never a 400 for a value the server doesn't recognize, and `"probe"` is now recognized).

**Acceptance criteria:**
1. Vitest + Testing Library: "Verify live" is disabled with the correct copy for a non-qemu/
   external/IP-literal src and for a qemu src with no detected guest agent; enabled and calls
   `POST /simulate/verify` for an eligible src (mocked API response).
2. Vitest + Testing Library: result rendering shows simulated and observed outcomes side by side;
   a divergent response renders a distinct callout, a matching response does not.
3. `web/e2e` Playwright scenario (extends `web/e2e/simulator.spec.ts`) against `make dev` + pvemock
   (`sim-lab` extended per T-802 with a scripted divergent outcome): clicking Verify live surfaces
   the divergence callout on the map overlay.
4. Backend table test: a `diverges: true` call creates exactly one `sim_divergence` finding
   (`source: "probe"`) that survives a simulated daemon restart; its docsLink/deep-link query
   params round-trip to the exact tuple.
5. `GET /findings?source=probe` golden test: filters correctly, no `400` for the new value, no
   change to any existing source's filtering behavior.
6. `docs/api.md`'s `Source` enum, the findings section, and the simulator route section updated;
   `docs/data-model.md` gains the new table.
7. `make check` green.
