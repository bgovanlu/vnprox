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
- **Command palette** (`⌘K`/`Ctrl+K`, T-903): one app-wide dialog merging this section's spotlight entity search with every page's registered action verbs ("edit vmbr0", "new VLAN zone", "open drafts", "simulate path from `<entity>`", ...) — `web/src/keyboard/CommandPalette.tsx` + `actions.ts`'s `usePaletteActions` registry. Targets whichever renderer (Switch/Graph) is currently mounted; adopts T-901/T-905's canvas accessibility bridge automatically once that lands, with no rework here.
- **Roving keyboard focus** (T-903): once a map entity has DOM focus, arrow keys move focus to the next entity in on-screen visual-adjacency order (top-to-bottom, then left-to-right) across whichever view is mounted; Enter activates the focused entity exactly like a click (`web/src/keyboard/useRovingFocus.ts`/`rovingFocus.ts`).

## 3. Rendering contract

`GET /api/v1/topology` returns `nodes[]` (id=Ref, kind, label, layer, nodeGroup, status, badges[]) and `edges[]` (from, to, kind, badges[], status). The frontend owns layout; the backend owns structure and status. Deltas over WS (`topology.delta`) trigger targeted refetch, not full reload.

**Management-path visibility (T-702).** Each node's management path — which interface carries its PVE management IP and/or corosync links, the physical NICs that ultimately carry it, and whether that path has redundancy — is a first-class, visible thing, not just an internal safety-interlock input (`internal/change/protected.go`'s `DetectProtected`, docs/security.md's "Safety interlocks"). A shared classification/path resolver (`internal/topology.ResolveMgmtPaths`, called by both `internal/change`'s `GET /protected-interfaces/status` and, via the exact same computation, `internal/api`'s `GET /topology` badge painting) produces, per node, each protected ref's role(s), its resolved physical path, and a redundancy bool. `Project` itself stays a pure function of the inventory snapshot alone — this data is injected as a second, independent decoration at the `internal/api` handler level, the same seam the finding-badge overlay above already uses.

Badge vocabulary, additive to every badge `badgesOf` already emits:

- `mgmt` — this node/edge is the ref carrying the node's PVE management IP (`Node.IP`).
- `corosync` — this ref carries one of the node's corosync ring addresses (`ring*_addr` in `/etc/pve/corosync.conf`). A ref can carry both `mgmt` and `corosync` at once (e.g. a homelab node running corosync over its only bridge).
- `mgmt-path` — this entity is part of the physical path carrying a `mgmt`/`corosync` ref: the parent bridge of a VLAN sub-interface carrier, a carrier bridge's ports, a bond, and its slave NICs. A path member never carries `mgmt`/`corosync` itself — only the carrier does.

Rendering: the graph view (`EntityNode`) gives this trio a distinct (amber) treatment, additive to the plain badge-chip look every other badge gets; the switch faceplate badges the chassis header (carrier) and the uplink-bay NIC/bond chips on the path (`mgmt-path`) the same way. The inspector's "Management path" tab (node-scoped entities only) shows every resolved ref for that node: carrier, physical path chain, a plain-English redundancy statement, and — when `GET /protected-interfaces/status`'s `source` is `"detected"` (protected.json never confirmed during onboarding) — a caveat plus a link back into the onboarding "protected interfaces" step. See docs/api.md's `GET /protected-interfaces/status` entry for the full response shape, and docs/features/monitoring.md §5 for the `mgmt_single_path` health check this same data also feeds.

**Management page (`/management`).** The badges and inspector tab above make the management path visible wherever a user happens to be in the topology; the dedicated **Management** page (nav rail, `web/src/mgmt/ManagementPage.tsx`) is the one place to *find and configure* it. Driven by the same `GET /protected-interfaces/status`, it lists every node's management/corosync carrier with its resolved aspects (addresses, gateway, VLAN, MTU, comments, physical path, redundancy statement) and two write paths that already existed but were only reachable by hunting for the right entity on the map: the full entity **editor** (the shared `editorKindForInventoryKind` resolver opens the bridge/bond/vlan/iface editor for the carrier — addresses, gateway, VLAN, bond membership, comments, MTU) and the guided **redundancy wizard**. Editing stages a normal changeset like everywhere else; re-addressing the management IP *value* stays out of scope by construction (guarded by the T-203 net-effect interlock — docs/security.md), so the page configures every aspect of a management interface *except* moving its IP, which remains the redundancy wizard's dedicated-VLAN flow's job.

## 4. Scale targets

