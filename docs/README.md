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

## Reference

| | |
|---|---|
| [Datasheet](datasheet.md) | Shipped capability, requirements, and stated limits in one page |
| [Architecture](architecture.md) | Components, diagrams, key design decisions |
| [Data model](data-model.md) | Core entities and persistence design |
| [API](api.md) | REST + WebSocket API, the automation contract |
| [Security](security.md) | AuthN/AuthZ, TLS, threat model |
| [PVE compatibility](compatibility.md) | Which PVE versions are tested, and how |
| [Performance](performance.md) | Budgets and measured numbers |

## Community

| | |
|---|---|
| [Support](support.md) | Where to file a bug, what to attach, what response to expect |
| [Contributing](../CONTRIBUTING.md) | Build, test, and the PR process |
| [Community-repository assessment](community-repo-assessment.md) | Whether inclusion in a Proxmox community repository (e.g. the Community-Scripts project) makes sense for vnprox, and why |
| [Forum announcement (draft)](forum-announcement.md) | The Proxmox forum post — drafted, not yet posted |

## Project

[Status](project-status.md) · [Roadmap](roadmap.md) · [Changelog](../CHANGELOG.md) · [License: Apache-2.0](../LICENSE)

---

*This site is built from the `docs/` folder using [docsify](https://docsify.js.org/) — no build
step, no new toolchain. See [`docs-site.md`](docs-site.md) for what that means, and for the one
thing standing between this rendering as a live URL and it not: GitHub Pages has not been enabled
for this repository yet.*
