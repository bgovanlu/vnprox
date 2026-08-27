# Phase 39 — Deepen the map

**Arc:** *the core promise is "see your network"; these twelve cards add the layers operators
currently shell out for — STP, multicast, routing, the compiled firewall, cross-cluster links, and
the views that stitch existing entity/finding data into a story instead of a table.*

## Premise

Source: `planning/roadmap-open-source.md`'s Phase 39 table (T-3901–T-3912) and Sequencing section.
Cards below are **stubs at Phase-37 fidelity** — 2–4 sentence summary, deliverable bullets naming
real files/packages, checkable acceptance criteria. Per the roadmap's Execution model, each is
expanded to full fidelity by a sonnet agent immediately before dispatch, grounded in the code as it
exists *then*.

**The one rule that matters most for this phase.** Per `CLAUDE.md`: never model a PVE object or
kernel interface from docs, mocks, or release notes — the SDN-Fabrics defect (Phase 31) came from
four sources agreeing with each other because each copied the last. Every card here that reads new
host or kernel state (STP via `ip -d link` / `/sys/class/net/*/bridge/`, MDB via `bridge mdb show`
or netlink, FIB/RIB via netlink + FRR `vtysh`, nftables via `nft -j list ruleset`) carries an
explicit first deliverable: **observe the real output on pvecube (read-only commands only) and
check the transcript into `planning/reports/evidence/` before writing the parser or the mock
fixture.** pvecube is read-only outside authorized deploys; every command named below is a read.

**Gate.** This phase does not dispatch until Wave 0 clears (`planning/roadmap-open-source.md`
lines 21–39) — in particular **T-3712 must land before T-3905** starts, since T-3905 builds
directly on the package (`internal/neighbor`) T-3712 fixes. T-3712 is not yet closed as of this
writing; check its card before starting T-3905.

---

## Lane: L2 truth

### T-3901 · STP/RSTP on the map
**model:** sonnet-5 · **size:** M · **depends:** —

Root bridge, port roles (root/designated/blocked), and port states painted on the topology map —
the first question in any L2 loop hunt. Read-only. `internal/inventory.Bridge` today carries only a
boolean `STP`/`STPSet` pair (`internal/inventory/entity.go:349`) — no root bridge ID, no per-port
role/state — so this is genuinely new state, not a formatting change over existing fields.
`internal/findings/health_stpburst.go` already documents that Linux exposes no cumulative STP
counter, only a per-bridge topology-change *flag*; this card is about steady-state role/state, a
different (and available) surface.

**Deliverables**
- Observe `ip -d link show type bridge`, `bridge -d link show`, and
  `/sys/class/net/<bridge>/bridge/{root_id,bridge_id,root_port}` plus
  `/sys/class/net/<port>/brport/{state,designated_root,designated_bridge,priority}` on pvecube;
  check the transcript into `planning/reports/evidence/` before writing any parser.
- A pure parser in `internal/host` (new file, same shape as `lldp.go`/`fdb.go`: fetch via
  `netlink_linux.go`, parse independent of I/O, `FixtureReader` delegates in `fixture.go`).
- Extend `internal/inventory.Bridge` (and its `pick`/`fieldMap` merge convention in `entity.go`)
  with root-bridge and per-port role/state fields.
- `internal/pvemock` fixtures updated to match the observed shape, not invented from docs.
- Topology overlay in `web/src/topology` (follow `diffOverlay.ts`/`cephOverlay.ts`'s existing
  toggle-overlay pattern) marking the root bridge and blocking/forwarding ports on `EntityNode`/
  `EntityEdge`.

**Acceptance criteria**
1. Evidence transcript exists in `planning/reports/evidence/` before parser/mock code is written.
2. Root bridge and any blocked port are visually distinguishable on the map for a lab bridge with
   STP enabled, verified against `vnprox-dev`.
3. `internal/pvemock` fixture shape is checked against the transcript, not against `docs/` or the
   old fixture.
4. No new change-engine op — this is read-only.

### T-3902 · Multicast visibility
**model:** sonnet-5 · **size:** M · **depends:** —