Smooth (≥30fps pan/zoom) at: 8 nodes × 6 NICs, 4 bridges/node, 300 guests, 40 VNets. Above that, progressive disclosure kicks in: guests collapse by default (server-side, `internal/topology/collapse.go`'s `DefaultCollapseThreshold`); hard render cap ~2,000 visible elements, beyond which the UI requires a filter (a banner names the exact count and points at the VLAN filter/layer toggles). Both measured and verified at the documented scale target in `docs/performance.md` (T-607).

**Level-of-detail (LOD), v2 renderer only (T-902 — resolves the T-607-flagged physical-layer-collapse gap below).** `web/src/topology/lod.ts` adds a second, client-side, zoom-driven progressive-disclosure layer on top of the server-side guest collapse above — active only under the v2 canvas renderer (`TopologyCanvasV2.tsx`), a pure `FlowElements -> FlowElements` transform re-evaluated as the viewport's zoom crosses named thresholds:

| Band | Zoom (≥) | Content |
|---|---|---|
| `full` | 0.5 | Every entity at full faceplate detail — v1/v2 parity, unchanged. |
| `simplified` | 0.2 | Edge bundling engages (below); the physical layer stays uncollapsed. |
| `capsule` | 0 (i.e. below `simplified`'s threshold) | Additionally, each cluster node's physical layer (NICs + bonds) collapses into one per-node summary capsule. |

- **Physical-layer capsule** (the T-607 gap's resolution): at the `capsule` band, every `physnic`/`bond`/`ovs-bond` node belonging to one cluster node collapses into a single synthetic capsule node (`NIC/bond count`, worst-of aggregate status, a `count=N` badge), redirecting/merging the edges that used to touch its members (e.g. several `bond--port-of-->bridge` edges collapse to one `capsule--port-of-->bridge` edge with a `links=N` badge when they'd otherwise overlap) so the capsule stays visually connected rather than an island. `lldp-neighbor` entities (also `LayerPhysical`) and bridges/VLAN interfaces (`LayerL2` but not "NICs + bonds") are intentionally excluded from the capsule's scope. Zooming back above the `capsule` threshold, or a click on the capsule, restores full per-NIC/per-bond detail for that one node.
- **Edge bundling**: below the `full` band, a dense set of individual guest-NIC "attached-to" edges into the same target — grouped by `(target, source's own cluster node)`, the identical grouping `internal/topology/collapse.go`'s server-side collapse already uses — collapses into one bundle node + one edge carrying a `count=N` badge once the group exceeds `EDGE_BUNDLE_THRESHOLD` (mirrors `DefaultCollapseThreshold`, 8, client-side — this repo has no Go↔TS shared-codegen, so the two constants are kept in sync by hand). This is the client-side counterpart to the server's guest-group pills: it re-engages after a pill has been expanded (`expand.ts`) and its individual guest-NIC nodes get dense again at low zoom, or for any group that was never collapsed server-side in the first place. Zooming back into the `full` band, or a click on the bundle, restores the individual guest-NIC entities.
- Both the capsule and the bundle reuse the existing collapsed-pill rendering (`EntityNodeData.isGuestGroup`/plain-box treatment already in `EntityNode.tsx`/`canvasDraw.ts`) rather than new dedicated components — no rendering-layer changes were needed to draw them.
- **Minimap**: a fixed-size overview canvas (`Minimap.tsx`) rendering every visible entity as a dot plus a viewport rectangle tracking the main canvas's current pan/zoom; dragging the rectangle recenters (pans) the main canvas on the graph point under the drag, at the main canvas's current zoom.
- Perf: the LOD transform is memoized on `(elements, band, manual overrides)` — it recomputes only when the zoom band actually changes (crossing a threshold) or a capsule/bundle is manually toggled, not on every pan/zoom animation frame, so it does not regress the v2 renderer's per-frame budget (`docs/performance.md` §3a).

**Resolved gap (was flagged T-607, closed T-902):** "physical layer collapses to per-node summary" — previously true (no collapse/summarization logic existed anywhere for the physical layer), now resolved client-side by the `capsule` LOD band above. The underlying `GET /topology` response is unchanged (`internal/topology` untouched, per CLAUDE.md's change-engine/backend-contract rules) — the physical layer still projects every NIC/bond node; the v2 renderer now collapses them client-side once zoomed out far enough that per-NIC detail isn't legible anyway. This does not (and was never intended to) change the *documented scale target* itself, which the guest-layer server-side collapse already covers as the real pressure point (300 guests dominates the entity count; the physical layer was only ~50-65 elements cluster-wide at target scale, per `docs/performance.md` §4) — LOD is a readability/perf refinement on top of that, valuable at the documented target and increasingly load-bearing beyond it (the follow-up scenario the original gap note called out: "cluster sizes materially larger than the documented target"). The v1 (React Flow) renderer does not gain this transform — it remains the fallback renderer, not the phase's LOD target.

## 5. Empty/degraded states

- No LLDP data → physical layer shows NICs only, with a dismissible hint linking to LLDP setup docs.
- Peer node unreachable → its band renders greyed from last-known data with a staleness banner and timestamp.

## 6. Drift detection

A background checker (30s) compares across nodes: bridge presence/VLAN-awareness/VID sets for same-named bridges, MTU consistency along each L2 path (NIC→bond→bridge→VNet), SDN zone node-membership vs. actual realization, pending-but-unapplied `interfaces.new` files, and `interfaces` file vs. runtime state (someone edited by hand / ran `ip` commands). Findings surface in `GET /drift`, as dashed outlines on the map, and as a count badge in the nav. Each finding offers "create fixing changeset" where a safe fix is computable.
