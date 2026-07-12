# Feature spec — Topology map

The topology map is the product's home screen and primary differentiator: one live, interactive picture of everything from switch ports to guest NICs, cluster-wide.

## 1. Layers

Four toggleable layers, rendered as horizontal bands per cluster node (nodes side by side, cluster-scoped SDN entities in a band spanning all nodes):

1. **Physical** — switch chassis/ports (from LLDP), physical NICs with link state/speed.
2. **L2** — bonds, bridges (Linux + OVS), VLAN interfaces; edges show enslavement and bridge ports; VLAN-aware bridges show trunked VID ranges as edge badges.
3. **Overlay (SDN)** — zones, VNets, subnets; edges show which bridges realize which VNets on which nodes; EVPN zones show VTEP mesh edges between nodes.
4. **Guests** — VMs/CTs grouped per node; guest NIC edges to the physical bridge that realizes their attachment (an SDN VNet's own zone bridge included), with a VLAN tag badge naming the VID/VNet — not a separate edge to a VNet node (T-607 docs audit: verified against both the shipped three-node-vlan.yaml fixture and a real 8-node/40-VNet scale run — `guest-nic` entities always resolve to their `bridge:` target, badge-only for the VLAN/VNet identity). Collapsible per bridge ("23 guests" pill expands on click).

## 2. Interactions

**Two views of the same data (Switch / Graph toggle).** The page header carries a segmented `Switch | Graph` control selecting how the one `GET /topology` response is rendered:

- **Switch view (default)** — the virtual-switch faceplate. Each Linux/OVS bridge is drawn as a switch appliance: an uplink bay (bonds expanded to their member NICs with LACP/MII active state and the LLDP neighbor on the far end of each wire), a VLAN sub-interface strip, a grid of guest **access ports** (VMID as the port number, guest name, VLAN tag), and a strip of the SDN VNets realized on the bridge. Switches are grouped per cluster node; NICs/bonds not wired into any bridge surface as an "Unattached ports" panel. Because a Proxmox bridge *is* a virtual switch, this is the most literal rendering of the model. The four layer toggles map to faceplate sections (Physical→uplink bay + unattached ports, L2→VLAN strip, SDN→VNet strip, Guests→access ports); the switch header always shows. Click any switch/port/NIC/VNet → the same inspector; the VLAN filter greys switches (and individual ports) not carrying the VID; a stale node's switches grey out; collapsed-guest bridges show a `+N` access port that expands in place.
- **Graph view** — the elk pan/zoom node-link canvas (below). It retains the interactions that are inherently spatial: drag-and-drop editing, the path-simulator map overlay, traffic paint mode, and hover-chain highlight. Selecting an entity, searching, the VLAN filter, and layer toggles work in both views.

The chosen view is a per-session preference (like traffic mode), not part of the saved layout.


- **Pan/zoom canvas** (React Flow), auto-layout via elkjs layered algorithm; manual repositioning persists per user (saved layouts, `layouts` table).
- **Click** any entity → inspector panel: normalized fields, live status, raw source (interfaces(5) stanza or PVE object), related entities, quick actions (Edit → opens changeset drawer; Trace path; Show metrics).
- **Hover** → highlight the full connectivity chain (guest NIC → bridge → bond → NIC → switch port) and dim the rest.
- **VLAN filter**: enter VID(s) → the map dims everything not carrying that VLAN; trunk edges show where the VID is tagged/untagged/pruned.
- **Search/spotlight** (`/` hotkey): fuzzy across names, MACs, IPs, VMIDs, comments; selecting focuses + highlights the entity.
- **Drag-and-drop editing** (Phase 2+): dragging a free NIC onto a bond/bridge, or a guest NIC edge to a different bridge, creates the corresponding draft op in the changeset drawer. Drops that fail validation snap back with the finding shown inline.
- **Status painting**: link down = red edge; degraded bond (missing slave) = amber; unconfirmed changeset entities = blue pulse; drift = dashed outline.

## 3. Rendering contract

`GET /api/v1/topology` returns `nodes[]` (id=Ref, kind, label, layer, nodeGroup, status, badges[]) and `edges[]` (from, to, kind, badges[], status). The frontend owns layout; the backend owns structure and status. Deltas over WS (`topology.delta`) trigger targeted refetch, not full reload.

## 4. Scale targets

Smooth (≥30fps pan/zoom) at: 8 nodes × 6 NICs, 4 bridges/node, 300 guests, 40 VNets. Above that, progressive disclosure kicks in: guests collapse by default (server-side, `internal/topology/collapse.go`'s `DefaultCollapseThreshold`); hard render cap ~2,000 visible elements, beyond which the UI requires a filter (a banner names the exact count and points at the VLAN filter/layer toggles). Both measured and verified at the documented scale target in `docs/performance.md` (T-607).

**Known gap (flagged, T-607):** "physical layer collapses to per-node summary" — the physical layer (NICs + bonds) has no collapse/summarization logic anywhere in `internal/topology` or the frontend; it renders every NIC/bond node uncollapsed regardless of scale. This was never implemented in any phase. It is not release-blocking for v1.0: at the documented scale target the physical layer is only ~50-65 nodes cluster-wide (8 nodes × ~6-8 phys/L2 entities), nowhere near the 2,000-element cap on its own, so the gap has no observed effect at target scale — the guest-layer collapse (which does exist and is load-bearing at scale, since 300 guests is the dominant contributor) covers the scale target's real pressure point. Follow-up: build physical-layer-to-per-node-summary collapse before supporting cluster sizes materially larger than the documented target (e.g. 16+ nodes or NIC counts high enough to matter on their own), or remove this sentence if the product decision is that it's not needed. Tracked as a P2-backlog follow-up in `docs/roadmap.md`.

## 5. Empty/degraded states

- No LLDP data → physical layer shows NICs only, with a dismissible hint linking to LLDP setup docs.
- Peer node unreachable → its band renders greyed from last-known data with a staleness banner and timestamp.

## 6. Drift detection

A background checker (30s) compares across nodes: bridge presence/VLAN-awareness/VID sets for same-named bridges, MTU consistency along each L2 path (NIC→bond→bridge→VNet), SDN zone node-membership vs. actual realization, pending-but-unapplied `interfaces.new` files, and `interfaces` file vs. runtime state (someone edited by hand / ran `ip` commands). Findings surface in `GET /drift`, as dashed outlines on the map, and as a count badge in the nav. Each finding offers "create fixing changeset" where a safe fix is computable.
