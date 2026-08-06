# vnprox — Visual Networking for Proxmox

**vnprox** is a self-hosted add-on for [Proxmox VE](https://www.proxmox.com/en/proxmox-virtual-environment) that replaces spreadsheet-and-shell networking with a complete, modern, visual web interface for **all** Proxmox networking — physical NICs, bonds, bridges, VLANs, OVS, the SDN stack (zones, VNets, subnets, EVPN/VXLAN), IPAM, DHCP, and the firewall — across an entire cluster.

It runs as a single service on your PVE nodes and serves its UI at **`https://<node>:8007`**.

> vnprox is not a clone of any other hypervisor's network UI. It is designed to be *more* capable and *more* friendly: a live cluster-wide topology map that spans the physical layer (LLDP) through overlays, commit-confirmed changes with automatic rollback, a configuration time machine, and a path simulator that answers "can VM A reach VM B on port 443, and if not, which rule or missing route blocks it?"

## Status

**Shipped through v3.0.2.** All three implementation arcs are complete — the core product
([`planning/implementation-plan.md`](planning/implementation-plan.md)), multi-cluster federation
([`planning/implementation-plan-next.md`](planning/implementation-plan-next.md)), and the open
platform arc ([`planning/implementation-plan-universal.md`](planning/implementation-plan-universal.md))
— per the phased task cards executed by AI sub-agents. See [`CHANGELOG.md`](CHANGELOG.md) for
what shipped in each release, and
[`planning/reports/needs-hardware-validation.md`](planning/reports/needs-hardware-validation.md)
for the items still awaiting validation on real Proxmox hardware.

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

| Document | Contents |
|---|---|
| [`docs/datasheet.md`](docs/datasheet.md) | **Shipped capability, requirements, and stated limits — start here** |
| [`docs/project-status.md`](docs/project-status.md) | Percent complete, open items ranked, recommended sequence |
| [`docs/status-matrix.md`](docs/status-matrix.md) | Full-stack audit grid (feature × backend × GUI × API × docs × tests × validation) |
| [`docs/architecture.md`](docs/architecture.md) | System architecture, components, diagrams, key design decisions |
| [`docs/data-model.md`](docs/data-model.md) | Core entities and persistence design |
| [`docs/api.md`](docs/api.md) | REST + WebSocket API design |
| [`docs/security.md`](docs/security.md) | AuthN/AuthZ, TLS, threat model |
| [`docs/features.md`](docs/features.md) | v1.0 feature matrix and priorities; per-feature specs in [`docs/features/`](docs/features/). **Stale — describes the v1.0 scope and lists since-shipped capabilities as non-goals; use `datasheet.md` for current capability** |
| [`docs/roadmap.md`](docs/roadmap.md) | Release phases and milestones |
| [`docs/deployment.md`](docs/deployment.md) | Install, upgrade, uninstall, port-conflict handling |
| [`docs/user-guide.md`](docs/user-guide.md) | End-user guide (first run, common tasks) |
| [`docs/development.md`](docs/development.md) | Tech stack, repo layout, standards, testing, CI |
| [`planning/implementation-plan.md`](planning/implementation-plan.md) | Phased plan, dependency graph, sub-agent task index |
| [`planning/tasks/`](planning/tasks/) | Exact, self-contained task cards for implementation sub-agents |

## Requirements (target)

- Proxmox VE **8.2+** or **9.x** (Debian 12/13 based)
- Installed directly on PVE nodes (every node in the cluster)
- Port **8007** (default; configurable — the installer detects Proxmox Backup Server, which also uses 8007, and offers an alternative)

## License

AGPL-3.0 (matching the Proxmox ecosystem). License file to be added before first release.
