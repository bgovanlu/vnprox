# Feature spec — Topology map

The topology map is the product's home screen and primary differentiator: one live, interactive picture of everything from switch ports to guest NICs, cluster-wide.

## 1. Layers

Four toggleable layers, rendered as horizontal bands per cluster node (nodes side by side, cluster-scoped SDN entities in a band spanning all nodes):

1. **Physical** — switch chassis/ports (from LLDP), physical NICs with link state/speed.
2. **L2** — bonds, bridges (Linux + OVS), VLAN interfaces; edges show enslavement and bridge ports; VLAN-aware bridges show trunked VID ranges as edge badges.
3. **Overlay (SDN)** — zones, VNets, subnets; edges show which bridges realize which VNets on which nodes; EVPN zones show VTEP mesh edges between nodes.
4. **Guests** — VMs/CTs grouped per node; guest NIC edges to their bridge/VNet with VLAN tag badges. Collapsible per bridge ("23 guests" pill expands on click).

## 2. Interactions

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

Smooth (≥30fps pan/zoom) at: 8 nodes × 6 NICs, 4 bridges/node, 300 guests, 40 VNets. Above that, progressive disclosure kicks in: guests collapse by default, physical layer collapses to per-node summary. Hard render cap ~2,000 visible elements; beyond, require a filter (UI prompts).

## 5. Empty/degraded states

- No LLDP data → physical layer shows NICs only, with a dismissible hint linking to LLDP setup docs.
- Peer node unreachable → its band renders greyed from last-known data with a staleness banner and timestamp.

## 6. Drift detection

A background checker (30s) compares across nodes: bridge presence/VLAN-awareness/VID sets for same-named bridges, MTU consistency along each L2 path (NIC→bond→bridge→VNet), SDN zone node-membership vs. actual realization, pending-but-unapplied `interfaces.new` files, and `interfaces` file vs. runtime state (someone edited by hand / ran `ip` commands). Findings surface in `GET /drift`, as dashed outlines on the map, and as a count badge in the nav. Each finding offers "create fixing changeset" where a safe fix is computable.
