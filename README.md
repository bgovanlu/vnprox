# vnprox — Visual Networking for Proxmox

**vnprox** is a self-hosted add-on for [Proxmox VE](https://www.proxmox.com/en/proxmox-virtual-environment) that replaces spreadsheet-and-shell networking with a complete, modern, visual web interface for **all** Proxmox networking — physical NICs, bonds, bridges, VLANs, OVS, the SDN stack (zones, VNets, subnets, EVPN/VXLAN), IPAM, DHCP, and the firewall — across an entire cluster.

It runs as a single service on your PVE nodes and serves its UI at **`https://<node>:8007`**.

> vnprox is not a clone of any other hypervisor's network UI. It is designed to be *more* capable and *more* friendly: a live cluster-wide topology map that spans the physical layer (LLDP) through overlays, commit-confirmed changes with automatic rollback, a configuration time machine, and a path simulator that answers "can VM A reach VM B on port 443, and if not, which rule or missing route blocks it?"

## Status

**Shipped through v4.0.0** (2026-08-14, [`CHANGELOG.md`](CHANGELOG.md)), across five implementation
arcs — the core product ([`planning/implementation-plan.md`](planning/implementation-plan.md)),
multi-cluster federation ([`planning/implementation-plan-next.md`](planning/implementation-plan-next.md)),
the open platform ([`planning/implementation-plan-universal.md`](planning/implementation-plan-universal.md)),
proving it on real hardware ([`planning/implementation-plan-proven.md`](planning/implementation-plan-proven.md)
— its last phase, "ecosystem and reach", is what made this release 4.0), and making it
adoptable by someone other than its own developers
([`planning/implementation-plan-adopted.md`](planning/implementation-plan-adopted.md)) — per the
phased task cards executed by AI sub-agents. Three phases sit outside that numbering and shipped
in the v3.5.0 line: online help, certificate management, and operator leverage
([`docs/roadmap-leverage.md`](docs/roadmap-leverage.md)).

**Arc 6, [`docs/roadmap-earned.md`](docs/roadmap-earned.md) ("Earned"), is now active**, and it
exists because of what an audit on 2026-08-15 found: vnprox's distance from this README is not
missing features but **claims running ahead of truth**. The shipped v4.0.0 PWA, for one, could not
register its own service worker in any real browser — the CSP refused it. Arc 6 adds no headline
feature; every card closes a verified gap between what is written here and what a user actually
gets. Two things v4.0.0 counted as delivered were not — localization, and the *hosting* of the
signed apt repository — and both are named and rescheduled there rather than quietly carried.

[`docs/project-status.md`](docs/project-status.md) has the current, precise delivery breakdown;
this is also where "feature-complete" and "validated on real hardware" diverge most —
[`planning/reports/needs-hardware-validation.md`](planning/reports/needs-hardware-validation.md)
and [`docs/status-matrix.md`](docs/status-matrix.md) track exactly what's still owed there.

## Why vnprox

Proxmox VE's networking has grown real capabilities (ifupdown2, SDN with EVPN, firewall, IPAM) but its configuration experience has not kept pace:

- Networking is scattered across five UI locations (node → Network, Datacenter → SDN, Datacenter → Firewall, per-VM NICs, `/etc/network/interfaces` by hand).
- There is no visualization at all — no topology, no "what is plugged into what," no view of which VMs ride which VLAN.
- Mistakes are punished: a bad bridge edit can drop the management interface with no automatic recovery.
- Cluster-wide consistency (same bridges/VLANs on every node) is entirely manual.

## Headline features

| | |
|---|---|
| 🗺️ **Live topology map** | One interactive map from physical switch ports (LLDP) → NICs → bonds → bridges → VLANs → SDN overlays → VM NICs, cluster-wide. |
| ✅ **Commit-confirm changes** | Every change is staged, validated, diffed, and applied with an automatic rollback timer — if the change breaks connectivity, vnprox reverts it. |
| ⏪ **Config time machine** | Every applied change is snapshotted. Diff any two points in time; restore with one click. |
| 🔍 **Path simulator** | Firewall- and routing-aware reachability checks between any two endpoints, with the blocking rule identified. |
| 🌐 **Full SDN cockpit** | Visual zones, VNets, subnets, EVPN/BGP peering status, VXLAN overlays, DHCP — with guided wizards. |
| 📒 **Visual IPAM** | Subnet grids, allocation views, conflict detection, integrated with PVE/NetBox/phpIPAM IPAM plugins. |
| 🧩 **Blueprints** | Reusable topology templates ("two-node HA with LACP + storage VLAN") applied cluster-wide in one operation. |
| 🚨 **Drift detection** | Continuous comparison of desired vs. live state across nodes, with per-node diffs. |

Full list: [`docs/features.md`](docs/features.md).

## Documentation map

**New here?** [`docs/README.md`](docs/README.md) is the reader-facing entry point — install, your
first hour, task guides, then reference — organized for someone *running* vnprox rather than
building it. The table below is the full corpus, contributor-dense documents included.

| Document | Contents |
|---|---|
| [`docs/README.md`](docs/README.md) | **Reader-facing docs site entry point — start here if you're evaluating or running vnprox** |
| [`docs/install.md`](docs/install.md) | What actually installs vnprox today vs. what the finished distribution story looks like |
| [`docs/first-hour.md`](docs/first-hour.md) | Log in, read the map, make your first change safely |
| [`docs/support.md`](docs/support.md) | Where to file a bug, what to attach, what response to expect |
| [`docs/datasheet.md`](docs/datasheet.md) | Shipped capability, requirements, and stated limits |
| [`docs/project-status.md`](docs/project-status.md) | Percent complete, open items ranked, recommended sequence |
| [`docs/status-matrix.md`](docs/status-matrix.md) | Full-stack audit grid (feature × backend × GUI × API × docs × tests × validation) |
| [`docs/architecture.md`](docs/architecture.md) | System architecture, components, diagrams, key design decisions |
| [`docs/data-model.md`](docs/data-model.md) | Core entities and persistence design |
| [`docs/api.md`](docs/api.md) | REST + WebSocket API design |
| [`docs/security.md`](docs/security.md) | AuthN/AuthZ, TLS, threat model |
| [`docs/features.md`](docs/features.md) | Current feature index by area, with a pointer to each detailed spec in [`docs/features/`](docs/features/) |
| [`docs/roadmap.md`](docs/roadmap.md) | Release phases and milestones |
| [`docs/roadmap-adopted.md`](docs/roadmap-adopted.md) | Arc 5 (phases 25–28) — the current planned arc: proof, guardrails, config-as-code, adoption |
| [`docs/deployment.md`](docs/deployment.md) | Install, upgrade, uninstall, port-conflict handling |
| [`docs/user-guide.md`](docs/user-guide.md) | End-user guide (first run, common tasks) |
| [`docs/development.md`](docs/development.md) | Tech stack, repo layout, standards, testing, CI |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | Build, test, and the PR process for human contributors |
| [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md) | Community standards for this project's spaces (issues, PRs, discussions) |
| [`SECURITY.md`](SECURITY.md) | How to report a vulnerability, privately |
| [`docs/community-repo-assessment.md`](docs/community-repo-assessment.md) | Whether inclusion in a Proxmox community repository (e.g. Community-Scripts) makes sense, and why |
| [`planning/implementation-plan.md`](planning/implementation-plan.md) | Phased plan, dependency graph, sub-agent task index |
| [`planning/tasks/`](planning/tasks/) | Exact, self-contained task cards for implementation sub-agents |

## Requirements (target)

- Proxmox VE **8.2+** or **9.x** (Debian 12/13 based)
- Installed directly on PVE nodes (every node in the cluster)
- Port **8007** (default; configurable — the installer detects Proxmox Backup Server, which also uses 8007, and offers an alternative)

## License

Apache-2.0. See [`LICENSE`](LICENSE), [`NOTICE`](NOTICE), and
[`THIRD-PARTY-LICENSES.md`](THIRD-PARTY-LICENSES.md) — the last enumerates every bundled
third-party component, notably `elkjs` (EPL-2.0) which ships inside the SPA.
