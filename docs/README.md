# vnprox docs

**vnprox** is a self-hosted add-on for [Proxmox VE](https://www.proxmox.com/en/proxmox-virtual-environment) that replaces spreadsheet-and-shell networking with a visual web interface for physical NICs, bonds, bridges, VLANs, OVS, the SDN stack (zones, VNets, subnets, EVPN/VXLAN), IPAM, DHCP, and the firewall — across an entire cluster. It runs as a single service on your PVE nodes.

This is the reader-facing entry point into `docs/` — organized for someone *running* vnprox, not
building it. If you're looking for the implementation plan or task cards instead, see
[`../planning/`](../planning/) from the repository root.

> **Read this first if you're new:** as of this writing, vnprox has **no live hosted
> infrastructure** — no published apt repository, no hosted plugin/blueprint registry, no public
> demo instance, and (see [`support.md`](support.md)) no confirmed-public source repository
> either. Every page here says plainly, in its own words, which parts of what it describes work
> today and which are built but not yet reachable. If a page tells you to run a command, that
> command has been checked against this repository's own tooling or test fixtures — see each
> page's own verification notes.

## Start here

| | |
|---|---|
| [**Install**](install.md) | What actually installs vnprox today, and what the finished distribution story will look like once it exists |
| [**Your first hour**](first-hour.md) | Log in, read the map, make your first change safely |

## Guides

| | |
|---|---|
| [User guide](user-guide.md) | Every screen and workflow, end to end — read the map, stage and apply changes, federation, the open platform, guardrails, adoption features |
| [Deployment guide](deployment.md) | Full install/upgrade/config reference: ports, `vnprox.toml`, upgrade paths, troubleshooting, backup/restore |
| [Feature index](features.md) | What ships, by area, with a pointer to each detailed spec |

## Feature specs

The detailed spec behind each area of the product. [`features.md`](features.md) is the index over
these; this list exists so no spec is reachable only by one route.

| | |
|---|---|
| [Topology](features/topology.md) | The map — physical, L2, SDN and guest layers; Switch and Graph views |
| [Change management](features/change-management.md) | Stage → validate → diff → apply → confirm, interlocks, snapshots, rollback |
| [SDN](features/sdn.md) | Zones, VNets, subnets, EVPN/VXLAN, the SDN cockpit |
| [IPAM](features/ipam.md) | Address plans, conflicts, next-free, DHCP ranges and leases |
| [Firewall](features/firewall.md) | Rule editors at every scope, resolved view, effects preview, log viewer |
| [Monitoring](features/monitoring.md) | Metrics, findings, health checks, flow explorer, alerting |
| [LLDP discovery](features/lldp-discovery.md) | Physical neighbour discovery, switch merging, VLAN cross-check |
| [Blueprints](features/blueprints.md) | Reusable network patterns, starters, capture |
| [Demo mode](features/demo-mode.md) | The synthetic cluster used for demos and screenshots |

## Reference

| | |
|---|---|
| [Datasheet](datasheet.md) | Shipped capability, requirements, and stated limits in one page |
| [Architecture](architecture.md) | Components, diagrams, key design decisions |
| [Data model](data-model.md) | Core entities and persistence design |
| [API](api.md) | REST + WebSocket API, the automation contract |
| [Security](security.md) | AuthN/AuthZ, TLS, threat model |
| [Security verification](security-verification.md) | How each security claim in the doc above is actually checked, and by what |
| [PVE compatibility](compatibility.md) | Which PVE versions are tested, and how |
| [Performance](performance.md) | Budgets and measured numbers |
| [Development](development.md) | Building from source, the toolchain, the quality gate, approved dependencies |

## Community

| | |
|---|---|
| [Support](support.md) | Where to file a bug, what to attach, what response to expect |
| [Contributing](../CONTRIBUTING.md) | Build, test, and the PR process |
| [Community-repository assessment](community-repo-assessment.md) | Whether inclusion in a Proxmox community repository (e.g. the Community-Scripts project) makes sense for vnprox, and why |
| [Forum announcement (draft)](forum-announcement.md) | The Proxmox forum post — drafted, not yet posted |
| [The signed apt repo](../packaging/apt-repo.md) | The repository tooling and signing pipeline — built; the hosting it points at does not exist yet |
| [The hosted blueprint/plugin registry](hub-registry.md) | The registry protocol and client — built; no instance is hosted yet |
| [The docs site itself](docs-site.md) | How this site is built, and what stands between it and a live URL |

## Project

| | |
|---|---|
| [Status](project-status.md) | Current, precise delivery breakdown — what shipped, what did not, and where the two were confused |
| [Status matrix](status-matrix.md) | The full audit grid and the method behind it |
| [Changelog](../CHANGELOG.md) | Every user-facing change, by release |

## Roadmaps

Seven documents — one per numbered arc, plus `roadmap-leverage.md`, which covers phase 24 alone.
Phases 22–24 sit **outside** the arc numbering entirely and shipped in the v3.5.0 line. The active
document is first.

| | |
|---|---|
| [**Arc 6 — "Earned"** (active)](roadmap-earned.md) | Phases 29–33, v4.1 → v5.0. Closes the gap between what the docs claim and what ships, and consolidates every open item left by the arcs below |
| [Arc 1 — core product](roadmap.md) | Phases 0–7, → v1.0. Shipped |
| [Arc 2 — beyond the cluster](roadmap-next.md) | Phases 8–12, v1.4 → v2.0. Shipped |
| [Arc 3 — the open platform](roadmap-universal.md) | Phases 13–17, v2.1 → v3.0. Shipped |
| [Arc 4 — proven in production](roadmap-proven.md) | Phases 18–21, cut as v4.0.0. Shipped |
| [Operator leverage](roadmap-leverage.md) | Phase 24 — not an arc; phases 22–24 sit outside the numbering and folded into the v3.5.0 line. Shipped |
| [Arc 5 — adoptable, not just proven](roadmap-adopted.md) | Phases 25–28, v3.5.0. Shipped |

[License: Apache-2.0](../LICENSE)

---

*This site is built from the `docs/` folder using [docsify](https://docsify.js.org/) — no build
step, no new toolchain. See [`docs-site.md`](docs-site.md) for what that means — GitHub Pages is
enabled for this repository and serving it live at `docs.vnprox.com`, unversioned (always "latest").*
