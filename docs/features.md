# Feature set

Priorities: **P0** = must ship in v1.0, **P1** = v1.x, **P2** = later. Detailed specs live in `docs/features/`. The phase column links to the roadmap (`docs/roadmap.md`).

## Visualization & discovery

| Feature | Pri | Phase | Spec |
|---|---|---|---|
| Cluster-wide interactive topology map (physical → L2 → overlay → guests) | P0 | 1 | [topology.md](features/topology.md) |
| Layer toggles, VLAN filter, search/spotlight, saved layouts | P0 | 1 | topology.md |
| Entity inspector (every object clickable → full detail + raw config) | P0 | 1 | topology.md |
| LLDP/CDP physical discovery (switch name/port per NIC, shown on map) | P0 | 3 | [lldp-discovery.md](features/lldp-discovery.md) |
| MAC/FDB table browser ("which port is this MAC behind") | P1 | 3 | lldp-discovery.md |
| Drift detection (cross-node consistency report) | P0 | 3 | topology.md §6 |
| Export config docs as Markdown/HTML (embedded topology SVG) | P1 | 6 | topology.md |

**Known gap (flagged, T-607):** a standalone "export the map itself as SVG/PNG" control does not exist — the only SVG export is the topology diagram embedded inside the config-doc export above (a different, broader artifact). Previously listed as a single combined row implying both existed; split out and corrected here. Not release-blocking (P1); follow-up: add a dedicated map-export control when a task picks this up.

## Configuration (all via change engine)

| Feature | Pri | Phase | Spec |
|---|---|---|---|
| Staged changesets: validate → diff → apply → commit-confirm → auto-rollback | P0 | 2 | [change-management.md](features/change-management.md) |
| Config time machine (snapshots, diff, restore) | P0 | 2 | change-management.md |
| Linux bridge create/edit/delete, VLAN-aware bridges, port drag-and-drop | P0 | 2 | change-management.md §5 |
| Bond/LAG editor (all modes; live LACP partner status) | P0 | 2 | change-management.md §5 |
| VLAN interface management + trunk visualization | P0 | 2 | change-management.md §5 |
| Interface settings (MTU, addresses, gateway, comments, autostart) | P0 | 2 | change-management.md §5 |
| OVS bridges/bonds/IntPorts | P1 | 4 | change-management.md §5 |
| Guest NIC management (reattach, VLAN, rate limit, firewall flag; bulk ops) | P0 | 2 | change-management.md §6 |
| Raw `/etc/network/interfaces` editor with linting (escape hatch, still changeset-wrapped) | P1 | 2 | change-management.md §7 |

## SDN

| Feature | Pri | Phase | Spec |
|---|---|---|---|
| SDN cockpit: zones/VNets/subnets visual tree + map overlay | P0 | 4 | [sdn.md](features/sdn.md) |
| Guided zone wizards (simple, VLAN, QinQ, VXLAN, EVPN) with plain-English explanations | P0 | 4 | sdn.md |
| EVPN/BGP health: FRR peering status, route counts, exit-node state | P0 | 4 | sdn.md |
| SDN apply orchestration with per-node status | P0 | 4 | sdn.md |
| DHCP management (PVE dnsmasq per zone; ranges, leases view) | P1 | 4 | sdn.md |
| Visual IPAM (subnet grids, utilization, conflicts; PVE/NetBox/phpIPAM) | P0 | 4 | [ipam.md](features/ipam.md) |

## Firewall & analysis

| Feature | Pri | Phase | Spec |
|---|---|---|---|
| Visual firewall editor: cluster/node/guest scopes, groups, aliases, ipsets | P0 | 5 | [firewall.md](features/firewall.md) |
| Rule effects preview ("this rule matches these 14 guests") | P1 | 5 | firewall.md |
| Path simulator (src → dst reachability with blocking-rule identification) | P0 | 5 | firewall.md §5 |
| Firewall log viewer with rule correlation | P1 | 5 | firewall.md |

## Operations

| Feature | Pri | Phase | Spec |
|---|---|---|---|
| Live per-interface/bridge/NIC throughput on the map (heat + sparklines) | P0 | 6 | [monitoring.md](features/monitoring.md) |
| 24h metric history, top talkers per bridge | P1 | 6 | monitoring.md |
| Blueprints: parameterized topology templates, apply cluster-wide | P1 | 6 | [blueprints.md](features/blueprints.md) |
| Onboarding: import/scan existing config, guided health review | P0 | 6 | blueprints.md §4 |
| Audit log UI | P0 | 2 | change-management.md §8 |
| Health checks (MTU mismatch, half-configured bonds, orphan VLANs, STP topology changes) | P1 | 3/6 | monitoring.md §5 |

## Explicit non-goals for v1

- Managing non-Proxmox devices (physical switch config push) — LLDP *read* only.
- NetFlow/sFlow collection and long-term metrics retention (>24h) — integrate with existing observability instead (P2: Prometheus `/metrics` exporter).
- Proxmox Backup Server networking.
- Replacing the PVE firewall engine — vnprox configures the existing pve-firewall, never its own nftables.
- Multi-cluster federation (P2).