A bridge MDB browser plus IGMP/MLD snooping state, sibling to the existing MAC/FDB browser
(`internal/host/fdb.go`'s `FlattenFDB`, `web/src/tools/MacFdbBrowser.tsx`). No multicast/MDB
reading exists anywhere in `internal/` today.

**Deliverables**
- Observe `bridge -j mdb show` and `/sys/class/net/<bridge>/bridge/multicast_snooping` on pvecube;
  evidence transcript into `planning/reports/evidence/` before the parser.
- `internal/host/mdb.go`: a pure parser mirroring `fdb.go`'s `FlattenFDB` shape (group/port/vlan
  rows tagged by bridge), with `Real` fetch in `netlink_linux.go` and `FixtureReader` delegation.
- A peer API route (`GET /api/peer/host/mdb`, following the existing `.../fdb` route's contract in
  `docs/api.md`) and `internal/pvemock` server support.
- `web/src/tools/MulticastMdbBrowser.tsx`, wired into `ToolsPage.tsx` next to `MacFdbBrowser.tsx`.

**Acceptance criteria**
1. Evidence transcript exists before parser/mock.
2. MDB entries (group, port, VLAN) list the same way FDB entries do, and per-bridge snooping
   enabled/disabled state is shown.
3. Read-only; Vitest + Testing Library coverage for the new browser component.

### T-3905 · Neighbor binding timeline
**model:** sonnet-5 · **size:** M · **depends:** T-3712 (must land first)

IP↔MAC binding history with flap detection, building on `internal/neighbor/service.go`'s existing
`Neighbors()` fan-out (local `host.Reader` + peer fan-out, already typed as `host.Neighbor`). Turns
"the ARP table now" into "what changed and when." **This package is exactly where T-3712's
duplicate-poll defect lives** (`internal/neighbor/service.go:125`, plus consumers in
`internal/ipam/service.go` and `cmd/vnproxd/rogue.go`) — building history on top of a service that
silently loses half its peer reads to the replay guard would bake a data gap into the new feature
from day one. Confirm T-3712 is closed and deployed before starting.

**Deliverables**
- A bounded, append-on-change ring (not append-on-every-poll) recording binding transitions per
  IP, following the retention-bound convention `internal/store` already establishes for
  `metric_samples`/`flow_samples` (`internal/store/metrics.go`, `internal/store/flows.go`).
- Flap detection: rapid oscillation between two MACs for one IP flagged distinctly from a single
  clean rebind.
- A web view surfaced from wherever the current ARP/neighbor table lives today (locate and extend
  rather than duplicate).

**Acceptance criteria**
1. T-3712 verified closed and deployed before this card is dispatched.
2. A binding change appears as a discrete timeline event, not only as the latest snapshot.
3. A flap sequence is distinguishable from a single rebind in the UI.
4. The new ring is bounded — no unbounded table growth.

---

## Lane: Paths

### T-3903 · Route explorer
**model:** sonnet-5 · **size:** L · **depends:** —

Kernel FIB + FRR RIB per node, a visual next-hop graph, and a "which path would this address take"
lookup. This is **not** a duplicate of `internal/sim`: `internal/sim/l3.go` explicitly declines to
evaluate L3 routing once both endpoints don't resolve to on-fabric subnets, emitting
`FeatureExternalRouting` with the comment "depends on host/upstream routing tables not carried in
the inventory snapshot — honestly not evaluated rather than guessed." T-3903 is exactly the
capability `internal/sim` disclaims, not a competing engine — the card should say so and
cross-reference that caveat rather than let the two surfaces drift apart independently.

**Deliverables**
- Observe `vtysh -c "show ip route json"` and `ip route show table all -j` on pvecube; evidence
  transcript into `planning/reports/evidence/` before the parser.
- Extend `internal/host/frr.go` (the existing BGP-summary/EVPN-VNI parser — same file, same
  fuzz-tested pure-parser-over-fetched-bytes pattern, same tolerance for FRR's shape variance
  across versions) with RIB parsing; kernel FIB read via the existing netlink infra.
- A service + read-only API route (`docs/api.md`) and a next-hop graph view, reusing
  `web/src/topology/canvasDraw.ts`/`canvasScene.ts` rendering primitives rather than a new canvas
  stack.
- Explicit cross-reference (in the expanded card and in code comments) to `internal/sim/l3.go`'s
  `FeatureExternalRouting` caveat.

