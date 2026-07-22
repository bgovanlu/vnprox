# Changelog

All notable user-facing changes to vnprox are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).
vnprox uses semantic versioning; the SQLite schema migrates forward-only.

Versions up to v1.0 correspond to the milestones in `docs/roadmap.md`;
v2.0 is the milestone cut of the second arc in `docs/roadmap-next.md`
("Beyond the cluster"); v3.0 is the platform cut of the third arc in
`docs/roadmap-universal.md` ("The open platform"). Phase 3 ("Discovery &
true cluster") does not have its own version cut per the roadmap — its
functionality (peer clustering, LLDP discovery, drift detection, FDB
browsing) shipped as part of the v0.8 development cycle and is listed under
v0.8 below, alongside that release's SDN/IPAM work.

Precise dates are given for the v1.0.0, v2.0.0, and v3.0.0 release cuts;
earlier milestones predate this file and are dated only by year.

**Note on v1.1.0–v1.3.6:** these tags (2026-07-12 to 2026-07-15) were interim
development checkpoints cut while the v1.4 → v2.0 arc was in progress; they
don't have dedicated entries below because the `v2.0.0` tag wasn't applied
until the whole arc — including phases 13–15 of the *next* arc, see the note
on `[2.0.0]` below — had already merged onto the same branch. Their
functionality is folded into `[2.0.0]`.

## [3.0.2] - 2026-07-22

Packaging patch — no code or schema change (schema stays 31; a v3.0.0/v3.0.1
install upgrades in place). Completes the `ProtectSystem=strict` fix begun in
v3.0.1, which covered only the first-boot key writes.

### Fixed

- **WireGuard apply fails `read-only file system` on a hardened host.**
  Applying a WireGuard changeset op writes each tunnel's wg-quick config under
  `/etc/wireguard` (`cmd/vnproxd/wireguard.go`'s `hostWGGateway` — `MkdirAll`
  `0700` + `WriteFile` `0600`), but the hardened unit's `ProtectSystem=strict`
  left `/etc/wireguard` read-only, so every WireGuard apply on a hardened node
  failed — the same crash class as the v3.0.1 keys bug, missed because that
  fix only added `/etc/vnprox/keys`. `ReadWritePaths` now also includes
  `/etc/wireguard`, and `postinst` creates that directory (`0700 root:root`,
  wireguard-tools' own convention) so the sandbox bind target always exists
  even on nodes without `wireguard-tools` installed. The rest of `/etc` stays
  read-only. The cluster secret's fallback generate-if-absent write to
  `/etc/pve/priv/vnprox/` is deliberately not added: `/etc/pve` is a pmxcfs
  FUSE mount that `vnprox-setup` pre-seeds, and whether `ProtectSystem=strict`
  even makes it read-only is unconfirmed — tracked in
  `planning/reports/needs-hardware-validation.md`.

## [3.0.1] - 2026-07-21

Packaging patch — no code or schema change (schema stays 31; a v3.0.0
install upgrades in place).

### Fixed

- **Service crash-loop on first upgrade to the v1.4→v3.0 key-generating
  releases.** The hardened systemd unit's `ProtectSystem=strict` made
  `/etc/vnprox` read-only, but the daemon generates first-run secrets under
  `/etc/vnprox/keys/` on startup — the Prometheus metrics scrape token
  (`metrics.key`) and the blueprint signing key (`blueprint-signing.key`) —
  which `postinst` does not pre-seed (only `session.key` is). On a node
  upgrading from a release that predates those keys, the first boot failed
  with `open /etc/vnprox/keys/…: read-only file system` and the service
  restart-looped. `ReadWritePaths` now includes `/etc/vnprox/keys` so the
  daemon can create its own first-run secrets (still `0600 root:root`; the
  rest of `/etc` stays read-only). Found on real hardware upgrading
  1.3.6 → 3.0.0; no sandboxed or fresh-`vnprox-setup` test caught it because
  setup had already created the key files.

## [3.0.0] - 2026-07-21

The platform release — the cut where vnprox stops being only a product and
becomes infrastructure other tools build on. v3.0 caps the v2.0 → v3.0 arc:
deep-sight diagnostics, edge/WireGuard, and Kubernetes/Ceph/QoS visibility
(phases 13–15) shipped earlier, under the `[2.0.0]` cut below — see that
entry's note. This release adds phase 16 (flow baselining, microsegmentation
planning, failure-impact simulation, rogue-service detection, capacity
forecasting, and a posture score) plus phase 17: an AI-operator MCP surface,
a plugin SDK, multi-tenancy, daemon HA, a blueprint/plugin hub, and
embeddable views. Target platforms: Proxmox VE 8.2+ and 9.x; **PVE 10.x and
11.x** are the forward compatibility targets for this arc (see
`docs/deployment.md`, flagged needs-hardware-validation until validated on
real hardware).

**Every new surface stages through the one change engine.** AI-proposed
changesets (MCP), plugin-staged changesets, and tenant request-changesets
are all ordinary changesets — staged, validated, diffed, applied, and
confirmed/rolled-back exactly like every mutation since v0.5. No card in
this arc introduced a second mutation path, and this release **freezes** the
new programmable surfaces as stable, versioned compatibility contracts. DB
migrations remain forward-only; a v2.x install upgrades in place.

### Added

- **Flow baselining, microsegmentation, and failure simulation.** Automatic
  per-guest traffic baselines with anomaly findings when observed flow
  deviates from the learned norm; a microsegmentation planner that proposes
  least-privilege firewall rules from observed flow history with a
  dry-run/review step before any rule is staged; a failure-impact simulator
  that answers "what breaks if this bridge/bond/link goes down" before it
  happens; rogue-service detection (unexpected listeners, unauthorized DHCP/
  DNS servers on the network); capacity forecasting for VLANs, subnets, and
  link utilization trending toward exhaustion; and a cluster-wide posture
  score rolling up firewall coverage, drift, and findings into one number
  with a point-in-time report.
- **MCP server for AI operators (read + stage only).** A first-class Model
  Context Protocol server exposes vnprox's read surfaces (topology, findings,
  flows, IPAM, path simulation, diagnostics) and lets an AI operator *stage*
  a draft changeset — and nothing else. No apply/confirm/rollback verb is
  reachable through MCP by any tool or combination; a human remains the sole
  apply authority. Every AI-originated changeset is unerasably labelled
  `origin: "mcp"` and every tool call is audited with an `mcp:<token-name>`
  actor, so an operator can always tell an AI action from a human one. Off by
  default (`[mcp] enabled`), authenticated with a capability-scoped
  automation token.
