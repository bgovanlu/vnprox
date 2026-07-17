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

## 4. Out of scope v1

Managing IP space outside Proxmox's knowledge (arbitrary external subnets as pure records) — P2; DNS record management — P2 (display PowerDNS SDN integration status only).