**Acceptance criteria**
1. Evidence transcript exists before parser/mock code.
2. A next-hop lookup for a destination matches what `ip route get` actually returns on a lab node.
3. `internal/sim`'s caveat text and this feature's framing agree on which routing questions each
   one answers — checked, not assumed.
4. Read-only.

### T-3904 · Compiled-ruleset inspector
**model:** sonnet-5 · **size:** M · **depends:** —

A read-only view of the nftables ruleset PVE actually installed, cross-linked to the visual
firewall rules that produced each chain. No nftables reading exists anywhere in `internal/` today —
this is wholly new kernel-facing state. **Explicitly not an editor** — the permanent boundary
("vnprox never replaces the PVE firewall engine," `docs/features.md`) applies in full; this card
adds no mutation path of any kind.

**Deliverables**
- Observe `nft -j list ruleset` on pvecube; evidence transcript into `planning/reports/evidence/`
  before the parser.
- `internal/host/nftables.go`: a pure parser over fetched JSON (same fetch/parse split as
  `lldp.go`/`frr.go`), `FixtureReader` delegation, `internal/pvemock` fixtures matched to the
  transcript.
- A read-only viewer in `web/src`, cross-linked from the existing firewall rule editor so clicking
  a rule highlights the compiled chain it produced (and vice versa).

**Acceptance criteria**
1. Evidence transcript exists before parser/mock code.
2. Every rendered chain links back to the vnprox-authored rule that produced it, or is labeled
   "not vnprox-authored" for anything added out of band.
3. A test asserts the new route is GET-only; no edit affordance exists in the UI — reviewed
   explicitly against the permanent PVE-firewall-engine boundary.

### T-3906 · Guest ego view
**model:** sonnet-5 · **size:** M · **depends:** builds on T-3903/T-3904 data where available

One guest's whole network story on one screen: NICs, bridges, paths, firewall verdicts, flows,
findings. `web/src/topology/InspectorPanel.tsx` shows an entity; this shows a neighborhood — a new
view, not a new InspectorPanel tab.

**Deliverables**
- A new composed view (e.g. `web/src/guest/GuestEgoView.tsx`) assembling existing queries —
  `guestInteriorQueries.ts`, `entityHistoryQuery.ts`, findings queries, flow queries — onto one
  screen, plus whatever T-3903/T-3904 expose by the time this card is dispatched (path evaluation,
  compiled-ruleset verdict).
- A new route and nav entry.
- Every panel deep-links to its owning full view, matching the convention `DashboardPage.tsx`'s
  tiles already use.

**Acceptance criteria**
1. From one guest, an operator reaches NICs, bridge attachment, at least one path evaluation,
   firewall verdict summary, recent flows, and open findings without leaving the page.
2. Every panel deep-links out.
3. Read-only; Vitest + Testing Library coverage for the composition logic.

---

## Lane: Map layers

### T-3908 · "What changed" heat layer
**model:** sonnet-5 · **size:** S · **depends:** —

Paint entities by config-change recency from snapshot history; incident triage starts with "what
moved last." No new backend read — snapshot/entity history already exists
(`web/src/topology/entityHistoryQuery.ts`, `web/src/topology/history/historyPlayback.ts`).

**Deliverables**
- A new overlay module in `web/src/topology` following `diffOverlay.ts`/`cephOverlay.ts`'s exact
  shape (same toggle convention, same composability with other overlays) computing per-entity
  "time since last change" and mapping it to a color scale.
- A legend/toggle wired into wherever the existing overlay toggles live.

**Acceptance criteria**
1. Recently changed entities are visually distinguishable from stable ones using data already in
   snapshot history — no new backend route added.
2. The overlay composes with (does not visually collide with) the diff and Ceph overlays.
3. Vitest coverage for the recency-to-color mapping function.

### T-3912 · Blast-radius lens
**model:** sonnet-5 · **size:** M · **depends:** —

From a finding or a failsim result, collapse the map to the affected subgraph.
`internal/failsim/impact.go` already computes `Impact` (severity, quorum risk, management-path
loss, guests losing uplink, stranded SDN segments) — nothing renders it as a focused view today.

**Deliverables**
- A map mode in `web/src/topology` that, given a failsim `Impact` result or a finding's
  affected-entity set, dims/hides everything outside that subgraph — reuse `canvasScene.ts`'s
  existing node/edge filtering rather than a new rendering path.