- **Plugin SDK.** Stable, versioned extension points third parties can
  implement — switch drivers, flow/telemetry ingestors, finding packs,
  ingress discoverers, and dashboard tiles — with an in-process and a
  supervised out-of-process option, each plugin declaring a capability scope
  that is a server-enforced *ceiling*, never a grant. A plugin can stage a
  changeset for a human to apply but is never itself a mutation path. Install/
  enable/disable/uninstall are all audited with the recorded scope.
- **Multi-tenancy & self-service.** Delegated, server-side-scoped views: a
  tenant sees only its own guests/VLANs/subnets and *requests* changes
  through request-changesets that route to an approver, with scoped
  dashboards and alert routes. Scoping is enforced at the data-access layer
  (an out-of-scope lookup is a `404`, never confirming existence); a member
  can never approve, and an approver can never approve their own request.
- **vnproxd high availability (active/standby).** An optional active/standby
  daemon pair with state replication and VIP-or-DNS failover, so the network
  tool is not itself a single point of failure. Commit-confirm timers and
  scheduled applies survive failover — re-armed to their original absolute
  deadlines — governed by a fenced single-writer lease with explicit
  split-brain handling. Off by default; a single-daemon install is unchanged.
- **Blueprint & plugin hub.** An opt-in client for a public registry of
  signed blueprint bundles and SDK plugins — browse and install with
  Ed25519 signature verification, a per-installation trust decision, and an
  informational "vetted" tier that never substitutes for that decision.
- **Embeddable views & Grafana panels.** Read-only, token-scoped embeds of
  the map, dashboards, and posture report for wikis/NOC screens, plus Grafana
  panels backed by the Prometheus exporter and the event stream. An embed
  token is hard-restricted to read-only scopes at mint and can never exceed
  its minting user.

