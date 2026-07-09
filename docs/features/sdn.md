# Feature spec — SDN cockpit

Proxmox SDN (zones → VNets → subnets, applied cluster-wide via `/etc/pve/sdn/*.cfg` + the SDN apply mechanism) is powerful but opaque in the stock UI. vnprox makes it visual, guided, and observable.

## 1. Views

- **Tree + detail**: zones expand to VNets to subnets; every level shows per-node realization status (applied / pending / error, from PVE SDN status endpoints).
- **Map overlay**: the topology map's overlay layer shows each VNet as a colored plane connecting the bridges that realize it across nodes; EVPN/VXLAN zones draw the VTEP mesh with tunnel endpoints and MTU annotations.
- **Pending state**: PVE stages SDN edits until an apply — vnprox surfaces staged-vs-running as a first-class diff instead of a mystery "pending" flag.

## 2. Guided zone wizards

One wizard per zone type. Each step explains *what this actually does* in plain English, with a live preview pane drawing the resulting topology before anything is created:

- **Simple** — isolated bridge per node, optional SNAT. ("A private network that exists on every node; VMs on it can talk to each other on the same node.")
- **VLAN** — picks the VLAN-aware bridge, validates the physical path actually trunks the chosen VIDs (cross-checks LLDP VLAN info when available).
- **QinQ** — service VLAN + inner range with double-tag illustration.
- **VXLAN** — peer address list auto-suggested from cluster node IPs; MTU math shown explicitly (underlay MTU − 50) with a one-click "set VNet MTU accordingly".
- **EVPN** — controller (ASN, peers), VRF-VXLAN tag, exit nodes, primary exit, route-target explanation. The wizard renders the resulting BGP session graph before creation.

Wizard output is a normal changeset (ops: `sdn.zone.create`, `sdn.vnet.create`, `sdn.subnet.create`, `sdn.apply`) — reviewable and rollback-protected like everything else.

## 3. EVPN/BGP observability

`GET /sdn/evpn/status` aggregates per-node FRR state (via `vtysh -c "show bgp summary json"` and `show evpn vni json` through the peer API): session state per peer, prefixes received, VNI list, exit-node health. UI: a peering matrix (nodes × peers, green/amber/red) and per-session detail. Flapping sessions raise a health finding.

## 4. SDN apply orchestration

vnprox wraps the SDN apply (`PUT /cluster/sdn`) with: pre-apply validation (zone node coverage, bridge existence on member nodes, MTU sanity), per-node progress from the resulting PVE tasks, and post-apply verification that each node's status reports the zone healthy. Failures link straight to the failing node's task log.

## 5. DHCP (P1)

For zones with PVE-managed DHCP (dnsmasq): range editor on subnets, static reservations bound to guest MACs (picker), and a live leases view (parsed per-node via peer API). Reservations are IPAM allocations (`ipam.alloc.create`) so the IPAM grid and DHCP stay one dataset.

## 6. Out of scope v1

Custom FRR config beyond what PVE's EVPN controller writes; BGP to external fabrics beyond PVE's controller/exit-node model (view-only where present); IPv6 SLAAC management (display yes, config P1).
