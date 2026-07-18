# Phase 13 — Deep sight: the troubleshooting layer (v2.1)

Goal: when it breaks, the operator never leaves vnprox. Today the product is excellent at the
config-and-counters layer; the moment a problem needs packets, a historical latency trend, a
guest's own view of its network, or "what is this connection doing right now," the operator falls
back to SSH. Phase 13 builds the tools they fall back *to*, with the map as the entry point, and
closes with a guided ladder that chains them into one verdict.

Dependency shape: T-1301 (capture engine), T-1303 (latency & loss mesh), T-1304 (guest interior),
and T-1305 (conntrack/NAT explorer) are four independent entry points — the whole "instead of SSH"
toolkit — and can be built in parallel from day one of the phase; none depends on another. T-1302
(capture UX) depends only on T-1301. T-1306 (path MTU prober) depends only on T-1303's probe
infrastructure. T-1307 (guided diagnosis) is the phase's join: it composes T-1301, T-1303, T-1304,
and T-1305 into one ladder and is necessarily the last card to close. All seven build on shipped
arc-1/arc-2 machinery — the guest-agent probe engine (T-802), health pack 2 (T-803), ARP/IPAM
enrichment (T-805), verify-live (T-806), the canvas/LOD renderer (T-901/T-902), inspector v2
(T-908), and the flow explorer (T-1003) — none of it is rebuilt here.

Origin: `docs/roadmap-universal.md`'s "Phase 13 — Deep sight: the troubleshooting layer" section
and its carried-forward invariants — Proxmox (or the owning system) stays the source of truth for
every new domain this phase touches, every mutation still flows through the change engine (this
phase adds none of consequence beyond audited diagnostic actions), retention stays bounded with
explicit size/age caps, and nothing here requires real hardware to prove — mock-first, with gaps
flagged into `planning/reports/needs-hardware-validation.md`. Exit demo: a guest with intermittent
packet loss diagnosed entirely in vnprox — the latency mesh localizes it to one bond slave,
dual-point capture proves the drop, the finding links the fix; total SSH sessions opened: zero.

---

## T-1301 · Distributed packet capture engine & permission model ★

**model:** strong · **size:** L · **depends:** T-105 (auth/caps), T-303 (peer fan-out), T-206 (audit) · **context:** `docs/architecture.md` §5 §6 §7 (peer API, auth model, storage scope), `docs/security.md` (Authorization, Host footprint, Safety interlocks, Audit), `docs/api.md` (Peer API section, Audit section, `GET /findings` finding shape), `internal/auth/caps.go`, `internal/peer/`, `internal/host/`

**Objective:** Server-side capture orchestration — on the local node or any peer — with a
dedicated permission gate, server-enforced caps, and simultaneous multi-point capture across ≥2
nodes for the same flow. This card owns the entire trust boundary every capture consumer (T-1302's
UX, T-1307's diagnosis ladder, T-1701's MCP surface next arc) inherits: a wrong permission or
payload-retention decision here is a data-exposure incident that cannot be retrofitted, which is
why it is one of the arc's seven strong-executor cards.

**Safety analysis (required, T-703-level rigor):**
- **A dedicated `capture` capability, distinct from `netRead`/`netWrite`.** `internal/auth/caps.go`
  gains `CapCapture Cap = "capture"`, mapped from a PVE ACL privilege pairing that is at least as
  strict as `netWrite`'s (documented in `docs/security.md`'s Authorization section) — holding
  `netRead`/`netWrite` alone never grants capture; a session must hold `capture` explicitly.
- **Server-side, un-overridable caps.** Every capture session has a hard time cap, size cap, and
  packet-count cap enforced by the server capture loop itself (not the client-submitted filter or
  request) — whichever limit is hit first stops the capture. No API parameter, admin flag, or
  filter construction can raise these past their configured ceiling; the UI (T-1302) can request
  a lower value, never a higher one.