### Changed

- **Platform API freeze.** The MCP tool surface, the plugin SDK interfaces,
  and the WebSocket `"events"` stream schema are now stable, documented
  compatibility contracts with the same deprecation policy the changeset API
  adopted at v1.7 (additive-only within a version; a breaking change mints a
  new version, keeping the old accepted for ≥1 minor release). See
  `docs/architecture.md` §13.
- Compatibility target advanced to Proxmox VE 10.x and 11.x for this arc
  (8.2+/9.x still supported); real-hardware validation is tracked as a
  needs-hardware-validation item.

### Security

- Every new credential/write-adjacent surface across the v2.0 → v3.0 arc is
  covered in `docs/security.md`'s threat-model summary: packet-capture files
  (Phase 13), WireGuard tunnel keys (Phase 14), the MCP AI-operator
  write-adjacent surface, plugin capability grants and the out-of-process
  plugin boundary, tenant credentials/isolation, and the HA replication
  channel. Every new at-rest credential class is sealed with the single
  AES-256-GCM session key vnprox already uses, never a second cipher or key,
  and each has a targeted encrypted-at-rest test.

## [2.0.0] - 2026-07-20

The multi-cluster release — the cut where a vnprox instance is no longer
1:1 with a Proxmox cluster. v2.0 caps the v1.4 → v2.0 arc (federation,
cross-cluster IPAM, DNS management, guarded switch push, PBS awareness, and
OIDC SSO). Target platforms: Proxmox VE 8.2+ and 9.x; PVE 10.x is the
forward compatibility target (see `docs/deployment.md`, flagged
needs-hardware-validation until validated on real hardware).

**Note:** this tag was applied after phases 13–15 of the *next* arc
(`docs/roadmap-universal.md`) had already merged onto the same branch, so
this release also includes deep-sight diagnostics (Phase 13), edge/WireGuard
networking (Phase 14, excluding T-1407 — see below), and Kubernetes/Ceph/QoS
workload visibility (Phase 15) — listed under "Added" below alongside the
v1.4 → v2.0 federation work they shipped together with. Phase 14's T-1407
("tunnel-aware federation transport") was **not** implemented and is not
included; it remains open, tracked as P1 in `docs/roadmap-universal.md`.

**Federation is additive, not a fork:** a v1.x single-cluster install that
upgrades with zero clusters attached keeps serving its existing
single-cluster experience unchanged — the global cluster view only appears
once a second cluster is attached, and DNS/switch-push/OIDC stay dormant
until explicitly configured. DB migrations remain forward-only.

### Added

- **Multi-cluster federation.** Attach any number of PVE clusters to one
  designated primary and see them all on one screen: a global topology with
  per-cluster capsules and drill-down into each cluster's ordinary view, a
  global search and command palette spanning clusters, per-cluster
  changesets with a merged cluster-wide audit trail, and per-cluster failure
  isolation — an unreachable cluster is greyed out and flagged as a partial
  result, never blanking or erroring the whole view. Config ownership stays
  strictly per-cluster; there is no cross-cluster mutation.
- **Cross-cluster IPAM and external subnets.** The same or an overlapping
  subnet allocated in two attached clusters now surfaces as a conflict
  finding. Non-PVE subnets (office LANs, upstream transit, colo ranges) are
  first-class IPAM records you can add and manage directly. The
  NetBox/phpIPAM bridge is upgraded from read-merge to **bidirectional
  sync**, with a dry-run preview that never writes and an explicit-confirm
  apply step — every sync write is audited with before/after per record.
- **DNS management.** Surface and edit PVE SDN's DNS plugin (PowerDNS):
  zone and record visibility, guest names shown as badges on the map, and
  record edits staged as ordinary changesets through the same SDN
  apply flow as zones/VNets/subnets.
- **Guarded switch config push.** The read-write step beyond LLDP discovery:
  driver-based (OpenConfig/gNMI) pushes scoped strictly to switch ports
  facing your PVE nodes (VLAN membership, port descriptions, LACP), each one
  an ordinary changeset with validate/diff/confirm and the management-path
  interlocks extended onto the uplink port. Per-switch, explicit opt-in;
  ships dark (feature-flagged off) until you enable it for a specific switch,
  with a plainly-stated residual risk that a switch made unreachable by a
  push cannot be remotely reverted.
