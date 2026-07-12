# Changelog

All notable user-facing changes to vnprox are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).
vnprox uses semantic versioning; the SQLite schema migrates forward-only.

Versions below correspond to the milestones in `docs/roadmap.md`. Phase 3
("Discovery & true cluster") does not have its own version cut per the
roadmap — its functionality (peer clustering, LLDP discovery, drift
detection, FDB browsing) shipped as part of the v0.8 development cycle and
is listed under v0.8 below, alongside that release's SDN/IPAM work.

Precise dates are only given for the v1.0.0 release being cut by this
change; earlier milestones predate this file and are dated only by year.

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
