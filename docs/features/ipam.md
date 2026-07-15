# Feature spec — Visual IPAM

Proxmox has IPAM plugins (built-in `pve`, NetBox, phpIPAM) but no usable view of them. vnprox provides the view and the workflow.

## 1. Data sources

- Primary: PVE IPAM API (allocations per SDN subnet).
- Enrichment: guest agent-reported IPs, DHCP leases (dnsmasq zones) — merged with confidence labels (`allocated`, `observed`, `both`, `conflict`). **Known gap (flagged, T-607):** ARP/neighbor tables via peer API are not wired in — `internal/ipam/service.go`'s `NeighborSource` interface exists but has no implementation, and no `internal/host.Reader` method for it either, so today's merge is these two enrichment sources, not three. Not release-blocking (conflict detection already works off the two wired sources); follow-up: implement an ARP/neighbor collector when a task picks this up.
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