- **PBS network awareness.** Proxmox Backup Server hosts appear on the map
  with their interfaces, the backup traffic path (node → PBS) is
  highlighted, and the inspector shows datastore-network sizing hints.
  Entirely read-only — no new write actions, no PBS credentials stored.
- **OIDC SSO.** Log in via OpenID Connect (authorization-code + PKCE)
  alongside the existing Proxmox ticket bridge, for federated deployments
  where per-cluster PVE credentials stop scaling, with group→role mapping.
  OIDC authenticates you to vnprox; your Proxmox permissions still gate every
  cluster-scoped action per cluster, and an OIDC role can never grant a
  capability your real PVE ACLs don't already allow.
- **Distributed packet capture, with a permission model and in-browser
  decode.** Capture on any node/interface, build filters with a guided BPF
  builder, decode packets in the browser, and download a `.pcap` — gated by
  a dedicated capability so capture access is granted independently of
  general admin rights.
- **Latency & loss mesh, guest network interior inspector, and conntrack/NAT
  table explorer.** A continuously-probed latency/loss heatmap across the
  cluster; a live view into a guest's own interfaces/routes/DNS via the
  guest agent; and a searchable connection-tracking/NAT table explorer per
  node.
- **Path MTU prober and guided diagnosis flows.** Active end-to-end MTU
  discovery feeding the existing `vxlan_underlay_mtu` finding, and
  guided, honesty-contract-preserving diagnosis flows that walk a user from
  symptom ("can't reach X") through the capture/latency/conntrack/interior
  tools above to a root cause.
- **WireGuard tunnel engine.** First-class WireGuard tunnels — key
  generation and custody, changeset-integrated apply (`wg.*` ops through the
  ordinary stage/validate/diff/apply/confirm flow), map edges for
  tunnel-connected clusters, and a "connect two clusters" wizard.
- **Edge & NAT cockpit, IPv6 enablement, WAN/upstream health, and ingress
  visibility.** A dedicated view for edge routing/NAT configuration; a
  guided IPv6 enablement suite; upstream/WAN link health monitoring; and
  visibility into ingress paths reaching the cluster from outside.
- **Kubernetes overlay mapping (read-only, CNI-aware) and Ceph network
  awareness.** A read-only Kubernetes overlay layer on the topology map with
  service-flow attribution, and Ceph's own network topology (public/cluster
  networks, OSD/mon placement) surfaced without new credentials — both
  follow the "read the owning system's own knowledge, zero new write
  surface" pattern PBS awareness established in this same release.
- **Service-network attribution, QoS & traffic shaping, SR-IOV lifecycle,
  and a migration network planner.** Flow records classified against known
  services (including Ceph/PBS/K8s traffic); `qos.*` changeset ops for
  traffic shaping; real SR-IOV VF lifecycle management; and a planner that
  recommends the least-disruptive network path/timing for a live migration
  using the latency mesh's data.

### Changed

- Compatibility target advanced to Proxmox VE 9.x and 10.x for this arc
  (8.2+ still supported); PVE 10.x validation on real hardware is tracked as
  a needs-hardware-validation item.

### Security

- Every new v2.0 credential class — the per-cluster registry credential
  (`clusters.credential_enc`), the OIDC client secret and mapped PVE
  credentials (`oidc_pve_links.credential_enc`), and switch-driver
  credentials (`switches.credentials_enc`) — is sealed at rest with the same
  single AES-256-GCM session key vnprox already uses for Proxmox tickets, and
  is never returned by any API response, log line, or audit entry. Each has a
  targeted encrypted-at-rest test.
- The threat-model summary gains rows for the arc's new surfaces
  (cluster-registry credential theft, a rogue or compromised attached
  cluster, switch-driver credential theft/errant push, OIDC token forgery)
  with stated mitigations.

## [1.0.0] - 2026-07-12

The 1.0 release: operations, hardening, and release polish on top of
everything below. Target platforms: Proxmox VE 8.2+ and 9.x.

### Added

- Live traffic visualization on the topology map: edge thickness and color
  reflect real-time link utilization, with a dedicated "Traffic" view mode
  and per-bond slave balance shown in the inspector.
