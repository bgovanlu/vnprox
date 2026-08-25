# vnprox — feature audit matrix

**Audit date:** 2026-08-23 · **Commit:** `42fd9d7f` · **Deployed:** `4.0.0+88+g42fd9d7f` on pvecube
(PVE 9.2.4, ~~single node~~ — see the correction below)

236 task cards across 37 waves.

**On method, corrected 2026-08-23.** This line originally read: *"Every cell is derived from the
repository and from the running daemon by a command, not from a card's claim about itself. Method is
in §Method."* Both halves were false. There is no §Method section — it was promised and never
written. And the Deployment column was **not** derived by command: 17 rows were scored
`Shipped, unproven` from a premise repeated out of `CLAUDE.md`, which one `pvecm status` would have
refuted. See correction 1.

What is actually true, per column: **Implementation**, **Docs** and **Tests** were derived from the
repository by command (file and symbol presence, doc references, test presence). **Deployment** is
the weak column, and is only as good as the evidence cited beside each row — which is why the
corrections below name evidence files rather than asserting verdicts.

## Corrections since publication

Two of this matrix's four status axes were wrong on the day it was written. Both are recorded here
rather than silently patched, because how they were wrong is more useful than the corrected value.

### 1. "single node" is false, and it invalidated the whole `Shipped, unproven` column

pvecube is a member of a **quorate two-node corosync cluster**, `vnprox-dev`, with `pve001` at
192.168.1.7 — formed **2026-08-18, five days before this audit ran.** `pvecm status` reports
`Quorate: Yes`. Evidence: `planning/reports/evidence/pve-9.2.4-cluster-vnprox-dev.txt`.

Every one of the 17 `Shipped, unproven` rows was classified on the premise that a second node did
not exist. That premise came from `CLAUDE.md` and from
`planning/reports/needs-hardware-validation.md`, both of which said so — **and the audit repeated
them instead of running one command against the node.** This is the same failure that produced the
SDN-zone defect the audit itself reported, and that CLAUDE.md explicitly warns about. The header's
claim that every cell is derived by a command does not hold for this column — see the method note
above, which is itself a correction.

The 17 rows are not re-scored here; they are re-scored by evidence in T-3705. What is corrected is
the premise, so nothing downstream keeps inheriting it. `CLAUDE.md` is fixed as of `36927119`.

The real remaining limit is **authorisation, not hardware**: we hold no credentials for `pve001` and
may not modify it. It is observable through pvecube's `pvesh` and peerable, which covers most
validation, but anything requiring a change *on* that node is blocked on its owner.

### 2. `Shipped, inert` conflated "switched off" with "deficient"

T-3707 asked the owner whether the hosted services eleven of those rows wait on are going to exist.
Answered 2026-08-23 — see `planning/hosted-services-decision.md`:

- **Registry / hub — YES.** The 7 rows stay pending, now against scheduled work (`T-3709`).
- **Telemetry (T-2503) — YES.** `T-3710` landed: `cmd/vnproxtelemetryd` /
  `internal/telemetrycollector`, docs/security.md's "The collector (T-3710)". Reclassified `Live`.
- **Hosted demo — NO.** `T-2801` and `T-2802` are reclassified **`Shipped, deliberately unhosted`**
  and are no longer a gap. Do not re-report them as one.

`T-2801` also shows the conflation directly: its built-in `--demo` mode is a separate daemon, and
being off on a production node is the *intended* state, not a deficiency. Scoring it `inert`
alongside genuinely blocked features made a correct configuration look like a defect.

`T-3303` spans both groups; it stays open under the registry decision, and only its demo half is
closed.

### 4. T-3706 found two real defects, and one row class was scored too kindly (2026-08-24)

Enabling the flow stack on the disposable lab surfaced two product bugs that no amount of reading
would have found, both the same shape as the SDN-zone defect this matrix already reports:

- **sFlow decoded every record as `bytes=0, packets=0`.** `decodeSFlowRawPacketHeader` `skip()`ped
  the `frame_length` field outright. It went unnoticed because `internal/flow/decode_test.go`'s
  hand-built golden `Record` never set `Bytes`/`Packets` either — **a decoder checked against its
  own omission.** Found by crafting a real sFlow v5 datagram, sending it over the wire between the
  two lab nodes, and reading the resulting row. Fixed in the same pass.
- **Conntrack sampling reads `/proc/net/nf_conntrack`, which cannot exist on PVE 9.**
  `CONFIG_NF_CONNTRACK_PROCFS` is compiled out; the module is loaded and netlink works fine, so
  every availability check the code performs passes and only the file is missing. Enabling the
  feature produces an error every 10 seconds and no data, forever. Filed as **`T-3711`**.

**`T-1004` and `T-1305` are therefore reclassified `Shipped, inert` → `Degraded`.** `Inert` means
"switched off; turn the key and it runs". Turning this key produces an error loop. That is a
different and worse thing, and scoring it as inert understated it.

This also re-opens the Degraded column, which correction 3 had emptied. That is the honest outcome:
the column was empty because nothing had been switched on, not because everything worked.

### 3. T-3705's re-score of the 17 `Shipped, unproven` rows (2026-08-23)

Worked against `vnprox-dev` (still the same quorate 2-node cluster, `pvecm status` re-checked
before and after, unchanged). Evidence: `planning/reports/evidence/T-3705-pvecube-2026-08-23.txt`
and `planning/reports/needs-hardware-validation.md`. T-301 and T-303 were already `Live` from
Wave 1 and are unchanged here.

