# Feature spec — Visual IPAM

Proxmox has IPAM plugins (built-in `pve`, NetBox, phpIPAM) but no usable view of them. vnprox provides the view and the workflow.

## 1. Data sources

- Primary: PVE IPAM API (allocations per SDN subnet).
- Enrichment: guest agent-reported IPs, DHCP leases (dnsmasq zones), ARP/neighbor tables via peer API — merged with confidence labels (`allocated`, `observed`, `both`, `conflict`).
- External IPAM (NetBox/phpIPAM) is configured in PVE, not vnprox; vnprox reads through PVE's plugin transparently and deep-links to the external tool for records it doesn't own.

## 2. Views

- **Subnet list**: all SDN subnets + (read-only) detected non-SDN subnets from bridge addresses; utilization bars, gateway, zone/VNet, DHCP on/off.
- **Allocation grid**: /24-and-smaller render as a color grid (free / allocated / observed-unallocated / reserved / gateway / conflict); larger subnets render as paged block summaries. Click any cell → detail (who, what, since when, source).
- **Conflict surfacing** (P0 value): duplicate IPs (two guests reporting the same address), observed-but-unallocated (squatters), allocated-but-dark (stale records). Each conflict is a health finding with suggested resolution.

## 3. Workflow

- Reserve/release addresses (`ipam.alloc.*` ops through the change engine).
- "Next free address" picker exposed everywhere an IP is entered elsewhere in the UI (subnet-aware autocomplete).
- CSV export per subnet.

## 4. Out of scope v1

Managing IP space outside Proxmox's knowledge (arbitrary external subnets as pure records) — P2; DNS record management — P2 (display PowerDNS SDN integration status only).