- 24-hour traffic history with rate and error charts in the entity
  inspector (sparklines plus a full history chart).
- A unified health and findings stream that brings together drift
  detection, LLDP/switch mismatches, IPAM conflicts, and new health checks
  (interface error/drop rate thresholds, bond slave down, bridge without a
  carrier uplink, MTU mismatches, STP topology instability, stale
  unreloaded network changes, dnsmasq/FRR service health) — each finding
  has a plain-English explanation, affected entities, and, where possible,
  a one-click fixing change.
- Optional notifications (email/webhook, via your existing Proxmox
  notification targets) when a finding's severity crosses a threshold.
- Blueprints: reusable, parameterized network templates you can save,
  import, and export as files. Five ready-made starters are included
  (single-NIC homelab, dual-NIC management+trunk, LACP bond with a storage
  VLAN, VXLAN overlay, and EVPN datacenter). Re-applying a blueprint is
  idempotent — already-matching parts of your network are left alone, only
  the differences are staged. You can also "capture" an existing node's
  configuration as a new blueprint to replicate it elsewhere.
- A guided first-login walkthrough: see a summary of what vnprox found on
  your cluster, confirm (or correct) which interfaces are protected as
  management/cluster links, optionally install LLDP for switch discovery,
  and review your initial health findings — dismissible and resumable, and
  it never blocks normal use.
- Config documentation export: generate a point-in-time Markdown or
  standalone HTML report of your entire network — per-node interface
  tables, VLAN matrix, SDN inventory, firewall summary, LLDP wiring, and an
  embedded topology diagram — for change records or audits.
- Optional, server-enforced read-only mode: when enabled, no write actions
  are possible through vnprox for anyone, regardless of their Proxmox
  privileges.
- Production-grade packaging: a signed apt repository, a one-command
  cluster installer that rolls the package out to every cluster node over
  SSH (or prints manual per-node steps if SSH isn't available), automatic
  provisioning of vnprox's own read-only Proxmox API token and role, and a
  tested upgrade path that preserves your data and config across versions.
  `apt remove` keeps your config and data; `apt purge` removes them
  (prompting before touching cluster-shared state on the last node).
- `vnproxctl status` now reports collector health, Proxmox API
  reachability, and peer reachability/version compatibility all in one
  place, for quick troubleshooting.
- A multi-node change now refuses to start at all if a peer is running an
  incompatible version, rather than risking a partially-applied change.

### Changed

- Tightened the browser Content-Security-Policy to the minimum the app
  actually needs (no inline scripts, no third-party connections, no
  frames/objects/workers beyond what the app uses).
- The daemon's systemd service now runs with additional Linux hardening
  (restricted address families, a syscall allow-list) to reduce its attack
  surface further.
- Session identifiers are now truncated in log output rather than logged
  in full.

### Fixed

- The SQLite database file could be created world-readable under some
  system umask settings; it's now always created (and corrected on
  upgrade) with strict, owner-only permissions.
- A guest-selection bug in the firewall UI that could send the wrong
  reference and fail to load a guest's rules.
- An EVPN zone created via the guided wizard or the plain editor could
  silently omit its BGP controller reference, leaving the zone
  non-functional.
- A cluster-installer parsing bug could mistake the coordinating node's own
  status line for another node's name during multi-node rollout.
- SDN zone/VNet/subnet creation, editing, and deletion (both the guided
  wizards and the plain editors) could be permanently disabled for every
  user, including full-privilege admins, on any real multi-node cluster —
  a capability-resolution bug that made the entire SDN cockpit's write
  path non-functional outside of single-node test setups. Found and fixed
  during v1.0 release verification.
- Several SDN and firewall edit/delete controls (zone/VNet/subnet editors,
  per-rule delete/enable/reorder, shared object deletion) were missing
  read-only-mode capability gating entirely, so a read-only or
  under-privileged session could see them enabled (the underlying write
  would still be rejected server-side — no privilege escalation was
  possible, but the controls should have been disabled with an
  explanatory tooltip).
- `vnproxctl snapshots list/restore` and `rollback-now` — the documented
  "works even when the daemon/UI is unreachable" disaster-recovery path —
  incorrectly required a resolvable Proxmox TLS certificate to run at
  all, which these commands never actually need. Fixed so they work on
  any host, with or without a working Proxmox VE certificate.

