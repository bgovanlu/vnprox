# Feature spec — Visual IPAM

Proxmox has IPAM plugins (built-in `pve`, NetBox, phpIPAM) but no usable view of them. vnprox provides the view and the workflow.

## 1. Data sources

- Primary: PVE IPAM API (allocations per SDN subnet).
- Enrichment: guest agent-reported IPs, DHCP leases (dnsmasq zones), and ARP/IPv6-neighbor tables via peer API (`internal/neighbor.Service`, T-805) — merged with confidence labels (`allocated`, `observed`, `both`, `conflict`). The neighbor source reads `internal/host.Reader.Neighbors` (real: `/proc/net/arp` for IPv4, the IPv6 neighbor table via netlink) locally and fans it out cluster-wide via `GET /api/peer/host/neighbors`, exactly like the guest-agent and DHCP-lease sources — never authoritative, filtered to resolved neighbor-cache states (`REACHABLE`/`STALE`/`PERMANENT`). All three enrichment sources are wired today; no known gap remains here.
- External IPAM (NetBox/phpIPAM) is configured in PVE, not vnprox; vnprox reads through PVE's plugin transparently and deep-links to the external tool for records it doesn't own.

## 2. Views

- **Subnet list**: all SDN subnets + (read-only) detected non-SDN subnets from bridge addresses; utilization bars, gateway, zone/VNet, DHCP on/off.
- **Address list** (NetBox-style): the selected subnet's occupied addresses as rows — IP, state (allocated / reserved / observed-unallocated / gateway / conflict), hostname, VMID, MAC, source — with the contiguous free space between them collapsed into "N addresses free" range rows. Because the response is sparse (proportional to actual usage, not the address space), the same view serves a /30 and a /16 with no paging. A segmented utilization strip and the conflict callouts sit above the list; a search box and per-state filter chips narrow it; clicking a row opens its detail + reserve/release. This replaced the earlier colored-square allocation grid (which didn't scale past a /24 and hid per-address detail behind a click).
- **Conflict surfacing** (P0 value): duplicate IPs (two guests reporting the same address), observed-but-unallocated (squatters), allocated-but-dark (stale records). Each conflict is a health finding with suggested resolution.

## 3. Workflow

- Reserve/release addresses (`ipam.alloc.*` ops through the change engine).
- "Next free address" picker (`web/src/ipam/NextFreePicker.tsx`) exposed on the bridge editor's address field today; wiring it into every other IP-entry field in the UI (VLAN editor, interface editor, SDN subnet gateway) is a known follow-up (flagged, T-607), not yet done for all of them.
- CSV export per subnet.

## 4. IPv6 planning grid (T-1404)

Given a delegated IPv6 prefix (e.g. a `/56` from an upstream ISP/RIR), `GET /ipam/subnets/{prefix}/v6-plan` (`docs/api.md`) enumerates its `/64`-aligned blocks — PVE SDN's (and nearly every real-world v6 addressing plan's) atomic per-subnet unit — and proposes one block per currently v4-only VLAN/VNet, aligned in ascending order against VNet ID for a deterministic, reviewable proposal. A block an already-configured SDN v6 subnet occupies renders `allocated`; everything else not proposed to a target renders `free`. Read-only: the grid is a planning aid, not a write path — turning a `proposed` block into a real subnet goes through the ordinary `sdn.subnet.create` changeset op (directly, or via the dual-stack rollout wizard below).

DHCPv6-PD from an upstream device vnprox doesn't manage is visibility-only, surfaced through `GET /ipv6/segments`'s RA observation (`docs/api.md`'s IPv6 section) — never fetched, requested, or configured by vnprox itself.

## 5. Dual-stack rollout wizard (T-1404)

`web/src/ipv6/DualStackWizard.tsx`, built on `docs/features/blueprints.md`'s `blueprint.Instantiate` pattern: picks an existing VLAN/VNet, collects a v6 subnet CIDR/gateway/SNAT choice, and stages an `sdn.subnet.create` op as one reviewable changeset — the ordinary stage→validate→diff→apply→confirm/rollback lifecycle, no new op type. Idempotent by construction (the same "entities that already match are skipped" contract every blueprint instantiation gets): re-running the wizard against a VNet that already has the requested v6 subnet yields a zero-op changeset, rendered as "already up to date" rather than a duplicate or conflicting draft. RA/DHCPv6 *parameter* control beyond addressing (M/O flags, DHCPv6 ranges) remains P1 — PVE SDN's own subnet model has no such fields yet (§6 of `docs/features/sdn.md`); once addressing exists on an SDN zone, the zone's own dnsmasq/radvd instance is what actually emits the RA the segments view (§4 above's sibling, `GET /ipv6/segments`) then observes.

## 6. Out of scope v1

Managing IP space outside Proxmox's knowledge (arbitrary external subnets as pure records) — P2; DNS record management — P2 (display PowerDNS SDN integration status only). Full RA/DHCPv6 parameter control (M/O flags, ranges) beyond addressing — P1, see §5 above.