- Entry points from a finding row (`web/src/findings`) and from wherever failsim results already
  surface in the UI (locate and extend).

**Acceptance criteria**
1. Triggering the lens from a real failsim `Impact` result collapses the map to exactly the
   entities in `Impact`'s affected sets.
2. The lens is reversible (clear/reset returns to the full map) without a page reload.
3. Read-only, confirmed against `failsim.Input`'s existing no-mutation contract.
4. Vitest coverage for the subgraph-filter function.

### T-3910 · Flow replay
**model:** sonnet-5 · **size:** M · **depends:** —

Animate the bounded flow/metric rings across the map — distinct from the existing config-history
playback (`web/src/topology/history/historyPlayback.ts`, `HistoryTimeline.tsx`), which replays
inventory/topology snapshots, not flow/metric samples. **The ring stays bounded; this is a view,
not a warehouse — no retention increase.**

Note for the expanding agent: the two rings are **not** both 24h, contrary to the roadmap's
shorthand. `metric_samples` is pruned to a 24h rolling window (`internal/store/metrics.go`,
`docs/data-model.md` §2); `flow_samples` defaults to a 60-minute retention window
(`internal/store/flows.go`, `docs/features/monitoring.md`). Scope "flow replay" to each ring's
actual documented window rather than assuming 24h for flow data.

**Deliverables**
- A new scrub/animate view in `web/src/topology` (separate component from `HistoryTimeline.tsx`,
  reusing `canvasDraw.ts` primitives) that plays flow/metric activity across map edges over each
  ring's real retention window.
- No change to `internal/store`'s prune/retention constants.

**Acceptance criteria**
1. The replay window matches each ring's actual documented retention — metrics at 24h, flows at
   `flow_samples`' own (shorter, configurable) window — not a single assumed figure for both.
2. Visibly distinct entry point and UI from `HistoryTimeline.tsx`'s config-history playback, so
   operators cannot confuse the two.
3. No modification to retention/prune bounds in `internal/store`.
4. Vitest coverage for the scrub/animation state.

### T-3907 · Physical cabling plan
**model:** sonnet-5 · **size:** M · **depends:** —

Rack/cable map derived from LLDP with printable output. `internal/host/lldp.go` already parses real
switch-side LLDP data (`ParseLLDP`, `LLDPNeighbor`) — **no new host observation is needed for LLDP
itself**; the new work is the rack/cable diagram layout and print output, not a new parser. State
this plainly in the expanded card so the agent doesn't re-derive LLDP reading from scratch.

**Deliverables**
- A rack/cable diagram view driven by `LLDPNeighbor` data (chassis ID, port ID, port description)
  already flowing through the inventory/peer pipeline.
- Print output, extending whatever pattern `web/src/topology/export.ts`/`ExportMapMenu.tsx` already
  use for map export rather than building a second export path.
- An explicit "not discovered" state for links with no LLDP neighbor, rather than a blank slot.

**Acceptance criteria**
1. The diagram renders from real LLDP data on `vnprox-dev`, not only from a synthetic fixture.
2. Printable output produces a usable layout (manual check + a snapshot test).
3. Missing LLDP data renders as an explicit gap, never a silently blank rack slot.

---

## Independent

### T-3909 · Federated map stitching
**model:** sonnet-5 · **size:** L · **depends:** —

**Repo fact that changes this card's scope:** the global multi-cluster map largely already exists.
`internal/federation`, `internal/api/federationtopo.go` (T-1202's `GET /federation/topology`,
`GET /federation/topology/clusters/{id}`, `GET /federation/search`), and
`web/src/topology/federation/{GlobalTopologyView.tsx,ClusterCapsule.tsx,GlobalTopologyGate.tsx}`
already render one capsule per attached cluster with drill-down and the standard
partial/`failedClusters` degrade-gracefully envelope. What's actually missing, per the roadmap's
"WireGuard interconnect links drawn as edges": today `GlobalTopologyView.tsx` lays capsules out in
a plain `flex-wrap` grid with **no inter-cluster edges at all**. T-3909 is that missing edge layer,
not a rebuild of the capsule view — the expanded card must say so, so the dispatched agent extends
rather than duplicates T-1202's work. **Views only — federation never owns another cluster's
config**, a boundary `federationtopo.go` already respects (read-only aggregation).