### Security

- Completed a full security-hardening review: every claim in the security
  documentation is now backed by an automated test or a documented
  verification procedure.
- Zero known high/critical vulnerabilities in Go and npm dependencies,
  checked continuously in CI.
- Extended fuzz testing to cover every parser that handles data from
  outside vnprox's own control (interface files, LLDP, FRR/BGP output,
  DHCP leases, firewall logs, peer authentication).

## [0.9.0] - 2026

Firewall management and the path simulator.

### Added

- Full visual firewall management across datacenter, node, and guest
  scopes: rule tables, aliases, IP sets, and security groups.
- A "resolved rules" view for any guest showing the exact effective rule
  evaluation order Proxmox's firewall applies, with each rule labeled by
  where it came from (cluster, security group, or the guest itself).
- Clear warning banners whenever a firewall scope is disabled, so a
  disabled datacenter or guest firewall is never a silent surprise.
- Drag-to-reorder rule editing, inline enable/disable, and a rule builder
  with autocomplete for aliases, IP sets, and service macros (with an
  expansion preview showing exactly what a macro matches).
- Safety guard on deleting a shared alias, IP set, or security group: if
  it's still referenced by any rule, the delete is blocked and every
  referencing rule is listed.
- Rule effects preview: for a group-referencing rule, see exactly which
  guests it applies to before you apply it.
- The path simulator: ask "can this VM reach that VM (or IP, or the
  internet) on this port?" and get a real, statically-computed answer —
  allowed, blocked, unreachable, or an honest "couldn't determine" — with
  the full hop-by-hop path, the specific rule that blocked it (one click
  takes you to that rule's editor), and a complete list of caveats. The
  simulator is built to never give a confidently wrong answer.
- Trace-path from the topology map: right-click any guest to trace a path
  to another guest, an IP address, or "the outside world"; the result
  highlights the path on the map with its verdict.
- Simulation results are shareable via URL.
- A cluster-wide firewall log viewer with rule correlation (which rule
  produced a given log line, where determinable) and protection against
  log storms overwhelming the browser.

## [0.8.0] - 2026

True multi-node clustering, physical-layer discovery, and the SDN/IPAM
cockpit. (Includes Phase 3's cluster/discovery work, which the roadmap
does not cut as a separate release.)

### Added — cluster & discovery

- Genuine multi-node clustering: every view and every edit works the same
  way whether the node involved is local or a cluster peer.
- Physical switch discovery via LLDP: real switches now appear on the
  topology map with port-level wiring to your hosts.
- A VLAN cross-check that compares your bridge/bond VLAN configuration
  against what your switches actually advertise over LLDP, and flags
  mismatches.
- A dedicated ports table (with CSV export) showing every LLDP-discovered
  physical link.
- Automatic drift detection: vnprox continuously checks for configuration
  inconsistencies across your cluster — mismatched same-named bridges,
  MTU mismatches along a path or across nodes, SDN configuration that
  doesn't match zone membership, staged-but-never-applied network changes,
  and live state that's drifted from declared configuration — with a
  one-click fixing change wherever the fix is unambiguous.
- Distributed rollback safety for multi-node changes: each affected node
  arms its own independent safety timer, so no single node's rollback
  protection depends on the others staying reachable.
- A cluster-wide MAC/FDB browser: look up any MAC address (or part of one)
  and see which bridge and port it's on, and which guest owns it.
- Cluster-wide audit log and snapshot history, with a clear "partial
  results" indicator if a peer is temporarily unreachable — never a silent
  gap.

### Added — SDN & IPAM

- An SDN cockpit: browse your zones, VNets, and subnets as a tree with
  per-node health/apply status, and see exactly what a staged-but-
  unapplied SDN change will do before you apply it.
- A topology map overlay for SDN — VNet planes across the bridges that
  realize them, and a VXLAN/EVPN tunnel mesh with MTU annotations.
- Guided setup wizards for all five SDN zone types (Simple/NAT, VLAN,
  QinQ, VXLAN, EVPN), each with plain-English explanations and a live
  preview of the resulting topology as you fill in the form.
- The VLAN zone wizard cross-checks your chosen VLAN ID against what LLDP
  says your switch port actually trunks, and warns you if it's missing.
- The VXLAN wizard shows the MTU math explicitly (underlay MTU minus
  overhead) with a one-click "use the safe value" fix.
- EVPN/BGP observability: a peering matrix, per-session detail (prefixes,
  uptime, last error), a VNI list, exit-node health, and detection of
  flapping BGP sessions.
- Visual IPAM: color-coded allocation grids for your subnets showing
  confidence (allocated via Proxmox IPAM, observed via guest agent, both,
  or conflicting), automatic conflict detection (duplicate IPs, observed-
  but-unallocated addresses, allocations that don't match any known
  guest) with suggested resolutions, and a "next free address" picker
  usable directly from the bridge editor.
- DHCP range management on SDN subnets, plus a live leases view correlated
  to your guests by MAC address — reservations and allocations are one
  and the same record, shown consistently in both the IPAM grid and the
  DHCP view.
- First-class Open vSwitch support: OVS bridges, bonds, and internal ports
  are visualized, edited, and validated with the same safety checks as
  Linux bridging.

## [0.5.0] - 2026

Beta: the change engine and core network editing. This is the first
release where vnprox can actually modify your network, not just show it
to you.

### Added

- Safe network editing end to end: every change is staged as a draft,
  validated, shown as a diff, applied, and requires explicit confirmation
  — with automatic rollback if you never confirm.
- A changeset drawer: accumulate multiple pending edits, reorder them,
  and park a named draft to resume later.
- Live, plain-English validation as you edit, with one-click fixes for
  common problems (e.g. an MTU or VLAN ID out of range).
- A review screen before every apply, with three views of exactly what
  will happen: a human-readable summary, the literal file diff, and the
  execution plan.
- A commit-confirm safety window after every apply: a visible countdown
  during which you must confirm the change worked; if you don't (for
  example, because the change broke your connection to vnprox), it's
  automatically rolled back.