- **Payload bytes never persist beyond the bounded capture file.** Captured packets write only to
  one per-session file on the capturing node(s), path/size/age-capped; nothing is copied into
  SQLite, logs, or any other store. An auto-purge sweep deletes files past their age cap (and past
  the caps table's `retention_hours` even for an unfinished session's file, if the daemon
  restarts mid-capture) on the same tick-cadence pattern `internal/metrics`' ring pruning uses.
- **Audit on every start/stop.** `capture.start`/`capture.stop` audit rows record actor, target
  Ref (bridge/bond/guest NIC/SDN VNet), the resolved filter, and the caps in effect — following
  `docs/api.md`'s Audit section's existing action-vocabulary/`detail` convention.

**Deliverables:**
- New `internal/capture` package: a `Session` type (id, target Ref, node(s), filter, caps,
  status, started/stopped timestamps) and a coordinator that starts/stops capture on the local
  node directly and on peers via new HMAC-gated peer routes (`POST /api/peer/capture/start`,
  `POST /api/peer/capture/stop`, `GET /api/peer/capture/status`), added to `docs/api.md`'s Peer
  API table following T-1002's `flows`-route precedent (additive, no protocol-version bump).
- Multi-point coordination: a single capture request naming ≥2 target Refs on different nodes
  starts one session per node, correlated under one session group id, so the same flow captured on
  two nodes can be matched up later (T-1302 consumes the pairing).
- Server-side BPF filter validation: compiles/verifies the submitted filter (reject unsafe or
  oversized filters — e.g. a filter compiling to more than a configured instruction-count ceiling,
  or one that can't be scoped to the target's own interface) before any capture starts; a rejected
  filter never reaches the capture process.
