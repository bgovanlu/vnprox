# Feature spec — LLDP discovery & physical layer

Answers "what is this NIC actually plugged into?" — the question every Proxmox admin currently answers with a flashlight and a spreadsheet.

## 1. Collection

- Uses **lldpd** (Debian package). The vnprox installer offers to install and enable it (`--with-lldp`, default yes); vnproxd reads via `lldpctl -f json` (fixed argv, parsed defensively — treat as untrusted input).
- Collector cadence 30s, per node, aggregated cluster-wide via peer API.
- Captured per neighbor: chassis name/ID, port ID/description, mgmt address, advertised VLANs (PVID + tagged), MAU/speed, TTL. CDP neighbors appear too (lldpd decodes CDP).
- If lldpd is absent: feature degrades gracefully — physical layer shows NICs only with a setup hint (one-click "install lldpd on all nodes" runs through a changeset-like confirmation, executed via peer API apt install; audited).
  - **A node that already has lldpd is a no-op, not an install.** The daemon asks `dpkg-query` first and skips apt entirely. This is the ordinary case for a cluster-wide button — some nodes have it, some do not — and skipping matters for more than tidiness: `apt-get install -y` on an already-installed package still rewrites `/var/lib/apt/extended_states`, which `ProtectSystem=strict` forbids, so it exited 100 and reported a correctly configured node as failed.
  - **The install itself runs via `systemd-run`, not apt directly.** vnproxd's unit mounts `/` read-only in its own namespace (not just `/var`), and installing lldpd writes to `/usr` and `/etc` — so the subprocess is handed to PID 1 as a transient unit outside that namespace rather than widening `ReadWritePaths`, which would have meant granting `/usr` + `/etc` + `/var` and ending the sandbox. See `packaging/systemd/vnprox.service`.
  - Failures are reported as one actionable line; apt's transcript is not surfaced.

## 2. Presentation

- **Map:** switch chassis rendered as physical-layer nodes; identical chassis IDs seen from multiple nodes/NICs merge into one switch entity — this is what makes the map show *the actual wiring*, including which nodes share a switch (failure-domain visibility).
- **VLAN cross-check:** VLANs the bridge/bond expects vs. VLANs the switch advertises on that port, with mismatches flagged ("bridge vmbr1 is VLAN-aware for 10–30 but switch port Gi1/0/14 advertises only 10,20"). Surfaces through the unified findings stream (`GET /findings`, `source: "lldp"`), not as a dedicated section inside the entity inspector — this doc previously said "Inspector," which overstated where it renders.
- **Ports view:** a flat table (node, NIC, switch, port, speed, PVID, tagged VLANs, last seen) — exportable CSV (`GET /ports`); this alone replaces most wiring spreadsheets. **Known gap (flagged, T-607):** the backend is complete, but there is no frontend page consuming `GET /ports` yet — reachable only by hitting the API URL directly, not through any in-app nav entry. Follow-up: add a Ports page (P1, not release-blocking — the data is fully available via the documented CSV export in the meantime).
- **Cabling plan (T-3907, `/cabling`):** every physical NIC in the cluster, grouped by node, with the same far-end switch/port join the Ports view and the Switch faceplate already carry — but unlike the Ports view (which lists a row only when LLDP found a neighbor), this view enumerates every physnic and explicitly marks the ones with none `Not discovered` rather than omitting them. That gap matters: an unmanaged switch, a directly-attached host, and LLDP disabled on the far end all produce "no neighbor," and none of them means "unplugged" — the NIC's own link-up state answers that question independently. No rack elevation: LLDP names a far end's identity and port, never a U-position, cable length, or colour, so none of that is drawn or inferred; if an operator wants rack placement recorded, the map annotation layer (`internal/annotate`, pinned to the NIC's entity ref) is the place for it, not a fabricated rack model. Prints via the browser's own print command over a dedicated print stylesheet (the same `print:` convention the Topology map's T-906 export uses) rather than a second, docexport-based export path; the on-screen table is what prints, and the small patch-panel diagram beneath it is a visual, `aria-hidden` enhancement over the same data, never a second source of the facts.

## 3. Staleness & trust

Neighbors carry `lastSeen`; entries older than 2×TTL grey out, older than 10min drop from the map (kept in the table with a "stale" tag for troubleshooting unplugged links). LLDP data is display/validation input only — never a changeset source.

## 4. MAC/FDB browser (P1)

Per-bridge forwarding table (read directly via netlink `NeighList(AF_BRIDGE)`, not a `bridge fdb show` shell-out — functionally equivalent, this doc previously named the wrong mechanism) merged with guest MAC ownership from inventory: search any MAC → which bridge/port/guest it lives behind, cluster-wide. Useful for "where is this rogue device" hunts. Shipped with a frontend (`web/src/lldp/MacFdbBrowser.tsx`) more complete than the "P1" label above implies.