- Protected-interface guardrails: vnprox detects your management IP and
  cluster (corosync) links during setup and blocks any change that would
  cut them off, unless you explicitly override it.
- Deleting a bridge that still has running VMs/containers attached is
  blocked unless you also move those guests in the same change.
- Full editors for bridges (including VLAN-aware bridges with a VID range
  editor), bonds, VLAN sub-interfaces, and plain interfaces, each with
  inline plain-English help.
- Drag-and-drop editing directly on the topology map — drag a NIC onto a
  bond or bridge, or retarget a guest's network connection.
- Bulk guest network reattachment: move many VMs/containers to a new
  bridge in a single operation.
- A raw interfaces-file editor for advanced users, with syntax
  highlighting and live linting, protected by the same safety checks as
  the guided editors.
- Full change history: every applied change is snapshotted; browse,
  diff against any other point in time or against live state, and
  restore.
- An audit log recording every action taken through vnprox.
- `vnproxctl` command-line recovery tools that work even if the daemon
  itself is down (direct snapshot restore and emergency rollback).

## [0.1.0] - 2026

Private preview: read-only visibility.

### Added

- Installable as a Proxmox VE add-on via a `.deb` package with a
  systemd service.
- Log in with your existing Proxmox VE credentials — no separate account
  or password to manage.
- A live, auto-laid-out network topology map of your whole cluster:
  physical NICs, bonds, bridges, VLANs, and guest network connections.
- Four togglable map layers (physical / L2 / SDN / guests) so you can
  focus on the level of detail you need.
- Cluster-wide search by name, MAC address, IP address, or VM/container
  ID, with a keyboard shortcut to jump straight to it.
- Click any element on the map for full detail: its normalized fields,
  live status, and raw underlying configuration.
- Real-time updates: the map reflects changes on your cluster within one
  polling cycle, no manual refresh needed.
- Works from any single node's vnprox instance — the whole cluster's
  network is visible regardless of which node you're connected to.
- Dark and light themes, and keyboard shortcuts for layer toggles, VLAN
  filtering, and search.
- Read-only by design at this stage, and permission-aware: what you can
  see mirrors your existing Proxmox VE permissions, and vnprox never
  modifies your network configuration.
