# Feature spec — SDN cockpit

Proxmox SDN (zones → VNets → subnets, applied cluster-wide via `/etc/pve/sdn/*.cfg` + the SDN apply mechanism) is powerful but opaque in the stock UI. vnprox makes it visual, guided, and observable.

## 1. Views

- **Tree + detail**: zones expand to VNets to subnets; every level shows per-node realization status (applied / pending / error, from PVE SDN status endpoints).
- **Map overlay**: the topology map's overlay layer shows each VNet as a colored plane connecting the bridges that realize it across nodes; EVPN/VXLAN zones draw the VTEP mesh with tunnel endpoints and MTU annotations.
- **Pending state**: PVE stages SDN edits until an apply — vnprox surfaces staged-vs-running as a first-class diff instead of a mystery "pending" flag.

## 2. Guided zone wizards

One wizard per zone type. Each step explains *what this actually does* in plain English, with a live preview pane drawing the resulting topology before anything is created:

- **Simple** — isolated bridge per node, optional SNAT. ("A private network that exists on every node; VMs on it can talk to each other on the same node.") A simple zone's SNAT flag (`Subnet.snat`, already read-only here) is also the source of one row on T-1403's Edge layer (`GET /edge/nat`'s `sdnSimpleZoneNat` — see docs/api.md's Edge & NAT cockpit section): that layer never re-derives or shadows this data, it only re-shapes the identical read into "what does this cluster expose/mask outbound" terms alongside PVE-host masquerade/port-forward rules and static routes, which are edited via the ordinary `nat.*`/`route.static.*` changeset ops.
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

**Session health attaches to the controller, not just the zone (T-3102).** Before SDN Controllers existed as first-class objects (§7), the only per-zone signal this route offered was `exitNodes` (derived from a zone's own `exitNodes` list). The same response now also carries `controllers`: one entry per §7 controller, `healthy`/`peers` computed by matching the controller's own configured peer address list against observed sessions across the cluster fan-out — an operator asking "is this controller's underlay up" gets a direct answer keyed by the controller they configured, not only an answer inferred by cross-referencing a zone's exit nodes. `exitNodes` itself also gained a `controller` field (the owning zone's own reference, when it resolves) so the two views can be cross-linked. See docs/api.md's `GET /sdn/evpn/status` section for the exact shape.

## 4. SDN apply orchestration

vnprox wraps the SDN apply (`PUT /cluster/sdn`) with: pre-apply validation (zone node coverage, bridge existence on member nodes, MTU sanity), per-node progress from the resulting PVE tasks, and post-apply verification that each node's status reports the zone healthy. Failures link straight to the failing node's task log.

## 5. DHCP (P1)

For zones with PVE-managed DHCP (dnsmasq): range editor on subnets, static reservations bound to guest MACs (picker), and a live leases view (parsed per-node via peer API). Reservations are IPAM allocations (`ipam.alloc.create`) so the IPAM grid and DHCP stay one dataset.

## 6. SDN Fabrics (T-3101)

PVE 9 added SDN Fabrics — an underlay-routing object family (`/cluster/sdn/fabrics`) distinct from the zone/vnet/subnet tree above: a fabric configures BGP, OpenFabric, OSPF, or WireGuard underlay routing that a vxlan/evpn zone can ride on via its own `--fabric` field, not a tenant network in its own right. It is **not** rendered on the topology map or folded into the zone tree's view — like EVPN/BGP observability above, it gets its own read-only status view (a Fabrics tab in the SDN cockpit): fabric list with protocol badge and per-node membership (`StatusDot` per member, from `GET /cluster/sdn/fabrics/node` — configured membership, not verified realization health; the captured API has no per-fabric status route the way a zone has one), a create/edit form whose fields reveal per the selected protocol (`csnpInterval`/`helloInterval`/`routeFilter` for OpenFabric; `area`/`redistribute`/`routeFilter` for OSPF; `redistribute` for BGP; `persistentKeepalive` for WireGuard), and read-only tables for the two BGP route-policy families the same capture exposed, `prefix-lists` and `route-maps` (display only — no CRUD; they almost certainly couple to a future SDN controllers card's `--route-map-in`/`--route-map-out`, not established here).

