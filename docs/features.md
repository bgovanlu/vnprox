# Feature set

**Scope of this file.** The feature *index*, organised by area, with a pointer to each detailed spec in `docs/features/`. It covers what ships today across all four arcs.

- For a reader evaluating the product, [`datasheet.md`](datasheet.md) is the better entry point — it states capability, requirements, and limits in one place.
- For delivery state and open items, see [`project-status.md`](project-status.md).
- For the per-feature implementation grid, see [`status-matrix.md`](status-matrix.md).

**Priorities** below are historical: **P0** = shipped in v1.0, **P1** = v1.x, **P2** = later. The **Arc** column says which release arc delivered it — 1 (v1.0, phases 0–7), 2 (v2.0, phases 8–12), 3 (v3.0, phases 13–17), 4 (v3.1+, phases 18–23).

> **Correction (T-2107, 2026-08-06).** This file previously described only the v1.0 scope and listed five since-shipped capabilities under "explicit non-goals": flow collection, Proxmox Backup Server networking, multi-cluster federation, physical switch config push, and the Prometheus exporter. All five now ship. The non-goals section below has been rewritten to state what is *still* out of scope, and the arcs 2–4 features have been added.

## Visualization & discovery

| Feature | Pri | Arc | Spec |
|---|---|---|---|
| Cluster-wide interactive topology map (physical → L2 → overlay → guests), Switch (default) / Graph view toggle | P0 | 1 | [topology.md](features/topology.md) |
| Layer toggles, VLAN filter, search/spotlight (both views); saved layouts, drag-edit, path-sim overlay, traffic paint (Graph view only) | P0 | 1 | topology.md |
| Entity inspector (every object clickable → full detail + raw config) | P0 | 1 | topology.md |
| LLDP/CDP physical discovery (switch name/port per NIC, shown on map) | P0 | 1 | [lldp-discovery.md](features/lldp-discovery.md) |
| MAC/FDB table browser | P1 | 1 | lldp-discovery.md |
| Drift detection (config-vs-live and cross-node) | P0 | 1 | topology.md §6 |
| Export config docs as Markdown/HTML (embedded topology SVG) | P1 | 1 | topology.md |
| Guest network interior inspector (opt-in, read-only) | P1 | 3 | monitoring.md §6 |
| Physical-layer progressive collapse | P1 | 4 | topology.md |

**Known gap (T-607):** a standalone "export the map as SVG/PNG" control does not exist — the only SVG export is the topology diagram embedded in the config-doc export. Not release-blocking.

## Configuration (all via the change engine)

| Feature | Pri | Arc | Spec |
|---|---|---|---|
| Staged changesets: validate → diff → apply → commit-confirm → auto-rollback | P0 | 1 | [change-management.md](features/change-management.md) |
| Config time machine (snapshots, diff, restore) | P0 | 1 | change-management.md |
| Linux bridge create/edit/delete, VLAN-aware bridges, port drag-and-drop | P0 | 1 | change-management.md §5 |
| Bond/LAG editor (all modes; live LACP partner status) | P0 | 1 | change-management.md §5 |
| VLAN interface management + trunk visualization | P0 | 1 | change-management.md §5 |
| Interface settings (MTU, addresses, gateway, comments, autostart) | P0 | 1 | change-management.md §5 |
| OVS bridges/bonds/IntPorts | P1 | 1 | change-management.md §5 |
| Guest NIC management (reattach, VLAN, rate limit, firewall flag; bulk ops) | P0 | 1 | change-management.md §6 |
| Raw `/etc/network/interfaces` editor with linting | P1 | 1 | change-management.md §7 |
| Management-redundancy wizard + protected-path ceremony | P0 | 2 | [security.md](security.md) |
| Scheduled apply / maintenance windows | P1 | 2 | change-management.md §4 |
| Declarative cluster network spec (apply/plan) | P1 | 2 | change-management.md |
| Change review: comments, approvals, shareable review link | P1 | 4 | change-management.md §3.1 |
| Unattended revert for `fw.*`/`sdn.*` via apply-time sealed ticket | P0 | 4 | change-management.md §4 |

## SDN, IPAM & addressing

| Feature | Pri | Arc | Spec |
|---|---|---|---|
| SDN cockpit: zones/VNets/subnets visual tree + map overlay | P0 | 1 | [sdn.md](features/sdn.md) |
| Guided zone wizards (simple, VLAN, QinQ, VXLAN, EVPN) | P0 | 1 | sdn.md |
| EVPN/BGP health: FRR peering status, route counts, exit-node state | P0 | 1 | sdn.md |
| SDN apply orchestration with per-node status | P0 | 1 | sdn.md |
| DHCP management (PVE dnsmasq per zone; ranges, leases) | P1 | 1 | sdn.md |
| Visual IPAM (subnet list, address list, utilization, conflicts) | P0 | 1 | [ipam.md](features/ipam.md) |
| DNS management (PowerDNS SDN plugin) | P1 | 2 | ipam.md |
| External subnets as first-class records | P1 | 2 | ipam.md §7 |
| Bidirectional NetBox/phpIPAM sync (preview → confirmed apply) | P1 | 2 | ipam.md §7 |
| Cross-cluster IPAM conflict detection | P1 | 2 | ipam.md §7 |
| IPv6 planning grid + dual-stack rollout wizard | P1 | 3 | ipam.md §4–5 |

## Firewall & analysis