**Moved to `Live`, with evidence:**
- **T-801, T-1101, T-1102** — never actually blocked by node count. All three cards' own completion
  reports say so explicitly (`T-801.md`: "No hardware-validation items — this is pure, in-process
  comparison logic"; `T-1101.md`: "No hardware validation needed... developed entirely against
  `internal/pvemock` fixtures via the real collector"; `T-1102.md` names no hardware gap at all).
  Their inclusion in the 17 was the blanket "single node" premise applied without checking the
  individual card, exactly the failure this correction section already names.

  **Amended by the coordinator, same day.** "Never blocked by node count" is a sound argument for
  removing these from the unproven column. It is *not* an argument that they are exercised, and
  `Live` means exercised. Checked on the deployed node: **no pinned spec is configured** in
  `/etc/vnprox/`, and the drift families that actually fired in five days are `sdn_zone_status` and
  `file_runtime_divergence` only. T-1102 is pinned-spec drift mode — with no pinned spec it cannot
  have run, so it is **`Shipped, inert`** (switched off by configuration), not `Live`. T-1101 stays
  `Live` on reachability, but its export/import/reconcile path has not been invoked on this
  deployment and that is not the same as proven. Recording the distinction because collapsing it is
  precisely how the original column went wrong. (T-801's live
  cluster does offer one piece of supporting, not required, evidence: the shared
  `internal/xnode.BridgeDivergences`/`CrossNodeMTU` primitives it's built on are exercised
  continuously by the live drift engine against real per-node inventory from both nodes, with zero
  false-positive cross-node findings across 5 days of real polling.)
- **T-1803** — its own deliverable, `planning/reports/blocked-validation.md`, exists (766 lines)
  and is populated with real evidence and real defects found (the corosync knet parser gap, the
  `ping`/`CAP_SETPCAP` failure, `T-1906-bug-01`'s live disposition), satisfying its acceptance
  criteria in substance.
- **T-2303** — `T-1906-bug-01`'s actual failure mode was observed live on real hardware (T-3201,
  2026-08-18): zero verification failures over hours of pve001 dialling pvecube's stale-SAN cert
  by IP, because the fix verifies by PVE node name. Already in `needs-hardware-validation.md`.
- **T-3201** — the card's own status entry (`planning/tasks/phase-32.md`) already reads "done",
  backed by `blocked-validation.md`'s §1–§2, and Wave 1 added further corroboration on top.
- **T-2410** (`cluster-ssh` packaging job) — never ran before this pass. First 5 attempts failed
  inside `make build`'s own `npm ci`/`vite build` step, before `packaging/test/cluster-ssh.sh`
  ever ran; root-caused (not just retried past) to two concurrent agent processes both running
  `make build` against the same `web/node_modules` directory at once, plus a real but separate
  `PATH` gap (`go` not on a non-interactive shell's `PATH` in this environment). With the race
  resolved and `PATH` set explicitly, the job **ran to completion and passed**, including the
  debt-sweep-item-8 PVE-token-copy check that had never been exercised before. Evidence:
  `planning/reports/evidence/T-2410-cluster-ssh-pass-2026-08-23.log` (1,026 lines, full
  `install.sh` transcript) and `planning/reports/evidence/T-2410-cluster-ssh-attempt-2026-08-23.txt`
  (the failed attempts and root cause).

  **AC3 closed later the same day.** Two further runs were executed back to back with no
  intervening change, giving **three consecutive green runs**, which is what AC3 asks for:
  `T-2410-cluster-ssh-run2-2026-08-23.log` and `T-2410-cluster-ssh-run3-2026-08-23.log`, both
  ending `ALL CHECKS PASSED` at 863 lines each, ~10 minutes apart. The earlier five failures are
  worth keeping on record precisely because they were environmental (concurrent `make build`s
  sharing one `web/node_modules`, plus a `PATH` gap) — the job itself is not flaky, and the three
  clean runs, executed serially, are the evidence for that rather than an assertion of it.

**Stay `Shipped, unproven`, restated precisely (no longer just "needs hardware"):**
- **T-1906** (peer TLS trust) — most of its open items closed this session (real cert-chain shape
  captured on both nodes, verified against the pinned root with `openssl verify` returning `OK`
  on both). The "mixed-version rollout" item was *not* closed, despite an earlier draft of this
  section claiming it was: `git merge-base --is-ancestor f5ec68e4 0f970685` shows pve001's older
  build already contains T-1906's own merge commit, so this cluster's version skew never actually
  straddled the fix — a genuinely pre-T-1906 peer talking to a pinned one remains unobserved.
  Remaining: a custom-CA node, `pvecm updatecerts -f`, a `pve-cluster` restart, and the
  mixed-version case above — all require either mutating live PVE config or a peer this cluster
  doesn't have. **Blocked: destructive, needs the T-3704 lab.**
- **T-2602** (canary/staged apply) and **T-2902** (peer host-write parity) — both require a real
  network-changing apply against the live cluster (T-2902's peer write routes have zero traffic in
  the journal; `changeset_apply_stages` has zero rows). **Blocked: destructive, needs the T-3704
  lab (or explicit operator sign-off to apply against `vnprox-dev` directly, which this task was
  not given).**
- **T-1201, T-2001** (federation core, federation UI) — need a second, *distinct* PVE cluster to
  attach; `vnprox-dev`'s two nodes are one cluster, not two, and no second cluster exists anywhere
  in this environment. **Blocked: none of the four stated categories fit precisely — the honest
  gap is "no second PVE cluster exists to federate with," which is not the same claim as "needs
  3+ nodes."**
- **T-1407** (tunnel-aware federation transport) — needs both a second federated cluster *and* a
  live WireGuard tunnel; neither is configured (`wireguard_tunnels` = 0 rows). Same non-fitting
  blocker as above, compounded.
- **T-1203** (cross-cluster IPAM) — the specific remaining gap is a concrete NetBox/phpIPAM write
  client, which needs a real external IPAM instance — unrelated to node count or PVE at all.
- **T-2703** (drift-to-git reconciliation) — depends on T-2701's git-backed spec sync, which has
  no `[git]` section in the running config and zero PR/git activity in the journal. Never
  configured on this install; not a node-count question either.

| Wave | Item | Feature | What it does | Implementation | Docs | Tests | Deployment |
|---|---|---|---|---|---|---|---|
| 0 | T-001 | Repo scaffolding, Makefile, CI | Create the repository skeleton exactly per the documented layout, the Make targets contract, and GitHub Actions CI. | Shipped | Documented | Covered | Live |
| 0 | T-002 | Daemon skeleton | vnproxd starts, loads config, serves HTTPS with security headers, serves an embedded SPA placeholder, shuts down gracefully. | Shipped | Documented | Covered | Live |
| 0 | T-003 | SQLite store & migrations | The store layer: schema migrations and typed repositories for all documented tables. | Shipped | Documented | Covered | Live |
| 0 | T-004 | Mock PVE server & fixtures | The development linchpin: an HTTP server imitating the PVE API surface vnprox uses, driven by YAML cluster fixtures. | Shipped | Documented | Covered | Live |
| 0 | T-005 | React app shell | The SPA skeleton every UI task builds inside. | Shipped | Documented | Covered | Live |
| 0 | T-006 | Packaging skeleton | Installable artifact from day one: .deb, hardened systemd unit, installer + setup + ctl stubs. | Shipped | Documented | Covered | Live |
| 1 | T-101 | PVE API client | Typed Go client for the PVE API surface vnprox uses, supporting both auth modes. | Shipped | Documented | Covered | Live |
| 1 | T-102 | Host readers | Read the node's real network state: the interfaces(5) file (intent) and netlink (runtime). | Shipped | Documented | Covered | Live |
| 1 | T-103 | Inventory model & graph ★ | The normalized in-memory model everything reads: typed entities, edges, snapshotting, delta computation. | Shipped | Documented | Covered | Live |
| 1 | T-104 | Collectors | The poll loops feeding inventory: PVE poller and local-host poller, with lifecycle and on-demand refresh. | Shipped | Documented | Covered | Live |
| 1 | T-105 | Auth: PVE bridge, sessions, capabilities | Login with PVE credentials; | Shipped | Documented | Covered | Live |
| 1 | T-106 | Topology builder, API & WS hub | Project inventory into the renderable topology contract; | Shipped | Documented | Covered | Live |
| 1 | T-107 | Topology UI | The map: React Flow canvas, four layers, inspector, search, layouts — the product's home screen, read-only at this stage. | Shipped | Documented | Covered | Live |
| 2 | T-201 | Changeset model, store, draft API | Changesets as data: the op type system, persistence, and draft CRUD API. | Shipped | Documented | Covered | Live |
| 2 | T-202 | Validation framework + schema/referential validators | The layered validator pipeline and its first two classes, with machine-applicable fixes. | Shipped | Documented | Covered | Live |
| 2 | T-203 | Safety interlocks & protected interfaces | The hard-error validator class that keeps users from sawing off the branch: management IP, corosync links, guest-bearing bridge deletion. | Shipped | Documented | Covered | Live |
| 2 | T-204 | interfaces(5) writer & differ | Turn ops into minimal edits of the interfaces AST, and render human-reviewable diffs. | Shipped | Documented | Covered | Live |
| 2 | T-205 | Apply engine: planner, executor, commit-confirm, rollback ★ | The safety core: ordered plans, execution against PVE, the commit-confirm window with daemon-side auto-rollback, and failure recovery. | Shipped | Documented | Covered | Live |
| 2 | T-206 | Snapshots, time machine, audit UI | History as a product surface: snapshot browsing/diff/restore and the audit view. | Shipped | Documented | Covered | Live |
| 2 | T-207 | Editing UI: changeset drawer, entity editors, guest NICs | The write-side UX: the drawer, the four entity editors, map drag-edits, guest NIC ops with bulk mode, and the apply/confirm flow. | Shipped | Documented | Covered | Live |
| 2 | T-208 | Raw interfaces editor | The power-user escape hatch that stays inside the safety envelope. | Shipped | Documented | Covered | Live |
| 3 | T-301 | Peer API: secret, HMAC, client/server | The authenticated intra-cluster channel every cluster feature rides on. | Shipped | Documented | Covered | Live |
| 3 | T-302 | LLDP collection, switch merging, ports table | The physical layer: lldpd integration, cross-node switch entity merging, the VLAN cross-check, and the ports table. | Shipped | Documented | Covered | Live |
| 3 | T-303 | Cluster fan-out | Make every read surface truly cluster-wide: remote host data via peers, merged queries, per-node staleness. | Shipped | Documented | Covered | Live |
| 3 | T-304 | Distributed rollback: per-node local timers | Extend T-205's safety to multi-node applies: each node arms its own local timer so no node's safety depends on cluster connectivity. | Shipped | Documented | Covered | Live |
| 3 | T-305 | Drift detection | The cross-node consistency engine and its surfacing. | Shipped | Documented | Covered | Live |
| 3 | T-306 | MAC/FDB browser (P1) | Cluster-wide "where does this MAC live" search. | Shipped | Documented | Covered | Live |
| 4 | T-401 | SDN read: cockpit tree, map overlay, pending diff | Full visibility of the SDN stack before any SDN writes. | Shipped | Documented | Covered | Live |
| 4 | T-402 | SDN ops & apply orchestration | SDN mutations through the change engine, with the orchestrated cluster apply. | Shipped | Documented | Covered | Live |
| 4 | T-403 | Zone wizards | The five guided zone wizards with live topology preview. | Shipped | Documented | Covered | Live |
| 4 | T-404 | EVPN/BGP status | FRR observability: peering matrix, session detail, VNI state, exit-node health. | Shipped | Documented | Covered | Live |
| 4 | T-405 | Visual IPAM | The IPAM views, the multi-source merge with confidence labels, conflicts, and reserve/release ops. | Shipped | Documented | Covered | Live |
| 4 | T-406 | DHCP management (P1) | PVE dnsmasq DHCP: ranges, reservations (as IPAM allocations), leases view. | Shipped | Documented | Covered | Live |
| 4 | T-407 | OVS support | First-class Open vSwitch: read, visualize, edit (OVSBridge/OVSBond/OVSIntPort/OVSPort). | Shipped | Documented | Covered | Live |
| 5 | T-501 | Firewall read: rulesets, objects, resolved view | Complete, correct visibility of pve-firewall state across all three scopes before any writes. | Shipped | Documented | Covered | Live |
| 5 | T-502 | Firewall ops & editors | Full firewall editing through the change engine. | Shipped | Documented | Covered | Live |
| 5 | T-503 | Path simulator engine ★ | Simulate(graph, src, dst, proto, port) → Result: static reachability over configured state with the blocking cause identified. | Shipped | Documented | Covered | Live |
| 5 | T-504 | Simulator UI & map path rendering | Make the simulator a first-class tool: endpoint pickers, verdict presentation, the path drawn on the map. | Shipped | Documented | Covered | Live |
| 5 | T-505 | Firewall log viewer (P1) | Tail and correlate pve-firewall logs cluster-wide. | Shipped | Documented | Covered | Live |
| 6 | T-601 | Metrics: live rates, map traffic mode, history | The monitoring surface: 5s sampling, WS live rates, traffic-painted map, 24h history. | Shipped | Documented | Covered | Live |
| 6 | T-602 | Health checks & findings stream | One findings stream unifying drift, LLDP mismatch, IPAM conflicts, and the monitoring-driven checks. | Shipped | Documented | Covered | Live |
| 6 | T-603 | Blueprints | Parameterized topology templates: author, capture, instantiate idempotently, plus the five starters. | Shipped | Documented | Covered | Shipped, inert |
| 6 | T-604 | Security hardening pass | Verify and tighten every documented security property; | Shipped | Documented | Covered | Live |
| 6 | T-605 | Onboarding walkthrough & config doc export | The first-login experience per the user guide, and the as-built documentation export. | Shipped | Documented | Covered | Live |
| 6 | T-606 | Packaging final: installer, apt repo, upgrades | Production-grade install/upgrade/uninstall exactly as the deployment guide documents. | Shipped | Documented | Covered | Live |
| 6 | T-607 | Performance, E2E suite, release | Prove the scale targets, ship the E2E suite, cut v1.0. | Shipped | Documented | Covered | Live |
| 7 | T-701 | Subnet gateways in the guided flow: defaults, requirement validation, PVE fidelity | A wizard-created SDN network is functional by default: the guided flow proposes a gateway where one is needed (zone-type-aware), the change engine blocks the configs real PVE rejec | Shipped | Documented | Covered | Live |
| 7 | T-702 | Management-path visibility: detect, badge, inspect | Make each node's management path a first-class, visible thing: which interface carries the node's management IP (and corosync links), which physical NICs ultimately carry it, and w | Shipped | Documented | Covered | Live |
| 7 | T-703 | Guided management-redundancy & dedicated-mgmt-interface wizard | A guided flow to fix what T-702 exposes: make a node's management path redundant, or move management onto a proper dedicated VLAN interface — the single most dangerous edit the pro | Shipped | Documented | Covered | Live |
| 8 | T-801 | Cross-node consistency validator class | Implement the validator class internal/change/validate.go explicitly marks as unassigned ("a future task's cross-node consistency class ... | Shipped | Documented | Covered | Live |
| 8 | T-802 | Guest-agent live path probes (engine + API) | Execute a real, explicit probe (ICMP ping, TCP handshake) from a source guest via the QEMU guest agent toward a destination, and report the observed outcome alongside the path simu | Shipped | Documented | Covered | Live |
| 8 | T-803 | Health-check pack 2 | Five new checks in internal/findings. | Shipped | Documented | Covered | Live |
| 8 | T-804 | LACP actor/partner state in the bond inspector | Parse 802.3ad actor/partner system ID, key, and per-slave sync/collecting/ distributing state from /proc/net/bonding/<name> (netlink AD-info attributes opportunistically, where the | Shipped | Documented | Covered | Live |
| 8 | T-805 | ARP/neighbor tables as IPAM enrichment | Implement the internal/ipam.NeighborSource seam the codebase already reserved a slot for: a per-node ARP/neighbor-table collector, fanned in cluster-wide, merged into the IPAM addr | Shipped | Documented | Covered | Live |
| 8 | T-806 | "Verify live" simulator UX + divergence findings | The path simulator UI gains an explicit "verify live" action wired to T-802's endpoint: gate it on guest-agent availability with plain-English preconditions, render observed-vs-sim | Shipped | Documented | Covered | Live |
| 9 | T-901 | Topology renderer v2: canvas/WebGL core | Replace the Graph view's DOM/SVG entity rendering (today's React Flow node-link canvas) with a canvas/WebGL engine, behind a feature flag, without touching the topology projection | Shipped | Documented | Covered | Live |
| 9 | T-902 | Level-of-detail scale semantics | Zoom-driven level-of-detail on the v2 renderer: full faceplates when zoomed in, per-node summary capsules when zoomed out (closing the physical-layer collapse gap flagged at T-607 | Shipped | Documented | Covered | Live |
| 9 | T-903 | Command palette & keyboard-first navigation | A ⌘K/Ctrl+K command palette unifying the existing / spotlight entity search with an action registry ("edit vmbr0", "new VLAN zone", "open drafts", "simulate path from <entity>"), s | Shipped | Documented | Covered | Live |
| 9 | T-904 | Home dashboard | A network-at-a-glance landing page that becomes the default route: open findings by severity, drift status, pending/awaiting-confirm changesets, mgmt-path redundancy per node (Phas | Shipped | Documented | Covered | Live |
| 9 | T-905 | Design system & accessibility pass | Consolidate the component set into one documented library (density modes, consistent form/table/drawer patterns), complete dark-theme coverage (map included, via T-901's new render | Shipped | Documented | Covered | Live |
| 9 | T-906 | Map export (SVG/PNG) + print stylesheet | A dedicated export-the-map control (SVG and PNG) honoring the current layer toggles, VLAN filter, and zoom/viewport — distinct from ToolsPage.tsx's existing config-document export | Shipped | Documented | Covered | Live |
| 9 | T-907 | Saved views & annotations | Named presets of layer+filter+zoom+selection state, shareable as URLs, plus sticky-note annotations pinned to map entities — persisted in the app-owned layout store. | Shipped | Documented | Covered | Live |
| 9 | T-908 | Inspector v2 | Pinnable multiple inspectors, side-by-side entity compare (two bonds, two nodes' bridges), and inline sparkline history in metrics tabs — building on the existing single-entity Ins | Shipped | Documented | Covered | Live |
| 9 | T-909 | Responsive triage layout | A read-only tablet/phone layout for on-call triage: dashboard, findings, and changeset confirm/rollback actions only — commit-confirm from a phone is the target scenario. | Shipped | Documented | Covered | Live |
| 10 | T-1001 | Prometheus exporter | GET /metrics in Prometheus text exposition format, exporting data the sampler and findings engine already compute — no new collection logic, this is an export surface only, per doc | Shipped | Documented | Covered | Live |
| 10 | T-1002 | Flow ingestion engine | Ingest sFlow v5, NetFlow v5/v9, and IPFIX into a bounded, node-local ring store, with cluster fan-in so any node's UI can query flows observed anywhere in the cluster — the same pe | Shipped | Documented | Covered | Shipped, inert |
| 10 | T-1003 | Flow explorer + map flow painting | Turn ingested flows into two UI surfaces: a filterable/sortable/aggregatable explorer table, and animated, weighted edges layered on the T-902 v2 map renderer — the topology map be | Shipped | Documented | Covered | Shipped, inert |
| 10 | T-1004 | Host-local flow sampling (conntrack/eBPF) | Per-bridge flow sampling on nodes with no external sFlow/NetFlow source — a conntrack-based sampler first (works with the capabilities vnproxd already holds), an eBPF-based sampler | Shipped | Documented | Covered | Degraded |
| 10 | T-1005 | Alert routing | Route findings/drift transitions to webhooks directly from vnprox, independent of PVE's own notification-target system — closing the documented gap in pvenotify.go's doc comment (P | Shipped | Documented | Covered | Live |
| 10 | T-1006 | Firewall log analytics | Aggregate the v1 log viewer (internal/fwlog) into rule hit counts, top blocked-source/destination rankings, and an unused-rule report that closes the loop between firewall editing | Shipped | Documented | Covered | Live |
| 10 | T-1007 | History playback | Scrub the map's traffic paint and flow-painted edges back through the retained metric/flow window — "what did the network look like at 02:00" — with a timeline control showing even | Shipped | Documented | Covered | Live |
| 11 | T-1101 | Declarative cluster network spec: export, import, reconcile | One versionable YAML document capturing cluster-wide network intent — bridges, bonds, VLANs, SDN, firewall, IPAM ranges — as blueprints v2, cluster-scoped (unlike v1's per-node-sel | Shipped | Documented | Covered | Live |
| 11 | T-1102 | Pinned-spec drift mode | The drift checker learns a second reference: diff live state against a *pinned* spec, distinct from and additional to the existing five cross-node-consistency families (docs/featur | Shipped | Documented | Covered | Shipped, inert |
| 11 | T-1103 | Scheduled changesets & maintenance windows | Stage now, apply inside a window, with the existing commit-confirm/rollback machinery (T-205/T-304) making unattended apply safe. | Shipped | Documented | Covered | Live |
| 11 | T-1104 | Event stream & automation tokens | An authenticated WS + webhook firehose of audit, changeset lifecycle, drift, and finding events, plus scoped API tokens for automation — vnprox-local and capability-scoped, explici | Shipped | Documented | Covered | Live |
| 11 | T-1105 | vnproxctl parity | CLI parity with the UI's read and changeset surfaces, plus vnproxctl apply spec.yaml --plan/--apply for the GitOps flow. | Shipped | Documented | Covered | Live |
| 11 | T-1106 | Terraform provider + Ansible collection | Thin shims over the changeset API — plan = validate+diff, apply = apply+confirm. | Shipped | Documented | Covered | Live |
| 11 | T-1107 | Blueprint sharing bundles | Signed, parameterized blueprint bundles exportable/importable across installations — the community layer on top of blueprints v2. | Shipped | Documented | Covered | Shipped, inert |
| 12 | T-1201 | Federation core | One vnprox instance (or a designated primary) attaches multiple PVE clusters as app-owned registry entries and aggregates reads across them with per-cluster failure isolation. | Shipped | Documented | Covered | Shipped, unproven |
| 12 | T-1202 | Global topology, search & palette | A global map with per-cluster drill-down (cluster capsules at the outermost LOD level, reusing T-902's summary-capsule primitive) and a global search/command palette spanning clust | Shipped | Documented | Covered | Live |
| 12 | T-1203 | Cross-cluster IPAM, external subnets & bidirectional sync | External (non-PVE) subnets become first-class IPAM records; | Shipped | Documented | Covered | Shipped, unproven |
| 12 | T-1204 | DNS management | Surface and edit PVE SDN's DNS plugin (PowerDNS-backed): zone/record visibility, guest names on the map, and record edits as sdn.*-style changeset ops routed through the existing S | Shipped | Documented | Covered | Live |
| 12 | T-1205 | Guarded switch config push | The read-write physical step beyond LLDP-read: driver-based (OpenConfig/gNMI first, vendor drivers behind the same interface), scoped strictly to ports facing PVE nodes (VLAN membe | Shipped | Documented | Covered | Live |
| 12 | T-1206 | PBS network awareness | Read-only: PBS hosts appear on the map with their interfaces, the backup traffic path (node → PBS) is highlighted, and the inspector shows datastore-network sizing hints. | Shipped | Documented | Covered | Live |
| 12 | T-1207 | OIDC SSO | OIDC login alongside the existing PVE ticket bridge, for federated deployments where per-cluster PVE credentials stop scaling. | Shipped | Documented | Covered | Live |
| 12 | T-1208 | v2.0 release: performance, docs, packaging | The v2.0 cut: a multi-cluster genscale performance pass, a docs freeze covering this arc's new features, packaging/upgrade-path testing from the v1.x line, a security pass over the | Shipped | Documented | Covered | Live |
| 13 | T-1301 | Distributed packet capture engine & permission model ★ | Server-side capture orchestration — on the local node or any peer — with a dedicated permission gate, server-enforced caps, and simultaneous multi-point capture across ≥2 nodes for | Shipped | Documented | Covered | Live |
| 13 | T-1302 | Capture UX: BPF builder, in-browser decode, pcap download | The right-click-to-capture entry point on the map, a guided BPF filter builder that submits to T-1301's server-side validator, live capture status, in-browser protocol decode, and | Shipped | Documented | Covered | Live |
| 13 | T-1303 | Latency & loss mesh | Continuous, low-rate node↔node probes on every shared VLAN/fabric (corosync, migration, storage, guest), rendered as a latency heatmap on the map with bounded jitter/loss/ latency | Shipped | Documented | Covered | Live |
| 13 | T-1304 | Guest network interior inspector | An opt-in inspector tab showing a guest's inside view — interfaces, addresses, routing table, DNS config, listening sockets, default-gateway reachability — via the QEMU guest agent | Shipped | Documented | Covered | Live |
| 13 | T-1305 | Conntrack & NAT table explorer | Live per-node conntrack read, peer-proxied, filtered by guest/flow, showing state, timers, and NAT translations — the "what is this connection doing right now" complement to the fl | Shipped | Documented | Covered | Degraded |
| 13 | T-1306 | Path MTU prober | Active per-path MTU discovery across bridges, bonds, and VXLAN/EVPN tunnels, upgrading the MTU findings T-803 already ships (config-derived, vxlan_underlay_mtu) with a *verified* ( | Shipped | Documented | Covered | Live |
| 13 | T-1307 | Guided diagnosis flows | A "Diagnose" action on any guest/edge that runs the ladder automatically — config check (simulator) → live probe (verify-live) → guest interior → conntrack → capture — and produces | Shipped | Documented | Covered | Live |
| 14 | T-1401 | WireGuard tunnel engine & changeset integration core ★ | First-class WireGuard as a new wg.* changeset op group — key/config generation included — flowing through the ordinary stage→validate→diff→apply→confirm/rollback lifecycle, with pr | Shipped | Documented | Covered | Shipped, inert |
| 14 | T-1402 | WireGuard map edges & "connect two clusters" wizard | Tunnels rendered as map edges — between federated clusters or to standalone endpoints — with live handshake/transfer/endpoint-drift status, plus a guided "connect two clusters" flo | Shipped | Documented | Covered | Shipped, inert |
| 14 | T-1403 | Edge & NAT cockpit | A dedicated "Edge" map layer answering "how does traffic actually leave, and what's exposed inbound?" — default routes, upstream gateway(s), PVE-host NAT/masquerade and port-forwar | Shipped | Documented | Covered | Live |
| 14 | T-1404 | IPv6 enablement suite | Promote IPv6 from "addresses render correctly" to a managed capability: RA/SLAAC/ DHCPv6 visibility per segment, dual-stack drift findings that catch the classic silent failure (v4 | Shipped | Documented | Covered | Live |
| 14 | T-1405 | WAN & upstream health | Per-uplink availability/latency/loss to configurable reference targets, multi-WAN visibility where it exists, and wan_degraded findings routed through T-1005's alert rules — the da | Shipped | Documented | Covered | Live |
| 14 | T-1406 | Ingress visibility | Read-only discovery of the reverse-proxy layer (HAProxy, nginx, Caddy, Traefik, via their own status endpoints/config, only where the operator explicitly points vnprox at them) beh | Shipped | Documented | Covered | Live |
| 14 | T-1407 | Tunnel-aware federation transport | A federation peer reachable only over a vnprox-managed WireGuard tunnel gets health-checked as a unit — tunnel down ⇒ peer unreachable ⇒ one finding, not the cascade of per-surface | Shipped | Documented | Covered | Shipped, unproven |
| 15 | T-1501 | Kubernetes overlay mapping engine (read-only, CNI-aware) | A read-only kubeconfig client that correlates k8s nodes to PVE guests, models pod/service CIDRs, detects the common CNIs, cross-checks NodePort/LoadBalancer exposure against PVE fi | Shipped | Documented | Covered | Shipped, inert |
| 15 | T-1502 | Kubernetes overlay map layer & service-flow attribution UX | Render T-1501's pod/service CIDR model as a topology overlay layer, attribute flow-explorer traffic to k8s services, and give the "why is this pod unreachable" view that includes t | Shipped | Documented | Covered | Shipped, inert |
| 15 | T-1503 | Ceph network awareness | Render Ceph's public/cluster networks as first-class map layers, attribute which OSDs ride which bonds, classify replication-vs-client Ceph traffic in the flow explorer (via T-1504 | Shipped | Documented | Covered | Shipped, inert |
| 15 | T-1504 | Service-network attribution | Classify migration, backup (PBS), Ceph, and corosync traffic in the flow explorer and history playback using flow metadata only — never payload inspection, this is not an IDS — and | Shipped | Documented | Covered | Live |
| 15 | T-1505 | QoS & traffic shaping | Manage per-guest-NIC rate limits (PVE's existing rate knob, already a guest.nic.update field) and per-service tc/HTB shapes on bridges, entirely through the change engine — staged, | Shipped | Documented | Covered | Live |
| 15 | T-1506 | SR-IOV & accelerated NIC lifecycle | Inventory PF/VF topology, map today-invisible passthrough VFs to the guests using them, validate VLAN/MAC-spoof-check consistency between VF config and the equivalent bridge policy | Shipped | Documented | Covered | Live |
| 15 | T-1507 | Migration network planner | A pre-flight, purely advisory check for live migrations and evacuations — bandwidth headroom on the migration network versus guest RAM size and a best-effort dirty-rate estimate — | Shipped | Documented | Covered | Live |
| 16 | T-1601 | Flow baselining & anomaly findings | Learn per-guest/per-segment traffic baselines (talkers, ports, volumes, time-of-day shape) over the retained flow window and raise findings on statistically significant deviation — | Shipped | Documented | Covered | Shipped, inert |
| 16 | T-1602 | Microsegmentation planner core ★ | From observed flows, compute the minimal firewall policy that preserves observed-good traffic — "these N rules cover 30 days of traffic; | Shipped | Documented | Covered | Shipped, inert |
| 16 | T-1603 | Microsegmentation review & dry-run UX | A reviewable suggested-ruleset presentation per guest/security-group and a monitor-only dry-run mode reporting *would-have-blocked* flows before anyone enforces — the UX half of th | Shipped | Documented | Covered | Shipped, inert |
| 16 | T-1604 | Failure impact simulation core ★ | "What breaks if X dies?" for any node, bond, switch, uplink, or tunnel — computed from real topology: guests losing connectivity, VLANs stranded, quorum/Ceph risk, mgmt-path loss. | Shipped | Documented | Covered | Live |
| 16 | T-1605 | Rogue-service detection | Detect rogue DHCP servers, unexpected IPv6 RAs, ARP/ND spoofing, and unknown MACs on protected segments — entirely from data the collectors already gather (T-805's neighbor/ARP enr | Shipped | Documented | Covered | Live |
| 16 | T-1606 | Capacity forecasting | Trend link/segment utilization and IPAM pool consumption against history; | Shipped | Documented | Covered | Live |
| 16 | T-1607 | Posture score & report | One periodically-computed network security/resilience score with named contributing factors — SPOF count, unsegmented guests, exposed ports, anomaly rate, drift hygiene — never an | Shipped | Documented | Covered | Live |
| 17 | T-1701 | MCP server & AI operator readiness ★ | A first-class MCP (Model Context Protocol) server exposing vnprox's read surfaces (topology, findings, flows, IPAM, simulations, diagnostics ladders) and its staging surfaces (draf | Shipped | Documented | Covered | Shipped, inert |
| 17 | T-1702 | Plugin SDK ★ | Stable, versioned extension points for the surfaces third parties keep asking to extend — switch drivers (beyond T-1205's OpenConfig/gNMI), flow/telemetry ingestors, finding packs, | Shipped | Documented | Covered | Shipped, inert |
| 17 | T-1703 | Multi-tenancy & self-service | Delegated views and workflows on the federation-era permission model: a tenant sees only their guests/VLANs/subnets and can request changes through request-changesets that route to | Shipped | Documented | Covered | Live |
| 17 | T-1704 | vnproxd HA ★ | Active/standby daemon with state replication and VIP-or-DNS failover, so the network tool is not itself the single point of failure T-1604's failure simulator would flag. | Shipped | Documented | Covered | Live |
| 17 | T-1705 | Blueprint & plugin hub | An opt-in public registry client for T-1107's signed blueprint bundles and T-1702's SDK plugins — browse, install, update, with signature verification and a vetted tier. | Shipped | Documented | Covered | Shipped, inert |
| 17 | T-1706 | Embeddable views & Grafana panels | Read-only, token-scoped embeds of the map, dashboards, and posture report for wikis/ NOC screens/status pages, plus Grafana panel plugins backed by T-1001's exporter and T-1104's e | Shipped | Documented | Covered | Shipped, inert |
| 17 | T-1707 | v3.0 release: HA/genscale performance, platform-API freeze, docs, packaging, security & PVE-compat pass | The v3.0 cut — an HA + multi-cluster genscale performance pass; | Shipped | Documented | Covered | Live |
| 18 | T-1801 | Validation harness and evidence protocol ★ | The machinery that turns a 105-item hardware checklist into roughly eight human turns instead of sixty. | Shipped | Documented | Covered | Live |
| 18 | T-1802 | Hardware-validation burndown, `pvecube`-reachable sections ★ | Burn down every checklist item reachable on a single node — roughly 60 of 105 — with evidence committed and the PVE version recorded, converting each divergence into a bug card rat | Shipped | Documented | Covered | Live |
| 18 | T-1803 | Blocked-validation register and multi-node mock fidelity ★ 🔒 | Two deliverables for the ~45 items single-node hardware cannot reach, per D3. | Shipped (scope cut) | Documented | Covered | Live |
| 18 | T-1804 | Failure-injection proof of commit-confirm ★ | The product's central claim is "if the change locks you out, it reverts itself." That path has never run on hardware against a real lockout. | Shipped | Documented | Covered | Live |
| 18 | T-1805 | Unattended revert for `fw.*` and `sdn.apply` via apply-time revert ticket ★ 🔒 | Close the one genuine hole in the change engine's safety guarantee. | Shipped (scope cut) | Documented | Covered | Live |
| 18 | T-1806 | Trustworthy CI and branch protection ★ | The check and fuzz jobs fail independently of the diff and main has no branch protection — the signal everyone ignores is also the signal nothing enforces. | Shipped | Documented | Covered | Live |
| 18 | T-1807 | Migration upgrade-chain testing ★ | Schema migrations are forward-only and there are 33 of them (32 shipped, plus T-1805's), and no test walks a v1.0-era database up to current. | Shipped | Documented | Covered | Live |
| 18 | T-1808 | Scale validation on real cluster data ★ | The documented scale target is measured synthetically. | Shipped | Documented | Covered | Shipped, unproven |
| 18 | T-2002 | bug-01 · Frozen MCP tool payloads have no field-removal regression guard | Medium — caught in review before merge this time (T-2002's coordinator noticed the coupling by hand), but the mechanism that let it get that far — a well-reasoned, fully-tested, ze | Shipped | Documented | Covered | Shipped, inert |
| 19 | T-1901 | Backup, restore, and disaster recovery of vnprox state ★ 🔒 | There is no backup story. The SQLite store holds changesets, pre/post snapshots, audit history, layout, tenants, and blueprint state; | Shipped (scope cut) | Documented | Covered | Live |
| 19 | T-1902 | Support bundle export ★ 🔒 | One command producing one redacted archive that lets someone diagnose a stranger's broken install without SSH. | Shipped (scope cut) | Documented | Covered | Live |
| 19 | T-1903 | Self-observability: RED metrics for the daemon ★ | Today's exporter reports cluster-derived gauges (vnprox_findings_open, vnprox_drift_open, vnprox_changesets, interface counters) plus build and session info. | Shipped | Documented | Covered | Live |
| 19 | T-1904 | `vnproxctl doctor` ★ | A preflight and self-check that turns "it doesn't work" into an actionable message — runnable before install and any time after. | Shipped | Documented | Covered | Live |
| 19 | T-1905 | Retention, rotation, and compaction ★ | Audit rows, flow records, capacity samples, latency-mesh history, snapshots, and .pcap captures all accumulate with no documented ceiling. | Shipped | Documented | Covered | Live |
| 19 | T-1906 | Peer-API CA pinning ★ 🔒 | internal/peer.Client inherits the system trust store rather than pinning the cluster's own /etc/pve/pve-root-ca.pem, which is what real peer daemons present. | Shipped (scope cut) | Documented | Covered | Shipped, unproven |
| 19 | T-1907 | Physical-layer progressive collapse ★ | The last unclosed gap from T-607's docs audit. | Shipped | Documented | Covered | Live |
| 20 | T-2001 | Federation cluster editor UI ★ | /federation/clusters has full CRUD, audit coverage, and capability gating — and no UI whatsoever. | Shipped | Documented | Covered | Shipped, unproven |
| 20 | T-2003 | Change review: approvals, comments, side-by-side diff ★ | The changeset is the product's unit of work and its review surface is thin. | Shipped | Documented | Covered | Live |
| 20 | T-2004 | Accessibility and design-system second pass ★ | Phase 9 did the first accessibility and design-system pass. | Shipped | Documented | Covered | Live |
| 20 | T-2005 | Mobile PWA with push ★ | T-909 shipped a responsive triage layout; | Shipped | Documented | Covered | Live |
| 20 | T-2006 | Localization (i18n) ★ | No i18n scaffolding exists; | Shipped | Documented | Covered | Live |
| 21 | T-2101 | Terraform provider and Ansible collection artifacts ★ | T-1106 shipped the stable API contract and a conformance suite and explicitly did not create terraform-provider-vnprox or ansible-collection-vnprox — those were always "separate, p | Shipped | Documented | Covered | Live |
| 21 | T-2102 | Signed apt repository on GitHub Pages ★ 🔒 | Today's install is "download a .deb from GitHub Releases and hope." For software that runs as root on a hypervisor and rewrites its network configuration, that is not good enough. | Shipped (scope cut) | Documented | N/A (non-code) | Live |
| 21 | T-2103 | PVE compatibility matrix and automated compat testing ★ | docs/roadmap.md commits to "a compatibility validation task within one phase of each new PVE release" — a promise with no mechanism behind it. | Shipped | Documented | Covered | Live |
| 21 | T-2104 | Hosted blueprint and plugin registry ★ | T-1705 shipped a local hub with signature and capability gates. | Shipped | Documented | Covered | Shipped, inert |
| 21 | T-2105 | Community distribution and docs site ★ | Get vnprox in front of the people who would use it. | Shipped | Documented | N/A (non-code) | Live |
| 21 | T-2106 | The repository has no license | There is no LICENSE file. T-2102 (public apt repository) and T-2105 (Proxmox-community | Shipped | Documented | Covered | Live |
| 21 | T-2107 | `docs/features.md` describes a product that no longer exists | The file still describes the v1.0 feature set and, under "Explicit non-goals for v1", lists five | Shipped | Documented | N/A (non-code) | Live |
| 21 | T-2108 | Triage the e2e backlog and make the suite blocking | Turning the Playwright suite on (T-1806-bug-01) ended three arcs of it running nowhere. | Shipped | Documented | Covered | Live |
| 22 | T-2201 | Help engine | Build web/src/help/ — the content model, registry, and lookup that everything | Shipped | Documented | Covered | Live |
| 22 | T-2202 | Help content | Write the content. Every topic is sourced from an existing repo doc, and names | Shipped | Documented | Covered | Live |
| 22 | T-2203 | Coverage gate | The claim is "100%". This card is what makes the claim checkable. | Shipped | Documented | Covered | Live |
| 22 | T-2204 | Help surface and entry points | implementation depends on: T-2201, T-2202 | Shipped | Documented | Covered | Live |
| 22 | T-2205 | Documentation and gate wiring | All five cards complete. 72 topics registered; | Shipped | Documented | Covered | Live |
| 23 | T-2301 | `internal/certs`: inventory | Inventory the certificates the node and its peers present, so expiry and trust problems become findings rather than surprises. | Shipped | Documented | Covered | Live |
| 23 | T-2302 | Certificate findings (`source: "cert"`) | All detection-only (Fixable false). | Shipped | Documented | Covered | Live |
| 23 | T-2303 | Peer dialling: fix `T-1906-bug-01`, don't just report it | Detection alone leaves the cluster broken. | Shipped | Documented | Covered | Live |
| 23 | T-2304 | API and CLI | implementation depends on: T-2301, T-2302 | Shipped | Documented | Covered | Live |
| 23 | T-2305 | UI, help, docs | PVE already owns this: pvecm updatecerts -f regenerates a node's leaf from the | Shipped | Documented | Covered | Live |
| 24 | T-2401 | Scheduled automatic config snapshots ★ | Today a snapshot exists only where vnprox itself acted: pre/post around an apply, or | Shipped | Documented | Covered | Live |
| 24 | T-2402 | Finding acknowledgement and mute ★ | implementation · depends on: — context: internal/findings/engine.go, internal/findings/hysteresis.go, docs/features/monitoring.md §5 | Shipped | Documented | Covered | Live |
| 24 | T-2403 | Entity change history ("blame") ★ | Standing on any entity in the inspector, there is no way to ask "what has been done to this, and | Shipped | Documented | Covered | Live |
| 24 | T-2404 | Blast-radius preview before apply ★ | The diff says *what changes*; | Shipped | Documented | Covered | Live |
| 24 | T-2405 | OpenAPI 3.1 document and completeness gate ★ | implementation · depends on: — context: docs/api.md, internal/apicontract/ | Shipped | Documented | Covered | Live |
| 24 | T-2406 | `vnproxctl doctor --live` ★ | Four of doctor's ten checks — pve_reachable, pve_privileges, clock_skew, peer_secret — | Shipped | Documented | Covered | Live |
| 24 | T-2407 | Alert quiet hours and digest coalescing ★ | An alert rule fires per event. | Shipped | Documented | Covered | Live |
| 24 | T-2408 | Batch-fix findings into one changeset ★ | implementation · depends on: T-2402 context: internal/api/findings.go, internal/findings/engine.go | Shipped | Documented | Covered | Live |
| 24 | T-2409 | Per-spec e2e store isolation · **closes `T-2108-followup-01`** | implementation · depends on: — context: web/e2e/, docs/development.md | Shipped | Documented | Covered | Live |
| 24 | T-2410 | Packaging matrix `cluster-ssh` root cause · **closes `T-1806-bug-02`** | Red on the runner, green locally under podman, on 2 of the last 3 pushes. | Shipped | Documented | N/A (non-code) | Live |
| 25 | T-2501 | Self-executing hardware validation suite ★ | The hardware-validation figure is 12 of 130 because validating an item means a human reading a | Shipped | Documented | Covered | Live |
| 25 | T-2502 | Record/replay real PVE traffic into fixtures ★ | Add a record mode to the PVE client and a replay backend to pvemock, so a fixture can be | Shipped | Documented | Covered | Live |
| 25 | T-2503 | Opt-in compatibility telemetry | One cluster validated by us is an anecdote. | Shipped | Documented | Covered | Live |
| 25 | T-2504 | Nightly soak and resource-leak gate | implementation · depends on: — context: internal/collect/collector.go, cmd/vnproxd/server.go, docs/performance.md | Shipped | Documented | Covered | Live |
| 25 | T-2505 | E2E sharding, isolation, and flake quarantine | Do not re-derive those two. | Shipped | Documented | Covered | Live |
| 25 | T-2506 | Performance regression budget gate | implementation · depends on: T-2505 context: docs/performance.md, internal/collect/sim_bench_test.go, web/e2e/scale.spec.ts | Shipped | Documented | Covered | Live |
| 26 | T-2601 | Policy-as-code guardrails at the validate stage ★ | Add a declarative policy file evaluated at the validate stage, so a violating changeset never | Shipped | Documented | Covered | Live |
| 26 | T-2602 | Canary / staged multi-node apply ★ | An apply fans out to every affected node. | Shipped | Documented | Covered | Shipped, unproven |
| 26 | T-2603 | Finding-triggered auto-rollback inside the confirm window ★ | Commit-confirm rolls back when the operator fails to confirm — which means the failure mode it | Shipped | Documented | Covered | Live |
| 26 | T-2604 | Enforced two-person rule on protected op classes | implementation · depends on: T-2601 context: internal/change/review.go, ApprovalPolicy, internal/change/protected.go | Shipped | Documented | Covered | Live |
| 26 | T-2605 | Post-apply topology preview | All five cards have their own commit (24c48fb, c535750, 2fb8c4e, 0856607, 432bd86, | Shipped | Documented | Covered | Live |
| 27 | T-2701 | Git-backed spec sync ★ | There is no git integration anywhere in the tree. | Shipped | Documented | Covered | Live |
| 27 | T-2702 | Changeset → pull request | If intent lives in git, then a change made in the vnprox GUI is a change made outside the system | Shipped | Documented | Covered | Live |
| 27 | T-2703 | Drift-to-git reconciliation | Drift detection reports that config and live have diverged. | Shipped | Documented | Covered | Shipped, unproven |
| 27 | T-2704 | Point-in-time topology diff | Changesets record what vnprox did. | Shipped | Documented | Covered | Live |
| 27 | T-2705 | Mutating MCP tools that stage, never apply | The MCP surface exposes nine read-only tools. | Shipped | Documented | Covered | Shipped, inert |
| 27 | T-2706 | Compliance profiles and evidence export | All six cards have their own commit (3e4ef09, a4f00bb, 0ad45fe, 46c7ed2, 3213ef8, | Shipped | Documented | Covered | Live |
| 28 | T-2801 | One-command install and built-in demo mode ★ | Evaluating vnprox currently requires a Proxmox cluster. | Shipped | Documented | Covered | Shipped, deliberately unhosted |
| 28 | T-2802 | Hosted read-only demo and guided tour | The demo dataset from T-2801, published, turns a datasheet into something clickable. | Shipped | Documented | Covered | Shipped, deliberately unhosted |
| 28 | T-2803 | Hosted signed registry for blueprints and plugins | implementation · depends on: — context: internal/hub/, internal/blueprint/, internal/plugin/, T-2104 | Shipped | Documented | Covered | Shipped, inert |
| 28 | T-2804 | Incident mode | When a network breaks, an operator needs the diagnosis ladder, a capture, the current findings, | Shipped | Documented | Covered | Live |
| 28 | T-2805 | Multi-user presence and changeset locking | Nothing stops two operators staging conflicting changes to the same bridge at the same time. | Shipped | Documented | Covered | Live |
| 28 | T-2806 | Map annotation layer | The map shows what is true. | Shipped | Documented | Covered | Live |
| 28 | T-2807 | Scheduled digest reports | Posture scores, capacity forecasts, and drift are computed continuously and looked at when someone | Shipped | Documented | Covered | Live |
| 28 | T-2808 | In-app assistant over the MCP read tools | The MCP surface answers real questions — "which guests can reach the internet", "what changed on | Shipped | Documented | Covered | Shipped, inert |
| 29 | T-2901 | Un-break the PWA and the embeds ★ | implementation · depends on: — context: internal/api/middleware.go, internal/api/embed.go, web/public/sw.js, web/public/manifest.webmanifest, web/src/push/registerServiceWorker.ts, | Shipped | Documented | Covered | Live |
| 29 | T-2902 | Peer host-write safety parity + audit attribution ★ | implementation · depends on: — · migration: 0047 context: internal/peer/server.go (/host/stage-interfaces, /host/ifreload, /host/restore, /host/discard-staged, /host/lldp/install), | Shipped | Documented | Covered | Shipped, unproven |
| 29 | T-2903 | Bearer tokens honor `read_only`; token expiry; CSRF constant-time | implementation · depends on: wave 1 merged · migration: 0048 context: internal/auth/middleware.go (authenticateBearer, CSRF check), internal/auth/handlers.go (forceReadOnly), inter | Shipped | Documented | Covered | Live |
| 29 | T-2904 | Hub plugin install hardening | implementation · depends on: — context: cmd/vnproxd/hubinstall.go (buildRegistration), internal/api/hub.go (trustUnsigned gate), internal/plugin/registry, internal/config, docs/hub | Shipped | Documented | Covered | Shipped, inert |
| 29 | T-2905 | Auth and daemon hardening punch list | The remainder of the 2026-08-15 security audit. | Shipped | Documented | Covered | Live |
| 29 | T-2906 | Documentation truth pass + single doc index | Make the documentation stop lying by omission, without rewriting history. | Shipped | Documented | N/A (non-code) | Live |
| 30 | T-3001 | Config-as-code cockpit ★ | Six routes, one workflow, no screen. | Shipped | Documented | Covered | Live |
| 30 | T-3002 | Governance surfaces ★ | Fifteen routes across four governance features, none reachable from the UI. | Shipped | Documented | Covered | Live |
| 30 | T-3003 | Platform panel | Eleven routes behind Settings. | Shipped | Documented | Covered | Live |
| 30 | T-3004 | Analysis surfaces | Eight routes across six analysis features. | Shipped | Documented | Covered | Live |
| 30 | T-3005 | Canary apply: give it a UI ★ | Canary apply (T-2602) and finding-triggered auto-rollback (T-2603) both ship complete, | Shipped | Documented | Covered | Live |
| 30 | T-3006 | Help completion and a panel-aware coverage gate | The help coverage gate derives its inventory from App.tsx/NavRail.tsx — **routed screens | Shipped | Documented | Covered | Live |
| 31 | T-3101 | SDN Fabrics, modelled from the real API ★ | Note three things the shape tells you that prose would not: | Shipped | Documented | Covered | Live |
| 31 | T-3102 | SDN controllers as first-class objects | Controllers are a string field on a zone today (internal/sdn/service.go:77, | Shipped | Documented | Covered | Live |
| 31 | T-3103 | Firewall fidelity: `forward`, VNet scope, real resolution order | Three items, and hardware reordered them — the one the roadmap ranked last is the most urgent. | Shipped | Documented | Covered | Live |
| 31 | T-3104 | IPAM completion | Real IPAM plugin types are <netbox \| phpipam \| pve> — which is what vnprox already reads. | Shipped | Documented | Covered | Live |
| 31 | T-3105 | Restore fidelity — **rescoped, and mostly closed as already-decided** | The roadmap gave this card three items. | Shipped | Documented | Covered | Live |
| 31 | T-3106 | Localization (i18n) — the rescheduled T-2006 | T-2006 verbatim: the single Arc-4 roadmap item with zero code in the tree. | Shipped | Documented | Covered | Live |
| 32 | T-3201 | Second node + the blocked register: cross-node validation for real | Verified against planning/reports/blocked-validation.md and commits 044f74bb ("change+test: | Shipped | Documented | Covered | Live |
| 32 | T-3202 | Failure-injection proof of commit-confirm + validation burndown | Verified against planning/reports/T-3202-scenarios.md, commits 69fc944b, fa95217c, | Shipped | Documented | Covered | Live |
| 32 | T-3203 | Scale & performance on real cluster data | Verified against planning/reports/T-3203.md and commit 3df0b7eb ("planning: T-3203 — | Shipped | Documented | Covered | Shipped, unproven |
| 32 | T-3204 | Test-debt closure: quarantine, flake, isolation, frozen-payload guards | Verified against commit ef8abec4 ("test: close accumulated e2e/contract debt — quarantine, | Shipped | Documented | Covered | Live |
| 33 | T-3301 | Distribution that works: CI decision, signed apt repo at a real host, release publishing | Verified against commit 30465dda ("build: retire hosted CI for scripts/ci-local.sh; | Shipped | Documented | Covered | Live |
| 33 | T-3302 | Public presence: repo, docs site, security contact, forum announcement | Verified against commit 0f970685 ("docs: add a security-disclosure contact and wire up GitHub | Shipped | Documented | N/A (non-code) | Shipped, inert |
| 33 | T-3303 | Hosted instances + ecosystem: demo, registry, Terraform/Ansible | Verified against commit ac3a7c3f ("demo: real hosted public demo (T-3303), plus a bug it found | Shipped | Documented | Covered | Shipped, inert |
| 34 | T-3401 | Design tokens: Stripe-inspired foundation | Establish the phase's visual vocabulary as tokens only — accent, radii, borders, shadows, type scale — so every later card styles against names, not raw values. | Shipped | Documented | Covered | Live |
| 34 | T-3402 | Sidebar: grouped, collapsible, iconed, pinned-bottom | Replace NavRail with a Stripe-style Sidebar: light surface, grouped sections with muted section labels, collapsible groups with chevrons, real icons, an instance-identity chip at t | Shipped | Documented | Covered | Live |
| 34 | T-3403 | Top bar + demo test-mode bar | Restyle the top bar to the reference — rounded search field, quieter ghost actions, account dropdown as a chip — and restyle DemoBanner as Stripe's dark full-width test-mode bar. | Shipped | Documented | Covered | Live |
| 34 | T-3404 | PageHeader + pill actions + underlined tabs, rolled out to every page | One shared header pattern — large title, optional description line, pill action buttons right-aligned, optional underlined tab row — replacing each page's ad-hoc <h1>/toolbar marku | Shipped | Documented | Covered | Live |
| 34 | T-3405 | Core component restyle | Bring the shared primitives to the reference's look: pill-option buttons, quieter tables (muted uppercase-free headers, hairline row borders, no zebra), softer dialogs/drawers/toas | Shipped | Documented | Covered | Live |
| 34 | T-3406 | followup-01 · Pre-existing page-local contrast and ARIA defects | Clear the quarantined a11y failures at their source, then delete the quarantine entries. | Shipped | Documented | Covered | Live |
| 35 | T-3501 | Say what is actually wrong: split the finding badge by source and severity | An entity carrying a finding should say which kind of finding, and how bad. | Shipped | Documented | Covered | Live |
| 35 | T-3502 | Stop reporting Proxmox's own firewall veths as drift | drift/file_runtime_divergence must not fire on interfaces Proxmox creates and manages itself. | Shipped | Documented | Covered | Live |
| 35 | T-3503 | Faceplate v2: draw devices as devices | Replace the card-with-text-sections rendering with a real faceplate. | Shipped | Documented | Covered | Live |
| 35 | T-3504 | Firewall bridges: stop drawing empty boxes | fwbr* bridges currently render as two large, entirely empty cards — the biggest single waste of space on the screen, and confusing, since the operator did not create them. | Shipped | Documented | Covered | Live |
| 35 | T-3505 | Graph view parity | The Graph (canvas) view shares the badge vocabulary and status language with the Switch view and must not diverge from T-3501/T-3503 — two views disagreeing about what an entity's | Shipped | Documented | Covered | Live |
| 36 | T-3601 | A finding says what its remedy is, and every surface can render it | One vocabulary for "here is the remedy", declared by the producer and rendered identically wherever a finding appears. | Shipped | Documented | Covered | Live |
| 36 | T-3602 | The LLDP banner offers to install lldpd | The cheapest win in the phase: the backend has been there since T-605 and the banner still only links to documentation. | Shipped | Documented | Covered | Live |
| 36 | T-3603 | Collector staleness offers a retry, and a way to the real problem | "no successful poll yet — context canceled" currently tells an operator that something is broken and nothing about what to do next. | Shipped | Documented | Covered | Live |
| 36 | T-3604 | `service_down` offers to start the unit | dnsmasq and frr are SDN's DHCP and routing daemons; | Shipped | Documented | Covered | Live |
| 36 | T-3605 | Prove it end to end, and write down the power that was added | All five cards delivered. The three banners in the screenshot that started this now offer their | Shipped | Documented | Covered | Live |