**A WireGuard fabric is genuinely WireGuard, and is not a WireGuard tunnel.** One of the four fabric protocols is `wireguard` — a real WireGuard parameter set (`persistentKeepalive`). This is PVE-managed underlay transport, a wholly different management plane from this codebase's own WireGuard tunnels (§2's SNAT aside notwithstanding — see the WireGuard tunnels feature spec for that surface): the two never share a model, a changeset op family (`sdn.fabric.*` vs. `wg.tunnel.*`/`wg.peer.*`), or a map badge. A fabric never appears as a tunnel edge and a tunnel never appears in the Fabrics view.

Fabric create/update/delete are ordinary changeset ops, staging into the same pending SDN config every zone/vnet/subnet edit does and applying through the same trailing `sdn.apply` (docs/data-model.md §3) — there is no fabric-specific apply path, deliberately: `PUT /cluster/sdn`'s `--lock-token` gap (an operator's vnprox changeset can silently commit an unrelated PVE-GUI-staged edit alongside it) applies identically to a fabric op as to a zone op, and this card does not widen it — see `planning/reports/T-3101-followup-01.md` for the filed, not-yet-fixed gap.

## 7. SDN Controllers (T-3102)

A controller was a bare string on a zone's own `controller` field before this task — an EVPN/BGP-EVPN wizard (§2) could reference one by ID, but nothing in this codebase could create, edit, delete, or independently inspect one, and `internal/blueprint`'s EVPN datacenter starter documented the gap explicitly rather than papering over it. This section closes it: a controller is now a first-class SDN object family (`/cluster/sdn/controllers`), the same "sibling top-level collection, own status view, ordinary changeset ops" shape §6 gave fabrics.

**Controllers tab.** Controller list with type badge (`bgp`\|`evpn`\|`faucet`\|`isis` — real PVE 9.2 enum), and a create/edit form whose fields reveal per the selected type: ASN, BGP mode, eBGP settings, and a peer address list for `bgp`; the underlying fabric, peer-group name, and BGP-EVPN route-map settings for `evpn`; domain, interfaces, and network entity title for `isis`; `faucet` carries none of the type-specific fields — only the general `node`/`nodes`/`loopback` fields every type shares. Unlike a fabric, a controller carries no per-node membership/health read at all (the captured API has neither route); EVPN/BGP session health is reported on §3's EVPN/BGP tab instead, attached to the controller by id.

**A zone's `controller` field is a reference, not an opaque string.** The zone editors/wizards are unchanged at the wire level (`SdnZoneCreateParams.Controller` still names a controller by plain id string) — what changed is that the id now resolves to a real inspectable object on the Controllers tab rather than dangling.