| Feature | Pri | Arc | Spec |
|---|---|---|---|
| Visual firewall editor: cluster/node/guest scopes, groups, aliases, ipsets | P0 | 1 | [firewall.md](features/firewall.md) |
| Rule effects preview | P1 | 1 | firewall.md |
| Path simulator (4 verdicts, blocking-rule identification) | P0 | 1 | firewall.md §5 |
| Firewall log viewer with heuristic rule correlation | P1 | 1 | firewall.md §4 |
| Live path probe ("verify live") via guest agent | P1 | 1 | firewall.md §5 |
| Microsegmentation planner (propose → dry-run → stage) | P1 | 3 | firewall.md §7 |
| Firewall analytics (unused rules) | P1 | 2 | firewall.md |
| Diagnosis ladder | P1 | 3 | [monitoring.md](features/monitoring.md) |

## Observability

| Feature | Pri | Arc | Spec |
|---|---|---|---|
| Live per-interface throughput on the map (heat + sparklines) | P0 | 1 | monitoring.md §1 |
| 24h metric history, top talkers per bridge | P1 | 1 | monitoring.md §2–3 |
| Health checks (15 sources, 43 checks) feeding one findings stream | P1 | 1 | monitoring.md §5 |
| Prometheus `/metrics` exporter + Grafana panels | P1 | 2 | monitoring.md §4 |
| Alert rules → webhook or PVE notification system | P1 | 2 | monitoring.md §5 |
| Flow ingestion (sFlow/NetFlow/IPFIX) + flow explorer | P1 | 2 | monitoring.md §3 |
| Host-local conntrack sampling; conntrack & NAT explorer | P1 | 3 | monitoring.md §3 |
| Latency & loss mesh + heatmap paint mode | P1 | 3 | monitoring.md §1–2 |
| Path MTU prober | P1 | 3 | monitoring.md |
| WAN & upstream health | P1 | 3 | monitoring.md §5 |
| Packet capture with BPF builder (bounded, audited) | P1 | 3 | monitoring.md |
| Service-class flow attribution (migration/backup/Ceph/corosync) | P1 | 3 | monitoring.md §3 |
| Capacity forecasting; traffic baseline & anomaly detection | P2 | 3 | monitoring.md |
| Daemon self-observability (RED metrics) | P1 | 4 | monitoring.md |

## Platform & fleet

| Feature | Pri | Arc | Spec |
|---|---|---|---|
| Blueprints: parameterized topology templates, signed, idempotent | P1 | 1 | [blueprints.md](features/blueprints.md) |
| Onboarding: import/scan existing config, guided health review | P0 | 1 | blueprints.md §4 |
| Audit log UI | P0 | 1 | change-management.md §8 |
| History timeline + playback | P1 | 2 | change-management.md |
| Federation: attach other PVE clusters (views and workflows, never ownership) | P1 | 2 | [user-guide.md](user-guide.md) §7 |
| WireGuard cluster interconnect | P1 | 3 | user-guide.md §7 |
| Switch config push (opt-in, two-key, LLDP-confirmed ports only) | P2 | 2 | user-guide.md §7.4 |
| PBS backup-path awareness | P2 | 2 | user-guide.md §7.5 |
| Ceph network awareness; migration network planner | P2 | 3 | monitoring.md |
| Kubernetes overlay mapping + flow attribution | P2 | 3 | monitoring.md §3 |
| SR-IOV VF lifecycle | P2 | 3 | — |
| MCP surface for AI operators (read + stage only) | P1 | 3 | user-guide.md §8.1 |
| Plugin SDK (5 extension points, sandboxed) | P1 | 3 | user-guide.md §8.2 |
| Multi-tenancy & self-service request/approve | P1 | 3 | user-guide.md §8.3 |
| High availability (active/standby, timers survive failover) | P1 | 3 | user-guide.md §8.4 |
| Hub: signed blueprint/plugin registry client | P2 | 3 | user-guide.md §8.5 |
| Embeddable read-only views | P2 | 3 | user-guide.md §8.6 |
| OIDC single sign-on | P2 | 2 | user-guide.md §7.6 |
| Automation tokens & webhooks | P1 | 2 | [api.md](api.md) |

## Operations & lifecycle

| Feature | Pri | Arc | Spec |
|---|---|---|---|
| `vnproxctl` operator CLI (status, snapshots, rollback, apply, backup, restore, support-bundle, certs) | P1 | 2/4 | [deployment.md](deployment.md) |
| Backup, restore & disaster recovery of vnprox's own state | P0 | 4 | deployment.md |
| Support bundle export (secret-redacted) | P0 | 4 | [security.md](security.md) |
| Retention, rotation & compaction | P1 | 4 | deployment.md |
| Peer-API cluster-CA pinning + verification-name resolution | P1 | 4 | security.md |
| Certificate inventory, expiry/SAN/chain checks | P1 | 4 | security.md |
| Online help on every screen (build-gated coverage) | P1 | 4 | user-guide.md §6a |
| Accessibility (WCAG AA, axe-gated), responsive triage layout | P1 | 2 | — |

## Still out of scope

Superseded non-goals removed; these are the boundaries that genuinely still hold.

- **Replacing the PVE firewall engine.** vnprox configures the existing pve-firewall and never installs its own nftables ruleset.
- **Becoming a metrics or flow warehouse.** Retention is deliberately short-horizon (24 h metrics; bounded flow and latency rings). Export to real observability for anything longer.
- **Managing non-Proxmox devices generally.** The one exception is the guarded switch push (VLAN membership, port descriptions, LACP, on LLDP-confirmed PVE-facing ports only). vnprox is not a switch management platform.
- **Renewing or reissuing TLS certificates.** PVE owns that (`pvecm updatecerts`, `pvenode acme`); vnprox reports and points at the command.
- **Payload inspection.** Flow classification uses metadata only. vnprox is not an IDS.
- **Owning another cluster's configuration.** Federation federates views and workflows; a changeset always belongs to exactly one cluster.
- **Non-root operation.** The daemon runs as root with a scoped capability bounding set; full non-root operation remains post-1.0 work.