- Bounded on-disk file per session (`/var/lib/vnprox/captures/<sessionId>.pcap`, configurable
  root), explicit age/size caps in `vnprox.toml` (`[capture] max_duration_sec, max_bytes,
  max_packets, retention_hours`) and an auto-purge sweep goroutine (owned, has a shutdown path,
  per `docs/development.md`'s Go standards).
- New app-store table `capture_sessions` (id, targetRef, nodes_json, filter, caps_json, status,
  startedBy, startedAt, stoppedAt, filePath, fileBytes) — app-owned intent + audit only, per the
  new-domain invariant; the capture file itself is not a shadow copy of anything Proxmox owns.
- `POST /captures {targetRef, filter, durationSec?, maxBytes?, maxPackets?, peerTargets?}`,
  `POST /captures/{id}/stop`, `GET /captures/{id}`, `GET /captures` (netRead+`capture`-gated) —
  documented in a new `docs/api.md` Captures section.
- Fixtures: a mock capture agent (`internal/pvemock` or a sibling `internal/capturemock`) that
  simulates packet generation against a scripted target, plus a small pcap sample corpus
  (`testdata/captures/`) covering Ethernet/VLAN/ARP/IP/ICMP/TCP/UDP/DNS/DHCP — the corpus T-1302's
  decoder consumes.

**Acceptance criteria:**
1. A session started without the `capture` capability (holding only `netRead`/`netWrite`) is
   rejected `403`; a session with `capture` succeeds — table test against `internal/auth/caps.go`.
2. A capture request exceeding any configured cap (duration/bytes/packets) is clamped to the
   ceiling server-side; a fixture test asserts the resulting session's effective caps never exceed
   config regardless of what the request asked for.
3. An oversized/unsafe BPF filter is rejected before the mock capture agent is invoked — zero
   capture-process calls asserted in the negative case.
4. Multi-point test: a session naming two mock nodes for the same logical flow produces two
   correlated `capture_sessions` rows sharing a session-group id; stopping the group stops both.
5. Auto-purge test (injected clock): a session's file past `retention_hours` is deleted on the next
   sweep tick; a daemon-restart-mid-capture scenario still purges the orphaned file once its age
   cap passes.
6. Every start/stop produces exactly one `capture.start`/`capture.stop` audit row with actor,
   target Ref, filter, and effective caps in `detail` — golden test against `GET /audit`.
7. Regression: no payload bytes appear anywhere outside the bounded capture file (grep-verifiable
   across the store/audit code paths, stated in the report).
8. `docs/api.md`, `docs/security.md` (new `capture` capability + Host footprint note), and
   `docs/data-model.md` (`capture_sessions` table) updated; `make check` green.

---

## T-1302 · Capture UX: BPF builder, in-browser decode, pcap download

**model:** sonnet-5 · **size:** M · **depends:** T-1301, T-908 (inspector v2), T-901 (renderer) · **context:** `docs/features/topology.md` §2 §3 (map interactions, rendering contract), `docs/api.md` (new Captures section from T-1301), `web/src/topology/InspectorPanel.tsx`, `web/src/topology/TopologyCanvasV2.tsx`

**Objective:** The right-click-to-capture entry point on the map, a guided BPF filter builder that
submits to T-1301's server-side validator, live capture status, in-browser protocol decode, and
pcap download — the whole UX layer over T-1301's engine, which owns every safety decision this
card must not re-implement or weaken client-side.

**Deliverables:**
- `web/src/capture/` (new directory): a right-click context-menu entry on bridge/bond/guest-NIC/
  SDN-VNet entities (map + inspector, following T-908's pinned-inspector affordance pattern)
  opening a capture dialog.
- `BpfBuilder.tsx`: a guided filter builder (host/port/protocol pickers composing a BPF expression)
  that submits the resulting filter string to `POST /captures` — never evaluates or enforces caps
  client-side; every cap field in the dialog is a *request*, disabled/clamped to what
  `GET /captures/{id}` reports the server actually granted.
- Live status: polls/subscribes to capture session status (bytes/packets so far, remaining
  time), rendered on the dialog and as a map badge on the capturing entity while active.
- `CaptureDecoder.ts`: in-browser decode of Ethernet/VLAN/ARP/IP/ICMP/TCP/UDP/DNS/DHCP frames from
  a fetched pcap session file, rendered as a packet list + detail pane (Wireshark-lite), built
  against T-1301's pcap sample corpus (`testdata/captures/`).
- Per-session pcap download button (`GET /captures/{id}/download`, added to `docs/api.md`'s
  Captures section) for opening in real Wireshark.
- Multi-point side-by-side view: when a session group has ≥2 correlated sessions (T-1301), a split
  view renders both nodes' decoded packet lists aligned by timestamp, for "prove where the drop
  happens" comparisons.

**Acceptance criteria:**
1. Vitest + Testing Library: `BpfBuilder.test.tsx` covers building a filter from picker state and
   submitting it; a dialog whose requested caps exceed a mocked server-granted value renders the
   server's actual (lower) value, never the requested one.
2. Vitest: `CaptureDecoder.test.ts` decodes each protocol in the T-1301 pcap corpus to the expected
   field set (table-driven, one case per protocol); a corrupt/truncated sample decodes defensively
   (partial result, no crash).
3. `web/e2e` Playwright (`web/e2e/capture.spec.ts`, new): right-click a bridge on `three-node-vlan`
   → build a filter → start capture → live status updates → stop → decode renders in-browser →
   download link fetches a pcap.
4. Multi-point Playwright scenario: a session group across two mock nodes renders the side-by-side
   view with both panes populated.
5. Regression: no client-side code path calls anything other than `POST /captures`/`.../stop` to
   start or extend a capture — a grep-verifiable check that cap enforcement is never attempted
   client-side (stated in the report).
6. `docs/api.md`'s Captures section gains the download route; `make check` (incl. `tsc --noEmit`,
   no `any`) green.

---

## T-1303 · Latency & loss mesh

**model:** sonnet-5 · **size:** L · **depends:** T-802 (guest-agent/live-probe machinery), T-902 (LOD renderer for the heatmap), T-1005 (alert routing for findings) · **context:** `docs/features/monitoring.md` §1 §2 §5, `docs/api.md` (`GET /findings` finding shape, WebSocket section), `internal/probe/` (T-802's probe engine), `internal/metrics/` (ring-store pattern), `internal/findings/` (hysteresis convention)

**Objective:** Continuous, low-rate node↔node probes on every shared VLAN/fabric (corosync,
migration, storage, guest), rendered as a latency heatmap on the map with bounded jitter/loss/
latency history per link, feeding hysteresis-debounced findings and a historical baseline the
Phase 8 live-probe verdicts (T-806) can compare against.

**Deliverables:**
- New `internal/latmesh` package (extends `internal/probe`'s existing engine rather than
  duplicating it): a scheduler that runs low-rate ICMP/TCP probes between every pair of cluster
  nodes over each shared fabric it can identify (corosync ring addresses via `internal/host`'s
  existing corosync reader, migration/storage/guest networks via inventory), off a configurable
  interval (`[latmesh] probe_interval_sec`, default 10 — deliberately coarse, this is a mesh not a
  flood).
- Bounded history rings: `latency_samples` app-store table (linkId, fromNode, toNode, fabric, at,
  rttMs, lossPct), same retention philosophy as `metric_samples` (§2 of `docs/data-model.md`) — a
  configurable window and a hard row cap, whichever prunes first; no long-term warehouse.
- `path_latency_degraded`/`path_loss` findings (source `health`, new `check` values, hysteresis
  per `internal/findings`' `hysteresis.go` window convention) — fired when a link's rolling
  RTT/loss crosses a threshold and holds; routed through T-1005's alert rules like any other
  finding.
- `GET /latmesh/heatmap` (netRead-gated, documented in a new `docs/api.md` Latency mesh section):
  per-link current + rolling jitter/loss/latency, rendered as a heatmap layer on the T-902 canvas
  (color-scaled edge overlay, distinct from the existing traffic-paint mode).
- `GET /latmesh/history?linkId=&fromTs=&toTs=` for the per-link sparkline/detail view in the
  inspector (T-908 pattern).
- Historical baseline handoff: a `Baseline(linkId) (p50, p95 RttMs, LossPct)` function T-806's
  verify-live UX can call to caption an observed probe result against the mesh's own rolling
  baseline for the same path (no T-806 code changes required this card — a consumable function,
  wiring left to whichever card next touches that surface).
- Fixture: synthetic latency/loss series (`testdata/latmesh/`) — clean, degrading, and lossy link
  scenarios driving hysteresis-finding tests without real probes.

**Acceptance criteria:**
1. Scheduler test (injected clock): against `three-node-vlan`, probes run on the configured
   interval across every shared fabric pair; no probe pair is duplicated or skipped (table test).
2. Ring-store bound: seeding beyond the configured window prunes oldest rows on the next tick;
   seeding beyond the hard cap prunes to it regardless of age — same assertion shape as T-1002's
   `flow_samples` test.
3. Synthetic degrading-link fixture crosses the RTT threshold and holds → exactly one
   `path_latency_degraded` finding after the hysteresis window, not one per raw sample; the link
   recovering clears it after the symmetric window.
4. `GET /latmesh/heatmap` against `three-node-vlan` returns per-link current values; a heatmap
   render test (Vitest) confirms the color scale renders on the T-902 canvas without colliding
   with the existing traffic-paint legend.
5. `Baseline()` returns stable p50/p95 values for a fixed synthetic series (golden test).
6. Findings route through T-1005: a fixture-triggered `path_loss` finding produces a delivery via
   a test alert rule (extends T-1005's webhook receiver test).
7. `docs/features/monitoring.md` §5 and `docs/api.md` updated with the two new checks and the
   Latency mesh section; `make check` green.

---

## T-1304 · Guest network interior inspector

**model:** sonnet-5 · **size:** M · **depends:** T-802 (guest-agent channel), T-805 (ARP/IPAM enrich), T-908 (inspector v2) · **context:** `docs/features/monitoring.md` §1 §2, `docs/features/ipam.md` §1, `docs/api.md` (Live path probe section, IPAM section), `internal/pve/` (guest-agent exec methods from T-802), `internal/ipam/`

**Objective:** An opt-in inspector tab showing a guest's inside view — interfaces, addresses,
routing table, DNS config, listening sockets, default-gateway reachability — via the QEMU guest
agent, with the LXC equivalent read from the host side, plus a diff of guest-claimed addresses
against IPAM. Read-only, opt-in, and it never trusts the guest's self-report as ground truth.

**Deliverables:**
- `internal/pve`: new guest-agent read methods reusing T-802's `AgentExec`/`AgentExecStatus`
  seam (qemu-only, matching `GetGuestAgentInterfaces`'s existing precedent) to run the interior
  read set — interfaces/addresses (already partly covered by `GetGuestAgentInterfaces`, extended
  with routes/DNS/listening sockets) via guest-agent `network-get-interfaces`/`exec`-based reads
  for the pieces the agent API itself doesn't expose. Exact in-guest command choice per OS/
  toolchain is a needs-hardware-validation item, flagged rather than guessed (per T-802's own
  precedent).
- LXC equivalent: interior data for LXC guests read from the host side (`/proc/<pid>/net/`,
  container netns inspection via `internal/host`), since LXC has no QEMU guest agent.
- New endpoint `GET /guests/{ref}/interior` (netRead-gated, opt-in per guest — a per-guest toggle
  persisted alongside layout/annotation-style app data, off by default) returning
  `{interfaces, addresses, routes, dns, listeningSockets, defaultGatewayReachable, source:
  "qemu-ga"|"lxc-host"}`. Documented in a new `docs/api.md` Guest interior section.
- IPAM diff annotation: guest-claimed addresses cross-checked against `internal/ipam`'s resolved
  view (reusing T-805's `NeighborSource`-style "observed, never authoritative" confidence
  labeling) surfaced as an inspector annotation (`ipamDiff: {claimed, allocated, matches: bool}`
  per address) — never a write to IPAM.
- UI: `web/src/topology/InteriorTab.tsx` on the T-908 inspector, opt-in toggle with plain-English
  copy on why it's off by default (reaches into the guest), rendering the interior read set and
  the IPAM diff.

**Acceptance criteria:**
1. Table-driven Go tests against a fake `AgentExec`/`AgentExecStatus` covering interior parsing
   for a representative QEMU-GA response set (interfaces/routes/DNS/sockets), extending T-802's
   guest-agent mock.
2. LXC fixture test: host-side interior read against a fixture container netns produces the same
   response shape as the QEMU path, `source: "lxc-host"`.
3. `GET /guests/{ref}/interior` for a guest with the toggle off returns `404`/an explicit
   not-enabled response, never silently reaching into the guest — table test.
4. IPAM diff test: a guest-claimed address matching an IPAM allocation → `matches: true`; a
   claimed address absent from IPAM → `matches: false`, surfaced without mutating IPAM (regression
   asserting zero IPAM writes from this path).
5. Vitest + Testing Library: `InteriorTab.test.tsx` covers the opt-in toggle copy/gating and
   rendering of the interior data + IPAM diff against a mocked API response.
6. `web/e2e` scenario: enabling the interior tab on a `sim-lab`-style fixture guest with a scripted
   guest-agent response renders the interior view end to end.
7. `planning/reports/needs-hardware-validation.md` gains an entry for the exact in-guest interior
   command set per OS family; `docs/api.md`/`docs/features/monitoring.md` updated; `make check`
   green.

---

## T-1305 · Conntrack & NAT table explorer

**model:** sonnet-5 · **size:** M · **depends:** T-303 (peer fan-out), T-1003 (flow explorer, for the guest/flow filter surface) · **context:** `docs/architecture.md` §5 §7 (peer API, host footprint), `docs/api.md` (Peer API section, Flows section), `web/src/flows/` (T-1003's filter UX pattern), `internal/host/`

**Objective:** Live per-node conntrack read, peer-proxied, filtered by guest/flow, showing state,
timers, and NAT translations — the "what is this connection doing right now" complement to the
flow explorer's sampled/historical view. Read-only this arc; no flush/delete, no long-term
retention beyond the live view.

**Deliverables:**
- `internal/host.Reader` gains a `Conntrack(ctx, node)` method (real: `/proc/net/nf_conntrack` or
  netlink conntrack subsystem via the already-approved `vishvananda/netlink`, following T-1004's
  conntrack-sampler precedent for parsing conventions; fixture: backed by fixture data) returning
  `{proto, srcIp, srcPort, dstIp, dstPort, state, timeoutSec, natSrc?, natDst?}` per entry.
- New peer route `GET /api/peer/host/conntrack?node=` (additive, protocol-version-2-compatible,
  following the `links`/`neighbors` precedent — no version bump), added to `docs/api.md`'s Peer
  API table.
- `GET /conntrack?node=&guest=&srcIp=&dstIp=&port=&state=` (netRead-gated, new `docs/api.md`
  Conntrack section): fans out to every reachable peer (mirroring `GET /audit`/`GET /flows`'s
  `partial`/`failedNodes` envelope), filters ANDed and optional per the codebase's existing filter
  convention, resolves `guest=` against inventory to the entity's known IPs before filtering.
- `web/src/conntrack/ConntrackExplorer.tsx`: a live (polling/WS-refreshed) table with the same
  filter-control shape T-1003's `FlowExplorer.tsx` established, showing state/timers/NAT
  translation columns, reachable from the map's right-click menu and the flow explorer's
  guest-pair drill-down (a "view live connections" link alongside T-1003's existing "view in Flow
  Explorer" link).
- Explicit non-goal enforced: no mutation route exists for conntrack entries — stated as a
  regression test, not just documentation.

**Acceptance criteria:**
1. `host.Reader.Conntrack` table tests (real parser + fixture) against golden
   `/proc/net/nf_conntrack`-format samples covering TCP/UDP states and both SNAT and DNAT
   translations.
2. Peer route test: `GET /api/peer/host/conntrack` returns fixture data, HMAC-gated like every
   other peer route.
3. `GET /conntrack` cluster fan-out test (three-node fixture): entries from all reachable nodes
   merge; one peer unreachable degrades that peer's contribution only, `partial`/`failedNodes` set
   — mirrors T-303's existing contract.
4. Filter test matrix: `guest=`/`srcIp=`/`dstIp=`/`port=`/`state=` each independently narrow the
   result set correctly, ANDed when combined.
5. Vitest + Testing Library: `ConntrackExplorer.test.tsx` covers filter application and NAT-column
   rendering against a seeded fixture set; `web/e2e` scenario drills from a flow-explorer guest
   pair into the live conntrack view pre-filtered to that pair.
6. Regression: no `conntrack.*` route or changeset op exists anywhere in the API surface that
   writes/flushes an entry — grep-verifiable, stated in the report.
7. `docs/api.md` updated (Peer API + new Conntrack section); `make check` green.

---

## T-1306 · Path MTU prober

**model:** sonnet-5 · **size:** M · **depends:** T-803 (health pack 2), T-1303 (mesh probe infra) · **context:** `docs/features/monitoring.md` §5, `docs/api.md` (`GET /findings` finding shape — `vxlan_underlay_mtu`), `internal/findings/` (T-803's `checkVxlanMTU`/`vxlan_underlay_mtu` pair), `internal/latmesh/` (T-1303)

**Objective:** Active per-path MTU discovery across bridges, bonds, and VXLAN/EVPN tunnels,
upgrading the MTU findings T-803 already ships (config-derived, `vxlan_underlay_mtu`) with a
*verified* (measured) MTU annotation on map edges. WireGuard-link MTU probing is declared but
wired behind a capability gate that lights up once Phase 14's T-1401 lands — this card does not
block on it.

**Deliverables:**
- New `internal/mtuprobe` package, built on T-1303's `internal/latmesh` scheduler (reused, not
  duplicated): binary-search DF-set ICMP/UDP probes along each bridge/bond/VXLAN-EVPN path already
  known to the mesh, producing a verified path MTU per link, on a coarser interval than latency
  probing (`[mtuprobe] probe_interval_sec`, default 300 — MTU rarely changes, no need to hammer
  it).
- Map-edge annotation: verified MTU rendered on the relevant edge (badge/tooltip), distinct from
  the existing config-derived MTU value shown elsewhere, so a divergence between "configured" and
  "measured" is visually legible without waiting for a finding to fire.
- Upgrade path for the T-803 MTU findings: `vxlan_underlay_mtu`'s evaluation gains an optional
  measured-MTU input where the prober has a fresh reading for that path, tightening the
  encapsulation-overhead check against the *measured* underlay MTU instead of the configured one
  when both are available — the config-only evaluation remains the fallback where no probe result
  exists yet (never a regression for paths the prober hasn't reached).
- Capability-gated WireGuard hook: a `Prober.ProbeWireGuardLink(tunnelRef)` seam declared and
  no-op'd (returns "not yet available") until a WireGuard capability flag exists — wiring deferred
  to whichever Phase 14 card lands T-1401, not implemented here.
- Explicit non-goal: this card only measures and annotates; a measured MTU mismatch stays a
  finding with a human-confirm fix (no auto-remediation), and the prober never probes a path that
  doesn't yet exist in the topology model.

**Acceptance criteria:**
1. Binary-search probe test against a fixture path with a scripted true MTU (mock responses at
   varying DF-probe sizes) converges to the exact expected value within a bounded probe count.
2. Map-edge annotation test: a `three-node-vlan`/`evpn-lab` fixture with a probed link renders the
   verified-MTU badge distinct from the configured value; a path with no probe result yet shows no
   verified badge (not a stale/zero value).
3. `vxlan_underlay_mtu` upgrade test: the same underlay scenario evaluated with and without a fresh
   measured-MTU input produces the tighter (measured-based) verdict only when the reading exists —
   table test covering both branches, extending T-803's existing check tests.
4. The WireGuard probe seam exists, is exercised by a unit test asserting its current no-op
   response, and is named in the report as the exact hook Phase 14's T-1401/T-1306-completion work
   should wire.
5. Interval/scheduling test: probes run on the configured coarser interval, reusing T-1303's
   scheduler infrastructure rather than a second scheduler (asserted via goroutine/owner
   inventory, per `docs/development.md`'s "every goroutine has an owner" convention).
6. `docs/features/monitoring.md` §5 and `docs/api.md` updated for the measured-MTU annotation and
   the upgraded `vxlan_underlay_mtu` semantics; `make check` green.

---

## T-1307 · Guided diagnosis flows

**model:** sonnet-5 · **size:** L · **depends:** T-806 (verify-live), T-1301, T-1303, T-1304, T-1305, T-1003 · **context:** `docs/features/firewall.md` §5 §6 (the honesty contract T-806 already established), `docs/api.md` (Live path probe section, Findings section, this phase's new Captures/Latency mesh/Guest interior/Conntrack sections), `internal/sim/` (path simulator), `internal/probe/`

**Objective:** A "Diagnose" action on any guest/edge that runs the ladder automatically — config
check (simulator) → live probe (verify-live) → guest interior → conntrack → capture — and produces
one readable verdict page. Every verdict links a human-confirm fix; nothing here auto-remediates.
The ladder's result shape is deliberately stable and machine-consumable: this is the scaffolding
next arc's T-1701 MCP AI operator drives, so its shape is a contract, not an internal detail.

**Deliverables:**
- New `internal/diagnose` package: a `Ladder` orchestrator that, given a guest/edge target, runs
  each available step in order — `internal/sim.Simulate` (config check), `POST /simulate/verify`
  (T-806, live probe, when eligible), the T-1304 guest interior read (when opted in), the T-1305
  conntrack query (scoped to the target), and an optional T-1301 capture (only on explicit
  escalation — a capture is never triggered silently, since it requires the `capture` capability
  and audits an action) — short-circuiting steps that don't apply to the target (e.g. no guest
  interior step for a bare bridge target) rather than failing.
- Stable ladder-result shape: `{target, steps: [{name, status: "ran"|"skipped"|"error", summary,
  detail, ranAt}], verdict: {summary, confidence, linkedFindingIds: [string], suggestedFixRef?}}`
  — documented as a new `docs/api.md` Diagnosis section; `suggestedFixRef` (when present) points
  at an existing fixable finding's `POST /findings/{id}/fix` link, never a new auto-fix mechanism.
- `POST /diagnose {targetRef, escalateToCapture?: bool}` (netRead+relevant-capability-gated per
  step — a caller without `capture` simply gets that step marked `skipped`, reason stated, rather
  than a 403 for the whole ladder) → the ladder result. Audited as `diagnose.run`.
- `web/src/diagnose/DiagnosisPage.tsx`: the "Diagnose" map/inspector action, a step-by-step
  progress view as the ladder runs, and the verdict page (summary, per-step detail expandable,
  linked findings, a "fix" button wired to the existing changeset-drawer flow where
  `suggestedFixRef` is present — no new apply path).
- Explicit out-of-scope statement (card content, not just this doc): ladder steps for surfaces not
  yet built (WireGuard/edge diagnostics) are additive once Phase 14 lands — the ladder's step list
  is a registration table, not a hardcoded sequence, specifically so Phase 14 can append without
  touching this card's code again.

**Acceptance criteria:**
1. Table-driven ladder test against `sim-lab`/`three-node-vlan`: a target eligible for every step
   runs all five in order, correct `status` per step; a target ineligible for guest interior (e.g.
   a bridge) marks that step `skipped` with a stated reason, never `error`.
2. `escalateToCapture: false` (default) never starts a capture session — regression test asserting
   zero `internal/capture` calls; `true` with the `capture` capability held starts one scoped to
   the target, audited `capture.start` as T-1301 already requires.
3. A caller lacking `capture` gets the capture step `skipped` (capability-not-held reason) while
   every other eligible step still runs and the ladder still returns a verdict — table test.
4. Verdict-shape stability test: the ladder result matches its documented JSON schema exactly
   (schema/golden test) — the contract T-1701 will depend on next arc.
5. `suggestedFixRef` round-trip: a ladder run surfacing a fixable finding's id resolves through
   `POST /findings/{id}/fix` to the same draft changeset that finding's own fix endpoint would
   produce directly (no divergent fix-computation path).
6. `web/e2e/diagnose.spec.ts` (new): "Diagnose" from the map on a `sim-lab` guest runs the ladder,
   renders the step-by-step progress and verdict page, and a linked fix opens the changeset
   drawer.
7. `docs/api.md`'s new Diagnosis section documents the stable result shape explicitly flagged as
   the contract future MCP work consumes; `make check` green.

---

## Card-author notes

No conflicts found between `docs/roadmap-universal.md`'s Phase 13 section and the design
document's §1 Phase 13 task list — task IDs, titles, P0/P1 markers, sizes, and the T-1301 strong-
executor assignment all match. `docs/api.md` and `docs/data-model.md` do not yet contain any of
this phase's new routes/tables (Captures, Latency mesh, Guest interior, Conntrack, Diagnosis) —
expected, since those sections are this phase's own deliverables, not a pre-existing doc gap.