**Deleting a referenced controller is blocked**, the same protection an in-use firewall alias/ipset/security-group already gets (`internal/fw`'s `UsageCounts`): a `sdn.controller.delete` targeting a controller at least one zone's `controller` field still names fails `Validate()` with a blocking finding naming the referencing zone(s), rather than either silently orphaning the zone or leaving the operator to discover the breakage only when PVE itself rejects the apply.

Controller create/update/delete are ordinary changeset ops, staging into the same pending SDN config every zone/vnet/subnet/fabric edit does and applying through the same trailing `sdn.apply` — no controller-specific apply path, the identical `--lock-token` posture §6 states for fabrics.

## 8. SDN IPAM plugin instances (T-3104)

Like a controller, an ipam plugin instance was a bare string on a zone's own `ipam` field before this task — carried by `SdnZoneCreateParams`/`SdnZoneUpdateParams` and documented in `docs/data-model.md`'s entity table, but nothing in this codebase could create, edit, delete, or independently inspect one, and (a gap this task also found and closed) nothing even *read* a zone's chosen ipam plugin from a live poll: `internal/pve.SDNZone` carried no `ipam` field at all, so the value was silently dropped both on write (a changeset's `sdn.zone.create`/`.update` never reached PVE with it) and on read (a live snapshot's `SdnZone.ipam` was always empty). This section closes the object-model half of that gap the same way §7 closed it for controllers: an ipam plugin instance is now a first-class SDN object family (`/cluster/sdn/ipams`), the same "sibling top-level collection, own status view, ordinary changeset ops" shape §6/§7 gave fabrics/controllers — see this section's own closing paragraph for the zone-wiring half.

**IPAM plugins tab.** Instance list with type badge (`netbox`\|`phpipam`\|`pve` — real PVE 9.2 enum vnprox already modeled correctly), and a create/edit form whose fields reveal per the selected type: URL, token, certificate fingerprint, and phpIPAM section id for `netbox`/`phpipam` (url+token required); `pve` (the built-in plugin) takes none of those, having nothing external to connect to. Unlike a fabric, an instance carries no per-node membership/health read at all (the captured API has no such route, the same shape a controller has) — this is connection config, not a realization-health object.

**Token is write-only.** PVE never echoes a configured instance's token back on a read (the same treatment it gives the SDN DNS plugin's own key) — the tab's edit form always starts a `netbox`/`phpipam` instance's token field empty, and saving with it left blank keeps whatever PVE already has stored rather than clearing it. There is no way for this page, or any other vnprox surface, to display an existing instance's current token.

**A zone's `ipam` field is a reference, not an opaque string.** The zone editors/wizards are unchanged at the wire level (`SdnZoneCreateParams.IPAM` still names an instance by plain id string) — what changed is that the id now resolves to a real inspectable object on the IPAM plugins tab rather than dangling, *and* that the field is now actually carried end to end: `internal/pve.SDNZone` gained the `ipam` field the capture always documented (`--ipam <string>` on the zone create usage block) but this codebase never modeled, `cmd/vnproxd`'s zone create/update dispatch now sends it, and `internal/inventory`'s zone ingest now populates it from a live poll — the prerequisite for the next paragraph's delete-block check to mean anything against real (or pvemock-fixture) data rather than an always-empty field.

**Deleting a referenced ipam instance is blocked**, the same protection §7 gives a referenced controller: a `sdn.ipam.delete` targeting an instance at least one zone's `ipam` field still names fails `Validate()` with a blocking finding naming the referencing zone(s).

Ipam instance create/update/delete are ordinary changeset ops, staging into the same pending SDN config every zone/vnet/subnet/fabric/controller edit does and applying through the same trailing `sdn.apply` — no ipam-specific apply path, the identical `--lock-token` posture §6/§7 state for fabrics/controllers. External NetBox/phpIPAM *bidirectional sync* (a different feature — the app-owned diff/preview/apply ceremony that reads/writes the external system's own address records, not PVE's `/cluster/sdn/ipams` plugin config) lives in `docs/features/ipam.md` §7, not here.

## 9. Out of scope v1

Custom FRR config beyond what PVE's EVPN controller writes; BGP to external fabrics beyond PVE's controller/exit-node model (view-only where present); IPv6 SLAAC management — **display now real** (T-1404: `GET /ipv6/segments` surfaces per-segment RA presence, M/O flags, advertised prefixes, and DHCPv6-server presence, cluster-wide — `docs/api.md`'s IPv6 section, `docs/features/ipam.md` §4-§5), **basic addressing config now real** (a v6 subnet is an ordinary `sdn.subnet.create` op, staged directly or via the dual-stack rollout wizard); full RA/DHCPv6 *parameter* control (M/O flags, DHCPv6 ranges) beyond addressing remains P1 — PVE SDN's own subnet model has no such fields to set yet.