**Deliverables**
- Extend `federationtopo.go`'s response (or add a sibling read) to carry WireGuard interconnect
  link data between clusters, sourced from `internal/wireguard`.
- Extend `GlobalTopologyView.tsx` from a capsule grid to a stitched graph — reuse
  `web/src/topology/canvasDraw.ts`/`canvasScene.ts` rendering primitives — drawing interconnect
  edges between capsules.
- Preserve the existing partial/`failedClusters` degrade convention for the new edge data.

**Acceptance criteria**
1. WireGuard interconnect links render as edges between cluster capsules on the existing global
   map.
2. An unreachable cluster degrades its own edges/capsule without blanking the whole view (matching
   `federationtopo.go`'s existing convention).
3. No code path in this card writes to an attached cluster's config — reviewed against the
   permanent boundary.
4. Read-only.

### T-3911 · Composable dashboard
**model:** sonnet-5 · **size:** M · **depends:** —

Per-user tile grid over built-in tiles plus the plugin SDK's `dashboardTile` extension point
(`internal/plugin/caps.go`'s `ExtDashboardTile`, `docs/architecture.md`'s
`plugin.DashboardTileProvider`), which today has **no first-party surface that composes it** — a
plugin can declare a `dashboardTile` provider, but nothing on `web/src/dashboard/DashboardPage.tsx`
can place it. The current dashboard is a static grid of seven built-in tiles
(`FindingsSeverityTile`, `DriftStatusTile`, `PendingChangesetsTile`, `MgmtRedundancyTile`,
`TopTalkersTile`, `ServiceClassTile`, `RecentAuditTile`) with no reorder/add/remove affordance at
all.

**Deliverables**
- Per-user layout persistence — app-owned data per `CLAUDE.md`'s ground rules (sessions/layout are
  explicitly named as app-owned SQLite data, never a shadow of PVE config).
- Extend `DashboardTile.tsx`'s shared shell so plugin-provided tiles render through the same
  empty-state/deep-link contract built-in tiles already use — no bespoke rendering path for plugin
  tiles.
- A reorder/add/remove UI over `DashboardPage.tsx`'s current static grid.

**Acceptance criteria**
1. A user can add, remove, and reorder tiles — including at least one plugin-provided
   `dashboardTile` — and the layout persists per-user.
2. Plugin tiles render through `DashboardTile.tsx`'s existing contract; no separate rendering path.
3. The `netRead` capability ceiling from `caps.go` is enforced for plugin tiles — no plugin tile
   can trigger a write.
4. Vitest + Testing Library coverage for layout persistence and reorder logic.

---

## Sequencing

```
Gate:  Wave 0 clears (esp. T-3712) before this phase dispatches.

L2 truth:    T-3901 ──► T-3902 ──► T-3905 (hard block: after T-3712 lands)
Paths:       T-3903 ──► T-3904 ──► T-3906 (composes T-3903/T-3904's data)
Map layers:  T-3908 ──► T-3912 ──► T-3910 ──► T-3907
Independent: T-3909, T-3911  (either lane, any time)
```

Within-lane order above is a recommended sequence (each card's UI/data is easier to build once its
predecessor exists), not a hard dependency — the only hard block in the phase is T-3905 on T-3712.
Every wave that reads new host/kernel state (T-3901, T-3902, T-3903, T-3904) ends with an evidence
transcript checked into `planning/reports/evidence/` *before* code, per `CLAUDE.md`. Every
peer/PVE-facing card ends with a deploy check against `vnprox-dev`, per the roadmap's Execution
model.

## Explicitly not in this phase

- **A firewall editor.** T-3904 is read-only; the PVE-firewall-engine boundary is permanent.
- **Federation owning another cluster's config.** T-3909 is views only.
- **Any retention increase.** T-3910 is a view over the existing bounded rings (24h metrics,
  60-minute flows by default) — vnprox is not a metrics warehouse.
- **New SNMP/switch-counter reads or LACP hash visualization.** Those are T-4013/T-4110 in later
  phases, not this one.
- **General switch management beyond guarded push**, and anything requiring real NICs/a physical
  switch — per the roadmap's permanent absent list; file under
  `planning/reports/needs-hardware-validation.md` if a card's expansion turns out to need one.
