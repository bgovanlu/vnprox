# Feature spec — LLDP discovery & physical layer

Answers "what is this NIC actually plugged into?" — the question every Proxmox admin currently answers with a flashlight and a spreadsheet.

## 1. Collection

- Uses **lldpd** (Debian package). The vnprox installer offers to install and enable it (`--with-lldp`, default yes); vnproxd reads via `lldpctl -f json` (fixed argv, parsed defensively — treat as untrusted input).
- Collector cadence 30s, per node, aggregated cluster-wide via peer API.
- Captured per neighbor: chassis name/ID, port ID/description, mgmt address, advertised VLANs (PVID + tagged), MAU/speed, TTL. CDP neighbors appear too (lldpd decodes CDP).
- If lldpd is absent: feature degrades gracefully — physical layer shows NICs only with a setup hint (one-click "install lldpd on all nodes" runs through a changeset-like confirmation, executed via peer API apt install; audited).

## 2. Presentation

- **Map:** switch chassis rendered as physical-layer nodes; identical chassis IDs seen from multiple nodes/NICs merge into one switch entity — this is what makes the map show *the actual wiring*, including which nodes share a switch (failure-domain visibility).
- **Inspector:** per-NIC neighbor detail, including a **VLAN cross-check**: VLANs the bridge/bond expects vs. VLANs the switch advertises on that port, with mismatches flagged ("bridge vmbr1 is VLAN-aware for 10–30 but switch port Gi1/0/14 advertises only 10,20").
- **Ports view:** a flat table (node, NIC, switch, port, speed, PVID, tagged VLANs, last seen) — exportable CSV; this alone replaces most wiring spreadsheets.

## 3. Staleness & trust

Neighbors carry `lastSeen`; entries older than 2×TTL grey out, older than 10min drop from the map (kept in the table with a "stale" tag for troubleshooting unplugged links). LLDP data is display/validation input only — never a changeset source.

## 4. MAC/FDB browser (P1)

Per-bridge forwarding table (`bridge fdb show`, via peer API) merged with guest MAC ownership from inventory: search any MAC → which bridge/port/guest it lives behind, cluster-wide. Useful for "where is this rogue device" hunts.
