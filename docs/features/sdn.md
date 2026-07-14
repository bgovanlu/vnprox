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

**Subnet gateway semantics, by zone type (added by T-701).** The subnet step's gateway field pre-fills to the CIDR's first usable address as soon as a CIDR is entered (skipping known allocations when the subnet overlaps one vnprox already has IPAM data for), with an explicit "keep this network isolated" opt-out instead of a silently-empty field — a wizard-created network is functional by default. What the gateway actually means differs by zone type:
- **Simple** — optional unless SNAT is on: a private, single-node-scoped network with no gateway is a legitimate isolated network (the wizard's own default framing already got this right); SNAT needs a gateway to disguise traffic behind.
- **VLAN / QinQ / VXLAN** — the gateway lives on your external router, not on anything vnprox or PVE creates; vnprox only records it for DHCP option distribution and the IPAM grid's gateway marker.
- **EVPN** — the gateway becomes the anycast address realized on every zone member node; leaving it unset is strongly discouraged (routed/exit-node traffic through the subnet is silently broken, not rejected — see below) and SNAT additionally requires at least one of the zone's exit nodes, cross-checked live against the wizard's own exit-node selection.

The change engine enforces the two shapes real PVE actually rejects at subnet stage time — a gateway outside the subnet's own CIDR (`schema.gateway_not_in_subnet`), and `snat: true` with no effective gateway (`sdn.snat_requires_gateway`) — as blocking errors, both with a one-click fix setting the gateway to the CIDR's first usable address; and warns (`sdn.evpn_gateway_missing`, `sdn.snat_requires_exit_node`) on the two EVPN shapes real PVE *accepts* but ships broken traffic for. See docs/api.md's changesets section for the exact finding codes/severities and `POST /changesets/{id}/apply`'s `pve_session_required` rejection code. These checks apply uniformly to wizard output, the plain SDN editor forms, and raw `POST /changesets` bodies — there is no wizard-only safety net.

## 3. EVPN/BGP observability

`GET /sdn/evpn/status` aggregates per-node FRR state (via `vtysh -c "show bgp summary json"` and `show evpn vni json` through the peer API): session state per peer, prefixes received, VNI list, exit-node health. UI: a peering matrix (nodes × peers, green/amber/red) and per-session detail. Flapping sessions raise a health finding.

## 4. SDN apply orchestration

vnprox wraps the SDN apply (`PUT /cluster/sdn`) with: pre-apply validation (zone node coverage, bridge existence on member nodes, MTU sanity), per-node progress from the resulting PVE tasks, and post-apply verification that each node's status reports the zone healthy. Failures link straight to the failing node's task log.

## 5. DHCP (P1)

For zones with PVE-managed DHCP (dnsmasq): range editor on subnets, static reservations bound to guest MACs (picker), and a live leases view (parsed per-node via peer API). Reservations are IPAM allocations (`ipam.alloc.create`) so the IPAM grid and DHCP stay one dataset.

## 6. Out of scope v1

Custom FRR config beyond what PVE's EVPN controller writes; BGP to external fabrics beyond PVE's controller/exit-node model (view-only where present); IPv6 SLAAC management (display yes, config P1).
